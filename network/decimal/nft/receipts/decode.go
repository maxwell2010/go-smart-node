package receipts

import (
	"bytes"
	"errors"
	"fmt"
	"math/big"
	"strings"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
)

var ErrAdmissionRequired = errors.New("NFT operation requires authenticated metadata or financial admission")

type Decoder struct {
	auth                               Authenticator
	limits                             Limits
	transfer721, single1155, batch1155 common.Hash
	singleArgs, batchArgs              abi.Arguments
	admissionTopics                    map[common.Hash]bool
}

// NewDecoder validates compiler ABIs, then builds independent fixed primitive
// argument readers. Later mutation of caller ABI maps or type pointers cannot
// alter receipt interpretation.
func NewDecoder(auth Authenticator, nft721, nft1155 abi.ABI, limits Limits) (*Decoder, error) {
	if nilDependency(auth) {
		return nil, errors.New("NFT decoder requires an approved-state authenticator")
	}
	if err := limits.Validate(); err != nil {
		return nil, err
	}
	check := func(a abi.ABI, name, sig string, indexed []bool) (common.Hash, error) {
		e, ok := a.Events[name]
		if !ok || e.Anonymous || e.Sig != sig || e.ID != crypto.Keccak256Hash([]byte(sig)) || len(e.Inputs) != len(indexed) {
			return common.Hash{}, fmt.Errorf("unexpected compiler NFT event %s", name)
		}
		for i, input := range e.Inputs {
			if input.Indexed != indexed[i] {
				return common.Hash{}, fmt.Errorf("unexpected compiler NFT indexed layout")
			}
		}
		return e.ID, nil
	}
	d := &Decoder{auth: auth, limits: limits, admissionTopics: make(map[common.Hash]bool)}
	var err error
	if d.transfer721, err = check(nft721, "Transfer", "Transfer(address,address,uint256)", []bool{true, true, true}); err != nil {
		return nil, err
	}
	if d.single1155, err = check(nft1155, "TransferSingle", "TransferSingle(address,address,address,uint256,uint256)", []bool{true, true, true, false, false}); err != nil {
		return nil, err
	}
	if d.batch1155, err = check(nft1155, "TransferBatch", "TransferBatch(address,address,address,uint256[],uint256[])", []bool{true, true, true, false, false}); err != nil {
		return nil, err
	}
	u256, err := abi.NewType("uint256", "", nil)
	if err != nil {
		return nil, err
	}
	u256s, err := abi.NewType("uint256[]", "", nil)
	if err != nil {
		return nil, err
	}
	d.singleArgs = abi.Arguments{{Type: u256}, {Type: u256}}
	d.batchArgs = abi.Arguments{{Type: u256s}, {Type: u256s}}
	for _, a := range []abi.ABI{nft721, nft1155} {
		for name, event := range a.Events {
			if name == "ReserveUpdated" || name == "URI" || name == "MetadataUpdate" || name == "BatchMetadataUpdate" || name == "ContractURIUpdated" || name == "DisabledMint" {
				if event.Anonymous || !strings.HasPrefix(event.Sig, name+"(") || event.ID != crypto.Keccak256Hash([]byte(event.Sig)) {
					return nil, errors.New("unexpected compiler NFT admission event")
				}
				d.admissionTopics[event.ID] = true
			}
		}
	}
	return d, nil
}

type Decoded struct {
	Transfers []Transfer
	Profiles  map[common.Address]CollectionProfile
}

func (d *Decoder) Decode(ctx sdk.Context, execution Execution) (Decoded, error) {
	decoded, err := d.decode(ctx, execution)
	if err != nil {
		return Decoded{}, err
	}
	return decoded, nil
}

