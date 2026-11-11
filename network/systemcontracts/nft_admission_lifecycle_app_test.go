package systemcontracts_test

import (
	"bytes"
	"encoding/json"
	"math/big"
	"testing"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	evmtypes "github.com/cosmos/evm/x/vm/types"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	nftkeeper "github.com/maxwell2010/go-smart-node/network/decimal/nft/keeper"
	nfttypes "github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
	validatorkeeper "github.com/maxwell2010/go-smart-node/network/decimal/validator/keeper"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
	"github.com/stretchr/testify/require"
)

func runSignedNFTAdmissionLifecycle(t *testing.T, e isolatedBackendEnv, genesis signedNFTAdmissionGenesis) {
	t.Helper()
	profile, originAddress, originToken := genesis.Profile, genesis.Origin, genesis.OriginTokenID
	origin, vault, wrapper := e.load("MintCandyIsolatedDRC1155"), e.load("MintCandyNFTPositionVault"), e.load("MintCandyWrappedDEL")
	gateway, err := contracts.LoadLinked("MintCandyIsolatedDelegation",
		map[string]common.Address{"owned/DecimalDelegation.sol:DelegationNFTLib": profile.Library})
	require.NoError(t, err)
	user := crypto.PubkeyToAddress(e.keys[0].PublicKey)
	operator, err := e.app.NativeValidatorKeeper.ValidatorAddressCodec().BytesToString(genesis.Validator.Bytes())
	require.NoError(t, err)
	f := signedDELFixture{t: t, env: e, gateway: gateway, wrapper: wrapper, target: profile.Gateway,
		wrapped: profile.WrappedDEL, user: user, validator: genesis.Validator, beneficiary: sdk.AccAddress(user.Bytes()).String(), operator: operator}
	const quantity = nfttypes.MaxIsolatedOriginUnits
	units := big.NewInt(int64(quantity))
	one := big.NewInt(1)
	position := common.BigToHash(one)
	nftID := common.Hash(f.read(gateway, f.target, "getNFTStakeId", profile.PositionVault, one, user, f.validator, new(big.Int)).([32]byte))
	coinID := common.Hash(f.read(gateway, f.target, "getStakeId", f.validator, user, f.wrapped).([32]byte))
	reserve := sdkmath.NewIntWithDecimal(signedNFTUnitReserveDEL, 18).MulRaw(int64(quantity))
	direct := sdkmath.NewIntWithDecimal(3, 18)
	zero := sdkmath.ZeroInt()
	nftKeeper, validatorKeeper := e.app.NativeNFTKeeper, e.app.NativeValidatorKeeper
	originBalanceMethod := exactABIMethod(t, origin.ABI, "balanceOf(address,uint256)")
	originSupplyMethod := exactABIMethod(t, origin.ABI, "totalSupply(uint256)")
	bankSupply := e.app.BankKeeper.GetSupply(e.ctx(), identity.BaseDenom)
	checkBacking := func(originAmount, vaultAmount sdkmath.Int) {
		t.Helper()
		for _, target := range []struct {
			address common.Address
			want    sdkmath.Int
		}{{originAddress, originAmount}, {profile.PositionVault, vaultAmount}} {
			bank := e.app.BankKeeper.GetBalance(e.ctx(), sdk.AccAddress(target.address.Bytes()), identity.BaseDenom)
			require.True(t, target.want.Equal(bank.Amount))
			require.Zero(t, target.want.BigInt().Cmp(e.app.EVMKeeper.GetBalance(e.ctx(), target.address).ToBig()))
		}
		require.Equal(t, bankSupply, e.app.BankKeeper.GetSupply(e.ctx(), identity.BaseDenom))
	}
	checkOrigin := func(live, burnt uint32) {
		t.Helper()
		for _, method := range []string{originSupplyMethod, originBalanceMethod} {
			args := []interface{}{originToken.Big()}
			if method == originBalanceMethod {
				args = []interface{}{user, originToken.Big()}
			}
			require.Equal(t, int64(live), f.read(origin, originAddress, method, args...).(*big.Int).Int64())
		}
		token, err := nftKeeper.GetToken(e.ctx(), signedNFTNativeOriginID)
		require.NoError(t, err)
		require.Equal(t, uint32(quantity), token.Minted)
		require.Equal(t, burnt, token.Burnt)
		binding, err := nftKeeper.GetExternalNFTBinding(e.ctx(), token.ID)
		require.NoError(t, err)
		require.Equal(t, originToken.Bytes(), binding.TokenID)
		require.Equal(t, uint64(quantity)+1, binding.NextSubTokenID)
	}
	checkCoin := func(collateral sdkmath.Int) {
		t.Helper()
		require.Zero(t, direct.BigInt().Cmp(f.read(gateway, f.target, "getAvailableCoinAmount", f.validator, user, f.wrapped, new(big.Int)).(*big.Int)))
		actual := abi.ConvertType(f.read(gateway, f.target, "getStake", f.validator, user, f.wrapped), new(gatewayDELStake)).(*gatewayDELStake)
		require.Zero(t, direct.Add(collateral).BigInt().Cmp(actual.Amount))
		coin, err := validatorKeeper.GetEVMStakeBinding(e.ctx(), validatortypes.EVMStakeBindingKind_EVM_STAKE_BINDING_COIN, f.target.Bytes(), coinID.Bytes())
		require.NoError(t, err)
		require.True(t, direct.Add(collateral).Equal(coin.RawAmount))
		require.True(t, collateral.Equal(coin.RawNftBackedAmount))
		native, err := validatorKeeper.GetDelegation(e.ctx(), f.beneficiary, f.operator, validatortypes.StakeType_Coin, identity.BaseDenom)
		require.NoError(t, err)
		require.True(t, native.Stake.Equal(validatortypes.NewStakeCoin(sdk.NewCoin(identity.BaseDenom, direct))))
		f.activeReserve(direct.Add(collateral))
		f.backing(direct)
	}
	reportGas := func(route string, response *evmtypes.MsgEthereumTxResponse) {
		t.Helper()
		require.Positive(t, response.GasUsed)
		require.LessOrEqual(t, max(response.GasUsed, response.MaxUsedGas), uint64(signedNFTGasLimit))
		t.Logf("signed NFT gas route=%s origin_units=%d gas_used=%d max_used_gas=%d", route, quantity, response.GasUsed, response.MaxUsedGas)
	}

	require.Empty(t, f.native().Delegations)
	require.Empty(t, f.native().Undelegations)
	f.activeReserve(zero)
	require.Equal(t, one, f.read(vault, profile.PositionVault, "nextPositionId"))
	emptyVault, err := nftKeeper.GetIsolatedVaultState(e.ctx(), profile.PositionVault.Bytes())
	require.NoError(t, err)
	require.Equal(t, position.Bytes(), emptyVault.NextPositionID)
	checkOrigin(quantity, 0)
	for _, id := range []uint32{1, quantity} {
		sub, err := nftKeeper.GetSubToken(e.ctx(), signedNFTNativeOriginID, id)
		require.NoError(t, err)
		require.Equal(t, f.beneficiary, sub.Owner)
	}
	checkBacking(reserve, zero)
	f.backing(zero)
	f.signed("delegateDEL", direct, true, f.validator)
	checkCoin(zero)

	// Missing approval prevents admission and leaves the next position unused.
	delegateArgs := []interface{}{f.validator, originAddress, originToken.Big(), units}
	f.signed("delegateNFT1155", zero, false, delegateArgs...)
	checkOrigin(quantity, 0)
	checkBacking(reserve, zero)
	checkCoin(zero)
	require.Equal(t, one, f.read(vault, profile.PositionVault, "nextPositionId"))
	approval, err := origin.ABI.Pack("setApprovalForAll", f.target, true)
	require.NoError(t, err)
	e.send(e.keys[0], &originAddress, new(big.Int), approval, true)

	// A direct wallet transfer moves the same ERC1155 unit in both ledgers.
	other := crypto.PubkeyToAddress(e.keys[1].PublicKey)
	input, err := origin.ABI.Pack("safeTransferFrom", user, other, originToken.Big(), one, []byte{})
	require.NoError(t, err)
	args, err := json.Marshal(map[string]interface{}{"from": user, "to": originAddress, "input": hexutil.Encode(input), "gasPrice": "0xe8d4a51000"})
	require.NoError(t, err)
	ctx := e.ctx()
	estimate, err := e.app.EVMKeeper.EstimateGas(ctx, &evmtypes.EthCallRequest{
		Args: args, GasCap: signedNFTGasLimit, ProposerAddress: sdk.ConsAddress(ctx.BlockHeader().ProposerAddress),
	})
	require.NoError(t, err)
	require.Empty(t, estimate.VmError)
	require.Greater(t, estimate.Gas, uint64(21_000))
	require.LessOrEqual(t, estimate.Gas, uint64(signedNFTGasLimit))
	checkOrigin(quantity, 0)
	unitBeforeEstimate, err := nftKeeper.GetSubToken(e.ctx(), signedNFTNativeOriginID, 1)
	require.NoError(t, err)
	require.Equal(t, f.beneficiary, unitBeforeEstimate.Owner)
	transferOrigin := func(signer int, from, to common.Address, succeeds bool) {
		t.Helper()
		transfer, err := origin.ABI.Pack("safeTransferFrom", from, to, originToken.Big(), one, []byte{})
		require.NoError(t, err)
		e.nextGasLimit(signedNFTGasLimit)
		e.send(e.keys[signer], &originAddress, new(big.Int), transfer, succeeds)
	}
	transferOrigin(0, user, other, true)
	sub, err := nftKeeper.GetSubToken(e.ctx(), signedNFTNativeOriginID, 1)
	require.NoError(t, err)
	require.Equal(t, sdk.AccAddress(other.Bytes()).String(), sub.Owner)
	require.Equal(t, int64(1), f.read(origin, originAddress, originBalanceMethod, other, originToken.Big()).(*big.Int).Int64())
	transferOrigin(1, other, user, true)
	transferOrigin(0, user, f.target, false)
	sub, err = nftKeeper.GetSubToken(e.ctx(), signedNFTNativeOriginID, 1)
	require.NoError(t, err)
	require.Equal(t, f.beneficiary, sub.Owner)
	require.Zero(t, f.read(origin, originAddress, originBalanceMethod, f.target, originToken.Big()).(*big.Int).Sign())
	checkOrigin(quantity, 0)
	checkBacking(reserve, zero)
	checkCoin(zero)

	admitted := f.signed("delegateNFT1155", zero, true, delegateArgs...)
	reportGas("delegate", admitted)
	f.eventOnce("decimal_nft_delegated", map[string]string{"stake_id": nftID.Hex(), "position_id": position.Hex(), "reserve": reserve.String()})
	checkOrigin(0, quantity)
	checkBacking(zero, reserve)
	checkCoin(reserve)

	for _, id := range []uint32{1, quantity} {
		_, err := nftKeeper.GetSubToken(e.ctx(), signedNFTNativeOriginID, id)
		require.ErrorIs(t, err, nftkeeper.ErrNotFound)
	}
	vaultState, err := nftKeeper.GetIsolatedVaultState(e.ctx(), profile.PositionVault.Bytes())
	require.NoError(t, err)
	require.Equal(t, common.BigToHash(big.NewInt(2)).Bytes(), vaultState.NextPositionID)
	require.Equal(t, big.NewInt(2), f.read(vault, profile.PositionVault, "nextPositionId"))
	lineage, err := nftKeeper.GetIsolatedLineage(e.ctx(), profile.PositionVault.Bytes(), position.Bytes())
	require.NoError(t, err)
	require.Equal(t, originAddress.Bytes(), lineage.OriginContract)
	require.Equal(t, originToken.Bytes(), lineage.OriginTokenID)
	require.Equal(t, signedNFTNativeOriginID, lineage.OriginNativeTokenID)
	require.Equal(t, make([]byte, 32), lineage.ParentPositionID)
	expectedIDs := make([]uint32, quantity)
	for i := range expectedIDs {
		expectedIDs[i] = uint32(i + 1)
	}
	require.Equal(t, expectedIDs, lineage.OriginSubTokenIDs)
	f.eventOnce("decimal_nft_isolated", map[string]string{"position_id": position.Hex(), "origin_token_id": signedNFTNativeOriginID,
		"receipt_token_id": lineage.ReceiptNativeTokenID, "quantity": units.String(), "reserve": reserve.String()})
	require.Equal(t, nfttypes.IsolatedReserveSnapshot{Kind: nfttypes.IsolatedReserveDEL, Token: make([]byte, 20), Amount: common.BigToHash(reserve.BigInt()).Bytes()}, lineage.Reserve)
	receiptToken, err := nftKeeper.GetToken(e.ctx(), lineage.ReceiptNativeTokenID)
	require.NoError(t, err)
	require.True(t, receiptToken.IsolatedReceipt)
	require.Equal(t, sdk.NewCoin(identity.BaseDenom, reserve), receiptToken.Reserve)
	require.Equal(t, uint32(1), receiptToken.Minted)
	require.Zero(t, receiptToken.Burnt)
	require.Equal(t, uint32(1), lineage.ReceiptSubTokenID)

	checkPosition := func(owner common.Address) {
		t.Helper()
		require.Equal(t, owner, f.read(vault, profile.PositionVault, "ownerOf", one))
		require.Equal(t, one, f.read(vault, profile.PositionVault, "totalSupply"))
		row := abi.ConvertType(f.read(vault, profile.PositionVault, "getPosition", one), new(isolatedPosition)).(*isolatedPosition)
		require.Equal(t, owner, row.PhysicalOwner)
		require.Equal(t, originAddress, row.OriginContract)
		require.Equal(t, originToken.Big(), row.OriginTokenId)
		require.Equal(t, uint8(1), row.OriginStandard)
		require.Equal(t, units, row.OriginQuantity)
		require.Equal(t, isolatedReserve{Amount: reserve.BigInt(), ReserveType: 1}, row.Reserve)
	}
	checkNativeOwner := func(owner common.Address) {
		t.Helper()
		sub, err := nftKeeper.GetSubToken(e.ctx(), lineage.ReceiptNativeTokenID, lineage.ReceiptSubTokenID)
		require.NoError(t, err)
		require.Equal(t, sdk.AccAddress(owner.Bytes()).String(), sub.Owner)
	}
	checkPosition(f.target)
	checkNativeOwner(f.target)
	nftKind := validatortypes.EVMStakeBindingKind_EVM_STAKE_BINDING_NFT
	stake, err := validatorKeeper.GetEVMStakeBinding(e.ctx(), nftKind, f.target.Bytes(), nftID.Bytes())
	require.NoError(t, err)
	require.True(t, stake.Active)
	require.Equal(t, coinID.Bytes(), stake.CoinCompanionStakeID)
	require.True(t, reserve.Equal(stake.ReportedReserveAmount))
	require.NotNil(t, stake.NFTPosition)
	require.Equal(t, expectedIDs, stake.NFTPosition.Origin.NativeSubTokenIDs)
	require.Equal(t, lineage.ReceiptNativeTokenID, stake.NFTPosition.Custody.NativeTokenID)
	require.Equal(t, position.Bytes(), stake.NFTPosition.PositionID)
	nativeNFT, err := validatorKeeper.GetDelegation(e.ctx(), f.beneficiary, f.operator, validatortypes.StakeType_NFT, lineage.ReceiptNativeTokenID)
	require.NoError(t, err)
	require.True(t, nativeNFT.Stake.Equal(validatortypes.NewStakeNFT(lineage.ReceiptNativeTokenID, []uint32{lineage.ReceiptSubTokenID}, receiptToken.Reserve)))

	topUp := sdkmath.NewIntWithDecimal(2, 18)
	f.signed("delegateDEL", topUp, true, f.validator)
	direct = direct.Add(topUp)
	checkCoin(reserve)
	f.signedBy(1, "withdrawNFT", zero, false, f.validator, profile.PositionVault, one)
	checkPosition(f.target)
	checkNativeOwner(f.target)
	checkBacking(zero, reserve)
	checkCoin(reserve)
	require.Empty(t, f.native().Undelegations)

	request := f.signed("withdrawNFT", zero, true, f.validator, profile.PositionVault, one)
	reportGas("withdraw", request)
	frozenID := f.withdrawalIndex(request, coinID)
	frozenKey := common.BigToHash(frozenID)
	f.eventOnce("decimal_nft_withdrawal_requested", map[string]string{
		"stake_id": nftID.Hex(), "position_id": position.Hex(), "reserve": reserve.String(), "frozen_index": frozenKey.Hex(),
	})
	_, err = validatorKeeper.GetEVMStakeBinding(e.ctx(), nftKind, f.target.Bytes(), nftID.Bytes())
	require.ErrorIs(t, err, validatorkeeper.ErrNotFound)
	_, err = validatorKeeper.GetDelegation(e.ctx(), f.beneficiary, f.operator, validatortypes.StakeType_NFT, lineage.ReceiptNativeTokenID)
	require.ErrorIs(t, err, validatorkeeper.ErrNotFound)
	checkCoin(zero)
	pending, err := validatorKeeper.GetEVMFrozenBinding(e.ctx(), f.target.Bytes(), frozenKey.Bytes())
	require.NoError(t, err)
	require.True(t, pending.Active)
	require.Equal(t, nftID.Bytes(), pending.SourceStakeID)
	require.Equal(t, f.user.Bytes(), pending.Beneficiary)
	require.Equal(t, stake.NFTPosition, pending.NFTPosition)
	require.True(t, reserve.Equal(pending.ReportedReserveAmount))
	queue, err := validatorKeeper.GetUndelegation(e.ctx(), f.beneficiary, f.operator)
	require.NoError(t, err)
	require.Len(t, queue.Entries, 1)
	require.True(t, queue.Entries[0].Stake.Equal(nativeNFT.Stake))
	deadline := queue.Entries[0].CompletionTime.UTC()
	frozenRow := func() *gatewayFrozenWithdrawal {
		t.Helper()
		return abi.ConvertType(f.read(gateway, f.target, "getFrozenStake", frozenID), new(gatewayFrozenWithdrawal)).(*gatewayFrozenWithdrawal)
	}
	require.Equal(t, uint8(1), frozenRow().FreezeStatus)
	require.Equal(t, deadline.Unix(), frozenRow().UnfreezeTimestamp.Int64())
	f.signedBy(2, "complete", zero, false, []*big.Int{frozenID})
	for i := 0; e.ctx().BlockTime().Before(deadline); i++ {
		require.Less(t, i, signedDELFreezeSeconds)
		e.advance()
	}
	require.Equal(t, deadline, e.ctx().BlockTime())

	// The EVM transfer succeeds, then native projection rejects a missing
	// lineage. The same block's automatic attempt must defer this exact claim.
	nftStoreKey := e.app.GetKey(nfttypes.StoreKey)
	lineageKey := nfttypes.GetIsolatedPositionKey(profile.PositionVault.Bytes(), position.Bytes())
	receiptKey := nfttypes.GetSubTokenKey(lineage.ReceiptNativeTokenID, lineage.ReceiptSubTokenID)
	store := e.ctx().KVStore(nftStoreKey)
	savedLineage := bytes.Clone(store.Get(lineageKey))
	savedReceipt := bytes.Clone(store.Get(receiptKey))
	require.NotEmpty(t, savedLineage)
	require.NotEmpty(t, savedReceipt)
	store.Delete(lineageKey)
	restored := false
	t.Cleanup(func() {
		if !restored {
			e.ctx().KVStore(nftStoreKey).Set(lineageKey, savedLineage)
		}
	})
	previousDeferrals := len(e.deferrals())
	rejected := f.signedBy(2, "complete", zero, false, []*big.Int{frozenID})
	reportGas("late_rollback", rejected)
	require.Contains(t, rejected.VmError, "project withdrawal assets")
	require.Len(t, e.deferrals(), previousDeferrals+1)
	require.Contains(t, e.deferrals()[previousDeferrals], frozenKey.Hex()[2:])
	for _, event := range e.events() {
		require.NotEqual(t, "decimal_withdrawal_completed", event.Type)
	}
	require.Equal(t, uint8(1), frozenRow().FreezeStatus)
	checkPosition(f.target)
	checkBacking(zero, reserve)
	checkCoin(zero)
	require.Equal(t, savedReceipt, e.ctx().KVStore(nftStoreKey).Get(receiptKey))
	retained, err := validatorKeeper.GetEVMFrozenBinding(e.ctx(), f.target.Bytes(), frozenKey.Bytes())
	require.NoError(t, err)
	require.Equal(t, pending, retained)
	retainedQueue, err := validatorKeeper.GetUndelegation(e.ctx(), f.beneficiary, f.operator)
	require.NoError(t, err)
	require.Equal(t, queue, retainedQueue)

	e.ctx().KVStore(nftStoreKey).Set(lineageKey, savedLineage)
	restored = true
	completed := f.signedBy(2, "complete", zero, true, []*big.Int{frozenID})
	reportGas("complete_retry", completed)
	f.eventOnce("decimal_withdrawal_completed", map[string]string{"frozen_index": frozenKey.Hex()})
	for _, event := range e.blockEvents() {
		require.NotEqual(t, "decimal_withdrawal_completed", event.Type)
	}
	closed, err := validatorKeeper.GetEVMFrozenBinding(e.ctx(), f.target.Bytes(), frozenKey.Bytes())
	require.NoError(t, err)
	pending.Active, pending.UpdatedHeight = false, e.ctx().BlockHeight()-1
	require.Equal(t, pending, closed)
	require.Empty(t, f.native().Undelegations)
	require.Zero(t, frozenRow().FreezeStatus)
	checkPosition(user)
	checkNativeOwner(user)
	checkOrigin(0, quantity)
	checkBacking(zero, reserve)
	checkCoin(zero)
	actualLineage, err := nftKeeper.GetIsolatedLineage(e.ctx(), lineage.Vault, lineage.PositionID)
	require.NoError(t, err)
	require.Equal(t, lineage, actualLineage)
	f.signedBy(2, "complete", zero, false, []*big.Int{frozenID})
	require.Len(t, e.deferrals(), previousDeferrals+1)
	checkNativeOwner(user)
	checkBacking(zero, reserve)
	checkCoin(zero)
	transferReceipt, err := vault.ABI.Pack("transferFrom", user, other, one)
	require.NoError(t, err)
	e.nextGasLimit(signedNFTGasLimit)
	e.send(e.keys[0], &profile.PositionVault, new(big.Int), transferReceipt, true)
	checkPosition(other)
	checkNativeOwner(other)
	checkBacking(zero, reserve)
	checkCoin(zero)
	t.Log(signedNFTAdmissionPassed)
}
