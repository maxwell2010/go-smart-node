package app

import (
	"fmt"
	"testing"
	"time"

	upgradetypes "cosmossdk.io/x/upgrade/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	govv1 "github.com/cosmos/cosmos-sdk/x/gov/types/v1"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

func TestDRC20IssuanceGovernance(t *testing.T) {
	metadata := func(name string) string {
		return fmt.Sprintf("%s:{\"name\":%q,\"height\":70}", nativeUpgradeSchema, name)
	}
	oldPlan, err := parseNativeUpgradeProposal(metadata(nativeSDKMigrationPlan))
	require.NoError(t, err)
	require.Equal(t, nativeSDKMigrationPlan, oldPlan.Name)
	m, err := parseNativeUpgradeProposal(metadata(drc20IssuancePlan))
	if identity.ProfileChainID != identity.MainnetChainID {
		require.ErrorContains(t, err, "listed handler")
		return
	}
	require.NoError(t, err)
	require.Equal(t, int64(70), m.Height)
	app := testApp(t, identity.MainnetChainID, identity.MainnetEVMChainID)
	app.withdrawals = &withdrawalController{profile: WithdrawalProfile{EnableNativeGovernance: true, EnableNativeUpgradeGovernance: true}}
	ctx := app.NewUncachedContext(false, cmtproto.Header{ChainID: identity.MainnetChainID, Height: 60, Time: time.Unix(1000, 0)}).WithExecMode(sdk.ExecModeFinalize)
	require.Error(t, app.validateNativeUpgradeProposal(ctx.WithChainID(identity.TestnetChainID), metadata(drc20IssuancePlan)))
	require.Error(t, app.validateNativeUpgradeProposal(ctx.WithBlockHeight(70), metadata(drc20IssuancePlan)))
	require.NoError(t, app.validateNativeUpgradeProposal(ctx, metadata(drc20IssuancePlan)))
	p := govv1.Proposal{Id: 9, Status: govv1.StatusPassed, Expedited: true, Metadata: metadata(drc20IssuancePlan), FinalTallyResult: &govv1.TallyResult{YesCount: "100"}}
	require.NoError(t, app.GovKeeper.Proposals.Set(ctx, p.Id, p))
	require.NoError(t, app.scheduleNativeUpgrade(ctx, p, *m))
	plan, err := app.UpgradeKeeper.GetUpgradePlan(ctx)
	require.NoError(t, err)
	require.Equal(t, drc20IssuancePlan, plan.Name)
	require.Error(t, app.verifyNativeUpgradeApproval(ctx, plan, drc20IssuancePlan))
	at := ctx.WithBlockHeight(plan.Height)
	require.NoError(t, app.verifyNativeUpgradeApproval(at, plan, drc20IssuancePlan))
	require.Error(t, app.verifyNativeUpgradeApproval(at, plan, nativeSDKMigrationPlan))
	forged := upgradetypes.Plan{Name: plan.Name, Height: plan.Height, Info: "{}"}
	require.Error(t, app.verifyNativeUpgradeApproval(at, forged, drc20IssuancePlan))
	p.Status = govv1.StatusRejected
	require.NoError(t, app.GovKeeper.Proposals.Set(ctx, p.Id, p))
	require.Error(t, app.verifyNativeUpgradeApproval(at, plan, drc20IssuancePlan))
}
