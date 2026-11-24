//go:build appstate && mcnd_native_sdk_migration_v1

package systemcontracts_test

import (
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	govv1 "github.com/cosmos/cosmos-sdk/x/gov/types/v1"
	"github.com/cosmos/evm/crypto/ethsecp256k1"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	nftkeeper "github.com/maxwell2010/go-smart-node/network/decimal/nft/keeper"
	nfttypes "github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
	"github.com/stretchr/testify/require"
)

const userNFTRefundTestPhase = "MCND_USER_NFT_REFUND_TEST_PHASE"

type nftRefundCheckpoint struct {
	NativeIDs []string
	Owner     string
}

func TestOwnedAppNativeUserNFTRefundLifecycle(t *testing.T) {
	phase := os.Getenv(userNFTRefundTestPhase)
	if phase == "" {
		binary, err := os.Executable()
		require.NoError(t, err)
		dir := t.TempDir()
		require.NoError(t, os.Chmod(dir, 0700))
		for _, phase := range []string{"seed", "run", "restart"} {
			filter := "^TestOwnedAppNativeUserNFTRefundLifecycle$"
			if phase == "seed" {
				filter = "^TestOwnedAppSignedRegistryBootstrap$/^authenticated_isolated_position_reader$"
			}
			child := exec.CommandContext(t.Context(), binary, "-test.v", "-test.timeout=4m", "-test.run="+filter)
			child.Env = append(os.Environ(), emptyUserAssetsPhaseEnv+"="+phase, userNFTRefundTestPhase+"="+phase,
				nativeConsensusDaemonFixtureEnv+"="+dir)
			output, err := child.CombinedOutput()
			require.NoError(t, err, "phase %s: %s", phase, output)
			t.Logf("%s: %s", phase, output)
		}
		return
	}

	dir := os.Getenv(nativeConsensusDaemonFixtureEnv)
	l, profile := openEmptyUserAssets(t, dir, phase, true)
	checkpoint := filepath.Join(dir, "refund-checkpoint.json")
	if phase == "restart" {
		var saved nftRefundCheckpoint
		readNativeHistoricalJSON(t, checkpoint, &saved)
		require.Len(t, saved.NativeIDs, 2)
		for i, id := range saved.NativeIDs {
			assertRefundedUnits(t, l, id, saved.Owner, uint32(i+1), uint32(i+1))
		}
		return
	}
	require.Equal(t, "run", phase)
	govKey, err := registryFixtureKey(3, true)
	require.NoError(t, err)
	key, err := registryFixtureKey(4, true)
	require.NoError(t, err)
	owner := sdk.AccAddress(key.PubKey().Address())
	ownerEVM := common.BytesToAddress(owner)
	unit := sdkmath.NewIntWithDecimal(2, 18)
	price := big.NewInt(1_000_000_000_000)
	send := func(signer *ethsecp256k1.PrivKey, to common.Address, value sdkmath.Int, input []byte, pass bool) ([]byte, sdkmath.Int) {
		t.Helper()
		pk, err := crypto.ToECDSA(signer.Bytes())
		require.NoError(t, err)
		from := crypto.PubkeyToAddress(pk.PublicKey)
		tx := ethtypes.NewTx(&ethtypes.LegacyTx{
			To: &to, Value: value.BigInt(), Gas: 12_000_000, GasPrice: price, Data: input,
			Nonce: l.app.EVMKeeper.GetNonce(l.snapshot(), from),
		})
		signed, err := ethtypes.SignTx(tx, ethtypes.NewEIP155Signer(big.NewInt(990001)), pk)
		require.NoError(t, err)
		block, result := l.finalizeSignedEthereum(signed)
		if pass {
			require.Empty(t, result.VmError)
			require.NotEmpty(t, result.Logs)
		} else {
			require.NotEmpty(t, result.VmError)
			require.Empty(t, result.Logs)
		}
		l.commit(block)
		return result.Ret, sdkmath.NewIntFromBigInt(new(big.Int).Mul(price, new(big.Int).SetUint64(result.GasUsed)))
	}
	center, err := contracts.CompiledABI("NewNetworkNFTCenter")
	require.NoError(t, err)
	type asset struct {
		address common.Address
		burn    []byte
		id      string
	}
	mint := func(multi, refundable bool) asset {
		t.Helper()
		factoryMethod, implementation := "createDRC721", "NewNetworkDRC721"
		id := big.NewInt(1)
		uri := "ipfs://refund-asset"
		args, value := []interface{}{ownerEVM, uri}, unit
		if multi {
			factoryMethod, implementation = "createDRC1155", "MintCandyIsolatedDRC1155"
			id.SetInt64(123)
			args = []interface{}{ownerEVM, id, big.NewInt(2), uri}
			value = unit.MulRaw(2)
		}
		meta := struct {
			Creator     common.Address
			Symbol      string
			Name        string
			ContractURI string
			Refundable  bool
			Burnable    bool
		}{ownerEVM, "REFUND", "Native refund rehearsal", uri, refundable, true}
		payload, err := center.Pack(factoryMethod, meta)
		require.NoError(t, err)
		ret, _ := send(key, profile.NFTCenter, sdkmath.ZeroInt(), payload, true)
		values, err := center.Unpack(factoryMethod, ret)
		require.NoError(t, err)
		address := values[0].(common.Address)
		contract, err := contracts.CompiledABI(implementation)
		require.NoError(t, err)
		payload, err = contract.Pack("mintByDEL", args...)
		require.NoError(t, err)
		send(key, address, value, payload, true)
		burn := packNFTRefundBurn(t, contract, id, multi)
		nativeID, err := nfttypes.FreshExternalTokenID(l.doc.ChainID, 990001, address.Bytes(), common.BigToHash(id).Bytes())
		require.NoError(t, err)
		return asset{address: address, id: nativeID, burn: burn}
	}
	assets := []asset{mint(false, true), mint(true, true)}
	noRefund := mint(false, false)
	balance := func(address common.Address) sdkmath.Int {
		return l.app.BankKeeper.GetBalance(l.snapshot(), sdk.AccAddress(address.Bytes()), identity.BaseDenom).Amount
	}
	assertBurn := func(item asset, signer *ethsecp256k1.PrivKey, minted, burnt uint32, expectedRefund sdkmath.Int, pass bool) {
		t.Helper()
		supply := l.supply()
		custody := balance(item.address)
		wallet := balance(ownerEVM)
		_, fee := send(signer, item.address, sdkmath.ZeroInt(), item.burn, pass)
		require.Equal(t, supply.String(), l.supply().String())
		require.Equal(t, custody.Sub(expectedRefund).String(), balance(item.address).String())
		if owner.Equals(sdk.AccAddress(signer.PubKey().Address())) {
			require.Equal(t, wallet.Add(expectedRefund).Sub(fee).String(), balance(ownerEVM).String())
		} else {
			require.Equal(t, wallet.String(), balance(ownerEVM).String())
		}
		assertRefundedUnits(t, l, item.id, owner.String(), minted, burnt)
	}

	assertBurn(assets[0], key, 1, 0, sdkmath.ZeroInt(), false)
	assertBurn(assets[1], key, 2, 0, sdkmath.ZeroInt(), false)
	applyRefundUpgrade(t, l, govKey)
	assertBurn(noRefund, key, 1, 0, sdkmath.ZeroInt(), false)
	assertBurn(assets[0], govKey, 1, 0, sdkmath.ZeroInt(), false)
	assertBurn(assets[0], key, 1, 1, unit, true)
	assertBurn(assets[0], key, 1, 1, sdkmath.ZeroInt(), false)
	assertBurn(assets[1], key, 2, 1, unit, true)
	assertBurn(assets[1], key, 2, 2, unit, true)
	assertBurn(assets[1], key, 2, 2, sdkmath.ZeroInt(), false)
	writeNativeHistoricalJSON(t, checkpoint, nftRefundCheckpoint{NativeIDs: []string{assets[0].id, assets[1].id}, Owner: owner.String()})
}

