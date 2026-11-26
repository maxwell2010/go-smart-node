package app

import (
	"errors"
	"math/big"
	"testing"
	"time"

	sdkmath "cosmossdk.io/math"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	sdk "github.com/cosmos/cosmos-sdk/types"
	govv1 "github.com/cosmos/cosmos-sdk/x/gov/types/v1"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	nativekeeper "github.com/maxwell2010/go-smart-node/network/decimal/validator/keeper"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

func TestBootstrapUnanimityVote(t *testing.T) {
	require.True(t, fullBootstrapYes(govv1.NewNonSplitVoteOption(govv1.OptionYes)))
	require.True(t, fullBootstrapYes([]*govv1.WeightedVoteOption{{Option: govv1.OptionYes, Weight: "1"}}))
	for _, opt := range []govv1.VoteOption{govv1.OptionNo, govv1.OptionNoWithVeto, govv1.OptionAbstain} {
		require.False(t, fullBootstrapYes(govv1.NewNonSplitVoteOption(opt)))
	}
	for _, options := range [][]*govv1.WeightedVoteOption{
		nil, {nil}, {{Option: govv1.OptionYes, Weight: "0.5"}},
		{{Option: govv1.OptionYes, Weight: "bad"}},
		{{Option: govv1.OptionYes, Weight: "0.5"}, {Option: govv1.OptionAbstain, Weight: "0.5"}},
	} {
		require.False(t, fullBootstrapYes(options))
	}
}

func TestBootstrapUnanimityStakeOwners(t *testing.T) {
	v := validatortypes.Validator{Status: validatortypes.BondStatus_Bonded, Stake: 1}
	state := &validatortypes.GenesisState{Validators: []validatortypes.Validator{v, v, v, v}}
	for _, owner := range []string{"founder", "founder", "v1", "new-delegator"} {
		state.Delegations = append(state.Delegations, validatortypes.Delegation{Delegator: owner, Stake: validatortypes.Stake{Stake: sdk.NewCoin("msp", sdkmath.OneInt())}})
	}
	operators := func() map[string]bool { return map[string]bool{"v1": true, "v2": true, "v3": true, "v4": true} }
	for _, missing := range []string{"founder", "new-delegator", ""} {
		consulted := map[string]int{}
		yes, err := allBootstrapStakeConsents(state, operators(), func(owner string) (bool, error) {
			consulted[owner]++
			return owner != missing, nil
		})
		require.NoError(t, err)
		require.Equal(t, missing == "", yes)
		require.Zero(t, consulted["v1"])
		require.Equal(t, 1, consulted["founder"])
	}
	lookupErr := errors.New("store read failed")
	yes, err := allBootstrapStakeConsents(state, operators(), func(string) (bool, error) { return false, lookupErr })
	require.False(t, yes)
	require.ErrorIs(t, err, lookupErr)
	unexpected := func(string) (bool, error) { t.Fatal("ineligible roster consulted owner votes"); return false, nil }
	state.Validators[1].Jailed = true
	yes, err = allBootstrapStakeConsents(state, operators(), unexpected)
	require.NoError(t, err)
	require.False(t, yes)
	state.Validators[1].Jailed = false
	state.Validators[1].Status = validatortypes.BondStatus_Unbonded
	yes, err = allBootstrapStakeConsents(state, operators(), unexpected)
	require.NoError(t, err)
	require.False(t, yes)
	state.Validators[1] = v
	state.Validators = append(state.Validators, v)
	yes, err = allBootstrapStakeConsents(state, operators(), unexpected)
	require.NoError(t, err)
	require.False(t, yes)
}

func TestBootstrapUnanimityFourLaunchOperatorsAndFounder(t *testing.T) {
	chain, evm := identity.TestnetChainID, uint64(identity.TestnetEVMChainID)
	if identity.ProfileChainID == identity.MainnetChainID {
		chain, evm = identity.MainnetChainID, identity.MainnetEVMChainID
	}
	a := testApp(t, chain, evm)
	ctx := a.NewUncachedContext(false, cmtproto.Header{ChainID: chain, Height: 1, Time: time.Unix(2000, 0).UTC()})
	ctx, _ = ctx.CacheContext()
	k := a.NativeValidatorKeeper
	ops := [4]common.Address{
		common.HexToAddress("0x021c5bF9a6eb7e30430226e652ec8c5703B2493F"),
		common.HexToAddress("0x77Bab2F42752040e0d0b927dF9cf3c6755953A2b"),
		common.HexToAddress("0xac0f9eB76365f467c34cEE188682176B987C3527"),
		common.HexToAddress("0x3a572fe604efDe0807A408ad503E5A919C026144"),
	}
	founder := common.HexToAddress("0x40900a48273644768c09183e00e43528c17a29f6")
	domain, ok := k.ConfiguredEVMSourceDomain()
	require.True(t, ok)
	g := validatortypes.DefaultGenesisState()
	g.BindingDomain = &domain
	var roster []string
	for i, op := range ops {
		operator, err := k.ValidatorAddressCodec().BytesToString(op[:])
		require.NoError(t, err)
		v, err := validatortypes.NewValidator(operator, sdk.AccAddress(op[:]).String(), ed25519.GenPrivKeyFromSecret([]byte{byte(i + 1)}).PubKey(), validatortypes.Description{Moniker: "bootstrap operator"}, sdkmath.LegacyZeroDec())
		require.NoError(t, err)
		v.Online = true
		g.Validators = append(g.Validators, v)
		roster = append(roster, operator)
	}
	require.NoError(t, k.InitGenesis(ctx, g))
	gateway, wrapped := common.HexToAddress("0x1111"), common.HexToAddress("0x2222")
	delegate := func(op, owner common.Address) {
		t.Helper()
		p := nativekeeper.GatewayDELStake{Gateway: gateway, WrappedDEL: wrapped, Beneficiary: owner, Validator: op, Amount: sdkmath.NewInt(nativekeeper.NativeDELAtomsPerPower)}
		p.StakeID = crypto.Keccak256Hash(op[:], owner[:], wrapped[:], make([]byte, 64))
		require.NoError(t, k.ApplyGatewayDELDelegation(ctx, p, p.Amount))
	}
	for _, op := range ops {
		delegate(op, founder)
	}
	genesisCtx := ctx.WithBlockHeight(0)
	require.NoError(t, k.InitializeDELProvenance(genesisCtx, gateway, wrapped))
	require.NoError(t, k.InitializeNativeLiveValidatorRoster(genesisCtx))
	updates, err := k.BootstrapStakeABCIConsensus(genesisCtx, 1, roster)
	require.NoError(t, err)
	require.Len(t, updates, 4)
	const id uint64 = 42
	require.NoError(t, a.GovKeeper.VotingPeriodProposals.Set(ctx, id, []byte{1}))
	vote := func(addr common.Address, opt govv1.VoteOption) {
		t.Helper()
		require.NoError(t, a.GovKeeper.AddVote(ctx, id, sdk.AccAddress(addr[:]), govv1.NewNonSplitVoteOption(opt), ""))
	}
	assertEligible := func(want bool, expected [4]common.Address) {
		t.Helper()
		got, err := a.bootstrapProposalUnanimous(ctx, id, expected)
		require.NoError(t, err)
		require.Equal(t, want, got)
	}
	for _, op := range ops[:3] {
		vote(op, govv1.OptionYes)
	}
	assertEligible(false, ops)
	vote(ops[3], govv1.OptionYes)
	assertEligible(false, ops)
	vote(founder, govv1.OptionNo)
	assertEligible(false, ops)
	vote(founder, govv1.OptionYes)
	assertEligible(true, ops)
	alternative := ops
	alternative[3] = common.HexToAddress("0x9876")
	vote(alternative[3], govv1.OptionYes)
	assertEligible(false, alternative)
	if chain == identity.ProfileChainID {
		actual, enabled, err := a.bootstrapOperators(ctx)
		require.NoError(t, err)
		require.True(t, enabled)
		require.ElementsMatch(t, ops[:], actual[:])
	}
	var expected [4]string
	copy(expected[:], roster)
	for i := 1; i <= int(validatortypes.MaxBindingPageSize); i++ {
		owner := common.BigToAddress(big.NewInt(int64(i)))
		delegate(ops[0], owner)
		if i == 16 || i == int(validatortypes.MaxBindingPageSize)-1 || i == int(validatortypes.MaxBindingPageSize) {
			fits, err := k.BootstrapVotingSnapshotFits(ctx, expected)
			require.NoError(t, err)
			require.Equal(t, i < int(validatortypes.MaxBindingPageSize), fits)
		}
	}
	assertEligible(false, ops)
}
