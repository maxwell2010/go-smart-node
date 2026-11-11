package settlement

import (
	"bytes"
	"errors"
	"math/big"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/maxwell2010/go-smart-node/network/decimal/nft/evmview"
	"github.com/maxwell2010/go-smart-node/network/decimal/nft/receipts"
	nfttypes "github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
)

// NFTIsolation is a verified mint from a known, uniformly DEL-backed origin.
// The native keepers still reconcile the archived origin and stake prestates.
type NFTIsolation struct {
	Position           evmview.PositionSnapshot
	Companion          DELTransition
	StakeID            common.Hash
	OriginSupplyAfter  uint32
	OriginReserveAfter sdkmath.Int
}

// VerifySignedNFTIsolation admits only the direct no-hold Gateway route.
func (e *Executor) VerifySignedNFTIsolation(ctx sdk.Context, msg core.Message, receipt *ethtypes.Receipt) (NFTIsolation, error) {
	var proof NFTIsolation
	method := e.gateway.Methods["delegateNFT1155"]
	if msg.To == nil || *msg.To != e.roles.Gateway || msg.Value == nil || msg.Value.Sign() != 0 ||
		len(msg.SetCodeAuthorizations) != 0 || len(msg.Data) != 4+4*common.HashLength || !bytes.Equal(msg.Data[:4], method.ID) || msg.From == Sender() {
		return proof, errors.New("NFT isolation requires a direct signed Gateway call with zero value")
	}
	args, err := method.Inputs.Unpack(msg.Data[4:])
	if err != nil {
		return proof, err
	}
	canonical, err := method.Inputs.Pack(args...)
	if err != nil || !bytes.Equal(canonical, msg.Data[4:]) {
		return proof, errors.New("noncanonical NFT isolation call")
	}
	validator, origin := args[0].(common.Address), args[1].(common.Address)
	originID, quantity := args[2].(*big.Int), args[3].(*big.Int)
	if validator == (common.Address{}) || origin == (common.Address{}) || quantity.Sign() <= 0 ||
		!quantity.IsUint64() || quantity.Uint64() > uint64(nfttypes.MaxIsolatedOriginUnits) {
		return proof, errors.New("invalid signed NFT isolation identity or quantity")
	}
	if err := e.verifyDELStakeReceipt(ctx, receipt, validator, msg.From, 11); err != nil {
		return proof, err
	}
	collection, known, err := e.auth.AuthenticateCollection(ctx, origin, receipts.EntryPointSignedEthereum)
	if err != nil {
		return proof, err
	}
	if !known || collection.NFTType != nfttypes.NftType_NFT1155 || collection.ReserveLess {
		return proof, errors.New("NFT isolation requires an approved reserve ERC1155 origin")
	}
	logs := receipt.Logs
	if len(logs[0].Topics) != 4 {
		return proof, errors.New("NFT isolation requires the exact Gateway origin event")
	}
	positionID, originIDWord, quantityWord := logs[0].Topics[1], common.BigToHash(originID), common.BigToHash(quantity)
	ownerWord, originWord := common.BytesToHash(msg.From[:]), common.BytesToHash(origin[:])
	if !matchesNFTEvent(logs[0], e.roles.Gateway, []common.Hash{e.gateway.Events["NFTIsolationStarted"].ID, positionID, ownerWord, originWord}, originIDWord, quantityWord) {
		return proof, errors.New("Gateway NFT isolation event differs from the signed origin")
	}
	position, err := e.auth.Position(ctx, positionID)
	if err != nil {
		return proof, err
	}
	next := new(big.Int).Add(positionID.Big(), big.NewInt(1))
	reserve := position.ReserveAmount.Big()
	if position.Status != evmview.PositionActive || position.Owner != e.roles.Gateway || position.Origin.Contract != origin ||
		position.Origin.TokenID != originIDWord || position.OriginQuantity != uint32(quantity.Uint64()) || position.ParentPositionID != (common.Hash{}) ||
		position.ReserveKind != nfttypes.IsolatedReserveDEL || position.ReserveToken != (common.Address{}) ||
		next.Cmp(position.NextPositionID.Big()) != 0 || reserve.Sign() <= 0 || reserve.BitLen() > 255 {
		return proof, errors.New("new vault position differs from the signed DEL-backed origin")
	}
	supply, pool, err := e.isolationOriginPoststate(ctx, origin, originID)
	if err != nil {
		return proof, err
	}
	gatewayWord, vaultWord, validatorWord, wdelWord := common.BytesToHash(e.roles.Gateway[:]), common.BytesToHash(e.roles.PositionVault[:]),
		common.BytesToHash(validator[:]), common.BytesToHash(e.roles.WrappedDEL[:])
	var zero common.Hash
	transferSingle := e.origin.Events["TransferSingle"].ID
	reserveKind := smallWord(byte(nfttypes.IsolatedReserveDEL))
	supplyWord := common.BytesToHash(sdk.Uint64ToBigEndian(uint64(supply)))
	if !matchesNFTEvent(logs[1], origin, []common.Hash{transferSingle, gatewayWord, ownerWord, gatewayWord}, originIDWord, quantityWord) ||
		!matchesNFTEvent(logs[2], origin, []common.Hash{transferSingle, gatewayWord, gatewayWord, zero}, originIDWord, quantityWord) ||
		!matchesNFTEvent(logs[3], origin, []common.Hash{e.origin.Events["ReserveUpdated"].ID}, originIDWord, supplyWord, zero, common.BigToHash(pool), reserveKind) ||
		!matchesNFTEvent(logs[4], e.roles.PositionVault, []common.Hash{e.vault.Events["Transfer"].ID, zero, gatewayWord, positionID}) ||
		!matchesNFTEvent(logs[5], e.roles.PositionVault, []common.Hash{e.vault.Events["PositionIsolated"].ID, positionID, originWord, originIDWord}, gatewayWord, quantityWord, reserveKind, zero, position.ReserveAmount, boolWord(position.Refundable), boolWord(position.Burnable)) ||
		!matchesNFTEvent(logs[6], e.roles.PositionVault, []common.Hash{e.vault.Events["PositionReserveUpdated"].ID, positionID}, reserveKind, zero, position.ReserveAmount, quantityWord) ||
		!matchesNFTEvent(logs[7], origin, []common.Hash{e.origin.Events["OriginIsolated"].ID, originIDWord, positionID, gatewayWord}, gatewayWord, quantityWord, position.ReserveAmount) {
		return proof, errors.New("NFT isolation receipt does not reconcile origin burn and vault mint")
	}
	companion, post, err := e.readNFTCompanion(ctx, validator, msg.From)
	if err != nil {
		return proof, err
	}
	if companion.NFTBacked.BigInt().Cmp(reserve) < 0 {
		return proof, errors.New("Gateway did not include the new NFT reserve in the companion position")
	}
	isNew := common.BytesToHash(post[3*common.HashLength:4*common.HashLength]) == position.ReserveAmount
	if err := e.checkDELStakeLogs(logs[8:10], companion.StakeID, post, reserve, isNew); err != nil {
		return proof, err
	}
	stakeID, err := e.verifyNFTStake(ctx, position, validator, msg.From, true)
	if err != nil {
		return proof, err
	}
	if !matchesNFTEvent(logs[10], e.roles.Gateway, []common.Hash{e.gateway.Events["NFTDelegated"].ID, stakeID, companion.StakeID, validatorWord},
		vaultWord, positionID, smallWord(1), ownerWord, position.ReserveAmount, wdelWord) {
		return proof, errors.New("Gateway NFTDelegated event differs from the funded vault position")
	}
	return NFTIsolation{Position: position, Companion: companion, StakeID: stakeID,
		OriginSupplyAfter: supply, OriginReserveAfter: sdkmath.NewIntFromBigInt(pool)}, nil
}

