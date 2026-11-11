package keeper

import (
	"fmt"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

func TestPairDelegationsReadsExactPairAndRefusesTruncation(t *testing.T) {
	ctx, _, k := bindingEnvironment(t, false)
	owner, operator := addr(t, identity.Bech32PrefixAccAddr, 2), addr(t, identity.Bech32PrefixValAddr, 1)
	other := addr(t, identity.Bech32PrefixAccAddr, 3)
	for i := range maxPairDelegations {
		row := types.Delegation{Delegator: owner, Validator: operator, Stake: types.NewStakeCoin(sdk.NewInt64Coin(fmt.Sprintf("del%d", i), 1))}
		require.NoError(t, k.put(ctx, delegationKey(row), &row))
	}
	otherRow := types.Delegation{Delegator: other, Validator: operator, Stake: types.NewStakeCoin(sdk.NewInt64Coin("del", 3))}
	require.NoError(t, k.put(ctx, delegationKey(otherRow), &otherRow))
	rows, err := k.GetPairDelegations(ctx, owner, operator)
	require.NoError(t, err)
	require.Len(t, rows, maxPairDelegations)
	rows, err = k.GetPairDelegations(ctx, other, operator)
	require.NoError(t, err)
	require.Equal(t, []types.Delegation{otherRow}, rows)
	rows, err = k.GetPairDelegations(ctx, owner, addr(t, identity.Bech32PrefixValAddr, 2))
	require.NoError(t, err)
	require.Empty(t, rows)

	otherRow.Delegator = owner
	require.NoError(t, k.put(ctx, delegationKey(otherRow), &otherRow))
	rows, err = k.GetPairDelegations(ctx, owner, operator)
	require.ErrorIs(t, err, ErrDelegationQueryLimit)
	require.Nil(t, rows)
}
