package module

import (
	"strings"
	"testing"

	storetypes "cosmossdk.io/store/types"
	"github.com/cosmos/cosmos-sdk/codec/address"
	"github.com/cosmos/cosmos-sdk/runtime"
	"github.com/cosmos/cosmos-sdk/testutil"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/keeper"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestPairQueriesDistinguishAbsentRowsAndInvalidAddresses(t *testing.T) {
	key := storetypes.NewKVStoreKey(types.StoreKey)
	ctx := testutil.DefaultContext(key, storetypes.NewTransientStoreKey("transient"))
	k, err := keeper.NewStateKeeper(runtime.NewKVStoreService(key))
	require.NoError(t, err)
	require.NoError(t, k.InitGenesis(ctx, types.DefaultGenesisState()))
	q := queryServer{keeper: k}
	operator, err := address.NewBech32Codec(identity.Bech32PrefixValAddr).BytesToString(make([]byte, 20))
	require.NoError(t, err)
	owner, err := address.NewBech32Codec(identity.Bech32PrefixAccAddr).BytesToString(make([]byte, 20))
	require.NoError(t, err)
	rows, err := q.Delegations(ctx, &types.QueryDelegationsRequest{Validator: operator, Delegator: owner})
	require.NoError(t, err)
	require.Empty(t, rows.Delegations)
	_, err = q.Undelegation(ctx, &types.QueryUndelegationRequest{Validator: operator, Delegator: owner})
	require.Equal(t, codes.NotFound, status.Code(err))
	_, err = q.Undelegation(ctx, &types.QueryUndelegationRequest{Validator: owner, Delegator: operator})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	_, err = q.Delegations(ctx, &types.QueryDelegationsRequest{Validator: operator, Delegator: strings.ToUpper(owner)})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}
