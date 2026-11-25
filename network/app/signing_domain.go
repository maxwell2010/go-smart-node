package app

import (
	"bytes"
	"math/big"

	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	evmtypes "github.com/cosmos/evm/x/vm/types"
)

// protectEthereumDomain is consensus policy for the separate network. RPC-only
// admission flags cannot stop an unprotected legacy signature submitted in an
// ABCI transaction, so enforce EIP-155 protection before canonical ante writes.
func protectEthereumDomain(next sdk.AnteHandler, evmID uint64) sdk.AnteHandler {
	expected := new(big.Int).SetUint64(evmID)
	return func(ctx sdk.Context, tx sdk.Tx, simulate bool) (sdk.Context, error) {
		messages := tx.GetMsgs()
		for _, msg := range messages {
			eth, ok := msg.(*evmtypes.MsgEthereumTx)
			if !ok {
				continue
			}
			if eth == nil {
				return ctx, sdkerrors.ErrUnauthorized.Wrap("missing Ethereum envelope")
			}
			raw := eth.AsTransaction()
			if raw == nil || !raw.Protected() || raw.ChainId().Cmp(expected) != 0 {
				return ctx, sdkerrors.ErrUnauthorized.Wrap("a protected Ethereum signature for this network's EVM chain ID is required")
			}
			if testIssuanceActive(ctx.ChainID()) && len(messages) == 1 && raw.To() != nil && len(raw.Data()) == 68 && bytes.Equal(raw.Data()[:4], buySelector) && raw.Value().Sign() > 0 {
				ctx = ctx.WithValue(reserveBuyCreditKey{}, reserveBuyCredit{token: *raw.To(), amount: raw.Value()})
			}
		}
		return next(ctx, tx, simulate)
	}
}