func (d *Decoder) decode(ctx sdk.Context, execution Execution) (Decoded, error) {
	result := Decoded{Profiles: make(map[common.Address]CollectionProfile)}
	if d == nil || d.auth == nil {
		return result, errors.New("NFT decoder is not initialized")
	}
	if err := execution.EntryPoint.Validate(); err != nil {
		return result, err
	}
	r := execution.Receipt
	if r == nil {
		return result, errors.New("missing NFT execution receipt")
	}
	if r.Status == ethtypes.ReceiptStatusFailed {
		return result, nil
	}
	if r.Status != ethtypes.ReceiptStatusSuccessful || uint64(len(r.Logs)) > uint64(d.limits.MaxLogs) {
		return result, errors.New("invalid NFT receipt status or log limit")
	}
	if execution.EntryPoint == EntryPointSignedEthereum && r.TxHash == (common.Hash{}) {
		return result, errors.New("signed NFT execution lacks transaction identity")
	}
	totalBytes := uint64(len(r.PostState))
	if totalBytes > uint64(d.limits.MaxBytes) {
		return result, errors.New("NFT receipt byte limit exceeded")
	}
	for _, value := range []*big.Int{r.EffectiveGasPrice, r.BlobGasPrice, r.BlockNumber} {
		if value != nil && (value.Sign() < 0 || value.BitLen() > 256) {
			return result, errors.New("NFT receipt integer metadata exceeds bound")
		}
	}
	for _, log := range r.Logs {
		if log == nil {
			return result, errors.New("nil NFT receipt log")
		}
		totalBytes += uint64(len(log.Data)) + uint64(len(log.Topics))*32
		if totalBytes > uint64(d.limits.MaxBytes) {
			return result, errors.New("NFT receipt byte limit exceeded")
		}
	}
	ctx.GasMeter().ConsumeGas(250+totalBytes*2+uint64(len(r.Logs))*80, "NFT receipt bounded decoding")
	ignored := make(map[common.Address]bool)
	var units uint64
	for position, log := range r.Logs {
		if len(log.Topics) == 0 {
			continue
		}
		topic := log.Topics[0]
		isTransfer := topic == d.transfer721 || topic == d.single1155 || topic == d.batch1155
		if !isTransfer && !d.admissionTopics[topic] {
			continue
		}
		if ignored[log.Address] {
			continue
		}
		profile, known := result.Profiles[log.Address]
		if !known {
			readCtx, _ := ctx.CacheContext()
			var err error
			profile, known, err = d.auth.AuthenticateCollection(readCtx, log.Address, execution.EntryPoint)
			if err != nil {
				return result, fmt.Errorf("authenticate NFT emitter: %w", err)
			}
			if !known {
				ignored[log.Address] = true
				continue
			}
			if err := validateProfile(ctx, log.Address, profile); err != nil {
				return result, err
			}
			if uint64(len(result.Profiles)) >= uint64(d.limits.MaxAssets) {
				return result, errors.New("NFT receipt asset limit exceeded")
			}
			profile.Escrows = append([]common.Address(nil), profile.Escrows...)
			result.Profiles[log.Address] = profile
		}
		if log.Removed {
			return result, errors.New("registered NFT receipt log is removed")
		}
		if d.admissionTopics[topic] {
			return result, ErrAdmissionRequired
		}
		moves, err := d.decodeTransfers(log, uint32(position), profile.NFTType)
		if err != nil {
			return result, err
		}
		if uint64(len(result.Transfers))+uint64(len(moves)) > uint64(d.limits.MaxIDs) {
			return result, errors.New("NFT receipt transfer-ID limit exceeded")
		}
		for _, move := range moves {
			if move.From == (common.Address{}) || move.To == (common.Address{}) {
				return result, ErrAdmissionRequired
			}
			units += uint64(move.Quantity)
			if units > uint64(d.limits.MaxUnits) {
				return result, errors.New("NFT receipt unit-work limit exceeded")
			}
		}
		result.Transfers = append(result.Transfers, moves...)
	}
	return result, nil
}

