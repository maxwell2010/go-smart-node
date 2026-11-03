package systemcontracts_test

import (
	"context"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/gov"
	"github.com/cosmos/evm/x/vm/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/maxwell2010/go-smart-node/network/authority"
)

// Test-only integration wrapper keeps the canonical SDK module and tally;
// production wiring remains gated on the completed native validator ledger.
type authorityGovModule struct{ gov.AppModule }

func (m authorityGovModule) EndBlock(ctx context.Context) error {
	return authority.RunGovernance(sdk.UnwrapSDKContext(ctx), func(c sdk.Context) error { return m.AppModule.EndBlock(c) })
}

type authorityReceiptHook func(sdk.Context, common.Address, core.Message, *ethtypes.Receipt) error

var _ types.EvmHooks = authorityReceiptHook(nil)

func (h authorityReceiptHook) PostTxProcessing(c sdk.Context, s common.Address, m core.Message, r *ethtypes.Receipt) error {
	return h(c, s, m, r)
}
