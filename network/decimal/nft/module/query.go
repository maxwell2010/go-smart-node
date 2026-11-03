package module

import (
	"bytes"
	"context"
	"cosmossdk.io/core/store"
	"cosmossdk.io/store/prefix"
	storetypes "cosmossdk.io/store/types"
	"errors"
	"github.com/cosmos/cosmos-sdk/codec/address"
	"github.com/cosmos/cosmos-sdk/runtime"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/query"
	"github.com/cosmos/gogoproto/proto"
	"github.com/maxwell2010/go-smart-node/network/decimal/nft/keeper"
	"github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"strconv"
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

func (q queryServer) collections(ctx context.Context, prefixKey []byte, req *query.PageRequest) ([]types.Collection, *query.PageResponse, error) {
	var out []types.Collection
	remaining := 10000
	responseBytes := 0
	p, e := page(prefix.NewStore(q.store(ctx), prefixKey), req, func(key, value []byte) error {
		var record types.Collection
		if e := decode(value, &record); e != nil {
			return e
		}
		collection, e := q.keeper.GetCollection(sdk.UnwrapSDKContext(ctx), record.Creator, record.Denom)
		if e != nil {
			return e
		}
		creator, e := address.NewBech32Codec(identity.Bech32PrefixAccAddr).StringToBytes(record.Creator)
		if e != nil {
			return e
		}
		if !bytes.Equal(append(append([]byte(nil), prefixKey...), key...), types.GetCollectionKey(sdk.AccAddress(creator), record.Denom)) {
			return status.Error(codes.Internal, "collection key mismatch")
		}
		if len(prefixKey) > len(types.GetCollectionsKey()) {
			collection, e = q.hydrateCollection(ctx, collection, remaining)
			if e != nil {
				return e
			}
			remaining -= len(collection.Tokens)
		}
		responseBytes += proto.Size(&collection) + 16
		if responseBytes > 2<<20 {
			return status.Error(codes.ResourceExhausted, "collections response exceeds 2 MiB")
		}
		out = append(out, collection)
		return nil
	})
	return out, p, e
}
func (q queryServer) Collections(ctx context.Context, req *types.QueryCollectionsRequest) (*types.QueryCollectionsResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "missing request")
	}
	rows, p, e := q.collections(ctx, types.GetCollectionsKey(), req.Pagination)
	if e != nil {
		return nil, e
	}
	return &types.QueryCollectionsResponse{Collections: rows, Pagination: p}, nil
}
func (q queryServer) CollectionsByCreator(ctx context.Context, req *types.QueryCollectionsByCreatorRequest) (*types.QueryCollectionsByCreatorResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "missing request")
	}
	creator, e := address.NewBech32Codec(identity.Bech32PrefixAccAddr).StringToBytes(req.Creator)
	if e != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid creator")
	}
	rows, p, e := q.collections(ctx, types.GetCollectionsByCreatorKey(sdk.AccAddress(creator)), req.Pagination)
	if e != nil {
		return nil, e
	}
	return &types.QueryCollectionsByCreatorResponse{Collections: rows, Pagination: p}, nil
}
func (q queryServer) Collection(ctx context.Context, req *types.QueryCollectionRequest) (*types.QueryCollectionResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "missing request")
	}
	collection, e := q.keeper.GetCollection(sdk.UnwrapSDKContext(ctx), req.Creator, req.Denom)
	if e != nil {
		return nil, queryError(e)
	}
	collection, e = q.hydrateCollection(ctx, collection, 10000)
	if e != nil {
		return nil, e
	}
	return &types.QueryCollectionResponse{Collection: collection}, nil
}
func (q queryServer) hydrateCollection(ctx context.Context, collection types.Collection, remaining int) (result types.Collection, err error) {
	if int(collection.Supply) > remaining {
		return result, status.Error(codes.ResourceExhausted, "collection token count exceeds complete-response limit")
	}
	creator, e := address.NewBech32Codec(identity.Bech32PrefixAccAddr).StringToBytes(collection.Creator)
	if e != nil {
		return result, e
	}
	prefixKey := types.GetTokensByCollectionKey(sdk.AccAddress(creator), collection.Denom)
	iterator := prefix.NewStore(q.store(ctx), prefixKey).Iterator(nil, nil)
	defer func() {
		err = errors.Join(err, iterator.Close())
		if err != nil {
			result = types.Collection{}
		}
	}()
	size := proto.Size(&collection)
	for ; iterator.Valid(); iterator.Next() {
		if e := iterator.Error(); e != nil {
			return result, e
		}
		if len(collection.Tokens) >= remaining {
			return result, status.Error(codes.ResourceExhausted, "collection token count exceeds complete-response limit")
		}
		token, e := q.keeper.GetToken(sdk.UnwrapSDKContext(ctx), string(iterator.Value()))
		if e != nil {
			return result, e
		}
		if token.Creator != collection.Creator || token.Denom != collection.Denom || !bytes.Equal(append(append([]byte(nil), prefixKey...), iterator.Key()...), types.GetTokenByCollectionKey(sdk.AccAddress(creator), collection.Denom, token.ID)) {
			return result, status.Error(codes.Internal, "collection token index mismatch")
		}
		size += proto.Size(&token) + 16
		if size > 2<<20 {
			return result, status.Error(codes.ResourceExhausted, "collection response exceeds 2 MiB")
		}
		collection.Tokens = append(collection.Tokens, &token)
	}
	if uint64(len(collection.Tokens)) != uint64(collection.Supply) {
		return result, status.Error(codes.Internal, "collection token count mismatch")
	}
	return collection, nil
}

