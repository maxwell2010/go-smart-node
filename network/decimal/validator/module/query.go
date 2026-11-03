package module

import (
	"context"
	"errors"

	"github.com/cosmos/cosmos-sdk/codec/address"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/keeper"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Unconnected financial queries are explicitly Unimplemented. SDK staking
// records are never presented as native Decimal validator records.
type queryServer struct {
	types.UnimplementedQueryServer
	keeper *keeper.StateKeeper
}

func queryError(err error) error {
	if errors.Is(err, keeper.ErrNotFound) {
		return status.Error(codes.NotFound, err.Error())
	}
	return status.Error(codes.Internal, err.Error())
}

func (q queryServer) Params(ctx context.Context, req *types.QueryParamsRequest) (*types.QueryParamsResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "missing native validator params request")
	}
	p, err := q.keeper.GetParams(sdk.UnwrapSDKContext(ctx))
	if err != nil {
		return nil, queryError(err)
	}
	return &types.QueryParamsResponse{Params: p}, nil
}

func (q queryServer) Validator(ctx context.Context, req *types.QueryValidatorRequest) (*types.QueryValidatorResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "missing native validator request")
	}
	decoded, err := address.NewBech32Codec(identity.Bech32PrefixValAddr).StringToBytes(req.Validator)
	if err != nil || len(decoded) != 20 {
		return nil, status.Error(codes.InvalidArgument, "native validator operator must be a 20-byte d0valoper address")
	}
	v, err := q.keeper.GetValidator(sdk.UnwrapSDKContext(ctx), req.Validator)
	if err != nil {
		return nil, queryError(err)
	}
	return &types.QueryValidatorResponse{Validator: v}, nil
}

func (q queryServer) HistoricalInfo(ctx context.Context, req *types.QueryHistoricalInfoRequest) (*types.QueryHistoricalInfoResponse, error) {
	if req == nil || req.Height < 1 {
		return nil, status.Error(codes.InvalidArgument, "positive native history height required")
	}
	h, err := q.keeper.GetHistoricalInfo(ctx, req.Height)
	if err != nil {
		return nil, queryError(err)
	}
	return &types.QueryHistoricalInfoResponse{Hist: &h}, nil
}
