package app

import (
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
		for _, msg := range tx.GetMsgs() {
			eth, ok := msg.(*evmtypes.MsgEthereumTx)
			if !ok {
				continue
			}
			raw := eth.AsTransaction()
			if raw == nil || !raw.Protected() || raw.ChainId().Cmp(expected) != 0 {
				return ctx, sdkerrors.ErrUnauthorized.Wrap("a protected Ethereum signature for this network's EVM chain ID is required")
			}
		}
		return next(ctx, tx, simulate)
	}
}
