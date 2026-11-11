package settlement

import (
	"math/big"
	"testing"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/maxwell2010/go-smart-node/network/decimal/nft/evmview"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
	"github.com/stretchr/testify/require"
)

func proofFixture(t *testing.T) *Executor {
	t.Helper()
	gateway, err := contracts.CompiledABI("MintCandyIsolatedDelegation")
	require.NoError(t, err)
	wrapper, err := contracts.CompiledABI("MintCandyWrappedDEL")
	require.NoError(t, err)
	vault, err := contracts.CompiledABI("MintCandyNFTPositionVault")
	require.NoError(t, err)
	return &Executor{
		roles:  evmview.StakeRoles{Gateway: common.HexToAddress("0x7001"), WrappedDEL: common.HexToAddress("0x7002"), PositionVault: common.HexToAddress("0x7003")},
		config: Config{EVMGas: 500_000, MaxLogs: 8, MaxLogBytes: 1024}, gateway: gateway, wrapper: wrapper, vault: vault,
	}
}

func (e *Executor) completionFixture(index common.Hash) *ethtypes.Log {
	return &ethtypes.Log{Address: e.roles.Gateway, Topics: []common.Hash{e.gateway.Events["WithdrawCompleted"].ID}, Data: index.Bytes()}
}

func TestCompletionIDsPreserveOrderAndFullIndex(t *testing.T) {
	e := proofFixture(t)
	zero, wide := common.Hash{}, common.BigToHash(new(big.Int).Lsh(big.NewInt(1), 200))
	foreign := e.completionFixture(common.MaxHash)
	foreign.Address = common.HexToAddress("0x1234")
	receipt := &ethtypes.Receipt{Status: 1, Logs: []*ethtypes.Log{foreign, e.completionFixture(wide), e.completionFixture(zero)}}
	ids, err := e.CompletionIDs(receipt)
	require.NoError(t, err)
	require.Equal(t, []common.Hash{wide, zero}, ids)

	for _, tc := range []struct {
		name string
		edit func(*ethtypes.Receipt)
	}{
		{"sentinel", func(r *ethtypes.Receipt) { r.Logs[1].Data = common.MaxHash.Bytes() }},
		{"duplicate", func(r *ethtypes.Receipt) { r.Logs[1].Data = r.Logs[2].Data }},
		{"extra topic", func(r *ethtypes.Receipt) { r.Logs[1].Topics = append(r.Logs[1].Topics, zero) }},
		{"short data", func(r *ethtypes.Receipt) { r.Logs[1].Data = r.Logs[1].Data[1:] }},
		{"log bytes", func(r *ethtypes.Receipt) { r.Logs[0].Data = make([]byte, 1024) }},
		{"log count", func(r *ethtypes.Receipt) { r.Logs = append(r.Logs, make([]*ethtypes.Log, 6)...) }},
		{"failed execution", func(r *ethtypes.Receipt) { r.Status = 0 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copy := cloneReceipt(receipt)
			tc.edit(copy)
			_, err := e.CompletionIDs(copy)
			require.Error(t, err)
		})
	}
}