func (e *Executor) isolationOriginPoststate(ctx sdk.Context, origin common.Address, originID *big.Int) (uint32, *big.Int, error) {
	if e.origin.Methods["totalSupply0"].Sig != "totalSupply(uint256)" {
		return 0, nil, errors.New("compiler origin supply ABI differs")
	}
	rawSupply, err := e.read(ctx, origin, e.origin, "totalSupply0", common.HashLength, originID)
	if err != nil {
		return 0, nil, err
	}
	rawReserve, err := e.read(ctx, origin, e.origin, "getReserve", 3*common.HashLength, originID)
	if err != nil {
		return 0, nil, err
	}
	supply := new(big.Int).SetBytes(rawSupply)
	pool := new(big.Int).SetBytes(rawReserve[common.HashLength : 2*common.HashLength])
	if supply.BitLen() > 32 || common.BytesToHash(rawReserve[:common.HashLength]) != (common.Hash{}) ||
		common.BytesToHash(rawReserve[2*common.HashLength:]) != smallWord(byte(nfttypes.IsolatedReserveDEL)) ||
		e.evm.GetBalance(ctx, origin).ToBig().Cmp(pool) < 0 {
		return 0, nil, errors.New("ERC1155 origin pool or supply differs from a funded DEL position")
	}
	return uint32(supply.Uint64()), pool, nil
}

