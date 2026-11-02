// replay-window executes cached canonical blocks on an isolated restored home.
// There is no P2P networking, signing state or production database access.
package main

import (
	"bitbucket.org/decimalteam/go-smart-node/app"
	"bitbucket.org/decimalteam/go-smart-node/contracts"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/ibc-go/v6/testing/simapp"
	"github.com/decimalteam/ethermint/encoding"
	"github.com/ethereum/go-ethereum/common"
	abci "github.com/tendermint/tendermint/abci/types"
	tmjson "github.com/tendermint/tendermint/libs/json"
	"github.com/tendermint/tendermint/libs/log"
	coretypes "github.com/tendermint/tendermint/rpc/core/types"
	tmtypes "github.com/tendermint/tendermint/types"
	dbm "github.com/tendermint/tm-db"
	"net/http"
	"os"
	"path/filepath"
)

type offlineTransport struct{}

func (offlineTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("HTTP networking disabled in offline replay")
}

func main() {
	http.DefaultTransport = offlineTransport{}
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func block(dir string, height int64) (*coretypes.ResultBlock, error) {
	data, e := os.ReadFile(filepath.Join(dir, fmt.Sprintf("%d.block.json", height)))
	if e != nil {
		return nil, e
	}
	var response struct {
		Result coretypes.ResultBlock `json:"result"`
	}
	e = tmjson.Unmarshal(data, &response)
	if e != nil {
		return nil, e
	}
	b := response.Result.Block
	if b != nil {
		if err := b.ValidateBasic(); err != nil {
			return nil, fmt.Errorf("invalid block body: %w", err)
		}
	}
	if b == nil || b.Height != height || !bytes.Equal(b.Hash(), response.Result.BlockID.Hash) {
		return nil, fmt.Errorf("invalid cached block %d", height)
	}
	return &response.Result, nil
}
func run() error {
	home := flag.String("home", "", "isolated restored home with successful restore-report")
	dir := flag.String("blocks", "", "immutable cached blocks/validators directory")
	from := flag.Int64("from", 34271001, "first replay height")
	to := flag.Int64("to", 34271010, "last replay height")
	inspect := flag.Bool("inspect-only", false, "inspect an already replayed local checkpoint")
	flag.Parse()
	if *home == "" || *dir == "" || *to < *from || *to-*from >= 1000 {
		return fmt.Errorf("bounded home/blocks/from/to required")
	}
	report, e := os.ReadFile(filepath.Join(*home, "restore-report.json"))
	if e != nil {
		return e
	}
	var status struct {
		Matches bool `json:"matches_trusted"`
	}
	if e = json.Unmarshal(report, &status); e != nil || !status.Matches {
		return fmt.Errorf("home lacks a verified restore")
	}
	for _, p := range []string{"config/priv_validator_key.json", "data/priv_validator_state.json", "keyring-file", "keyring-test"} {
		if _, e = os.Stat(filepath.Join(*home, p)); e == nil {
			return fmt.Errorf("refuse home containing signing state")
		}
	}
	db, e := dbm.NewGoLevelDB("application", filepath.Join(*home, "data"))
	if e != nil {
		return e
	}
	defer db.Close()
	dsc := app.NewDSC(log.NewNopLogger(), db, nil, true, map[int64]bool{}, *home, 0, encoding.MakeConfig(app.ModuleBasics), simapp.EmptyAppOptions{})
	if *inspect {
		if dsc.LastBlockHeight() != *to {
			return fmt.Errorf("inspection height mismatch")
		}
		*from = *to + 1
	} else if dsc.LastBlockHeight() != *from-1 {
		return fmt.Errorf("application height %d does not precede requested start", dsc.LastBlockHeight())
	}
	type record struct {
		Height int64    `json:"height"`
		Hash   string   `json:"app_hash"`
		Txs    int      `json:"tx_count"`
		Codes  []uint32 `json:"tx_codes"`
	}
	var records []record
	if prior, err := os.ReadFile(filepath.Join(*home, "replay-window-report.json")); err == nil {
		if err := json.Unmarshal(prior, &records); err != nil {
			return err
		}
		if !*inspect && len(records) > 0 && records[len(records)-1].Height != *from-1 {
			return fmt.Errorf("checkpoint report does not precede replay start")
		}
	}
	for height := *from; height <= *to; height++ {
		entry, e := block(*dir, height)
		if e != nil {
			return e
		}
		b := entry.Block
		if b.ChainID != "decimal_75-1" {
			return fmt.Errorf("unexpected chain")
		}
		if !bytes.Equal(dsc.CommitMultiStore().LastCommitID().Hash, b.AppHash) {
			return fmt.Errorf("pre-block AppHash mismatch at %d", height)
		}
		next, e := block(*dir, height+1)
		if e != nil {
			return e
		}
		data, e := os.ReadFile(filepath.Join(*dir, fmt.Sprintf("%d.validators.json", height-1)))
		if e != nil {
			return e
		}
		var v struct {
			Result coretypes.ResultValidators `json:"result"`
		}
		if e = tmjson.Unmarshal(data, &v); e != nil {
			return e
		}
		if v.Result.BlockHeight != height-1 {
			return fmt.Errorf("wrong validator height")
		}
		previous, err := block(*dir, height-1)
		if err != nil {
			return fmt.Errorf("predecessor header required: %w", err)
		}
		if !bytes.Equal(tmtypes.NewValidatorSet(v.Result.Validators).Hash(), previous.Block.ValidatorsHash) {
			return fmt.Errorf("validator cache hash mismatch at %d", height-1)
		}
		if b.LastCommit == nil || len(v.Result.Validators) != len(b.LastCommit.Signatures) {
			return fmt.Errorf("incomplete previous validator set")
		}
		votes := make([]abci.VoteInfo, len(v.Result.Validators))
		for i, val := range v.Result.Validators {
			signature := b.LastCommit.Signatures[i]
			if signature.BlockIDFlag != tmtypes.BlockIDFlagAbsent && !bytes.Equal(signature.ValidatorAddress, val.Address) {
				return fmt.Errorf("validator ordering mismatch")
			}
			votes[i] = abci.VoteInfo{Validator: abci.Validator{Address: val.Address, Power: val.VotingPower}, SignedLastBlock: signedLastBlock(signature)}
		}
		req := abci.RequestBeginBlock{Hash: b.Hash(), Header: *b.Header.ToProto(), LastCommitInfo: abci.LastCommitInfo{Round: b.LastCommit.Round, Votes: votes}}
		for _, ev := range b.Evidence.Evidence {
			req.ByzantineValidators = append(req.ByzantineValidators, ev.ABCI()...)
		}
		dsc.BeginBlock(req)
		r := record{Height: height, Txs: len(b.Txs)}
		var canonical struct {
			Result coretypes.ResultBlockResults `json:"result"`
		}
		if len(b.Txs) > 0 {
			resultData, err := os.ReadFile(filepath.Join(*dir, fmt.Sprintf("%d.results.json", height)))
			if err != nil {
				return err
			}
			if err := tmjson.Unmarshal(resultData, &canonical); err != nil {
				return err
			}
			if canonical.Result.Height != height || len(canonical.Result.TxsResults) != len(b.Txs) {
				return fmt.Errorf("canonical tx results missing")
			}
		}
		var txResults []*abci.ResponseDeliverTx
		for i, tx := range b.Txs {
			res := dsc.DeliverTx(abci.RequestDeliverTx{Tx: tx})
			if res.Code != canonical.Result.TxsResults[i].Code {
				return fmt.Errorf("transaction code mismatch at %d index %d: %d != %d", height, i, res.Code, canonical.Result.TxsResults[i].Code)
			}
			txResults = append(txResults, &res)
			r.Codes = append(r.Codes, res.Code)
		}
		dsc.EndBlock(abci.RequestEndBlock{Height: height})
		if !bytes.Equal(tmtypes.NewResults(txResults).Hash(), next.Block.LastResultsHash) {
			return fmt.Errorf("LastResultsHash mismatch at %d", height)
		}
		committed := dsc.Commit()
		r.Hash = hex.EncodeToString(committed.Data)
		records = append(records, r)
		if !bytes.Equal(committed.Data, next.Block.AppHash) {
			return fmt.Errorf("post-block AppHash mismatch at %d: got %X expected %X", height, committed.Data, next.Block.AppHash)
		}
		out, _ := json.Marshal(r)
		fmt.Println(string(out))
		payload, _ := json.MarshalIndent(records, "", "  ")
		if e = os.WriteFile(filepath.Join(*home, "replay-window-report.json"), payload, 0600); e != nil {
			return e
		}
	}
	last, err := block(*dir, *to)
	if err != nil {
		return err
	}
	ctx := sdk.NewContext(dsc.CommitMultiStore().CacheMultiStore(), *last.Block.Header.ToProto(), true, log.NewNopLogger()).WithGasMeter(sdk.NewInfiniteGasMeter()).WithBlockGasMeter(sdk.NewInfiniteGasMeter())
	proxy, err := contracts.GetAddressFromContractCenter(ctx, &dsc.EvmKeeper, contracts.NameOfSlugForGetAddressDelegation)
	if err != nil {
		return fmt.Errorf("delegation registry check: %w", err)
	}
	addr := common.HexToAddress(proxy)
	implementation := dsc.EvmKeeper.GetState(ctx, addr, common.HexToHash("0x360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc"))
	owner := dsc.EvmKeeper.GetState(ctx, addr, common.HexToHash("0x9016d09d72d40fdae2fd8ceac6b6234c7706214fd39c1cd1e609a0528c199300"))
	reward, enabled := dsc.ValidatorKeeper.GetRewardPerBlockOverride(ctx)
	metadata := map[string]interface{}{"height": *to, "delegation_proxy": proxy, "implementation_slot": implementation.Hex(), "owner_slot": owner.Hex(), "reward_override": reward.String(), "reward_override_enabled": enabled}
	payload, _ := json.MarshalIndent(metadata, "", "  ")
	if err := os.WriteFile(filepath.Join(*home, "contract-state-report.json"), payload, 0600); err != nil {
		return err
	}
	fmt.Println(string(payload))
	return nil
}

// CometBFT counts both commit and nil votes as signed; only absent is unsigned.
func signedLastBlock(signature tmtypes.CommitSig) bool { return !signature.Absent() }
