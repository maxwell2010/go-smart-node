package systemcontracts_test

import (
	"encoding/json"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	ethsecp "github.com/cosmos/evm/crypto/ethsecp256k1"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	ownedapp "github.com/maxwell2010/go-smart-node/network/app"
	nfttypes "github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
	"github.com/stretchr/testify/require"
)

const (
	signedNFTAdmissionPhaseEnv = "MCND_SIGNED_NFT_ADMISSION_PHASE"
	signedNFTAdmissionFileEnv  = "MCND_SIGNED_NFT_ADMISSION_GENESIS"
	signedNFTAdmissionPassed   = "signed NFT admission from cold genesis, withdrawal rollback and retry passed"
	signedNFTNativeOriginID    = "signed_nft_origin"
	signedNFTNativeOriginDenom = "signed_nft_origin"
	signedNFTNativeVaultDenom  = "signed_nft_vault"
	signedNFTGasLimit          = 24_000_000
	signedNFTUnitReserveDEL    = 2
)

type signedNFTAdmissionGenesis struct {
	AppState      json.RawMessage
	Height        int64
	Time          time.Time
	Profile       ownedapp.WithdrawalProfile
	Validator     common.Address
	Origin        common.Address
	OriginTokenID common.Hash
}

// Both private subprocesses regenerate these disposable fixture keys. The
// serialized genesis envelope contains only the public state.
func registryFixtureKey(index int, deterministic bool) (*ethsecp.PrivKey, error) {
	if !deterministic {
		return ethsecp.GenerateKey()
	}
	return &ethsecp.PrivKey{Key: crypto.Keccak256([]byte("MintCandy isolated NFT fixture account"), []byte{byte(index)})}, nil
}

func TestOwnedAppSignedNFTDelegationLifecycle(t *testing.T) {
	binary, err := os.Executable()
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "signed-nft-genesis.json")
	for _, step := range []struct{ phase, filter string }{
		{"seed", "^TestOwnedAppSignedRegistryBootstrap$/^authenticated_isolated_position_reader$"},
		{"verify", "^TestOwnedAppSignedRegistryBootstrap$"},
	} {
		if !t.Run(step.phase, func(t *testing.T) {
			child := exec.CommandContext(t.Context(), binary, "-test.v", "-test.timeout=3m", "-test.run="+step.filter)
			child.Env = append(os.Environ(), signedNFTAdmissionPhaseEnv+"="+step.phase,
				signedNFTAdmissionFileEnv+"="+path, withdrawalGenesisFixtureEnv+"=", delLifecycleFixtureEnv+"=", delDelegationDaemonFixtureEnv+"=")
			output, err := child.CombinedOutput()
			require.NoError(t, err, "%s", output)
			if step.phase == "verify" {
				require.Contains(t, string(output), signedNFTAdmissionPassed)
				for _, line := range strings.Split(string(output), "\n") {
					if strings.Contains(line, "signed NFT gas route=") {
						t.Log(strings.TrimSpace(line))
					}
				}
			}
		}) {
			return
		}
	}
}

func readSignedNFTAdmissionGenesis(t *testing.T) signedNFTAdmissionGenesis {
	t.Helper()
	data, err := os.ReadFile(os.Getenv(signedNFTAdmissionFileEnv))
	require.NoError(t, err)
	var result signedNFTAdmissionGenesis
	require.NoError(t, json.Unmarshal(data, &result))
	require.Greater(t, result.Height, int64(1))
	require.NotEmpty(t, result.AppState)
	return result
}