func packNFTRefundBurn(t *testing.T, contract abi.ABI, id *big.Int, multi bool) []byte {
	t.Helper()
	args := []interface{}{id}
	if multi {
		args = append(args, big.NewInt(1))
	}
	burn, err := contract.Pack("burn", args...)
	require.NoError(t, err)
	return burn
}

func applyRefundUpgrade(t *testing.T, l nativeHistoricalLifecycle, voterKey *ethsecp256k1.PrivKey) {
	t.Helper()
	const name = "mintcandy-native-sdk-migrations-v1"
	deposit := sdk.NewCoins(sdk.NewCoin(identity.BaseDenom, sdkmath.NewIntWithDecimal(1, 18)))
	voter := sdk.AccAddress(voterKey.PubKey().Address())
	height := l.app.LastBlockHeight() + 10
	id, err := l.app.GovKeeper.ProposalID.Peek(l.snapshot())
	require.NoError(t, err)
	metadata := fmt.Sprintf(`mintcandy-sdk-upgrade/v1:{"name":"%s","height":%d}`, name, height)
	submit, err := govv1.NewMsgSubmitProposal(nil, deposit, voter.String(), metadata,
		"Native NFT refund", "Apply the registered update to project wallet refunds.", false)
	require.NoError(t, err)
	l.signedGovernance(voterKey, submit)
	l.signedGovernance(voterKey, govv1.NewMsgVote(voter, id, govv1.OptionYes, ""))
	for l.app.LastBlockHeight() < height-1 {
		l.commit(l.finalize(nil, nil))
	}
	plan, err := l.app.UpgradeKeeper.GetUpgradePlan(l.snapshot())
	require.NoError(t, err)
	require.Equal(t, height, plan.Height)
	require.Equal(t, name, plan.Name)
	l.commit(l.finalize(nil, nil))
	done, err := l.app.UpgradeKeeper.GetDoneHeight(l.snapshot(), name)
	require.NoError(t, err)
	require.Equal(t, height, done)
}

func assertRefundedUnits(t *testing.T, l nativeHistoricalLifecycle, id, owner string, minted, burnt uint32) {
	t.Helper()
	keeper := l.app.NativeNFTKeeper
	token, err := keeper.GetToken(l.snapshot(), id)
	require.NoError(t, err)
	require.Equal(t, minted, token.Minted)
	require.Equal(t, burnt, token.Burnt)
	count, err := keeper.GetActiveSubTokenCount(l.snapshot(), id, owner)
	require.NoError(t, err)
	require.Equal(t, minted-burnt, count)
	for serial := uint32(1); serial <= minted; serial++ {
		unit, err := keeper.GetSubToken(l.snapshot(), id, serial)
		if serial <= burnt {
			require.ErrorIs(t, err, nftkeeper.ErrNotFound)
		} else {
			require.NoError(t, err)
			require.Equal(t, owner, unit.Owner)
		}
	}
}
