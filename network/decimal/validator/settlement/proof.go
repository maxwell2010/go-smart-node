package settlement

import (
	"bytes"
	"errors"
	"math/big"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

type completion struct {
	index      common.Hash
	start, end int // Half-open receipt slice preceding the completion marker.
}

// CompletionIDs extracts the configured Gateway's ordered completion indices.
// Authenticity of the Gateway and its payout is checked by Complete or ValidateSigned.
func (e *Executor) CompletionIDs(receipt *ethtypes.Receipt) ([]common.Hash, error) {
	markers, err := e.completions(receipt)
	if err != nil {
		return nil, err
	}
	ids := make([]common.Hash, len(markers))
	for i, marker := range markers {
		ids[i] = marker.index
	}
	return ids, nil
}

func (e *Executor) completions(receipt *ethtypes.Receipt) ([]completion, error) {
	if e == nil || receipt == nil || receipt.Status != ethtypes.ReceiptStatusSuccessful {
		return nil, errors.New("successful Gateway receipt is required")
	}
	if uint64(len(receipt.Logs)) > uint64(e.config.MaxLogs) || len(receipt.PostState) > common.HashLength {
		return nil, errors.New("Gateway receipt exceeds configured bounds")
	}
	for _, value := range []*big.Int{receipt.BlockNumber, receipt.EffectiveGasPrice, receipt.BlobGasPrice} {
		if value != nil && (value.Sign() < 0 || value.BitLen() > 256) {
			return nil, errors.New("Gateway receipt integer exceeds uint256")
		}
	}
	topic := e.gateway.Events["WithdrawCompleted"].ID
	seen := make(map[common.Hash]struct{})
	var markers []completion
	var used uint64
	start := 0
	for i, log := range receipt.Logs {
		if log == nil || log.Removed || len(log.Topics) > 4 {
			return nil, errors.New("invalid Gateway receipt log")
		}
		used += uint64(len(log.Data)) + common.AddressLength + uint64(len(log.Topics))*common.HashLength
		if used > uint64(e.config.MaxLogBytes) {
			return nil, errors.New("Gateway receipt log bytes exceed configured bound")
		}
		if log.Address != e.roles.Gateway || len(log.Topics) == 0 || log.Topics[0] != topic {
			continue
		}
		if len(log.Topics) != 1 || len(log.Data) != common.HashLength {
			return nil, errors.New("invalid Gateway WithdrawCompleted event envelope")
		}
		index := common.BytesToHash(log.Data)
		if index == common.MaxHash {
			return nil, errors.New("forced withdrawal sentinel is not a frozen completion")
		}
		if _, exists := seen[index]; exists {
			return nil, errors.New("duplicate Gateway completion index")
		}
		seen[index] = struct{}{}
		markers = append(markers, completion{index: index, start: start, end: i})
		start = i + 1
	}
	return markers, nil
}

func signedMetadata(ctx sdk.Context, receipt *ethtypes.Receipt) error {
	hashPresent := receipt.TxHash != (common.Hash{})
	if hashPresent != (receipt.BlockNumber != nil) {
		return errors.New("incomplete Gateway receipt inclusion metadata")
	}
	// Gas estimation executes the same hook in a disposable cache without
	// inclusion metadata. An included receipt must identify this block.
	if hashPresent && (!receipt.BlockNumber.IsInt64() || receipt.BlockNumber.Int64() != ctx.BlockHeight()) {
		return errors.New("Gateway receipt belongs to another block height")
	}
	if receipt.BlockHash != (common.Hash{}) && (!hashPresent || receipt.BlockHash != common.BytesToHash(ctx.HeaderHash())) {
		return errors.New("Gateway receipt belongs to another block hash")
	}
	if hashPresent {
		for _, log := range receipt.Logs {
			// The owned StateDB supplies the transaction index. The raw log's
			// transaction hash and block hash may be absent before RPC indexing.
			if (log.TxHash != (common.Hash{}) && log.TxHash != receipt.TxHash) ||
				(log.BlockNumber != 0 && log.BlockNumber != uint64(ctx.BlockHeight())) ||
				(log.BlockHash != (common.Hash{}) && log.BlockHash != common.BytesToHash(ctx.HeaderHash())) || log.TxIndex != receipt.TransactionIndex {
				return errors.New("Gateway receipt log belongs to another transaction")
			}
		}
	}
	return nil
}

func cloneReceipt(in *ethtypes.Receipt) *ethtypes.Receipt {
	out := *in
	out.PostState = bytes.Clone(in.PostState)
	if in.BlockNumber != nil {
		out.BlockNumber = new(big.Int).Set(in.BlockNumber)
	}
	if in.EffectiveGasPrice != nil {
		out.EffectiveGasPrice = new(big.Int).Set(in.EffectiveGasPrice)
	}
	if in.BlobGasPrice != nil {
		out.BlobGasPrice = new(big.Int).Set(in.BlobGasPrice)
	}
	out.Logs = make([]*ethtypes.Log, len(in.Logs))
	for i, log := range in.Logs {
		copyLog := *log
		copyLog.Topics = append([]common.Hash(nil), log.Topics...)
		copyLog.Data = bytes.Clone(log.Data)
		out.Logs[i] = &copyLog
	}
	return &out
}

type payout struct {
	asset, beneficiary common.Address
	amount, tokenID    common.Hash
	nft                bool
}

func smallWord(value byte) (word common.Hash) {
	word[common.HashLength-1] = value
	return word
}

func (e *Executor) matchFrozen(words frozenWords, row validatortypes.EVMFrozenBinding, status byte) (payout, error) {
	// Layout comes from the pinned 11-word getFrozenStake ABI. readFrozen
	// checks canonical encoding before the individual fields are interpreted.
	validator, beneficiary, asset := common.BytesToAddress(words[0][:]), common.BytesToAddress(words[1][:]), common.BytesToAddress(words[2][:])
	if words[freezeStatusWord] != smallWord(status) || words[9] != smallWord(freezeWithdraw) || words[10] != common.BytesToHash(row.CompletionTimestamp) || validator != common.BytesToAddress(row.SourceValidator) || beneficiary != common.BytesToAddress(row.Beneficiary) || words[3] != common.BigToHash(row.RawAmount.BigInt()) {
		return payout{}, errors.New("authenticated frozen withdrawal differs from native binding")
	}
	out := payout{asset: asset, beneficiary: beneficiary, amount: words[3], tokenID: words[4]}
	var sourceID common.Hash
	switch row.SourceKind {
	case validatortypes.EVMStakeBindingKind_EVM_STAKE_BINDING_COIN:
		if row.ReserveAsset.ReserveKind != 1 || row.ReserveAsset.NativeDenom != identity.BaseDenom || asset != e.roles.WrappedDEL || words[4] != (common.Hash{}) || words[5] != smallWord(tokenDEL) {
			return payout{}, errors.New("frozen coin is outside the approved WDEL-backed DEL payout profile")
		}
		sourceID = crypto.Keccak256Hash(validator.Bytes(), beneficiary.Bytes(), asset.Bytes(), words[4][:], words[6][:])
	case validatortypes.EVMStakeBindingKind_EVM_STAKE_BINDING_NFT:
		position := row.NFTPosition
		if position.Mode != validatortypes.NFTPositionMode_NFT_POSITION_ISOLATED_RECEIPT || asset != e.roles.PositionVault || asset != common.BytesToAddress(position.Custody.Contract) || words[4] != common.BytesToHash(position.Custody.TokenID) || words[5] != smallWord(tokenERC721) || words[3] != smallWord(1) {
			return payout{}, errors.New("frozen NFT is outside the approved vault receipt payout profile")
		}
		out.nft = true
		sourceID = crypto.Keccak256Hash([]byte("NFT"), asset.Bytes(), words[4][:], beneficiary.Bytes(), validator.Bytes(), words[6][:])
	default:
		return payout{}, errors.New("unsupported frozen withdrawal payout")
	}
	if sourceID != common.BytesToHash(row.SourceStakeID) {
		return payout{}, errors.New("authenticated frozen withdrawal has a different source stake ID")
	}
	return out, nil
}

func (e *Executor) validatePayoutLog(receipt *ethtypes.Receipt, marker completion, payout payout) error {
	transfer := e.wrapper.Events["Transfer"].ID
	if payout.nft {
		transfer = e.vault.Events["Transfer"].ID
	}
	from := common.BytesToHash(e.roles.Gateway.Bytes())
	matches := 0
	for _, log := range receipt.Logs[marker.start:marker.end] {
		if log.Address != payout.asset || len(log.Topics) == 0 || log.Topics[0] != transfer {
			continue
		}
		if payout.nft {
			if len(log.Topics) != 4 || len(log.Data) != 0 || log.Topics[1] != from || log.Topics[2] != common.BytesToHash(payout.beneficiary.Bytes()) || log.Topics[3] != payout.tokenID {
				return errors.New("vault transfer differs from the bound withdrawal")
			}
		} else if len(log.Topics) != 3 || len(log.Data) != common.HashLength || log.Topics[1] != from || log.Topics[2] != (common.Hash{}) || common.BytesToHash(log.Data) != payout.amount {
			return errors.New("WDEL burn differs from the bound native withdrawal")
		}
		matches++
	}
	if matches != 1 {
		return errors.New("withdrawal requires exactly one matching payout log before completion")
	}
	return nil
}

type payoutState struct {
	beneficiary, gateway, backing, supply, wrapped *big.Int
}

func (e *Executor) payoutState(ctx sdk.Context, payout payout, nftOwner common.Address) (payoutState, error) {
	if payout.nft {
		owner, err := e.read(ctx, payout.asset, e.vault, "ownerOf", common.HashLength, payout.tokenID.Big())
		if err != nil {
			return payoutState{}, err
		}
		if common.BytesToHash(owner) != common.BytesToHash(nftOwner.Bytes()) {
			return payoutState{}, errors.New("vault receipt owner differs from the expected custody or beneficiary")
		}
		return payoutState{}, nil
	}
	supply, err := e.read(ctx, payout.asset, e.wrapper, "totalSupply", common.HashLength)
	if err != nil {
		return payoutState{}, err
	}
	wrapped, err := e.read(ctx, payout.asset, e.wrapper, "balanceOf", common.HashLength, e.roles.Gateway)
	if err != nil {
		return payoutState{}, err
	}
	// GetBalance uses the owned SDK bank wrapper for the native DEL balance.
	beneficiary := e.evm.GetBalance(ctx, payout.beneficiary)
	gateway := e.evm.GetBalance(ctx, e.roles.Gateway)
	backing := e.evm.GetBalance(ctx, payout.asset)
	if beneficiary == nil || gateway == nil || backing == nil {
		return payoutState{}, errors.New("invalid native DEL balance in Gateway withdrawal")
	}
	state := payoutState{beneficiary: beneficiary.ToBig(), gateway: gateway.ToBig(), backing: backing.ToBig(), supply: new(big.Int).SetBytes(supply), wrapped: new(big.Int).SetBytes(wrapped)}
	if state.backing.Cmp(state.supply) < 0 || state.wrapped.Cmp(state.supply) > 0 {
		return payoutState{}, errors.New("WDEL payout asset is not fully backed")
	}
	return state, nil
}

func (p payout) checkDelta(before, after payoutState) error {
	if p.nft {
		return nil // The exact owner changed from Gateway to beneficiary.
	}
	amount := p.amount.Big()
	if new(big.Int).Sub(after.beneficiary, before.beneficiary).Cmp(amount) != 0 ||
		before.gateway.Cmp(after.gateway) != 0 ||
		new(big.Int).Sub(before.backing, after.backing).Cmp(amount) != 0 ||
		new(big.Int).Sub(before.supply, after.supply).Cmp(amount) != 0 ||
		new(big.Int).Sub(before.wrapped, after.wrapped).Cmp(amount) != 0 {
		return errors.New("actual native DEL and WDEL withdrawal deltas differ from the bound amount")
	}
	return nil
}
