package systemcontracts_test

import (
	"bytes"
	"crypto/ecdsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"reflect"
	"testing"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
	cmttypes "github.com/cometbft/cometbft/types"
	"github.com/cosmos/cosmos-sdk/codec/address"
	sdk "github.com/cosmos/cosmos-sdk/types"
	evmtypes "github.com/cosmos/evm/x/vm/types"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	ownedapp "github.com/maxwell2010/go-smart-node/network/app"
	"github.com/maxwell2010/go-smart-node/network/identity"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
	"github.com/stretchr/testify/require"
)

// Two genuine penalties, separated by the ordinary owner unpause, share one
// FinalizeBlock/time. The cursor
// must distinguish append entries, not deduplicate timestamps or heights.
func appendSignedPenaltiesSameBlock(t *testing.T, a *ownedapp.App, ctx func() sdk.Context, vals *cmttypes.ValidatorSet, now time.Time, price *big.Int, key *ecdsa.PrivateKey, target common.Address, inputs ...[]byte) int64 {
	t.Helper()
	require.Len(t, inputs, 3)
	from := crypto.PubkeyToAddress(key.PublicKey)
	before := a.BankKeeper.GetBalance(ctx(), sdk.AccAddress(from.Bytes()), identity.BaseDenom).Amount
	nonce := a.EVMKeeper.GetNonce(ctx(), from)
	encoded := make([][]byte, 0, 3)
	signer := ethtypes.NewEIP155Signer(big.NewInt(990001))
	for i, input := range inputs {
		raw := ethtypes.NewTx(&ethtypes.LegacyTx{Nonce: nonce + uint64(i), To: &target, Value: new(big.Int), Gas: 8_000_000, GasPrice: price, Data: input})
		signed, err := ethtypes.SignTx(raw, signer, key)
		require.NoError(t, err)
		msg := &evmtypes.MsgEthereumTx{}
		require.NoError(t, msg.FromSignedEthereumTx(signed, signer))
		tx, err := msg.BuildTx(a.TxConfig().NewTxBuilder(), identity.BaseDenom)
		require.NoError(t, err)
		data, err := a.TxConfig().TxEncoder()(tx)
		require.NoError(t, err)
		encoded = append(encoded, data)
	}
	height := a.LastBlockHeight() + 1
	result, err := a.FinalizeBlock(&abci.RequestFinalizeBlock{Height: height, Time: now.Add(time.Duration(height) * time.Second), NextValidatorsHash: vals.Hash(), ProposerAddress: vals.Proposer.Address, Txs: encoded})
	require.NoError(t, err)
	require.Len(t, result.TxResults, 3)
	fee := new(big.Int)
	for _, tx := range result.TxResults {
		require.Zero(t, tx.Code, tx.Log)
		var messages sdk.TxMsgData
		require.NoError(t, a.AppCodec().Unmarshal(tx.Data, &messages))
		require.Len(t, messages.MsgResponses, 1)
		response := &evmtypes.MsgEthereumTxResponse{}
		require.NoError(t, response.Unmarshal(messages.MsgResponses[0].Value))
		require.Empty(t, response.VmError)
		fee.Add(fee, new(big.Int).Mul(new(big.Int).SetUint64(response.GasUsed), price))
	}
	_, err = a.Commit()
	require.NoError(t, err)
	after := a.BankKeeper.GetBalance(ctx(), sdk.AccAddress(from.Bytes()), identity.BaseDenom).Amount
	require.Zero(t, new(big.Int).Sub(before.BigInt(), after.BigInt()).Cmp(fee))
	require.Equal(t, nonce+3, a.EVMKeeper.GetNonce(ctx(), from))
	require.Zero(t, after.BigInt().Cmp(a.EVMKeeper.GetBalance(ctx(), from).ToBig()))
	t.Logf("two ordered penalties committed at height=%d timestamp=%s", height, now.Add(time.Duration(height)*time.Second))
	return height
}