func (q queryServer) SubToken(ctx context.Context, req *types.QuerySubTokenRequest) (*types.QuerySubTokenResponse, error) {
	if req == nil || req.TokenId == "" || len(req.TokenId) > 1024 {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	index, e := strconv.ParseUint(req.SubTokenId, 10, 32)
	if e != nil || index == 0 || strconv.FormatUint(index, 10) != req.SubTokenId {
		return nil, status.Error(codes.InvalidArgument, "canonical positive uint32 sub-token ID required")
	}
	sub, e := q.keeper.GetSubToken(sdk.UnwrapSDKContext(ctx), req.TokenId, uint32(index))
	if e != nil {
		return nil, queryError(e)
	}
	if sub.Reserve == nil {
		token, e := q.keeper.GetToken(sdk.UnwrapSDKContext(ctx), req.TokenId)
		if e != nil {
			return nil, queryError(e)
		}
		sub.Reserve = &token.Reserve
	}
	return &types.QuerySubTokenResponse{SubToken: sub}, nil
}
func (q queryServer) Token(ctx context.Context, req *types.QueryTokenRequest) (response *types.QueryTokenResponse, err error) {
	if req == nil || req.TokenId == "" || len(req.TokenId) > 1024 {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	token, e := q.keeper.GetToken(sdk.UnwrapSDKContext(ctx), req.TokenId)
	if e != nil {
		return nil, queryError(e)
	}
	responseBytes := proto.Size(&token)
	iterator := prefix.NewStore(q.store(ctx), types.GetSubTokensKey(req.TokenId)).Iterator(nil, nil)
	defer func() {
		err = errors.Join(err, iterator.Close())
		if err != nil {
			response = nil
		}
	}()
	for ; iterator.Valid(); iterator.Next() {
		if e := iterator.Error(); e != nil {
			return nil, e
		}
		if len(token.SubTokens) >= 10000 {
			return nil, status.Error(codes.ResourceExhausted, "token exceeds complete-response limit; query individual sub-tokens")
		}
		var record types.SubToken
		if e := decode(iterator.Value(), &record); e != nil {
			return nil, e
		}
		sub, e := q.keeper.GetSubToken(sdk.UnwrapSDKContext(ctx), req.TokenId, record.ID)
		if e != nil {
			return nil, queryError(e)
		}
		if !bytes.Equal(append(types.GetSubTokensKey(req.TokenId), iterator.Key()...), types.GetSubTokenKey(req.TokenId, record.ID)) {
			return nil, status.Error(codes.Internal, "sub-token key mismatch")
		}
		if sub.Reserve == nil {
			sub.Reserve = &token.Reserve
		}
		responseBytes += proto.Size(&sub) + 16
		if responseBytes > 2<<20 {
			return nil, status.Error(codes.ResourceExhausted, "token response exceeds 2 MiB")
		}
		token.SubTokens = append(token.SubTokens, &sub)
	}
	if uint64(len(token.SubTokens)) != uint64(token.Minted-token.Burnt) {
		return nil, status.Error(codes.Internal, "active sub-token count mismatch")
	}
	return &types.QueryTokenResponse{Token: token}, nil
}
