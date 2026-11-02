// Derived from cosmos/ibc-go v11.2.0 testing/coordinator.go. Apache-2.0; see LICENSE.
package ibctesting

import (
	"testing"
	"time"
)

var (
	ChainIDPrefix = "testchain"
	// to disable revision format, set ChainIDSuffix to ""
	ChainIDSuffix   = "-1"
	globalStartTime = time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC)
	TimeIncrement   = time.Second * 5
)

// Coordinator is a testing struct which contains N TestChain's. It handles keeping all chains
// in sync with regards to time.
type Coordinator struct {
	*testing.T

	CurrentTime time.Time
	Chains      map[string]*TestChain
}

// NewCoordinator initializes Coordinator with N TestChain's
func (c *Coordinator) IncrementTime() {
	c.IncrementTimeBy(TimeIncrement)
}

// IncrementTimeBy iterates through all the TestChain's and increments their current header time
// by specified time.
func (c *Coordinator) IncrementTimeBy(increment time.Duration) {
	c.CurrentTime = c.CurrentTime.Add(increment).UTC()
	c.UpdateTime()
}

// SetTime sets the coordinator's current time to the specified time and updates
// the proposed header time for all chains.
func (c *Coordinator) SetTime(t time.Time) {
	c.CurrentTime = t.UTC()
	c.UpdateTime()
}

// UpdateTime updates all clocks for the TestChains to the current global time.
func (c *Coordinator) UpdateTime() {
	for _, chain := range c.Chains {
		c.UpdateTimeForChain(chain)
	}
}

// UpdateTimeForChain updates the clock for a specific chain.
func (c *Coordinator) UpdateTimeForChain(chain *TestChain) {
	chain.ProposedHeader.Time = c.CurrentTime.UTC()
}

// Setup constructs a TM client, connection, and channel on both chains provided. It will
// fail if any error occurs.
//
// Deprecated: please use path.Setup(), this function will be removed in v10
func (*Coordinator) Setup(path *Path) {
	path.Setup()
}

// SetupClients is a helper function to create clients on both chains. It assumes the
// caller does not anticipate any errors.
//
// Deprecated: please use path.SetupClients(), this function will be removed in v10
func (*Coordinator) SetupClients(path *Path) {
	path.SetupClients()
}

// SetupConnections is a helper function to create clients and the appropriate
// connections on both the source and counterparty chain. It assumes the caller does not
// anticipate any errors.
//
// Deprecated: please use path.SetupConnections(), this function will be removed in v10
func (*Coordinator) SetupConnections(path *Path) {
	path.SetupConnections()
}

// CreateConnections constructs and executes connection handshake messages in order to create
// OPEN channels on chainA and chainB. The connection information of for chainA and chainB
// are returned within a TestConnection struct. The function expects the connections to be
// successfully opened otherwise testing will fail.
//
// Deprecated: please use path.CreateConnections(), this function will be removed in v10
func (*Coordinator) CreateConnections(path *Path) {
	path.CreateConnections()
}

// CreateMockChannels constructs and executes channel handshake messages to create OPEN
// channels that use a mock application module that returns nil on all callbacks. This
// function is expects the channels to be successfully opened otherwise testing will
// fail.
func (*Coordinator) CreateTransferChannels(path *Path) {
	path.EndpointA.ChannelConfig.PortID = TransferPort
	path.EndpointB.ChannelConfig.PortID = TransferPort

	path.CreateChannels()
}

// GetChain returns the TestChain using the given chainID and returns an error if it does
// not exist.
func (c *Coordinator) CommitBlock(chains ...*TestChain) {
	for _, chain := range chains {
		chain.NextBlock()
	}
	c.IncrementTime()
}

// CommitNBlocks commits n blocks to state and updates the block height by 1 for each commit.
func (c *Coordinator) CommitNBlocks(chain *TestChain, n uint64) {
	for range n {
		chain.NextBlock()
		c.IncrementTime()
	}
}
