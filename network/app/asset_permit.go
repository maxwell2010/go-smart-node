package app

import (
	"errors"
	"math/big"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

const (
	erc20PermitType   = "Permit(address owner,address spender,uint256 value,uint256 nonce,uint256 deadline)"
	nft721PermitType  = "Permit(address spender,uint256 tokenId,uint256 nonce,uint256 deadline)"
	nft1155PermitType = "Permit(address spender,uint256 nonce,uint256 deadline)"
)

// Caller has authenticated the proxy, its implementation, and the signed direct
// transaction. The implementation consumes the nonce. Check its post-state,
// exact approval event and owner signature independently of the fee-paying relayer.
func (c *withdrawalController) admitAssetPermit(ctx sdk.Context, msg core.Message, receipt *ethtypes.Receipt, asset abi.ABI, family byte, args []interface{}) error {
	if !testIssuanceActive(ctx.ChainID()) || msg.Value.Sign() != 0 || len(receipt.Logs) != 1 {
		return errors.New("MC_PERMIT_DISABLED_OR_INVALID_CALL")
	}
	target := *msg.To
	read := func(method string, args ...interface{}) (interface{}, error) {
		values, err := c.userNFTRead(ctx, target, asset, method, args...)
		if err != nil {
			return nil, err
		}
		if len(values) != 1 {
			return nil, errors.New("MC_PERMIT_GETTER")
		}
		return values[0], nil
	}
	var owner, spender common.Address
	var nonceKey interface{}
	var structure []common.Hash
	var log *ethtypes.Log
	switch family {
	case 0:
		owner, spender = args[0].(common.Address), args[1].(common.Address)
		value := args[2].(*big.Int)
		nonceKey = owner
		structure = []common.Hash{crypto.Keccak256Hash([]byte(erc20PermitType)), customAddressWord(owner), customAddressWord(spender), common.BigToHash(value)}
		log = reservelessLog(target, "Approval", owner, spender, value)
		amount, err := read("allowance", owner, spender)
		if err != nil || amount.(*big.Int).Cmp(value) != 0 {
			return errors.New("MC_PERMIT_ALLOWANCE")
		}
	case 2, 4:
		spender = args[0].(common.Address)
		id := args[1].(*big.Int)
		holder, err := read("ownerOf", id)
		if err != nil {
			return err
		}
		owner = holder.(common.Address)
		nonceKey = id
		structure = []common.Hash{crypto.Keccak256Hash([]byte(nft721PermitType)), customAddressWord(spender), common.BigToHash(id)}
		log = &ethtypes.Log{Address: target, Topics: []common.Hash{asset.Events["Approval"].ID, customAddressWord(owner), customAddressWord(spender), common.BigToHash(id)}}
		approved, err := read("getApproved", id)
		if err != nil || approved != spender {
			return errors.New("MC_PERMIT_NFT_APPROVAL")
		}
	case 3, 5:
		owner, spender = args[0].(common.Address), args[1].(common.Address)
		nonceKey = owner
		structure = []common.Hash{crypto.Keccak256Hash([]byte(nft1155PermitType)), customAddressWord(spender)}
		log = &ethtypes.Log{Address: target, Topics: []common.Hash{asset.Events["ApprovalForAll"].ID, customAddressWord(owner), customAddressWord(spender)}, Data: common.BigToHash(big.NewInt(1)).Bytes()}
		approved, err := read("isApprovedForAll", owner, spender)
		if err != nil || approved != true {
			return errors.New("MC_PERMIT_OPERATOR_APPROVAL")
		}
	default:
		return errors.New("MC_PERMIT_FAMILY")
	}
	if owner == (common.Address{}) || len(c.customCollateral.evm.GetCode(ctx, c.customCollateral.evm.GetCodeHash(ctx, owner))) != 0 ||
		!equalReservelessLogs(receipt.Logs, []*ethtypes.Log{log}) {
		return errors.New("MC_PERMIT_OWNER_OR_EVENT")
	}
	nonce, err := read("nonces", nonceKey)
	if err != nil || nonce.(*big.Int).Sign() <= 0 {
		return errors.New("MC_PERMIT_NONCE")
	}
	domain, err := read("DOMAIN_SEPARATOR")
	if err != nil {
		return err
	}
	deadline := args[len(args)-4].(*big.Int)
	if deadline.Cmp(big.NewInt(ctx.BlockTime().Unix())) < 0 {
		return errors.New("MC_PERMIT_EXPIRED")
	}
	previous := new(big.Int).Sub(nonce.(*big.Int), big.NewInt(1))
	structure = append(structure, common.BigToHash(previous), common.BigToHash(deadline))
	return verifyAssetPermitSignature(owner, domain.([32]byte), structure, args[len(args)-3].(uint8), args[len(args)-2].([32]byte), args[len(args)-1].([32]byte))
}

func verifyAssetPermitSignature(owner common.Address, domain common.Hash, fields []common.Hash, v byte, r, s [32]byte) error {
	if (v != 27 && v != 28) || !crypto.ValidateSignatureValues(v-27, new(big.Int).SetBytes(r[:]), new(big.Int).SetBytes(s[:]), true) {
		return errors.New("MC_PERMIT_SIGNATURE")
	}
	var encoded []byte
	for _, field := range fields {
		encoded = append(encoded, field[:]...)
	}
	digest := crypto.Keccak256([]byte{0x19, 0x01}, domain[:], crypto.Keccak256(encoded))
	var signature [65]byte
	copy(signature[:32], r[:])
	copy(signature[32:64], s[:])
	signature[64] = v - 27
	pub, err := crypto.SigToPub(digest, signature[:])
	if err != nil || crypto.PubkeyToAddress(*pub) != owner {
		return errors.New("MC_PERMIT_SIGNATURE")
	}
	return nil
}
