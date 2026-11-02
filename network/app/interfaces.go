// Derived from cosmos/evm/evmd at c3ae9067a7eadcdea55a5192226916deb44c71c4.
// Licensed under Apache-2.0; see LICENSE.

package app

import (
	cmn "github.com/cosmos/evm/precompiles/common"
	evmtypes "github.com/cosmos/evm/x/vm/types"
)

type BankKeeper interface {
	evmtypes.BankKeeper
	cmn.BankKeeper
}
