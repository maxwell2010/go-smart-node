package settlement

import (
	"testing"

	sdkmath "cosmossdk.io/math"
	"github.com/ethereum/go-ethereum/common"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

func TestGenesisCensusClassifiesFrozenAndHistoricalRecords(t *testing.T) {
	withdrawal := frozenWords{smallWord(7), smallWord(8), smallWord(9), smallWord(10), {}, smallWord(tokenDEL), {}, {}, smallWord(statusFrozen), smallWord(freezeWithdraw), smallWord(11)}
	status, err := genesisFrozenStatus(withdrawal)
	require.NoError(t, err)
	require.Equal(t, byte(statusFrozen), status)
	withdrawal[freezeStatusWord] = smallWord(statusCompleted)
	status, err = genesisFrozenStatus(withdrawal)
	require.NoError(t, err)
	require.Equal(t, byte(statusCompleted), status)
	withdrawal[9] = smallWord(2) // Completed transfers may be historical rows.
	_, err = genesisFrozenStatus(withdrawal)
	require.NoError(t, err)
	withdrawal[freezeStatusWord] = smallWord(statusFrozen)
	_, err = genesisFrozenStatus(withdrawal)
	require.Error(t, err, "the cold withdrawal profile cannot strand an active transfer")
	_, err = genesisFrozenStatus(frozenWords{})
	require.Error(t, err, "a default all-zero slot is not completed history")
}

func TestGenesisClaimsCountDistinctCurrencyReserves(t *testing.T) {
	wrapped := common.HexToAddress("0x7002")
	row := validatortypes.EVMFrozenBinding{
		ReserveAsset:          &validatortypes.EVMReserveAsset{NativeDenom: identity.BaseDenom, ReserveKind: 1, Token: make([]byte, 20)},
		ReportedReserveAmount: sdkmath.ZeroInt(),
	}
	claims := new(genesisClaims)
	require.NoError(t, claims.addNFT(row, wrapped))
	require.True(t, claims.hasVaultNative, "zero-backed positions retain their reserve kind")
	row.ReportedReserveAmount = sdkmath.NewInt(10)
	require.NoError(t, claims.addNFT(row, wrapped))
	require.NoError(t, claims.addNFT(row, wrapped))
	require.Equal(t, int64(20), claims.vaultNative.Int64())
	row.ReserveAsset.ReserveKind, row.ReserveAsset.Token = 2, wrapped.Bytes()
	require.NoError(t, claims.addNFT(row, wrapped))
	require.Equal(t, int64(10), claims.vaultWrapped.Int64())
	row.ReserveAsset.Token = common.HexToAddress("0x7003").Bytes()
	require.Error(t, claims.addNFT(row, wrapped))
	require.Equal(t, int64(10), claims.vaultWrapped.Int64())
}
