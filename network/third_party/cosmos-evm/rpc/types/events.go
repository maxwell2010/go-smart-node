package types

import (
	"fmt"
	"math/big"
	"strconv"

	"github.com/ethereum/go-ethereum/common"

	abci "github.com/cometbft/cometbft/abci/types"
	cmtrpctypes "github.com/cometbft/cometbft/rpc/core/types"

	"github.com/cosmos/evm/server/types"
	evmtypes "github.com/cosmos/evm/x/vm/types"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

// EventFormat is the format version of the events.
//
// To fix the issue of tx exceeds block gas limit, we changed the event format in a breaking way.
// But to avoid forcing clients to re-sync from scatch, we make json-rpc logic to be compatible with both formats.
type EventFormat int

const (
	eventFormatUnknown EventFormat = iota

	// Event Format 1 (the format used before PR #1062):
	// ```
	// ethereum_tx(amount, ethereumTxHash, [txIndex, txGasUsed], txHash, [recipient], ethereumTxFailed)
	// tx_log(txLog, txLog, ...)
	// ethereum_tx(amount, ethereumTxHash, [txIndex, txGasUsed], txHash, [recipient], ethereumTxFailed)
	// tx_log(txLog, txLog, ...)
	// ...
	// ```
	eventFormat1

	// Event Format 2 (the format used after PR #1062):
	// ```
	// ethereum_tx(ethereumTxHash, txIndex)
	// ethereum_tx(ethereumTxHash, txIndex)
	// ...
	// ethereum_tx(amount, ethereumTxHash, txIndex, txGasUsed, txHash, [recipient], ethereumTxFailed)
	// tx_log(txLog, txLog, ...)
	// ethereum_tx(amount, ethereumTxHash, txIndex, txGasUsed, txHash, [recipient], ethereumTxFailed)
	// tx_log(txLog, txLog, ...)
	// ...
	// ```
	// If the transaction exceeds block gas limit, it only emits the first part.
	eventFormat2
)

// ParsedTx is the tx infos parsed from events.
type ParsedTx struct {
	MsgIndex int

	// the following fields are parsed from events

	Hash common.Hash
	// -1 means uninitialized
	EthTxIndex int32
	GasUsed    uint64
	Failed     bool
}

// NewParsedTx initialize a ParsedTx
func NewParsedTx(msgIndex int) ParsedTx {
	return ParsedTx{MsgIndex: msgIndex, EthTxIndex: -1}
}

// ParsedTxs is the tx infos parsed from eth tx events.
type ParsedTxs struct {
	// one item per message
	Txs []ParsedTx
	// map tx hash to msg index
	TxHashes map[common.Hash]int
}

// ParseTxResult parse eth tx infos from cosmos-sdk events.
// It supports two event formats, the formats are described in the comments of the format constants.
func ParseTxResult(result *abci.ExecTxResult, tx sdk.Tx) (*ParsedTxs, error) {
	format := eventFormatUnknown
	// the index of current ethereum_tx event in format 1 or the second part of format 2
	eventIndex := -1

	p := &ParsedTxs{
		TxHashes: make(map[common.Hash]int),
	}
	for _, event := range result.Events {
		if event.Type != evmtypes.EventTypeEthereumTx {
			continue
		}

		if format == eventFormatUnknown {
			// discover the format version by inspect the first ethereum_tx event.
			if len(event.Attributes) > 2 {
				format = eventFormat1
			} else {
				format = eventFormat2
			}
		}

		if len(event.Attributes) == 2 {
			// the first part of format 2
			if err := p.newTx(event.Attributes); err != nil {
				return nil, err
			}
		} else {
			// format 1 or second part of format 2
			eventIndex++
			if format == eventFormat1 {
				// append tx
				if err := p.newTx(event.Attributes); err != nil {
					return nil, err
				}
			} else {
				// the second part of format 2, update tx fields
				if err := p.updateTx(eventIndex, event.Attributes); err != nil {
					return nil, err
				}
			}
		}
	}

	// some old versions miss some events, fill it with tx result
	gasUsed := uint64(result.GasUsed) // #nosec G115
	fee, err := ParseMintCandyFeeEvent(result)
	if err != nil {
		return nil, err
	}
	if fee != nil {
		if len(p.Txs) != 1 || fee.Transaction != p.Txs[0].Hash || result.GasUsed < 0 || fee.ConversionGas > gasUsed {
			return nil, fmt.Errorf("reserve fee event does not match one bounded Ethereum result")
		}
		gasUsed -= fee.ConversionGas
		// Explicit EVM gas and combined SDK gas must reconcile exactly. Older
		// event formats without a gas count can still use the checked fallback.
		if result.Code == 0 && p.Txs[0].GasUsed != 0 && p.Txs[0].GasUsed != gasUsed {
			return nil, fmt.Errorf("reserve fee gas differs from the committed EVM gas")
		}
	}
	if len(p.Txs) == 1 {
		p.Txs[0].GasUsed = gasUsed
	}

	// this could only happen if tx exceeds block gas limit
	if result.Code != 0 && tx != nil {
		for i := 0; i < len(p.Txs); i++ {
			p.Txs[i].Failed = true

			// replace gasUsed with gasLimit because that's what's actually deducted.
			gasLimit := tx.GetMsgs()[i].(*evmtypes.MsgEthereumTx).GetGas()
			p.Txs[i].GasUsed = gasLimit
		}
	}
	return p, nil
}

// MintCandyFee is additional ante payment metadata, separate from Ethereum's
// signed execution gas. Values are parsed only from committed SDK events, not
// arbitrary EVM logs. It establishes no signature or reserve authorization.
type MintCandyFee struct {
	Payer, Token                                      common.Address
	Transaction                                       common.Hash
	FeeNonce, TokenInput, NativeOutput, ConversionFee string
	ConversionGas                                     uint64
}

// ParseMintCandyFeeEvent bounds and validates the one supported fee event.
// Malformed/duplicate metadata is an error, not a reason to publish inflated
// receipt.gasUsed. Standard non-opted-in transactions retain their old behavior.
func ParseMintCandyFeeEvent(result *abci.ExecTxResult) (*MintCandyFee, error) {
	if result == nil {
		return nil, fmt.Errorf("missing reserve fee transaction result")
	}
	var found *MintCandyFee
	for _, event := range result.Events {
		if event.Type != "mintcandy_drc20_fee" {
			continue
		}
		if found != nil || len(event.Attributes) != 8 {
			return nil, fmt.Errorf("duplicate or malformed reserve fee event")
		}
		fields := make(map[string]string, 8)
		for _, attr := range event.Attributes {
			if _, exists := fields[attr.Key]; exists {
				return nil, fmt.Errorf("duplicate reserve fee attribute")
			}
			switch attr.Key {
			case "payer", "token", "transaction", "fee_nonce", "token_input", "native_output", "conversion_fee", "conversion_gas":
			default:
				return nil, fmt.Errorf("unknown reserve fee attribute")
			}
			fields[attr.Key] = attr.Value
		}
		payer, token := fields["payer"], fields["token"]
		if len(payer) != 42 || len(token) != 42 || !common.IsHexAddress(payer) || !common.IsHexAddress(token) {
			return nil, fmt.Errorf("invalid reserve fee addresses")
		}
		txHash := fields["transaction"]
		if len(txHash) != 66 || common.HexToHash(txHash).Hex() != txHash || common.HexToHash(txHash) == (common.Hash{}) {
			return nil, fmt.Errorf("invalid reserve fee transaction hash")
		}
		values := make(map[string]*big.Int, 5)
		for _, key := range []string{"fee_nonce", "token_input", "native_output", "conversion_fee", "conversion_gas"} {
			s := fields[key]
			if len(s) == 0 || len(s) > 78 || (len(s) > 1 && s[0] == '0') {
				return nil, fmt.Errorf("invalid reserve fee integer")
			}
			for _, c := range s {
				if c < '0' || c > '9' {
					return nil, fmt.Errorf("invalid reserve fee decimal")
				}
			}
			n, ok := new(big.Int).SetString(s, 10)
			if !ok || n.BitLen() > 256 || (key != "fee_nonce" && n.Sign() == 0) {
				return nil, fmt.Errorf("reserve fee integer exceeds bounds")
			}
			values[key] = n
		}
		if !values["conversion_gas"].IsUint64() || values["conversion_fee"].Cmp(values["native_output"]) > 0 {
			return nil, fmt.Errorf("invalid reserve fee gas or payment")
		}
		found = &MintCandyFee{Payer: common.HexToAddress(payer), Token: common.HexToAddress(token), Transaction: common.HexToHash(txHash),
			FeeNonce: fields["fee_nonce"], TokenInput: fields["token_input"], NativeOutput: fields["native_output"], ConversionFee: fields["conversion_fee"], ConversionGas: values["conversion_gas"].Uint64()}
		if found.Payer == (common.Address{}) || found.Token == (common.Address{}) {
			return nil, fmt.Errorf("zero reserve fee address")
		}
	}
	return found, nil
}

// ParseTxIndexerResult parse tm tx result to a format compatible with the custom tx indexer.
func ParseTxIndexerResult(txResult *cmtrpctypes.ResultTx, tx sdk.Tx, getter func(*ParsedTxs) *ParsedTx) (*types.TxResult, error) {
	txs, err := ParseTxResult(&txResult.TxResult, tx)
	if err != nil {
		return nil, fmt.Errorf("failed to parse tx events: block %d, index %d, %v", txResult.Height, txResult.Index, err)
	}

	parsedTx := getter(txs)
	if parsedTx == nil {
		return nil, fmt.Errorf("ethereum tx not found in msgs: block %d, index %d", txResult.Height, txResult.Index)
	}
	index := uint32(parsedTx.MsgIndex) // #nosec G115
	return &types.TxResult{
		Height:            txResult.Height,
		TxIndex:           txResult.Index,
		MsgIndex:          index,
		EthTxIndex:        parsedTx.EthTxIndex,
		Failed:            parsedTx.Failed,
		GasUsed:           parsedTx.GasUsed,
		CumulativeGasUsed: txs.AccumulativeGasUsed(parsedTx.MsgIndex),
	}, nil
}

// newTx parse a new tx from events, called during parsing.
func (p *ParsedTxs) newTx(attrs []abci.EventAttribute) error {
	msgIndex := len(p.Txs)
	tx := NewParsedTx(msgIndex)
	if err := fillTxAttributes(&tx, attrs); err != nil {
		return err
	}
	p.Txs = append(p.Txs, tx)
	p.TxHashes[tx.Hash] = msgIndex
	return nil
}

// updateTx updates an exiting tx from events, called during parsing.
// In event format 2, we update the tx with the attributes of the second `ethereum_tx` event,
// Due to bug https://github.com/evmos/ethermint/issues/1175, the first `ethereum_tx` event may emit incorrect tx hash,
// so we prefer the second event and override the first one.
func (p *ParsedTxs) updateTx(eventIndex int, attrs []abci.EventAttribute) error {
	tx := NewParsedTx(eventIndex)
	if err := fillTxAttributes(&tx, attrs); err != nil {
		return err
	}
	if tx.Hash != p.Txs[eventIndex].Hash {
		// if hash is different, index the new one too
		p.TxHashes[tx.Hash] = eventIndex
	}
	// override the tx because the second event is more trustworthy
	p.Txs[eventIndex] = tx
	return nil
}

// GetTxByHash find ParsedTx by tx hash, returns nil if not exists.
func (p *ParsedTxs) GetTxByHash(hash common.Hash) *ParsedTx {
	if idx, ok := p.TxHashes[hash]; ok {
		return &p.Txs[idx]
	}
	return nil
}

// GetTxByMsgIndex returns ParsedTx by msg index
func (p *ParsedTxs) GetTxByMsgIndex(i int) *ParsedTx {
	if i < 0 || i >= len(p.Txs) {
		return nil
	}
	return &p.Txs[i]
}

// GetTxByTxIndex returns ParsedTx by tx index
func (p *ParsedTxs) GetTxByTxIndex(txIndex int) *ParsedTx {
	if len(p.Txs) == 0 {
		return nil
	}
	// assuming the `EthTxIndex` increase continuously,
	// convert TxIndex to MsgIndex by subtract the begin TxIndex.
	msgIndex := txIndex - int(p.Txs[0].EthTxIndex)
	// GetTxByMsgIndex will check the bound
	return p.GetTxByMsgIndex(msgIndex)
}

// AccumulativeGasUsed calculates the accumulated gas used within the batch of txs
func (p *ParsedTxs) AccumulativeGasUsed(msgIndex int) (result uint64) {
	for i := 0; i <= msgIndex; i++ {
		result += p.Txs[i].GasUsed
	}
	return result
}

// fillTxAttribute parse attributes by name, less efficient than hardcode the index, but more stable against event
// format changes.
func fillTxAttribute(tx *ParsedTx, key string, value string) error {
	switch key {
	case evmtypes.AttributeKeyEthereumTxHash:
		tx.Hash = common.HexToHash(value)
	case evmtypes.AttributeKeyTxIndex:
		txIndex, err := strconv.ParseUint(value, 10, 31) // #nosec G115
		if err != nil {
			return err
		}
		tx.EthTxIndex = int32(txIndex) // #nosec G115
	case evmtypes.AttributeKeyTxGasUsed:
		gasUsed, err := strconv.ParseUint(value, 10, 64)
		if err != nil {
			return err
		}
		tx.GasUsed = gasUsed
	case evmtypes.AttributeKeyEthereumTxFailed:
		tx.Failed = len(value) > 0
	}
	return nil
}

func fillTxAttributes(tx *ParsedTx, attrs []abci.EventAttribute) error {
	for _, attr := range attrs {
		if err := fillTxAttribute(tx, attr.Key, attr.Value); err != nil {
			return err
		}
	}
	return nil
}
