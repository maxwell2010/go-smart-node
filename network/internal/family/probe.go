// Package family checks the owned application's dependency wiring in memory.
// It does not implement the Decimal module port or a runnable network daemon.
package family

import (
	"fmt"

	"cosmossdk.io/log"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/client/flags"
	srvflags "github.com/cosmos/evm/server/flags"
	transfertypes "github.com/cosmos/ibc-go/v10/modules/apps/transfer/types"
	ownedapp "github.com/maxwell2010/go-smart-node/network/app"
)

type appOptions map[string]interface{}

func (o appOptions) Get(key string) interface{} { return o[key] }

// CheckWiring constructs the owned application with an empty
// in-memory database and a caller-owned disposable home. It opens no listeners.
func CheckWiring(home string) error {
	if home == "" {
		return fmt.Errorf("a disposable probe home is required")
	}
	app := ownedapp.NewApp(log.NewNopLogger(), dbm.NewMemDB(), true, appOptions{
		flags.FlagHome:      home,
		flags.FlagChainID:   "mintcandy_990001-1",
		srvflags.EVMChainID: uint64(990001),
	})
	defer app.Close()
	if app.EVMKeeper == nil || app.IBCKeeper == nil || app.ModuleManager.Modules[transfertypes.ModuleName] == nil {
		return fmt.Errorf("owned EVM, IBC or transfer keeper was not wired")
	}
	return nil
}
