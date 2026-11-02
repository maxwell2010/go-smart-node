package governance

import (
	"context"
	"errors"

	sdk "github.com/cosmos/cosmos-sdk/types"
	canonicalgov "github.com/cosmos/cosmos-sdk/x/gov"
	govkeeper "github.com/cosmos/cosmos-sdk/x/gov/keeper"
	v1 "github.com/cosmos/cosmos-sdk/x/gov/types/v1"
)

// EndBlock wraps the unmodified SDK module in a transactional session scope.
// The supplied keeper must have been constructed with this Sessions reader.
func (r *Sessions) EndBlock(ctx sdk.Context, keeper *govkeeper.Keeper) error {
	if keeper == nil {
		return errors.New("governance keeper is missing")
	}
	return r.Run(ctx, func(s sdk.Context) error { return canonicalgov.EndBlocker(s, keeper) })
}

type scopedQueryServer struct {
	v1.QueryServer
	sessions *Sessions
}

// QueryServer forwards canonical queries and scopes TallyResult in an always
// discarded SDK cache, because canonical live tally removes its vote records.
func (r *Sessions) QueryServer(keeper *govkeeper.Keeper) (v1.QueryServer, error) {
	if r == nil || r.native == nil || keeper == nil {
		return nil, errors.New("governance reader and keeper are required")
	}
	return scopedQueryServer{QueryServer: govkeeper.NewQueryServer(keeper), sessions: r}, nil
}
func (q scopedQueryServer) TallyResult(ctx context.Context, request *v1.QueryTallyResultRequest) (*v1.QueryTallyResultResponse, error) {
	s, e := sdkContext(ctx)
	if e != nil {
		return nil, e
	}
	var result *v1.QueryTallyResultResponse
	e = q.sessions.ReadOnly(s, func(scoped sdk.Context) error {
		var callErr error
		result, callErr = q.QueryServer.TallyResult(scoped, request)
		return callErr
	})
	if e != nil {
		return nil, e
	}
	return result, nil
}
