package systemcontracts_test

import (
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"testing"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	ownedapp "github.com/maxwell2010/go-smart-node/network/app"
	nfttypes "github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
	"github.com/stretchr/testify/require"
)

type nativeNFTDowntimeFixture struct {
	origin  contracts.Artifact
	address common.Address
	tokenID common.Hash
}

const nativeConsensusNFTDowntimeEnv = "MCND_NATIVE_CONSENSUS_NFT_DOWNTIME"

func mintNativeNFTDowntimeFixture(t *testing.T, e isolatedBackendEnv, origin contracts.Artifact, address, beneficiary common.Address) nativeNFTDowntimeFixture {
	t.Helper()
	tokenID := common.BigToHash(new(big.Int).Add(new(big.Int).Lsh(big.NewInt(1), 224), big.NewInt(401)))
	input, err := origin.ABI.Pack("mintByDEL", beneficiary, tokenID.Big(), big.NewInt(1), "ipfs://native-nft-downtime")
	require.NoError(t, err)
	e.nextGasLimit(24_000_000)
	e.send(e.keys[0], &address, sdkmath.NewIntWithDecimal(2, 18).BigInt(), input, true)
	return nativeNFTDowntimeFixture{origin: origin, address: address, tokenID: tokenID}
}

func (f nativeNFTDowntimeFixture) addGenesis(t *testing.T, e isolatedBackendEnv, profile ownedapp.WithdrawalProfile, genesis map[string]json.RawMessage, beneficiary string) {
	t.Helper()
	nft, err := e.app.NativeNFTKeeper.ExportGenesis(e.ctx())
	require.NoError(t, err)
	require.Empty(t, nft.Collections)
	require.Empty(t, nft.ExternalBindings)
	require.Empty(t, nft.IsolatedVaults)
	require.Empty(t, nft.IsolatedPositions)
	domain, ok := e.app.NativeValidatorKeeper.ConfiguredEVMSourceDomain()
	require.True(t, ok)
	creator := sdk.AccAddress(e.govOwner.Bytes()).String()
	nft.Collections = []nfttypes.Collection{
		{Creator: beneficiary, Denom: signedNFTNativeOriginDenom, Supply: 1, TypeNft: nfttypes.NftType_NFT1155,
			AddressDRC: f.address.Hex(), Tokens: nfttypes.Tokens{{Creator: beneficiary, Denom: signedNFTNativeOriginDenom,
				ID: signedNFTNativeOriginID, URI: "ipfs://native-nft-downtime", Reserve: sdk.NewCoin(identity.BaseDenom, sdkmath.NewIntWithDecimal(2, 18)),
				Minted: 1, SubTokens: nfttypes.SubTokens{{ID: 1, Owner: beneficiary}}}}},
		{Creator: creator, Denom: signedNFTNativeVaultDenom, TypeNft: nfttypes.NftType_NFT721, AddressDRC: profile.PositionVault.Hex()},
	}
	nft.ExternalBindings = []nfttypes.ExternalNFTBinding{{ChainID: domain.ChainID, EVMChainID: domain.EVMChainID,
		Contract: f.address.Bytes(), TokenID: f.tokenID.Bytes(), NativeTokenID: signedNFTNativeOriginID,
		NFTType: nfttypes.NftType_NFT1155, NextSubTokenID: 2}}
	nft.IsolatedVaults = []nfttypes.IsolatedVaultState{{ChainID: domain.ChainID, EVMChainID: domain.EVMChainID, Vault: profile.PositionVault.Bytes(),
		NextPositionID: common.BigToHash(big.NewInt(1)).Bytes(), ReceiptCreator: creator, ReceiptDenom: signedNFTNativeVaultDenom}}
	require.NoError(t, nft.Validate())
	genesis[nfttypes.ModuleName] = e.app.AppCodec().MustMarshalJSON(nft)
}

func (f nativeNFTDowntimeFixture) expectations(t *testing.T, e isolatedBackendEnv, profile ownedapp.WithdrawalProfile,
	gateway, vault contracts.Artifact, target, actor common.Address, key *ecdsa.PrivateKey, out map[string]interface{},
) {
	t.Helper()
	pack := func(artifact contracts.Artifact, method string, args ...interface{}) []byte {
		t.Helper()
		data, err := artifact.ABI.Pack(method, args...)
		require.NoError(t, err)
		return data
	}
	one := big.NewInt(1)
	position := common.BigToHash(one)
	var zero common.Hash
	stakeID := crypto.Keccak256Hash([]byte("NFT"), profile.PositionVault.Bytes(), position.Bytes(), actor.Bytes(), target.Bytes(), zero.Bytes())
	domain, ok := e.app.NativeValidatorKeeper.ConfiguredEVMSourceDomain()
	require.True(t, ok)
	receiptID, err := nfttypes.FreshExternalTokenID(domain.ChainID, domain.EVMChainID, profile.PositionVault.Bytes(), position.Bytes())
	require.NoError(t, err)
	data := func(raw []byte) string { return "0x" + hex.EncodeToString(raw) }
	out["stakeId"] = stakeID.Hex()
	out["stakePenaltyData"] = data(pack(gateway, "getNFTStakePenaltyIndex", stakeID))
	out["nftOrigin"], out["nftVault"] = f.address.Hex(), profile.PositionVault.Hex()
	out["originNative"] = sdk.AccAddress(f.address.Bytes()).String()
	out["vaultNative"] = sdk.AccAddress(profile.PositionVault.Bytes()).String()
	out["gatewayNative"] = sdk.AccAddress(profile.Gateway.Bytes()).String()
	out["originTokenNative"], out["receiptTokenNative"] = signedNFTNativeOriginID, receiptID
	out["nftOwnerData"] = data(pack(vault, "ownerOf", one))
	out["nftReserveData"] = data(pack(vault, "getReserve", one))
	out["originSupplyData"] = data(pack(f.origin, exactABIMethod(t, f.origin.ABI, "totalSupply(uint256)"), f.tokenID.Big()))
	out["originBalanceData"] = data(pack(f.origin, exactABIMethod(t, f.origin.ABI, "balanceOf(address,uint256)"), actor, f.tokenID.Big()))
	nonce := e.app.EVMKeeper.GetNonce(e.ctx(), actor)
	out["nftNonce"] = nonce
	price := big.NewInt(1_000_000_000_000)
	signer := ethtypes.NewEIP155Signer(big.NewInt(990001))
	calls := []struct {
		name   string
		target common.Address
		input  []byte
	}{
		{"nftApprove", f.address, pack(f.origin, "setApprovalForAll", profile.Gateway, true)},
		{"nftDelegate", profile.Gateway, pack(gateway, "delegateNFT1155", target, f.address, f.tokenID.Big(), one)},
	}
	for i, call := range calls {
		tx := ethtypes.NewTx(&ethtypes.LegacyTx{Nonce: nonce + uint64(i), To: &call.target,
			Gas: 24_000_000, GasPrice: price, Data: call.input})
		signed, err := ethtypes.SignTx(tx, signer, key)
		require.NoError(t, err)
		raw, err := signed.MarshalBinary()
		require.NoError(t, err)
		out[call.name] = map[string]string{"raw": data(raw), "hash": signed.Hash().Hex()}
	}
}