func seedSignedNFTAdmissionGenesis(t *testing.T, e isolatedBackendEnv, profile ownedapp.WithdrawalProfile,
	gateway, master, wrapper, origin contracts.Artifact, originAddress common.Address,
	gov func(contracts.Artifact, common.Address, string, ...interface{})) {
	t.Helper()
	f := prepareSignedDELFixture(t, e, profile, gateway, master, wrapper, gov)
	f.declareValidator()
	quantity := nfttypes.MaxIsolatedOriginUnits
	unitReserve := sdkmath.NewIntWithDecimal(signedNFTUnitReserveDEL, 18)
	totalReserve := unitReserve.MulRaw(int64(quantity))
	tokenID := common.BigToHash(new(big.Int).Add(new(big.Int).Lsh(big.NewInt(1), 224), big.NewInt(301)))
	const uri = "ipfs://signed-nft-admission"
	input, err := origin.ABI.Pack("mintByDEL", f.user, tokenID.Big(), big.NewInt(int64(quantity)), uri)
	require.NoError(t, err)
	e.nextGasLimit(signedNFTGasLimit)
	e.send(e.keys[0], &originAddress, totalReserve.BigInt(), input, true)
	ctx, cdc := e.ctx(), e.app.AppCodec()
	genesis, err := e.app.ModuleManager.ExportGenesisForModules(ctx, cdc, nil)
	require.NoError(t, err)
	nft, err := e.app.NativeNFTKeeper.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Empty(t, nft.Collections)
	require.Empty(t, nft.ExternalBindings)
	require.Empty(t, nft.IsolatedVaults)
	require.Empty(t, nft.IsolatedPositions)
	domain, ok := e.app.NativeValidatorKeeper.ConfiguredEVMSourceDomain()
	require.True(t, ok)
	vaultCreator := sdk.AccAddress(e.govOwner.Bytes()).String()
	units := make(nfttypes.SubTokens, quantity)
	for i := range units {
		units[i] = &nfttypes.SubToken{ID: uint32(i + 1), Owner: f.beneficiary}
	}
	nft.Collections = []nfttypes.Collection{
		{Creator: f.beneficiary, Denom: signedNFTNativeOriginDenom, Supply: 1, TypeNft: nfttypes.NftType_NFT1155,
			AddressDRC: originAddress.Hex(), Tokens: nfttypes.Tokens{{Creator: f.beneficiary, Denom: signedNFTNativeOriginDenom,
				ID: signedNFTNativeOriginID, URI: uri, Reserve: sdk.NewCoin(identity.BaseDenom, unitReserve), Minted: quantity, SubTokens: units}}},
		{Creator: vaultCreator, Denom: signedNFTNativeVaultDenom, TypeNft: nfttypes.NftType_NFT721, AddressDRC: profile.PositionVault.Hex()},
	}
	nft.ExternalBindings = []nfttypes.ExternalNFTBinding{{ChainID: domain.ChainID, EVMChainID: domain.EVMChainID,
		Contract: originAddress.Bytes(), TokenID: tokenID.Bytes(), NativeTokenID: signedNFTNativeOriginID,
		NFTType: nfttypes.NftType_NFT1155, NextSubTokenID: uint64(quantity) + 1}}
	nft.IsolatedVaults = []nfttypes.IsolatedVaultState{{ChainID: domain.ChainID, EVMChainID: domain.EVMChainID, Vault: profile.PositionVault.Bytes(),
		NextPositionID: common.BigToHash(big.NewInt(1)).Bytes(), ReceiptCreator: vaultCreator, ReceiptDenom: signedNFTNativeVaultDenom}}
	require.NoError(t, nft.Validate())
	genesis[nfttypes.ModuleName] = cdc.MustMarshalJSON(nft)
	genesis[ownedapp.WithdrawalGenesisKey], err = json.Marshal(profile)
	require.NoError(t, err)
	require.NoError(t, ownedapp.ReconcileNativeGenesisAllocation(cdc, genesis))
	raw, err := json.Marshal(genesis)
	require.NoError(t, err)
	envelope, err := json.Marshal(signedNFTAdmissionGenesis{AppState: raw, Height: ctx.BlockHeight(), Time: ctx.BlockTime(),
		Profile: profile, Validator: f.validator, Origin: originAddress, OriginTokenID: tokenID})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(os.Getenv(signedNFTAdmissionFileEnv), envelope, 0600))
}