func validateProfile(ctx sdk.Context, address common.Address, p CollectionProfile) error {
	if err := types.ValidateExternalDomain(p.ChainID, p.EVMChainID); err != nil {
		return err
	}
	if p.ChainID != ctx.ChainID() || p.Contract != address || p.Factory == (common.Address{}) || p.ProxyCodeHash == (common.Hash{}) || p.ImplementationCodeHash == (common.Hash{}) || (p.NFTType != types.NftType_NFT721 && p.NFTType != types.NftType_NFT1155) || len(p.Escrows) > 16 {
		return errors.New("invalid approved NFT collection profile")
	}
	seen := map[common.Address]bool{}
	for _, a := range p.Escrows {
		if a == (common.Address{}) || seen[a] {
			return errors.New("invalid approved NFT escrow profile")
		}
		seen[a] = true
	}
	return nil
}

func topicAddress(topic common.Hash) (common.Address, error) {
	if !bytes.Equal(topic[:12], make([]byte, 12)) {
		return common.Address{}, errors.New("noncanonical NFT address topic")
	}
	return common.BytesToAddress(topic[12:]), nil
}

func (d *Decoder) decodeTransfers(log *ethtypes.Log, position uint32, standard types.NftType) ([]Transfer, error) {
	if len(log.Topics) != 4 {
		return nil, errors.New("invalid NFT transfer topic count")
	}
	metadata := LogMetadata{BlockNumber: log.BlockNumber, TxHash: log.TxHash, TxIndex: log.TxIndex, Index: log.Index}
	base := Transfer{Ref: MoveRef{LogPosition: position}, Metadata: metadata, Asset: AssetKey{Contract: log.Address}, NFTType: standard}
	var err error
	if log.Topics[0] == d.transfer721 {
		if standard != types.NftType_NFT721 || len(log.Data) != 0 {
			return nil, errors.New("invalid ERC721 registered transfer")
		}
		if base.From, err = topicAddress(log.Topics[1]); err != nil {
			return nil, err
		}
		if base.To, err = topicAddress(log.Topics[2]); err != nil {
			return nil, err
		}
		base.Asset.TokenID, base.Quantity = log.Topics[3], 1
		return []Transfer{base}, nil
	}
	if standard != types.NftType_NFT1155 {
		return nil, errors.New("ERC1155 event differs from registered NFT standard")
	}
	if base.Operator, err = topicAddress(log.Topics[1]); err != nil {
		return nil, err
	}
	if base.From, err = topicAddress(log.Topics[2]); err != nil {
		return nil, err
	}
	if base.To, err = topicAddress(log.Topics[3]); err != nil {
		return nil, err
	}
	args := d.singleArgs
	if log.Topics[0] == d.batch1155 {
		args = d.batchArgs
	}
	values, err := args.Unpack(log.Data)
	if err != nil {
		return nil, fmt.Errorf("decode NFT transfer: %w", err)
	}
	encoded, err := args.Pack(values...)
	if err != nil || !bytes.Equal(encoded, log.Data) {
		return nil, errors.New("noncanonical NFT transfer payload")
	}
	ids, quantities := []*big.Int{}, []*big.Int{}
	if log.Topics[0] == d.single1155 {
		ids = append(ids, values[0].(*big.Int))
		quantities = append(quantities, values[1].(*big.Int))
	} else {
		ids = values[0].([]*big.Int)
		quantities = values[1].([]*big.Int)
	}
	if len(ids) != len(quantities) || uint64(len(ids)) > uint64(d.limits.MaxIDs) {
		return nil, errors.New("invalid ERC1155 batch dimensions")
	}
	moves := make([]Transfer, 0, len(ids))
	for i, id := range ids {
		quantity := quantities[i]
		if id == nil || id.Sign() < 0 || id.BitLen() > 256 || quantity == nil || quantity.Sign() < 0 || quantity.BitLen() > 32 || quantity.Uint64() > uint64(d.limits.MaxUnits) {
			return nil, errors.New("NFT transfer identity or quantity exceeds native bounds")
		}
		move := base
		move.Ref.ItemPosition = uint32(i)
		move.Asset.TokenID = common.BigToHash(id)
		move.Quantity = uint32(quantity.Uint64())
		moves = append(moves, move)
	}
	return moves, nil
}
