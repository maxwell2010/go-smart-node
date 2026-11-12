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

func TestCustodiedOrdinaryPenaltyRequiresMaturityAndOrdinaryScope(t *testing.T) {
	deadline := time.Unix(2000, 0).UTC()
	ctx := sdk.Context{}.WithContext(context.Background()).WithBlockHeight(10).WithBlockTime(deadline)
	scope := &penaltySourceScope{active: true}
	active := ctx.WithValue(penaltySourceKey{}, scope)
	row := validatortypes.EVMFrozenBinding{
		CreatedHeight: 6, CompletionTimestamp: common.BigToHash(big.NewInt(deadline.Unix())).Bytes(),
	}

	require.NoError(t, requireCustodiedOrdinaryPenalty(active, row, 7), "older claim advances without a debit")
	require.NoError(t, requireCustodiedOrdinaryPenalty(active.WithBlockHeight(7).WithBlockTime(deadline.Add(time.Second)), row, 6), "a later timestamp can mature an equal-height claim in the very next block")
	require.NoError(t, requireCustodiedOrdinaryPenalty(active, row, 5), "a newer claim remains ordinarily eligible")
	require.ErrorContains(t, requireCustodiedOrdinaryPenalty(active.WithBlockTime(deadline.Add(-time.Nanosecond)), row, 7), "mature claim")
	require.ErrorContains(t, requireCustodiedOrdinaryPenalty(active, row, 0), "valid infraction height")
	require.ErrorContains(t, requireCustodiedOrdinaryPenalty(active, row, 11), "valid infraction height")
	require.ErrorContains(t, requireCustodiedOrdinaryPenalty(ctx, row, 7), "active Master reconciliation")
	require.ErrorContains(t, requireCustodiedOrdinaryPenalty(ctx.WithValue(penaltySourceKey{}, &penaltySourceScope{active: true, infractionHeight: 7}), row, 7), "active Master reconciliation")
}
