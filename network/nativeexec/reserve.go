package nativeexec

import (
	"errors"

	sdk "github.com/cosmos/cosmos-sdk/types"
	coinkeeper "github.com/maxwell2010/go-smart-node/network/decimal/coin/keeper"
)

type reservePreparer struct{ hook *coinkeeper.ReserveHook }

var _ Prepared = (*coinkeeper.ReservePlan)(nil)

// NewReservePreparer adapts existing registered nonbase coin metadata plans.
// It does not admit new currencies, update native base supply or mount routes.
func NewReservePreparer(hook *coinkeeper.ReserveHook) (Preparer, error) {
	if hook == nil {
		return nil, errors.New("native reserve preparer requires an authenticated reserve hook")
	}
	return reservePreparer{hook: hook}, nil
}

func (p reservePreparer) Prepare(ctx sdk.Context, execution Execution) (Prepared, error) {
	if execution.EntryPoint != SignedEthereum && execution.EntryPoint != Governance {
		return nil, errors.New("reserve preparer entry point has no reviewed adapter")
	}
	return p.hook.Prepare(ctx, execution.Receipt)
}
