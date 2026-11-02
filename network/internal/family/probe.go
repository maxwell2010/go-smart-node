// Package family checks the upstream application dependency wiring in memory.
// It does not implement the Decimal module port or a runnable network daemon.
package family

import (
	"fmt"

	"cosmossdk.io/log/v2"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/evm/evmd"
	srvflags "github.com/cosmos/evm/server/flags"
)

type appOptions map[string]interface{}

func (o appOptions) Get(key string) interface{} { return o[key] }

// CheckWiring constructs the pinned upstream example application with an empty
// in-memory database and a caller-owned disposable home. It opens no listeners.
func CheckWiring(home string) error {
	if home == "" {
		return fmt.Errorf("a disposable probe home is required")
	}
	app := evmd.NewExampleApp(log.NewNopLogger(), dbm.NewMemDB(), true, appOptions{
		flags.FlagHome:      home,
		srvflags.EVMChainID: uint64(990001),
	})
	defer app.Close()
	if app.EVMKeeper == nil || app.IBCKeeper == nil || app.TransferKeeper == nil {
		return fmt.Errorf("upstream EVM, IBC or transfer keeper was not wired")
	}
	return nil
}
