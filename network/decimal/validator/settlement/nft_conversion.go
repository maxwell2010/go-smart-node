package settlement

import (
	"errors"
	"math/big"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

const (
	nftConversionPrefixLogs = 8
	nftConversionLogs       = nftConversionPrefixLogs + 1 // Plus the origin's allowance clearing log.
)

var (
	nftConversionApprovalTopic = crypto.Keccak256Hash([]byte("Approval(address,address,uint256)"))
	nftConversionTransferTopic = crypto.Keccak256Hash([]byte("Transfer(address,address,uint256)"))
	nftConversionReserveTopic  = crypto.Keccak256Hash([]byte("ReserveUpdated(uint256,uint256,uint256)"))
	nftCollateralConverted     = crypto.Keccak256Hash([]byte("CollateralConverted(address,address,address,uint256,uint256,uint256,uint256,bool)"))
)

// NFTConvertedIsolation proves the custom input separately from the vault's
// DEL reserve. ReserveLog still needs reconciliation with native coin state.
type NFTConvertedIsolation struct {
	NFTIsolation
	InputToken                common.Address
	InputAmount, NativeOutput sdkmath.Int
	ReserveLog                *ethtypes.Log
}

type nftIsolationConversion struct {
	converter, tokenCenter common.Address
	proof                  NFTConvertedIsolation
}

// VerifySignedNFTIsolationWithConversion accepts only the new direct signed
// Gateway route under the already attested converter and token center roles.
func (e *Executor) VerifySignedNFTIsolationWithConversion(ctx sdk.Context, msg core.Message, receipt *ethtypes.Receipt,
	converter, tokenCenter common.Address, downtimeDeposit bool,
) (NFTConvertedIsolation, error) {
	if converter == (common.Address{}) || tokenCenter == (common.Address{}) || converter == tokenCenter {
		return NFTConvertedIsolation{}, errors.New("NFT isolation conversion requires an attested converter and token center")
	}
	conversion := nftIsolationConversion{converter: converter, tokenCenter: tokenCenter}
	isolation, err := e.verifySignedNFTIsolation(ctx, msg, receipt, downtimeDeposit, &conversion)
	if err != nil {
		return NFTConvertedIsolation{}, err
	}
	conversion.proof.NFTIsolation = isolation
	return conversion.proof, nil
}

func (c *nftIsolationConversion) verify(ctx sdk.Context, logs []*ethtypes.Log, origin, vault, wrapped common.Address,
	minimum, deadline *big.Int, output common.Hash,
) error {
	if minimum.Sign() <= 0 || deadline.Sign() <= 0 || deadline.Cmp(big.NewInt(ctx.BlockTime().Unix())) < 0 || output.Big().Cmp(minimum) < 0 {
		return errors.New("NFT isolation conversion does not satisfy the signed minimum or deadline")
	}
	converted := logs[6]
	if len(converted.Topics) != 4 || len(converted.Data) != 5*common.HashLength {
		return errors.New("missing exact NFT collateral conversion event")
	}
	token := common.BytesToAddress(converted.Topics[2][common.HashLength-common.AddressLength:])
	if token == (common.Address{}) || token == wrapped {
		return errors.New("NFT isolation conversion requires a custom DRC20 source")
	}
	input := common.BytesToHash(converted.Data[:common.HashLength])
	if input == (common.Hash{}) || input.Big().BitLen() > 255 {
		return errors.New("invalid NFT collateral conversion input")
	}
	var zero common.Hash
	tokenWord, originWord, vaultWord, converterWord := common.BytesToHash(token[:]), common.BytesToHash(origin[:]),
		common.BytesToHash(vault[:]), common.BytesToHash(c.converter[:])
	if !matchesNFTEvent(logs[0], token, []common.Hash{nftConversionApprovalTopic, originWord, vaultWord}, input) ||
		!matchesNFTEvent(logs[1], token, []common.Hash{nftConversionTransferTopic, originWord, vaultWord}, input) ||
		!matchesNFTEvent(logs[2], token, []common.Hash{nftConversionApprovalTopic, vaultWord, converterWord}, input) ||
		!matchesNFTEvent(logs[3], token, []common.Hash{nftConversionTransferTopic, vaultWord, converterWord}, input) ||
		!matchesNFTEvent(logs[4], token, []common.Hash{nftConversionTransferTopic, converterWord, zero}, input) ||
		!matchesNFTEvent(converted, c.converter, []common.Hash{nftCollateralConverted, vaultWord, tokenWord, common.BytesToHash(c.tokenCenter[:])},
			input, output, common.BigToHash(minimum), common.BigToHash(deadline), zero) ||
		!matchesNFTEvent(logs[7], token, []common.Hash{nftConversionApprovalTopic, vaultWord, converterWord}, zero) {
		return errors.New("NFT isolation conversion does not prove the exact custom transfer, sale, and funded DEL output")
	}
	reserve := logs[5]
	if reserve.Address != token || len(reserve.Topics) != 1 || reserve.Topics[0] != nftConversionReserveTopic || len(reserve.Data) != 3*common.HashLength {
		return errors.New("NFT isolation conversion requires one exact curve reserve event")
	}
	c.proof.InputToken = token
	c.proof.InputAmount = sdkmath.NewIntFromBigInt(input.Big())
	c.proof.NativeOutput = sdkmath.NewIntFromBigInt(output.Big())
	c.proof.ReserveLog = reserve
	return nil
}