func checkSignedPagedPenalties(t *testing.T, keys []*ecdsa.PrivateKey, roles map[string]common.Address, artifacts map[string]contracts.Artifact, wrapped common.Address, nativeSupply func() *big.Int, send func(*ecdsa.PrivateKey, *common.Address, *big.Int, []byte, bool) *evmtypes.MsgEthereumTxResponse, appendBatch func(*ecdsa.PrivateKey, common.Address, [][]byte) int64) {
	t.Helper()
	roles, artifacts = cloneRoleMap(roles), cloneArtifactMap(artifacts)
	wrapper, err := contracts.Load("MintCandyWrappedDEL")
	require.NoError(t, err)
	roles["page-wdel"], artifacts["page-wdel"] = wrapped, wrapper
	var last *evmtypes.MsgEthereumTxResponse
	call := func(user int, role, method string, value *big.Int, args ...interface{}) []interface{} {
		target, artifact := roles[role], artifacts[role]
		data, err := artifact.ABI.Pack(method, args...)
		require.NoError(t, err)
		last = send(keys[user], &target, value, data, true)
		values, err := artifact.ABI.Unpack(method, last.Ret)
		require.NoError(t, err)
		return values
	}
	integer := func(values []interface{}, field string) *big.Int {
		if field == "" {
			return values[0].(*big.Int)
		}
		return reflect.ValueOf(values[0]).FieldByName(field).Interface().(*big.Int)
	}
	reject := func(user int, method string, args ...interface{}) *evmtypes.MsgEthereumTxResponse {
		data, err := artifacts["delegation"].ABI.Pack(method, args...)
		require.NoError(t, err)
		target := roles["delegation"]
		return send(keys[user], &target, new(big.Int), data, false)
	}
	catchupError := func(response *evmtypes.MsgEthereumTxResponse, id [32]byte, cursor, count int64) {
		require.GreaterOrEqual(t, len(response.Ret), 4)
		e := artifacts["delegation"].ABI.Errors["PenaltyCatchupRequired"]
		require.Equal(t, e.ID.Bytes()[:4], response.Ret[:4])
		values, err := e.Inputs.Unpack(response.Ret[4:])
		require.NoError(t, err)
		require.Equal(t, id, values[0])
		require.Equal(t, big.NewInt(cursor), values[1])
		require.Equal(t, big.NewInt(count), values[2])
	}
	addValidator := func(seed byte) common.Address {
		validator := common.BytesToAddress(crypto.Keccak256([]byte{seed}, []byte("bounded-penalty-ordinary"))[12:])
		operator, err := address.NewBech32Codec(identity.Bech32PrefixValAddr).BytesToString(validator.Bytes())
		require.NoError(t, err)
		meta, err := json.Marshal(map[string]interface{}{"operator_address": operator, "reward_address": validator.Hex(), "consensus_pubkey": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{seed}, 32)), "description": map[string]string{"moniker": "bounded-penalties"}, "commission": "10"})
		require.NoError(t, err)
		type validatorMeta struct {
			Validator common.Address
			Meta      string
		}
		call(1, "master-validator", "addValidators", new(big.Int), []validatorMeta{{validator, string(meta)}})
		return validator
	}
	t.Run("typed duplicate-vote penalty metadata", func(t *testing.T) {
		validator := addValidator(0xa3)
		const role = "master-validator"
		master, target := artifacts[role], roles[role]
		rejectAppend := func(user int, cursor, height int64) {
			t.Helper()
			input, err := master.ABI.Pack("appendDuplicateVotePenalty", validator, big.NewInt(cursor), big.NewInt(height), uint16(5))
			require.NoError(t, err)
			send(keys[user], &target, new(big.Int), input, false)
		}
		state := func() []interface{} {
			t.Helper()
			return call(1, role, "getValidatorState", new(big.Int), validator)
		}
		appendAt := func(cursor, height int64, alreadyPaused bool) {
			t.Helper()
			call(1, role, "appendDuplicateVotePenalty", new(big.Int), validator, big.NewInt(cursor), big.NewInt(height), uint16(5))
			logs := last.Logs
			if alreadyPaused {
				require.Len(t, logs, 1, "paused validator must not emit a second pause")
			} else {
				require.Len(t, logs, 2)
			}
			log := logs[len(logs)-1]
			event := master.ABI.Events["ValidatorDuplicateVotePenalty"]
			require.Equal(t, target, common.HexToAddress(log.Address))
			require.Len(t, log.Topics, 4)
			require.Equal(t, event.ID, common.HexToHash(log.Topics[0]))
			require.Equal(t, common.BytesToHash(validator[:]), common.HexToHash(log.Topics[1]))
			require.Equal(t, common.BigToHash(big.NewInt(cursor)), common.HexToHash(log.Topics[2]))
			require.Equal(t, common.BigToHash(big.NewInt(height)), common.HexToHash(log.Topics[3]))
			data, err := event.Inputs.NonIndexed().Pack(uint16(5))
			require.NoError(t, err)
			require.Equal(t, data, log.Data)
		}

		beforeSupply := new(big.Int).Set(nativeSupply())
		before := state()
		require.False(t, before[1].(bool))
		rejectAppend(0, 0, 1)
		rejectAppend(1, 1, 1)
		rejectAppend(1, 0, 0)
		require.Equal(t, before, state())

		call(1, role, "addPenalty", new(big.Int), validator, uint16(1))
		appendAt(1, 1, true)
		paused := state()
		require.Equal(t, before[0], paused[0])
		require.True(t, paused[1].(bool))
		require.Equal(t, big.NewInt(2), paused[2])
		rejectAppend(1, 2, 1)
		require.Equal(t, paused, state())

		call(1, role, "unpauseValidator", new(big.Int), validator)
		appendAt(2, 2, false)
		after := state()
		require.Equal(t, before[0], after[0])
		require.True(t, after[1].(bool))
		require.Equal(t, big.NewInt(3), after[2])
		for index, want := range []int64{0, 1, 2} {
			result := call(1, role, "getDuplicateVotePenaltyHeight", new(big.Int), validator, big.NewInt(int64(index)))
			require.Zero(t, result[0].(*big.Int).Cmp(big.NewInt(want)))
		}
		result := call(1, role, "getPenaltiesRange", new(big.Int), validator, big.NewInt(0), big.NewInt(3))
		require.Equal(t, []uint16{1, 5, 5}, result[0])
		require.Equal(t, beforeSupply, nativeSupply())
	})
	firstPair := func(validator common.Address) {
		first, err := artifacts["master-validator"].ABI.Pack("addPenalty", validator, uint16(1))
		require.NoError(t, err)
		second, err := artifacts["master-validator"].ABI.Pack("addPenalty", validator, uint16(2))
		require.NoError(t, err)
		unpause, err := artifacts["master-validator"].ABI.Pack("unpauseValidator", validator)
		require.NoError(t, err)
		appendBatch(keys[1], roles["master-validator"], [][]byte{first, unpause, second})
		require.Equal(t, big.NewInt(2), call(1, "master-validator", "getPenaltyCount", new(big.Int), validator)[0].(*big.Int))
	}
	remaining := func(validator common.Address) {
		for i := 0; i < 63; i++ {
			if i > 0 {
				call(1, "master-validator", "unpauseValidator", new(big.Int), validator)
			}
			call(1, "master-validator", "addPenalty", new(big.Int), validator, uint16(1))
		}
	}
	percents := []uint16{1, 2}
	for i := 0; i < 63; i++ {
		percents = append(percents, 1)
	}
	expected := func(initial *big.Int, start, end int) *big.Int {
		value := new(big.Int).Set(initial)
		for i := start; i < end; i++ {
			loss := new(big.Int).Mul(value, big.NewInt(int64(percents[i])))
			loss.Div(loss, big.NewInt(100))
			value.Sub(value, loss)
		}
		return value
	}
	freezeIndex := func() *big.Int {
		event := artifacts["delegation"].ABI.Events["WithdrawRequest"]
		for _, entry := range last.Logs {
			if common.HexToAddress(entry.Address) == roles["delegation"] && len(entry.Topics) > 0 && common.HexToHash(entry.Topics[0]) == event.ID {
				values, err := event.Inputs.NonIndexed().Unpack(entry.Data)
				require.NoError(t, err)
				return values[1].(*big.Int)
			}
		}
		t.Fatal("withdraw receipt missing")
		return nil
	}
	for _, historyCount := range []int{2, 20, 21} {
		t.Run("ordinary repeated native burn count "+big.NewInt(int64(historyCount)).String(), func(t *testing.T) {
			validator := addValidator(byte(0xb0 + historyCount))
			owner := crypto.PubkeyToAddress(keys[0].PublicKey)
			type collection struct {
				Creator                   common.Address
				Symbol, Name, ContractURI string
				Refundable, Burnable      bool
			}
			nft := call(0, "nft-center", "createDRC721", new(big.Int), collection{owner, "REPEATED", "Repeated native burn", "ipfs://repeated", true, true})[0].(common.Address)
			asset, err := contracts.Load("NewNetworkDRC721")
			require.NoError(t, err)
			roles["repeat-nft"], artifacts["repeat-nft"] = nft, asset
			initial := new(big.Int).Mul(big.NewInt(20), big.NewInt(1_000_000_000_000_000_000))
			token := call(0, "repeat-nft", "mintByDEL", initial, owner, "ipfs://repeat")[0].(*big.Int)
			call(0, "repeat-nft", "approve", new(big.Int), roles["delegation"], token)
			call(0, "delegation", "delegateNFT", new(big.Int), validator, nft, token)
			id := call(1, "delegation", "getNFTStakeId", new(big.Int), nft, token, owner, validator, new(big.Int))[0].([32]byte)
			firstPair(validator)
			for i := 2; i < historyCount; i++ {
				call(1, "master-validator", "unpauseValidator", new(big.Int), validator)
				call(1, "master-validator", "addPenalty", new(big.Int), validator, uint16(1))
			}
			before := new(big.Int).Set(nativeSupply())
			for cursor := 0; cursor < historyCount; {
				next := cursor + 16
				if next > historyCount {
					next = historyCount
				}
				call(0, "delegation", "applyPenaltiesToNFTStakes", new(big.Int), [][32]byte{id})
				t.Logf("ordinary history=%d page=%d..%d gasUsed=%d maxUsedGas=%d", historyCount, cursor, next, last.GasUsed, last.MaxUsedGas)
				require.Equal(t, big.NewInt(int64(next)), call(1, "delegation", "getNFTStakePenaltyIndex", new(big.Int), id)[0].(*big.Int))
				require.Equal(t, expected(initial, 0, next), integer(call(1, "repeat-nft", "getReserve", new(big.Int), token), "Amount"))
				require.Equal(t, new(big.Int).Sub(initial, expected(initial, 0, next)), new(big.Int).Sub(before, nativeSupply()))
				cursor = next
			}
		})
	}
	t.Run("coin pages and late position", func(t *testing.T) {
		validator := addValidator(0xa1)
		call(1, "delegation", "setFreezeTime", new(big.Int), new(big.Int), new(big.Int))
		call(0, "delegation", "delegateDEL", big.NewInt(101), validator)
		owner := crypto.PubkeyToAddress(keys[0].PublicKey)
		late := crypto.PubkeyToAddress(keys[2].PublicKey)
		oldID := call(1, "delegation", "getStakeId", new(big.Int), validator, owner, wrapped)[0].([32]byte)
		firstPair(validator)
		call(1, "master-validator", "unpauseValidator", new(big.Int), validator)
		call(2, "delegation", "delegateDEL", big.NewInt(3), validator)
		lateID := call(1, "delegation", "getStakeId", new(big.Int), validator, late, wrapped)[0].([32]byte)
		require.Equal(t, big.NewInt(2), call(1, "delegation", "getStakePenaltyIndex", new(big.Int), lateID)[0].(*big.Int))
		remaining(validator)
		before := new(big.Int).Set(nativeSupply())
		catchupError(reject(0, "withdraw", validator, wrapped, big.NewInt(98)), oldID, 32, 65)
		require.Equal(t, before, nativeSupply(), "failed early exit rolls back its attempted realized burn")
		require.Zero(t, call(1, "delegation", "getStakePenaltyIndex", new(big.Int), oldID)[0].(*big.Int).Sign())
		for _, cursor := range []int{32, 64, 65} {
			call(0, "delegation", "applyPenaltiesToStakes", new(big.Int), [][32]byte{oldID})
			require.Equal(t, big.NewInt(int64(cursor)), call(1, "delegation", "getStakePenaltyIndex", new(big.Int), oldID)[0].(*big.Int))
			require.Equal(t, expected(big.NewInt(101), 0, cursor), integer(call(1, "delegation", "getStake", new(big.Int), validator, owner, wrapped), "Amount"))
		}
		for _, cursor := range []int64{34, 65} {
			call(2, "delegation", "applyPenaltiesToStakes", new(big.Int), [][32]byte{lateID})
			require.Equal(t, big.NewInt(cursor), call(1, "delegation", "getStakePenaltyIndex", new(big.Int), lateID)[0].(*big.Int))
		}
		require.Equal(t, big.NewInt(3), new(big.Int).Sub(before, nativeSupply()), "same-height 1% then2% on101 burns1+2, later1% floors to0")
		require.Equal(t, big.NewInt(3), integer(call(1, "delegation", "getStake", new(big.Int), validator, late, wrapped), "Amount"), "late position skips earlier records")
		reject(0, "applyPenaltiesToStakes", [][32]byte{oldID})
		require.Equal(t, big.NewInt(3), new(big.Int).Sub(before, nativeSupply()))
		for _, position := range []struct {
			user   int
			amount int64
		}{{0, 98}, {2, 3}} {
			call(position.user, "delegation", "withdraw", new(big.Int), validator, wrapped, big.NewInt(position.amount))
			index := freezeIndex()
			call(1, "delegation", "complete", new(big.Int), []*big.Int{index})
		}
		require.Zero(t, call(1, "page-wdel", "balanceOf", new(big.Int), roles["delegation"])[0].(*big.Int).Sign())
	})
	t.Run("NFT pages and late NFT", func(t *testing.T) {
		validator := addValidator(0xa2)
		owner := crypto.PubkeyToAddress(keys[0].PublicKey)
		type collection struct {
			Creator                   common.Address
			Symbol, Name, ContractURI string
			Refundable, Burnable      bool
		}
		nft := call(0, "nft-center", "createDRC721", new(big.Int), collection{owner, "PAGES", "Paged NFT", "ipfs://pages", true, true})[0].(common.Address)
		artifact, err := contracts.Load("NewNetworkDRC721")
		require.NoError(t, err)
		roles["page-nft"], artifacts["page-nft"] = nft, artifact
		twenty := new(big.Int).Mul(big.NewInt(20), big.NewInt(1_000_000_000_000_000_000))
		mint := func() (*big.Int, [32]byte) {
			token := call(0, "page-nft", "mintByDEL", twenty, owner, "ipfs://one")[0].(*big.Int)
			call(0, "page-nft", "approve", new(big.Int), roles["delegation"], token)
			call(0, "delegation", "delegateNFT", new(big.Int), validator, nft, token)
			id := call(1, "delegation", "getNFTStakeId", new(big.Int), nft, token, owner, validator, new(big.Int))[0].([32]byte)
			return token, id
		}
		oldToken, oldID := mint()
		firstPair(validator)
		call(1, "master-validator", "unpauseValidator", new(big.Int), validator)
		lateToken, lateID := mint()
		require.Equal(t, big.NewInt(2), call(1, "delegation", "getNFTStakePenaltyIndex", new(big.Int), lateID)[0].(*big.Int))
		remaining(validator)
		before := new(big.Int).Set(nativeSupply())
		catchupError(reject(0, "withdrawNFT", validator, nft, oldToken), oldID, 16, 65)
		require.Equal(t, before, nativeSupply())
		require.Equal(t, twenty, integer(call(1, "page-nft", "getReserve", new(big.Int), oldToken), "Amount"))
		call(0, "delegation", "applyPenaltiesToNFTStakes", new(big.Int), [][32]byte{oldID, lateID})
		require.Equal(t, big.NewInt(16), call(1, "delegation", "getNFTStakePenaltyIndex", new(big.Int), oldID)[0].(*big.Int))
		require.Equal(t, big.NewInt(2), call(1, "delegation", "getNFTStakePenaltyIndex", new(big.Int), lateID)[0].(*big.Int), "two IDs share one16-entry native-action budget")
		require.Equal(t, twenty, integer(call(1, "page-nft", "getReserve", new(big.Int), lateToken), "Amount"))
		require.Equal(t, new(big.Int).Sub(twenty, expected(twenty, 0, 16)), new(big.Int).Sub(before, nativeSupply()))
		for _, position := range []struct {
			id      [32]byte
			token   *big.Int
			start   int
			cursors []int
		}{{oldID, oldToken, 0, []int{32, 48, 64, 65}}, {lateID, lateToken, 2, []int{18, 34, 50, 65}}} {
			for _, cursor := range position.cursors {
				call(0, "delegation", "applyPenaltiesToNFTStakes", new(big.Int), [][32]byte{position.id})
				require.Equal(t, big.NewInt(int64(cursor)), call(1, "delegation", "getNFTStakePenaltyIndex", new(big.Int), position.id)[0].(*big.Int))
				require.Equal(t, expected(twenty, position.start, cursor), integer(call(1, "page-nft", "getReserve", new(big.Int), position.token), "Amount"))
			}
		}
		wantedLoss := new(big.Int).Sub(new(big.Int).Mul(twenty, big.NewInt(2)), new(big.Int).Add(expected(twenty, 0, 65), expected(twenty, 2, 65)))
		require.Equal(t, wantedLoss, new(big.Int).Sub(before, nativeSupply()))
		coinID := call(1, "delegation", "getStakeId", new(big.Int), validator, owner, wrapped)[0].([32]byte)
		for _, cursor := range []int64{34, 65} {
			call(0, "delegation", "applyPenaltiesToStakes", new(big.Int), [][32]byte{coinID})
			require.Equal(t, big.NewInt(cursor), call(1, "delegation", "getStakePenaltyIndex", new(big.Int), coinID)[0].(*big.Int))
		}
		for _, token := range []*big.Int{oldToken, lateToken} {
			call(0, "delegation", "withdrawNFT", new(big.Int), validator, nft, token)
			event := artifacts["delegation"].ABI.Events["NFTUndelegated"]
			var index *big.Int
			for _, entry := range last.Logs {
				if common.HexToAddress(entry.Address) == roles["delegation"] && len(entry.Topics) > 0 && common.HexToHash(entry.Topics[0]) == event.ID {
					values, err := event.Inputs.NonIndexed().Unpack(entry.Data)
					require.NoError(t, err)
					index = values[0].(*big.Int)
				}
			}
			require.NotNil(t, index)
			call(1, "delegation", "complete", new(big.Int), []*big.Int{index})
			require.Equal(t, owner, call(1, "page-nft", "ownerOf", new(big.Int), token)[0])
		}
		require.Equal(t, wantedLoss, new(big.Int).Sub(before, nativeSupply()), "completed NFT exits cannot repeat realized burns")
		require.Zero(t, integer(call(1, "delegation", "getValidatorReserve", new(big.Int), validator, wrapped), "Reserve").Sign())
	})
}