func TestCoinCompletionUsesItsPayoutSegmentAndActualDeltas(t *testing.T) {
	e := proofFixture(t)
	validator := common.HexToAddress("0x7010")
	beneficiary := common.HexToAddress("0x7011")
	hold := smallWord(11)
	amount := smallWord(3)
	deadline := smallWord(100)
	sourceID := crypto.Keccak256Hash(validator.Bytes(), beneficiary.Bytes(), e.roles.WrappedDEL.Bytes(), make([]byte, 32), hold.Bytes())
	row := validatortypes.EVMFrozenBinding{
		SourceValidator: validator.Bytes(), Beneficiary: beneficiary.Bytes(), RawAmount: sdkmath.NewInt(3), CompletionTimestamp: deadline.Bytes(),
		SourceKind: validatortypes.EVMStakeBindingKind_EVM_STAKE_BINDING_COIN, SourceStakeID: sourceID.Bytes(),
		ReserveAsset: &validatortypes.EVMReserveAsset{ReserveKind: 1, NativeDenom: identity.BaseDenom, Token: make([]byte, 20)},
	}
	words := frozenWords{common.BytesToHash(validator.Bytes()), common.BytesToHash(beneficiary.Bytes()), common.BytesToHash(e.roles.WrappedDEL.Bytes()), amount,
		{}, smallWord(tokenDEL), hold, {}, smallWord(statusCompleted), smallWord(freezeWithdraw), deadline}
	p, err := e.matchFrozen(words, row, statusCompleted)
	require.NoError(t, err)
	wrong := row
	wrong.SourceStakeID = smallWord(42).Bytes()
	_, err = e.matchFrozen(words, wrong, statusCompleted)
	require.ErrorContains(t, err, "source stake ID")
	_, err = e.matchFrozen(words, row, statusFrozen)
	require.Error(t, err)

	burn := func() *ethtypes.Log {
		return &ethtypes.Log{Address: e.roles.WrappedDEL, Topics: []common.Hash{
			e.wrapper.Events["Transfer"].ID, common.BytesToHash(e.roles.Gateway.Bytes()), {},
		}, Data: amount.Bytes()}
	}
	receipt := &ethtypes.Receipt{Status: 1, Logs: []*ethtypes.Log{burn(), e.completionFixture(smallWord(1)), burn(), e.completionFixture(smallWord(2))}}
	markers, err := e.completions(receipt)
	require.NoError(t, err)
	for _, marker := range markers {
		require.NoError(t, e.validatePayoutLog(receipt, marker, p), "identical amounts on separate indices belong to separate payouts")
	}
	receipt.Logs[2].Data = smallWord(2).Bytes()
	require.Error(t, e.validatePayoutLog(receipt, markers[1], p))

	before := payoutState{beneficiary: big.NewInt(2), gateway: big.NewInt(4), backing: big.NewInt(11), supply: big.NewInt(10), wrapped: big.NewInt(8)}
	after := payoutState{beneficiary: big.NewInt(5), gateway: big.NewInt(4), backing: big.NewInt(8), supply: big.NewInt(7), wrapped: big.NewInt(5)}
	require.NoError(t, p.checkDelta(before, after))
	after.beneficiary = big.NewInt(4)
	require.Error(t, p.checkDelta(before, after))
}

func TestSignedMetadataAllowsEstimatorOrMatchingInclusion(t *testing.T) {
	ctx := (sdk.Context{}).WithBlockHeight(27)
	receipt := &ethtypes.Receipt{Status: 1}
	require.NoError(t, signedMetadata(ctx, receipt))
	receipt.TxHash = smallWord(3)
	require.Error(t, signedMetadata(ctx, receipt))
	receipt.BlockNumber = big.NewInt(27)
	receipt.Logs = []*ethtypes.Log{{BlockNumber: 27}}
	require.NoError(t, signedMetadata(ctx, receipt))
	receipt.Logs[0].TxHash = smallWord(4)
	require.Error(t, signedMetadata(ctx, receipt))
}

func TestVaultPayoutUsesFullReceiptIDAndExactBeneficiary(t *testing.T) {
	e := proofFixture(t)
	beneficiary := common.HexToAddress("0x7011")
	p := payout{asset: e.roles.PositionVault, beneficiary: beneficiary, nft: true,
		tokenID: common.BigToHash(new(big.Int).Lsh(big.NewInt(1), 200))}
	transfer := &ethtypes.Log{Address: p.asset, Topics: []common.Hash{e.vault.Events["Transfer"].ID,
		common.BytesToHash(e.roles.Gateway.Bytes()), common.BytesToHash(beneficiary.Bytes()), p.tokenID}}
	receipt := &ethtypes.Receipt{Status: 1, Logs: []*ethtypes.Log{transfer, e.completionFixture(smallWord(1))}}
	markers, err := e.completions(receipt)
	require.NoError(t, err)
	require.NoError(t, e.validatePayoutLog(receipt, markers[0], p))
	transfer.Topics[3] = common.Hash{}
	require.Error(t, e.validatePayoutLog(receipt, markers[0], p))
	transfer.Topics[3] = p.tokenID
	transfer.Topics[2] = common.BytesToHash(e.roles.Gateway.Bytes())
	require.Error(t, e.validatePayoutLog(receipt, markers[0], p))
}
