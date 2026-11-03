package module

import (
	"bytes"
	"context"
	"cosmossdk.io/core/store"
	"cosmossdk.io/store/prefix"
	storetypes "cosmossdk.io/store/types"
	"errors"
	"github.com/cosmos/cosmos-sdk/runtime"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/query"
	"github.com/cosmos/gogoproto/proto"
	coinconfig "github.com/maxwell2010/go-smart-node/network/decimal/coin/config"
	"github.com/maxwell2010/go-smart-node/network/decimal/coin/keeper"
	"github.com/maxwell2010/go-smart-node/network/decimal/coin/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type queryServer struct {
	keeper  *keeper.StateKeeper
	service store.KVStoreService
}

func (q queryServer) store(ctx context.Context) storetypes.KVStore {
	return runtime.KVStoreAdapter(q.service.OpenKVStore(sdk.UnwrapSDKContext(ctx)))
}
func queryError(err error) error {
	if _, ok := status.FromError(err); ok {
		return err
	}
	if errors.Is(err, keeper.ErrNotFound) {
		return status.Error(codes.NotFound, err.Error())
	}
	return status.Error(codes.Internal, err.Error())
}
func decode(data []byte, message proto.Message) error {
	if len(data) > 64*1024 {
		return status.Error(codes.ResourceExhausted, "native record too large")
	}
	return proto.Unmarshal(data, message)
}

func page(store storetypes.KVStore, request *query.PageRequest, consume func([]byte, []byte) error) (response *query.PageResponse, err error) {
	if request == nil {
		request = &query.PageRequest{Limit: 50}
	}
	copy := *request
	if copy.Limit == 0 {
		copy.Limit = 50
	}
	if copy.Limit > 50 || copy.Offset != 0 || copy.CountTotal || len(copy.Key) > 256 {
		return nil, status.Error(codes.InvalidArgument, "use key pagination with limit<=50, no offset/count_total")
	}
	var iterator storetypes.Iterator
	if copy.Reverse {
		var end []byte
		if len(copy.Key) > 0 {
			end = append(append([]byte(nil), copy.Key...), 0)
		}
		iterator = store.ReverseIterator(nil, end)
	} else {
		iterator = store.Iterator(copy.Key, nil)
	}
	defer func() {
		err = errors.Join(err, iterator.Close())
		if err != nil {
			response = nil
		}
	}()
	response = &query.PageResponse{}
	var count uint64
	for ; iterator.Valid(); iterator.Next() {
		if e := iterator.Error(); e != nil {
			return nil, e
		}
		if count == copy.Limit {
			response.NextKey = bytes.Clone(iterator.Key())
			break
		}
		if e := consume(iterator.Key(), iterator.Value()); e != nil {
			return nil, e
		}
		count++
	}
	return response, nil
}

func readCheck(data, hash []byte) (types.Check, error) {
	var check types.Check
	if e := decode(data, &check); e != nil {
		return check, e
	}
	if e := check.Validate(); e != nil {
		return check, e
	}
	actual := check.HashFull()
	if !bytes.Equal(actual[:], hash) {
		return check, status.Error(codes.Internal, "redeemed check key mismatch")
	}
	return check, nil
}
func (q queryServer) Check(ctx context.Context, req *types.QueryCheckRequest) (*types.QueryCheckResponse, error) {
	if req == nil || len(req.Hash) != 32 {
		return nil, status.Error(codes.InvalidArgument, "32-byte check hash required")
	}
	data := q.store(ctx).Get(types.GetCheckKey(req.Hash))
	if data == nil {
		return nil, status.Error(codes.NotFound, "redeemed check not found")
	}
	check, e := readCheck(data, req.Hash)
	if e != nil {
		return nil, queryError(e)
	}
	return &types.QueryCheckResponse{Check: check}, nil
}
func (q queryServer) Checks(ctx context.Context, req *types.QueryChecksRequest) (*types.QueryChecksResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "missing request")
	}
	out := &types.QueryChecksResponse{}
	responseBytes := 0
	p, e := page(prefix.NewStore(q.store(ctx), types.GetChecksKey()), req.Pagination, func(key, value []byte) error {
		check, e := readCheck(value, key)
		if e != nil {
			return e
		}
		responseBytes += proto.Size(&check) + 16
		if responseBytes > 2<<20 {
			return status.Error(codes.ResourceExhausted, "checks response exceeds 2 MiB")
		}
		out.Checks = append(out.Checks, check)
		return nil
	})
	if e != nil {
		return nil, e
	}
	out.Pagination = p
	return out, nil
}

func (q queryServer) Params(ctx context.Context, req *types.QueryParamsRequest) (*types.QueryParamsResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "missing request")
	}
	p, e := q.keeper.GetParams(sdk.UnwrapSDKContext(ctx))
	if e != nil {
		return nil, queryError(e)
	}
	return &types.QueryParamsResponse{Params: p}, nil
}
func (q queryServer) Coin(ctx context.Context, req *types.QueryCoinRequest) (*types.QueryCoinResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "missing request")
	}
	if !coinconfig.CoinDenomValidator.MatchString(req.Denom) {
		return nil, status.Error(codes.InvalidArgument, "invalid coin denomination")
	}
	coin, e := q.keeper.GetCoin(sdk.UnwrapSDKContext(ctx), req.Denom)
	if e != nil {
		return nil, queryError(e)
	}
	return &types.QueryCoinResponse{Coin: coin}, nil
}
func (q queryServer) Coins(ctx context.Context, req *types.QueryCoinsRequest) (*types.QueryCoinsResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "missing request")
	}
	out := &types.QueryCoinsResponse{}
	responseBytes := 0
	p, e := page(prefix.NewStore(q.store(ctx), types.GetCoinsKey()), req.Pagination, func(key, value []byte) error {
		coin, e := q.keeper.GetCoin(sdk.UnwrapSDKContext(ctx), string(key))
		if e != nil {
			return e
		}
		responseBytes += proto.Size(&coin) + 16
		if responseBytes > 2<<20 {
			return status.Error(codes.ResourceExhausted, "coins response exceeds 2 MiB")
		}
		out.Coins = append(out.Coins, coin)
		return nil
	})
	if e != nil {
		return nil, e
	}
	out.Pagination = p
	return out, nil
}
