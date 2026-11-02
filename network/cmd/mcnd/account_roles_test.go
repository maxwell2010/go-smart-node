package main

import (
	"testing"

	"github.com/cosmos/cosmos-sdk/crypto/hd"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	sdk "github.com/cosmos/cosmos-sdk/types"
	ethsecp "github.com/cosmos/evm/crypto/ethsecp256k1"
	evmhd "github.com/cosmos/evm/crypto/hd"
	"github.com/ethereum/go-ethereum/crypto"
	ownedapp "github.com/maxwell2010/go-smart-node/network/app"
	"github.com/stretchr/testify/require"
)

func TestOperatorAndRelayerAccountRoles(t *testing.T) {
	encoding := ownedapp.MakeEncodingConfig(990001)
	keys := keyring.NewInMemory(encoding.Codec, evmhd.EthSecp256k1Option())
	operator, _, err := keys.NewMnemonic("operator", keyring.English, evmhd.BIP44HDPath, "", evmhd.EthSecp256k1)
	require.NoError(t, err)
	public, err := operator.GetPubKey()
	require.NoError(t, err)
	require.IsType(t, &ethsecp.PubKey{}, public)
	native, err := operator.GetAddress()
	require.NoError(t, err)
	point, err := crypto.DecompressPubkey(public.Bytes())
	require.NoError(t, err)
	eoa := crypto.PubkeyToAddress(*point)
	require.Equal(t, eoa.Bytes(), native.Bytes(), "operator d0 signer and Ethereum EOA must identify the same account")
	require.Equal(t, sdk.AccAddress(eoa.Bytes()).String(), native.String())
	require.EqualValues(t, 60, sdk.GetConfig().GetCoinType())

	// The relayer's ordinary Cosmos public key has an honest RIPEMD account
	// address. Do not label it as that key's Ethereum EOA or rewrite its bytes.
	relayer, _, err := keys.NewMnemonic("relayer", keyring.English, evmhd.BIP44HDPath, "", hd.Secp256k1)
	require.NoError(t, err)
	relayerPublic, err := relayer.GetPubKey()
	require.NoError(t, err)
	relayerNative, err := relayer.GetAddress()
	require.NoError(t, err)
	relayerPoint, err := crypto.DecompressPubkey(relayerPublic.Bytes())
	require.NoError(t, err)
	require.NotEqual(t, crypto.PubkeyToAddress(*relayerPoint).Bytes(), relayerNative.Bytes())
}
