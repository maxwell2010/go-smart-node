package app

import (
	"context"
	"encoding/json"
	"testing"

	sdkmath "cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/authz"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	genutiltypes "github.com/cosmos/cosmos-sdk/x/genutil/types"
	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types/v1"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	evmtypes "github.com/cosmos/evm/x/vm/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/vm"
	nfttypes "github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

func TestNativeConsensusCircuit(t *testing.T) {
	ctx := context.Background()
	ordinary := &App{}
	admission := &App{withdrawals: &withdrawalController{profile: WithdrawalProfile{
		SettlementOnly: true, EnableDELDelegation: true,
	}}}
	native := &App{withdrawals: &withdrawalController{profile: WithdrawalProfile{
		SettlementOnly: true, EnableDELDelegation: true, EnableNativeConsensus: true,
	}}}
	modes := []struct {
		name   string
		app    *App
		ctx    context.Context
		native bool
	}{
		{"ordinary", ordinary, ctx, false},
		{"ordinary DEL profile", admission, ctx, false},
		{"native genesis", ordinary, context.WithValue(ctx, nativeConsensusInitKey{}, true), true},
		{"committed native profile", native, ctx, true},
	}
	for _, mode := range modes {
		t.Run(mode.name, func(t *testing.T) {
			circuit := nativeConsensusCircuit{app: mode.app}
			for _, msg := range []sdk.Msg{&evmtypes.MsgEthereumTx{}, &banktypes.MsgSend{}, &banktypes.MsgMultiSend{}} {
				allowed, err := circuit.IsAllowed(mode.ctx, sdk.MsgTypeURL(msg))
				require.NoError(t, err)
				require.True(t, allowed, "%T", msg)
			}
			for _, msg := range []sdk.Msg{&stakingtypes.MsgDelegate{}, &govtypes.MsgSubmitProposal{}, &authz.MsgExec{}} {
				allowed, err := circuit.IsAllowed(mode.ctx, sdk.MsgTypeURL(msg))
				require.NoError(t, err)
				require.Equal(t, !mode.native, allowed, "%T", msg)
			}
			allowed, err := circuit.IsAllowed(mode.ctx, "/test.Message")
			require.NoError(t, err)
			require.Equal(t, !mode.native, allowed)
		})
	}
}

type nativeCircuitProbeState struct {
	vm.StateDB
	ctx sdk.Context
}

func (s nativeCircuitProbeState) GetContext() sdk.Context { return s.ctx }

type nativeCircuitProbePrecompile struct {
	addr     common.Address
	readOnly []bool
}

func (p *nativeCircuitProbePrecompile) Address() common.Address   { return p.addr }
func (p *nativeCircuitProbePrecompile) RequiredGas([]byte) uint64 { return 97 }
func (p *nativeCircuitProbePrecompile) Run(_ *vm.EVM, _ *vm.Contract, readOnly bool) ([]byte, error) {
	p.readOnly = append(p.readOnly, readOnly)
	return []byte("probe"), nil
}

func TestNativeConsensusEconomicsPrecompileGuard(t *testing.T) {
	ctx := sdk.Context{}.WithContext(context.Background())
	genesisCtx := ctx.WithValue(nativeConsensusInitKey{}, true)
	evmFor := func(ctx sdk.Context) *vm.EVM {
		return &vm.EVM{StateDB: nativeCircuitProbeState{ctx: ctx}}
	}
	app := &App{}
	addresses := []string{
		evmtypes.StakingPrecompileAddress, evmtypes.DistributionPrecompileAddress,
		evmtypes.ICS20PrecompileAddress, evmtypes.GovPrecompileAddress, evmtypes.SlashingPrecompileAddress,
	}
	bankAddr := common.HexToAddress(evmtypes.BankPrecompileAddress)
	bank := &nativeCircuitProbePrecompile{addr: bankAddr}
	precompiles := map[common.Address]vm.PrecompiledContract{bankAddr: bank}
	probes := make(map[common.Address]*nativeCircuitProbePrecompile)
	for _, address := range addresses {
		addr := common.HexToAddress(address)
		probe := &nativeCircuitProbePrecompile{addr: addr}
		probes[addr] = probe
		precompiles[addr] = probe
	}
	app.guardSDKEconomicsPrecompiles(precompiles)
	require.Same(t, bank, precompiles[bankAddr])
	for addr, probe := range probes {
		t.Run(addr.Hex(), func(t *testing.T) {
			app.withdrawals = nil
			guard := precompiles[addr]
			require.Equal(t, addr, guard.Address())
			require.Equal(t, uint64(97), guard.RequiredGas(nil))
			out, err := guard.Run(evmFor(ctx), nil, true)
			require.NoError(t, err)
			require.Equal(t, []byte("probe"), out)
			_, err = guard.Run(evmFor(genesisCtx), nil, false)
			require.ErrorIs(t, err, vm.ErrExecutionReverted)
			app.withdrawals = &withdrawalController{profile: WithdrawalProfile{EnableNativeConsensus: true}}
			for _, readOnly := range []bool{false, true} {
				out, err := guard.Run(evmFor(ctx), nil, readOnly)
				require.ErrorIs(t, err, vm.ErrExecutionReverted)
				require.Nil(t, out)
			}
			_, err = guard.Run(&vm.EVM{}, nil, false)
			require.ErrorIs(t, err, vm.ErrExecutionReverted)
			require.Equal(t, []bool{true}, probe.readOnly)
		})
	}
	out, err := precompiles[bankAddr].Run(evmFor(ctx), nil, false)
	require.NoError(t, err)
	require.Equal(t, []byte("probe"), out)
}

func TestNativeConsensusRejectsSDKEconomicGenesis(t *testing.T) {
	cdc := MakeEncodingConfig(990001).Codec
	app := &App{appCodec: cdc}
	sdkValidator, err := stakingtypes.NewValidator("operator", &ed25519.PubKey{Key: make([]byte, ed25519.PubKeySize)}, stakingtypes.Description{})
	require.NoError(t, err)
	for _, test := range []struct {
		name string
		edit func(*stakingtypes.GenesisState, *genutiltypes.GenesisState)
		want string
	}{
		{"wrong denom", func(staking *stakingtypes.GenesisState, _ *genutiltypes.GenesisState) {
			staking.Params.BondDenom = "other"
		}, "empty SDK staking ledger in DEL"},
		{"prior power", func(staking *stakingtypes.GenesisState, _ *genutiltypes.GenesisState) {
			staking.LastTotalPower = sdkmath.OneInt()
		}, "empty SDK staking ledger in DEL"},
		{"prior validator power", func(staking *stakingtypes.GenesisState, _ *genutiltypes.GenesisState) {
			staking.LastValidatorPowers = []stakingtypes.LastValidatorPower{{Address: "operator", Power: 1}}
		}, "empty SDK staking ledger in DEL"},
		{"SDK validator", func(staking *stakingtypes.GenesisState, _ *genutiltypes.GenesisState) {
			staking.Validators = []stakingtypes.Validator{sdkValidator}
		}, "empty SDK staking ledger in DEL"},
		{"SDK stake", func(staking *stakingtypes.GenesisState, _ *genutiltypes.GenesisState) {
			staking.Delegations = []stakingtypes.Delegation{stakingtypes.NewDelegation("delegator", "operator", sdkmath.LegacyOneDec())}
		}, "empty SDK staking ledger in DEL"},
		{"SDK unbonding", func(staking *stakingtypes.GenesisState, _ *genutiltypes.GenesisState) {
			staking.UnbondingDelegations = []stakingtypes.UnbondingDelegation{{DelegatorAddress: "delegator", ValidatorAddress: "operator"}}
		}, "empty SDK staking ledger in DEL"},
		{"SDK redelegation", func(staking *stakingtypes.GenesisState, _ *genutiltypes.GenesisState) {
			staking.Redelegations = []stakingtypes.Redelegation{{
				DelegatorAddress: "delegator", ValidatorSrcAddress: "operator", ValidatorDstAddress: "other-operator",
			}}
		}, "empty SDK staking ledger in DEL"},
		{"genesis transaction", func(_ *stakingtypes.GenesisState, genutil *genutiltypes.GenesisState) {
			genutil.GenTxs = []json.RawMessage{json.RawMessage(`{}`)}
		}, "does not admit SDK genesis transactions"},
	} {
		t.Run(test.name, func(t *testing.T) {
			staking := stakingtypes.DefaultGenesisState()
			staking.Params.BondDenom = identity.BaseDenom
			staking.LastTotalPower = sdkmath.ZeroInt()
			genutil := genutiltypes.DefaultGenesisState()
			test.edit(staking, genutil)
			genesis := GenesisState{
				stakingtypes.ModuleName: cdc.MustMarshalJSON(staking),
				genutiltypes.ModuleName: cdc.MustMarshalJSON(genutil),
			}
			result, err := app.initNativeConsensusModules(sdk.Context{}, genesis, nil, 1)
			require.ErrorContains(t, err, test.want)
			require.Nil(t, result)
		})
	}
}

func TestNativeConsensusValidatesGenesisSnapshot(t *testing.T) {
	profile := WithdrawalProfile{EnableDELDelegation: true, EnableNativeConsensus: true}
	for _, test := range []struct {
		name string
		edit func(*validatortypes.GenesisState)
		want string
	}{
		{name: "structurally valid native snapshot"},
		{"missing active positions", func(state *validatortypes.GenesisState) {
			state.EVMStakeBindings = nil
			state.Delegations = nil
		}, "active ordinary DEL positions"},
		{"unmatched ledger", func(state *validatortypes.GenesisState) {
			state.Delegations = nil
		}, "active ordinary DEL positions"},
		{"pending claim", func(state *validatortypes.GenesisState) {
			state.EVMFrozenBindings = []validatortypes.EVMFrozenBinding{{}}
		}, "active ordinary DEL positions"},
		{"precomputed power", func(state *validatortypes.GenesisState) {
			state.Validators[0].Stake = 1
		}, "precomputed native power or mismatched eligibility"},
		{"offline signer", func(state *validatortypes.GenesisState) {
			state.Validators[0].Online = false
		}, "precomputed native power or mismatched eligibility"},
		{"jailed signer", func(state *validatortypes.GenesisState) {
			state.Validators[0].Jailed = true
		}, "precomputed native power or mismatched eligibility"},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := &validatortypes.GenesisState{
				BindingDomain:    &validatortypes.EVMSourceDomain{},
				Validators:       []validatortypes.Validator{{Status: validatortypes.BondStatus_Unbonded, Online: true}},
				EVMStakeBindings: []validatortypes.EVMStakeBinding{{}},
				Delegations:      []validatortypes.Delegation{{}},
			}
			if test.edit == nil {
				require.NoError(t, validateWithdrawalGenesisSnapshot(profile, &nfttypes.GenesisState{}, state))
				ordinary := WithdrawalProfile{EnableDELDelegation: true}
				require.ErrorContains(t, validateWithdrawalGenesisSnapshot(ordinary, &nfttypes.GenesisState{}, state), "cannot import active positions")
				return
			}
			test.edit(state)
			require.ErrorContains(t, validateWithdrawalGenesisSnapshot(profile, &nfttypes.GenesisState{}, state), test.want)
		})
	}
}
