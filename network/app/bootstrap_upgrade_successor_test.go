//go:build mintcandy_testnet && mintcandy_bootstrap_unanimity

package app

import (
	"fmt"
	"testing"
	"time"

	sdkmath "cosmossdk.io/math"
	upgradetypes "cosmossdk.io/x/upgrade/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	sdk "github.com/cosmos/cosmos-sdk/types"
	govv1 "github.com/cosmos/cosmos-sdk/x/gov/types/v1"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	nativekeeper "github.com/maxwell2010/go-smart-node/network/decimal/validator/keeper"
	nativetypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

func TestBootstrapUnanimityUpgradeApproval(t *testing.T) {
	a := testApp(t, identity.TestnetChainID, identity.TestnetEVMChainID)
	a.withdrawals = &withdrawalController{profile: WithdrawalProfile{SettlementOnly: true, EnableDELDelegation: true, EnableNativeConsensus: true, EnableNativeGovernance: true, EnableNativeUpgradeGovernance: true}}
	now := time.Unix(2010, 0).UTC()
	parent := a.NewUncachedContext(false, cmtproto.Header{ChainID: identity.TestnetChainID, Height: 1, Time: now})
	ctx, _ := parent.CacheContext()
	ctx = ctx.WithExecMode(sdk.ExecModeFinalize)
	k := a.NativeValidatorKeeper
	domain, ok := k.ConfiguredEVMSourceDomain()
	require.True(t, ok)
	genesis := nativetypes.DefaultGenesisState()
	genesis.BindingDomain = &domain
	for i := 1; i <= 4; i++ {
		addr := common.BytesToAddress([]byte{byte(i)})
		key := ed25519.GenPrivKeyFromSecret([]byte(fmt.Sprintf("bootstrap-test-%d", i)))
		v, err := nativetypes.NewValidator(sdk.ValAddress(addr.Bytes()).String(), sdk.AccAddress(addr.Bytes()).String(), key.PubKey(), nativetypes.Description{Moniker: fmt.Sprintf("operator-%d", i)}, sdkmath.LegacyZeroDec())
		require.NoError(t, err)
		genesis.Validators = append(genesis.Validators, v)
	}
	require.NoError(t, k.InitGenesis(ctx, genesis))
	gateway, wrapped := common.HexToAddress("0x1111"), common.HexToAddress("0x2222")
	addr := common.BytesToAddress([]byte{1})
	pos := nativekeeper.GatewayDELStake{Gateway: gateway, WrappedDEL: wrapped, Beneficiary: addr, Validator: addr, Amount: sdkmath.NewInt(nativekeeper.NativeDELAtomsPerPower)}
	var words [64]byte
	pos.StakeID = crypto.Keccak256Hash(addr[:], addr[:], wrapped[:], words[:])
	require.NoError(t, k.ApplyGatewayDELDelegation(ctx, pos, pos.Amount))
	genesisCtx := ctx.WithBlockHeight(0)
	require.NoError(t, k.InitializeDELProvenance(genesisCtx, gateway, wrapped))
	require.NoError(t, k.InitializeNativeLiveValidatorRoster(genesisCtx))
	_, eligible, err := a.bootstrapOperators(ctx)
	require.NoError(t, err)
	require.True(t, eligible)

	at := ctx.WithBlockHeight(50)
	plan := upgradetypes.Plan{Name: bootstrapUnanimityPlan, Height: 50, Info: `{"schema":"mintcandy-sdk-upgrade/v1","proposal_id":2}`}
	metadata := fmt.Sprintf("%s:{\"name\":%q,\"height\":50}", nativeUpgradeSchema, plan.Name)
	proposal := govv1.Proposal{Id: 2, Status: govv1.StatusPassed, FinalTallyResult: &govv1.TallyResult{}, Metadata: metadata}
	require.NoError(t, a.GovKeeper.ProposalID.Set(ctx, 7))
	require.NoError(t, a.GovKeeper.Proposals.Set(ctx, 2, proposal))
	require.NoError(t, a.UpgradeKeeper.ScheduleUpgrade(ctx, plan))
	from := a.ModuleManager.GetVersionMap()
	_, err = a.activateBootstrapUnanimity(ctx, plan, from)
	require.Error(t, err)
	copyCtx, _ := at.CacheContext()
	proposal.Status = govv1.StatusRejected
	require.NoError(t, a.GovKeeper.Proposals.Set(copyCtx, 2, proposal))
	_, err = a.activateBootstrapUnanimity(copyCtx, plan, from)
	require.ErrorContains(t, err, "no matching passed proposal")
	unchanged, err := a.activateBootstrapUnanimity(at, plan, from)
	require.NoError(t, err)
	require.Equal(t, from, unchanged)
	minimum, err := k.BootstrapUnanimityMinimumID(at)
	require.NoError(t, err)
	require.Equal(t, uint64(7), minimum)
	_, err = a.activateBootstrapUnanimity(at, plan, from)
	require.ErrorContains(t, err, "already consumed")
}