func (e *Executor) readNFTCompanion(ctx sdk.Context, validator, beneficiary common.Address) (DELTransition, []byte, error) {
	var zero common.Hash
	coinID := crypto.Keccak256Hash(validator[:], beneficiary[:], e.roles.WrappedDEL[:], zero[:], zero[:])
	if err := e.verifyDELStakeCursor(ctx, coinID); err != nil {
		return DELTransition{}, nil, err
	}
	post, err := e.read(ctx, e.roles.Gateway, e.gateway, "getStake", delStakeBytes, validator, beneficiary, e.roles.WrappedDEL)
	if err != nil {
		return DELTransition{}, nil, err
	}
	grossWord := common.BytesToHash(post[3*common.HashLength : 4*common.HashLength])
	if !matchesNFTWords(post, common.BytesToHash(validator[:]), common.BytesToHash(beneficiary[:]), common.BytesToHash(e.roles.WrappedDEL[:]),
		grossWord, zero, smallWord(tokenDEL), zero, zero) {
		return DELTransition{}, nil, errors.New("NFT companion is not the expected ordinary DEL position")
	}
	direct, backed, err := e.directDELPoststate(ctx, validator, beneficiary, 0, grossWord.Big())
	if err != nil {
		return DELTransition{}, nil, err
	}
	return DELTransition{Validator: validator, Beneficiary: beneficiary, StakeID: coinID,
		Remaining: sdkmath.NewIntFromBigInt(direct), NFTBacked: sdkmath.NewIntFromBigInt(backed)}, post, nil
}

func (e *Executor) verifyNFTStake(ctx sdk.Context, position evmview.PositionSnapshot, validator, beneficiary common.Address, active bool) (common.Hash, error) {
	var zero common.Hash
	vault := position.Asset.Contract
	id := crypto.Keccak256Hash([]byte("NFT"), vault[:], position.Asset.TokenID[:], beneficiary[:], validator[:], zero[:])
	post, err := e.read(ctx, e.roles.Gateway, e.gateway, "getNFTStakeById", 10*common.HashLength, id)
	if err != nil {
		return common.Hash{}, err
	}
	if !matchesNFTWords(post, common.BytesToHash(vault[:]), position.Asset.TokenID, smallWord(1), smallWord(tokenERC721),
		common.BytesToHash(beneficiary[:]), common.BytesToHash(validator[:]), common.BytesToHash(e.roles.WrappedDEL[:]), position.ReserveAmount, zero, boolWord(active)) {
		return common.Hash{}, errors.New("Gateway NFT stake differs from the authenticated vault position")
	}
	cursor, err := e.read(ctx, e.roles.Gateway, e.gateway, "getNFTStakePenaltyIndex", common.HashLength, id)
	if err != nil {
		return common.Hash{}, err
	}
	if common.BytesToHash(cursor) != (common.Hash{}) {
		return common.Hash{}, errors.New("NFT position has an unsupported penalty cursor")
	}
	return id, nil
}

func matchesNFTEvent(log *ethtypes.Log, address common.Address, topics []common.Hash, data ...common.Hash) bool {
	if log.Address != address || len(log.Topics) != len(topics) {
		return false
	}
	for i, topic := range topics {
		if log.Topics[i] != topic {
			return false
		}
	}
	return matchesNFTWords(log.Data, data...)
}

func matchesNFTWords(data []byte, words ...common.Hash) bool {
	if len(data) != common.HashLength*len(words) {
		return false
	}
	for i, word := range words {
		if !bytes.Equal(data[i*common.HashLength:(i+1)*common.HashLength], word[:]) {
			return false
		}
	}
	return true
}

func boolWord(value bool) common.Hash {
	if value {
		return smallWord(1)
	}
	return common.Hash{}
}
