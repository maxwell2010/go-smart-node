package authority

import (
	"errors"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

const MaxBlockWork uint64 = 32_000_000

type sessionKey struct{}
type executionSession struct {
	chain     string
	height    int64
	remaining uint64
	closed    bool
}

// RunGovernance scopes the shared work budget to one canonical EndBlock pass.
// Reservations survive rejected proposal caches; they are not committed state.
// Queries and direct message-server calls have no execution session by default.
func RunGovernance(ctx sdk.Context, run func(sdk.Context) error) error {
	if run == nil || ctx.BlockHeight() <= 0 || ctx.ChainID() == "" {
		return errors.New("invalid governance execution context")
	}
	if ctx.Value(sessionKey{}) != nil {
		return errors.New("nested governance execution session")
	}
	session := &executionSession{chain: ctx.ChainID(), height: ctx.BlockHeight(), remaining: MaxBlockWork}
	defer func() { session.closed = true }()
	return run(ctx.WithValue(sessionKey{}, session))
}

func reserve(ctx sdk.Context, work uint64) error {
	session, ok := ctx.Value(sessionKey{}).(*executionSession)
	if !ok || session == nil || session.closed || session.chain != ctx.ChainID() || session.height != ctx.BlockHeight() {
		return errors.New("system execution requires an active same-block governance session")
	}
	if work > session.remaining {
		return errors.New("governance system execution block budget exhausted")
	}
	session.remaining -= work
	return nil
}
