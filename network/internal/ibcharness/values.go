// Derived from ibc-go v11.2.0 testing/values.go, Apache-2.0.
package ibctesting

import (
	"github.com/cometbft/cometbft/crypto/tmhash"
	transfer "github.com/cosmos/ibc-go/v11/modules/apps/transfer/types"
	connectiontypes "github.com/cosmos/ibc-go/v11/modules/core/03-connection/types"
	commitmenttypes "github.com/cosmos/ibc-go/v11/modules/core/23-commitment/types"
	ibctm "github.com/cosmos/ibc-go/v11/modules/light-clients/07-tendermint"
	"time"
)

const (
	TrustingPeriod               = time.Hour * 24 * 7 * 2
	UnbondingPeriod              = time.Hour * 24 * 7 * 3
	MaxClockDrift                = time.Second * 10
	DefaultDelayPeriod    uint64 = 0
	DefaultChannelVersion        = transfer.V1
	TransferPort                 = transfer.PortID
)

var (
	DefaultOpenInitVersion       *connectiontypes.Version
	DefaultTrustLevel            = ibctm.DefaultTrustLevel
	DefaultTimeoutTimestampDelta = uint64(time.Hour.Nanoseconds())
	ConnectionVersion            = connectiontypes.GetCompatibleVersions()[0]
	prefix                       = commitmenttypes.NewMerklePrefix([]byte("ibc"))
	unusedHash                   = tmhash.Sum([]byte{0})
)

var UpgradePath = []string{"upgrade", "upgradedIBCState"}
var MerklePath = commitmenttypes.NewMerklePath([]byte("ibc"), []byte(""))
