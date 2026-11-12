package settlement

import (
	"context"
	"math/big"
	"testing"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/stretchr/testify/require"
)

func TestCustodiedOrdinaryCheckpointRequiresMaturityAndOlderClaim(t *testing.T) {
	deadline := time.Unix(2000, 0).UTC()
	ctx := sdk.Context{}.WithContext(context.Background()).WithBlockHeight(10).WithBlockTime(deadline)
	scope := &penaltySourceScope{active: true}
	active := ctx.WithValue(penaltySourceKey{}, scope)
	row := validatortypes.EVMFrozenBinding{
		CreatedHeight: 6, CompletionTimestamp: common.BigToHash(big.NewInt(deadline.Unix())).Bytes(),
	}

	require.NoError(t, requireCustodiedOrdinaryCheckpoint(active, row, 7))
	require.NoError(t, requireCustodiedOrdinaryCheckpoint(active.WithBlockTime(deadline.Add(time.Second)), row, 7))
	require.ErrorContains(t, requireCustodiedOrdinaryCheckpoint(active.WithBlockTime(deadline.Add(-time.Nanosecond)), row, 7), "mature claim")
	require.ErrorContains(t, requireCustodiedOrdinaryCheckpoint(active, row, 6), "predating the infraction")
	require.ErrorContains(t, requireCustodiedOrdinaryCheckpoint(active, row, 5), "predating the infraction")
	require.ErrorContains(t, requireCustodiedOrdinaryCheckpoint(ctx, row, 7), "active Master reconciliation")
	require.ErrorContains(t, requireCustodiedOrdinaryCheckpoint(ctx.WithValue(penaltySourceKey{}, &penaltySourceScope{active: true, infractionHeight: 7}), row, 7), "active Master reconciliation")
}
