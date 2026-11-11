package systemcontracts_test

import (
	"bytes"
	"fmt"
	"math/big"
	"testing"
	"time"

	sdkmath "cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/evm/x/vm/statedb"
	evmtypes "github.com/cosmos/evm/x/vm/types"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/crypto"
	ownedapp "github.com/maxwell2010/go-smart-node/network/app"
	nfttypes "github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
	"github.com/stretchr/testify/require"
)

// Run in the existing Gov-owned App fixture after its DEL completion tests.
// The contracts and signed calls are real. Native admission uses fixture rows
// derived from the actual position and withdrawal receipts.
func runSharedWithdrawalNFT(t *testing.T, e isolatedBackendEnv, profile ownedapp.WithdrawalProfile, gateway, origin, vault contracts.Artifact, originAddress, validator common.Address) {
	t.Helper()
	for _, quantity := range []uint32{1, nfttypes.MaxIsolatedOriginUnits} {
		t.Run(fmt.Sprintf("NFT_%d_origin_units", quantity), func(t *testing.T) {
			runSharedWithdrawalNFTQuantity(t, e, profile, gateway, origin, vault, originAddress, validator, quantity)
		})
	}
}

func runSharedWithdrawalNFTQuantity(t *testing.T, e isolatedBackendEnv, profile ownedapp.WithdrawalProfile, gateway, origin, vault contracts.Artifact, originAddress, validator common.Address, quantity uint32) {
	t.Helper()
	require.NotNil(t, e.advance)
	if quantity == nfttypes.MaxIsolatedOriginUnits {
		prepareWithdrawalGenesisFixtureClock(t, e)
	}
	beneficiary := crypto.PubkeyToAddress(e.keys[0].PublicKey)
	beneficiaryNative := sdk.AccAddress(beneficiary.Bytes()).String()
	escrowNative := sdk.AccAddress(profile.Gateway.Bytes()).String()
	unitReserve := sdkmath.NewIntWithDecimal(2, 18)
	mintQuantity := big.NewInt(int64(quantity))
	mintReserve := unitReserve.MulRaw(int64(quantity))
	originID := new(big.Int).Add(new(big.Int).Lsh(big.NewInt(1), 224), big.NewInt(650+int64(quantity)))
	zero := new(big.Int)
	call := func(key int, artifact contracts.Artifact, target common.Address, value *big.Int, success bool, method string, args ...interface{}) *evmtypes.MsgEthereumTxResponse {
		t.Helper()
		data, err := artifact.ABI.Pack(method, args...)
		require.NoError(t, err)
		return e.send(e.keys[key], &target, value, data, success)
	}
	read := func(artifact contracts.Artifact, target common.Address, method string, args ...interface{}) interface{} {
		t.Helper()
		data, err := artifact.ABI.Pack(method, args...)
		require.NoError(t, err)
		ctx, _ := e.ctx().CacheContext()
		ctx = ctx.WithGasMeter(storetypes.NewGasMeter(3_000_000))
		message := core.Message{From: beneficiary, To: &target, Value: new(big.Int), GasLimit: 500_000, GasPrice: new(big.Int), GasFeeCap: new(big.Int), GasTipCap: new(big.Int), Data: data, SkipNonceChecks: true, SkipFromEOACheck: true}
		response, err := e.app.EVMKeeper.ApplyMessage(ctx, statedb.New(ctx, e.app.EVMKeeper, statedb.NewEmptyTxConfig()), message, nil, false, false, true)
		require.NoError(t, err)
		require.Empty(t, response.VmError)
		used := max(response.GasUsed, response.MaxUsedGas)
		require.LessOrEqual(t, used, uint64(500_000))
		ctx.GasMeter().ConsumeGas(used, "NFT withdrawal fixture read")
		values, err := artifact.ABI.Unpack(method, response.Ret)
		require.NoError(t, err)
		require.Len(t, values, 1)
		return values[0]
	}
	vaultBank := func() sdk.Coin {
		return e.app.BankKeeper.GetBalance(e.ctx(), sdk.AccAddress(profile.PositionVault.Bytes()), identity.BaseDenom)
	}
	beforeVaultBacking := vaultBank()
	balanceOfOrigin := exactABIMethod(t, origin.ABI, "balanceOf(address,uint256)")
	call(0, origin, originAddress, mintReserve.BigInt(), true, "mintByDEL", beneficiary, originID, mintQuantity, "ipfs://shared-withdrawal-nft")
	require.Equal(t, mintQuantity, read(origin, originAddress, balanceOfOrigin, beneficiary, originID))
	call(0, origin, originAddress, zero, true, "setApprovalForAll", profile.Gateway, true)
	positionID := new(big.Int).Set(read(vault, profile.PositionVault, "nextPositionId").(*big.Int))
	call(0, gateway, profile.Gateway, zero, true, "delegateNFT1155", validator, originAddress, originID, mintQuantity)
	require.Zero(t, read(origin, originAddress, balanceOfOrigin, beneficiary, originID).(*big.Int).Sign())
	require.Equal(t, profile.Gateway, read(vault, profile.PositionVault, "ownerOf", positionID))
	actualPosition := abi.ConvertType(read(vault, profile.PositionVault, "getPosition", positionID), new(isolatedPosition)).(*isolatedPosition)
	require.Equal(t, originAddress, actualPosition.OriginContract)
	require.Equal(t, originID, actualPosition.OriginTokenId)
	require.Equal(t, uint8(1), actualPosition.OriginStandard)
	require.Equal(t, mintQuantity, actualPosition.OriginQuantity)
	require.Equal(t, profile.Gateway, actualPosition.PhysicalOwner)
	require.Equal(t, uint8(1), actualPosition.Status)
	require.Equal(t, isolatedReserve{Amount: mintReserve.BigInt(), ReserveType: 1}, actualPosition.Reserve)
	amount := sdkmath.NewIntFromBigInt(actualPosition.Reserve.Amount)
	require.True(t, vaultBank().Amount.Sub(beforeVaultBacking.Amount).Equal(amount))
	created := e.ctx()
	request := call(0, gateway, profile.Gateway, zero, true, "withdrawNFT", validator, profile.PositionVault, positionID)
	var frozenID, nftFrozenID *big.Int
	var nftStakeID common.Hash
	var freeze gatewayFrozenWithdrawal
	withdrawRequest := gateway.ABI.Events["WithdrawRequest"]
	nftUndelegated := gateway.ABI.Events["NFTUndelegated"]
	for _, event := range request.Logs {
		if event == nil || common.HexToAddress(event.Address) != profile.Gateway || len(event.Topics) == 0 {
			continue
		}
		switch common.HexToHash(event.Topics[0]) {
		case withdrawRequest.ID:
			require.Nil(t, frozenID)
			values, err := withdrawRequest.Inputs.NonIndexed().Unpack(event.Data)
			require.NoError(t, err)
			require.Len(t, values, 3)
			frozenID = values[1].(*big.Int)
			freeze = *abi.ConvertType(values[2], new(gatewayFrozenWithdrawal)).(*gatewayFrozenWithdrawal)
		case nftUndelegated.ID:
			require.Nil(t, nftFrozenID)
			require.Len(t, event.Topics, 2)
			values, err := nftUndelegated.Inputs.NonIndexed().Unpack(event.Data)
			require.NoError(t, err)
			require.Len(t, values, 1)
			nftStakeID = common.HexToHash(event.Topics[1])
			nftFrozenID = values[0].(*big.Int)
		}
	}
	require.NotNil(t, frozenID)
	require.Equal(t, frozenID, nftFrozenID)
	require.NotEqual(t, common.Hash{}, nftStakeID)
	require.Equal(t, uint8(1), freeze.FreezeType)
	require.Equal(t, uint8(1), freeze.FreezeStatus)
	require.Equal(t, uint8(2), freeze.Stake.TokenType)
	require.Equal(t, big.NewInt(1), freeze.Stake.Amount)
	require.Equal(t, positionID, freeze.Stake.TokenId)
	require.Equal(t, profile.PositionVault, freeze.Stake.Token)
	require.Equal(t, validator, freeze.Stake.Validator)
	require.Equal(t, beneficiary, freeze.Stake.Delegator)
	require.True(t, freeze.UnfreezeTimestamp.IsInt64())
	deadline := time.Unix(freeze.UnfreezeTimestamp.Int64(), 0).UTC()
	frozenFor := deadline.Sub(created.BlockTime())
	require.Contains(t, []time.Duration{20 * time.Second, 75 * time.Second}, frozenFor)
	position := common.BigToHash(positionID)
	originWord := common.BigToHash(originID)
	index := common.BigToHash(frozenID)
	uri := read(vault, profile.PositionVault, "tokenURI", positionID).(string)
	nextPosition := common.BigToHash(read(vault, profile.PositionVault, "nextPositionId").(*big.Int))
	domain, ok := e.app.NativeValidatorKeeper.ConfiguredEVMSourceDomain()
	require.True(t, ok)
	originName := fmt.Sprintf("withdrawal_origin_%d", quantity)
	receiptName := fmt.Sprintf("withdrawal_receipt_%d", quantity)
	const originDenom, receiptDenom = "withdrawal_origin", "withdrawal_vault"
	vaultCreator := sdk.AccAddress(e.govOwner.Bytes()).String()
	reserve := sdk.NewCoin(identity.BaseDenom, amount)
	originUnits := make([]uint32, actualPosition.OriginQuantity.Uint64())
	for i := range originUnits {
		originUnits[i] = uint32(i + 1)
	}
	lineage := nfttypes.IsolatedPositionLineage{
		Vault: profile.PositionVault.Bytes(), PositionID: position.Bytes(), ReceiptNativeTokenID: receiptName, ReceiptSubTokenID: 1,
		OriginContract: originAddress.Bytes(), OriginTokenID: originWord.Bytes(), OriginNativeTokenID: originName, OriginSubTokenIDs: originUnits,
		ParentPositionID: make([]byte, 32), Reserve: nfttypes.IsolatedReserveSnapshot{Kind: nfttypes.IsolatedReserveDEL, Token: make([]byte, 20), Amount: common.BigToHash(amount.BigInt()).Bytes()},
	}
	nftGenesis, err := e.app.NativeNFTKeeper.ExportGenesis(e.ctx())
	require.NoError(t, err)
	addToken := func(creator, denom string, standard nfttypes.NftType, address common.Address, token *nfttypes.Token) {
		for i := range nftGenesis.Collections {
			collection := &nftGenesis.Collections[i]
			if collection.Creator != creator || collection.Denom != denom {
				continue
			}
			require.Equal(t, standard, collection.TypeNft)
			require.Equal(t, address, common.HexToAddress(collection.AddressDRC))
			collection.Tokens = append(collection.Tokens, token)
			collection.Supply++
			return
		}
		nftGenesis.Collections = append(nftGenesis.Collections, nfttypes.Collection{
			Creator: creator, Denom: denom, Supply: 1, TypeNft: standard, AddressDRC: address.Hex(), Tokens: nfttypes.Tokens{token},
		})
	}
	addToken(beneficiaryNative, originDenom, nfttypes.NftType_NFT1155, originAddress, &nfttypes.Token{
		Creator: beneficiaryNative, Denom: originDenom, ID: originName, URI: uri, Reserve: sdk.NewCoin(identity.BaseDenom, unitReserve), Minted: quantity, Burnt: quantity,
	})
	addToken(vaultCreator, receiptDenom, nfttypes.NftType_NFT721, profile.PositionVault, &nfttypes.Token{
		Creator: vaultCreator, Denom: receiptDenom, ID: receiptName, URI: uri, Reserve: reserve, Minted: 1, IsolatedReceipt: true, SubTokens: nfttypes.SubTokens{{ID: 1, Owner: escrowNative}},
	})
	nftGenesis.ExternalBindings = append(nftGenesis.ExternalBindings,
		nfttypes.ExternalNFTBinding{ChainID: domain.ChainID, EVMChainID: domain.EVMChainID, Contract: originAddress.Bytes(), TokenID: originWord.Bytes(), NativeTokenID: originName, NFTType: nfttypes.NftType_NFT1155, NextSubTokenID: uint64(quantity) + 1},
		nfttypes.ExternalNFTBinding{ChainID: domain.ChainID, EVMChainID: domain.EVMChainID, Contract: profile.PositionVault.Bytes(), TokenID: position.Bytes(), NativeTokenID: receiptName, NFTType: nfttypes.NftType_NFT721, NextSubTokenID: 2},
	)
	nftGenesis.IsolatedVaults = []nfttypes.IsolatedVaultState{{ChainID: domain.ChainID, EVMChainID: domain.EVMChainID, Vault: profile.PositionVault.Bytes(), NextPositionID: nextPosition.Bytes(), ReceiptCreator: vaultCreator, ReceiptDenom: receiptDenom}}
	nftGenesis.IsolatedPositions = append(nftGenesis.IsolatedPositions, lineage)
	require.NoError(t, nftGenesis.Validate())
	replaceWithdrawalFixtureStore(t, e, nfttypes.StoreKey, func(ctx sdk.Context) error { return e.app.NativeNFTKeeper.InitGenesis(ctx, nftGenesis) })

	operator, err := e.app.NativeValidatorKeeper.ValidatorAddressCodec().BytesToString(validator.Bytes())
	require.NoError(t, err)
	_, err = e.app.NativeValidatorKeeper.GetValidator(e.ctx(), operator)
	require.NoError(t, err)
	entry := validatortypes.UndelegationEntry{CreationHeight: created.BlockHeight(), CompletionTime: deadline, Stake: validatortypes.NewStakeNFT(receiptName, []uint32{1}, reserve)}
	entryHash, err := validatortypes.NativeQueueEntryHash(1, beneficiaryNative, operator, "", &entry)
	require.NoError(t, err)
	binding := validatortypes.EVMFrozenBinding{
		Gateway: profile.Gateway.Bytes(), FrozenIndex: index.Bytes(), SourceKind: validatortypes.EVMStakeBindingKind_EVM_STAKE_BINDING_NFT,
		SourceStakeID: nftStakeID.Bytes(), SourceValidator: validator.Bytes(), Beneficiary: beneficiary.Bytes(), FreezeType: 1,
		CompletionTimestamp: common.BigToHash(freeze.UnfreezeTimestamp).Bytes(), RawAmount: sdkmath.OneInt(), ReportedReserveAmount: amount,
		ReserveAsset: &validatortypes.EVMReserveAsset{ReserveKind: 1, Token: make([]byte, 20), NativeDenom: identity.BaseDenom},
		NFTPosition: &validatortypes.NFTPositionIdentity{
			Mode:    validatortypes.NFTPositionMode_NFT_POSITION_ISOLATED_RECEIPT,
			Origin:  &validatortypes.EVMNFTReference{Contract: originAddress.Bytes(), TokenID: originWord.Bytes(), NativeTokenID: originName, NativeSubTokenIDs: originUnits, Standard: 1155},
			Custody: &validatortypes.EVMNFTReference{Contract: profile.PositionVault.Bytes(), TokenID: position.Bytes(), NativeTokenID: receiptName, NativeSubTokenIDs: []uint32{1}, Standard: 721},
			Vault:   profile.PositionVault.Bytes(), PositionID: position.Bytes(), ParentPositionID: make([]byte, 32), OriginQuantity: sdkmath.NewIntFromBigInt(actualPosition.OriginQuantity), ReceiptQuantity: sdkmath.OneInt(),
		},
		NativeTarget:    &validatortypes.NativeStakeTarget{StakeType: validatortypes.StakeType_NFT, ID: receiptName, SubTokenIDs: []uint32{1}},
		NativeEntryHash: entryHash, Active: true, CreatedHeight: created.BlockHeight(), UpdatedHeight: created.BlockHeight(),
	}
	validators, err := e.app.NativeValidatorKeeper.ExportGenesis(e.ctx())
	require.NoError(t, err)
	require.Empty(t, validators.Undelegations, "the earlier DEL fixture must have completed")
	validators.Undelegations = []validatortypes.Undelegation{{Delegator: beneficiaryNative, Validator: operator, Entries: []validatortypes.UndelegationEntry{entry}}}
	validators.EVMFrozenBindings = append(validators.EVMFrozenBindings, binding)
	replaceWithdrawalFixtureStore(t, e, validatortypes.StoreKey, func(ctx sdk.Context) error { return e.app.NativeValidatorKeeper.InitGenesis(ctx, validators) })
	if quantity == nfttypes.MaxIsolatedOriginUnits {
		exportWithdrawalGenesisFixture(t, e, profile, binding, beneficiary, deadline)
	}

	backing := vaultBank()
	assertEVM := func(owner common.Address, status uint8) {
		t.Helper()
		require.Equal(t, owner, read(vault, profile.PositionVault, "ownerOf", positionID))
		actualReserve := abi.ConvertType(read(vault, profile.PositionVault, "getReserve", positionID), new(isolatedReserve)).(*isolatedReserve)
		require.Equal(t, isolatedReserve{Amount: amount.BigInt(), ReserveType: 1}, *actualReserve)
		actualFrozen := abi.ConvertType(read(gateway, profile.Gateway, "getFrozenStake", frozenID), new(gatewayFrozenWithdrawal)).(*gatewayFrozenWithdrawal)
		require.Equal(t, status, actualFrozen.FreezeStatus)
		require.Equal(t, backing, vaultBank())
	}
	completions := func() int {
		t.Helper()
		count := 0
		for _, event := range e.events() {
			if event.Type != "decimal_withdrawal_completed" {
				continue
			}
			for _, attribute := range event.Attributes {
				if attribute.Key == "frozen_index" && attribute.Value == index.Hex() {
					count++
				}
			}
		}
		return count
	}
	for i := 0; e.ctx().BlockTime().Before(deadline); i++ {
		require.Less(t, i, int(frozenFor/time.Second), "the fixture clock must advance one second per block")
		e.advance()
		require.Zero(t, completions())
	}
	require.Equal(t, deadline, e.ctx().BlockTime())
	assertEVM(profile.Gateway, 1)
	nftStoreKey := e.app.GetKey(nfttypes.StoreKey)
	lineageKey := nfttypes.GetIsolatedPositionKey(profile.PositionVault.Bytes(), position.Bytes())
	subKey := nfttypes.GetSubTokenKey(receiptName, 1)
	store := e.ctx().KVStore(nftStoreKey)
	savedLineage := bytes.Clone(store.Get(lineageKey))
	savedSub := bytes.Clone(store.Get(subKey))
	require.NotEmpty(t, savedLineage)
	require.NotEmpty(t, savedSub)
	store.Delete(lineageKey)
	restored := false
	t.Cleanup(func() {
		if !restored {
			e.ctx().KVStore(nftStoreKey).Set(lineageKey, savedLineage)
		}
	})
	e.nextGasLimit(15_000_000)
	failed := call(2, gateway, profile.Gateway, zero, false, "complete", []*big.Int{frozenID})
	require.Contains(t, failed.VmError, "project withdrawal assets", "the signed EVM transfer reached native projection before rejection")
	require.Empty(t, failed.Logs)
	require.Zero(t, completions())
	assertEVM(profile.Gateway, 1)
	require.Equal(t, savedSub, e.ctx().KVStore(nftStoreKey).Get(subKey))
	pending, err := e.app.NativeValidatorKeeper.GetEVMFrozenBinding(e.ctx(), binding.Gateway, binding.FrozenIndex)
	require.NoError(t, err)
	require.Equal(t, binding, pending)
	stillQueued, err := e.app.NativeValidatorKeeper.ExportGenesis(e.ctx())
	require.NoError(t, err)
	require.Equal(t, validators.Undelegations, stillQueued.Undelegations)

	e.ctx().KVStore(nftStoreKey).Set(lineageKey, savedLineage)
	restored = true
	e.advance()
	require.Equal(t, 1, completions(), "automatic retry completes the same ID once")
	assertEVM(beneficiary, 0)
	nativeSub, err := e.app.NativeNFTKeeper.GetSubToken(e.ctx(), receiptName, 1)
	require.NoError(t, err)
	require.Equal(t, beneficiaryNative, nativeSub.Owner)
	nativeToken, err := e.app.NativeNFTKeeper.GetToken(e.ctx(), receiptName)
	require.NoError(t, err)
	require.Equal(t, reserve, nativeToken.Reserve)
	exactLineage, err := e.app.NativeNFTKeeper.GetIsolatedLineage(e.ctx(), lineage.Vault, lineage.PositionID)
	require.NoError(t, err)
	require.Equal(t, lineage, exactLineage)
	closed, err := e.app.NativeValidatorKeeper.GetEVMFrozenBinding(e.ctx(), binding.Gateway, binding.FrozenIndex)
	require.NoError(t, err)
	expected := binding
	expected.Active, expected.UpdatedHeight = false, e.ctx().BlockHeight()-1
	require.Equal(t, expected, closed)
	exported, err := e.app.NativeValidatorKeeper.ExportGenesis(e.ctx())
	require.NoError(t, err)
	require.Empty(t, exported.Undelegations)
	e.advance()
	require.Zero(t, completions())
	call(2, gateway, profile.Gateway, zero, false, "complete", []*big.Int{frozenID})
	require.Zero(t, completions())
	assertEVM(beneficiary, 0)
}
