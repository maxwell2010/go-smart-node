package evmview

import (
	"encoding/binary"
	"errors"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/maxwell2010/go-smart-node/network/decimal/nft/receipts"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
)

// StakeSource contains only authenticated role/code/domain identity. It never
// depends on native NFT prestate, ownership, stake amount or current collateral.
type StakeSource struct {
	ChainID     string
	EVMChainID  uint64
	Gateway     common.Address
	Fingerprint common.Hash
}

func (r *Reader) AuthenticateStakeSource(ctx sdk.Context, execution receipts.Execution) (source StakeSource, err error) {
	if r == nil || r.config.Protocol != ProfileIsolatedPositionsV1 || r.config.MasterValidator == (common.Address{}) {
		return source, errors.New("stake source requires explicit isolated profile and approved MasterValidator")
	}
	if err := execution.EntryPoint.Validate(); err != nil {
		return source, err
	}
	if execution.Receipt == nil || execution.Receipt.Status != ethtypes.ReceiptStatusSuccessful {
		return source, errors.New("stake source requires an actual successful execution receipt")
	}
	err = r.bounded(ctx, func(child sdk.Context) error {
		if e := r.domain(child); e != nil {
			return e
		}
		if e := r.systems(child); e != nil {
			return e
		}
		if e := r.authenticateVault(child); e != nil {
			return e
		}
		if e := r.system(child, r.config.MasterValidator, r.master); e != nil {
			return e
		}
		actual, e := r.address(child, r.config.Registry, r.registry.ABI, "getContractAddress", "master-validator")
		if e != nil {
			return e
		}
		if actual != r.config.MasterValidator {
			return errors.New("stake source MasterValidator registry role differs")
		}
		center, e := r.address(child, r.config.MasterValidator, r.master.ABI, "contractCenter")
		if e != nil {
			return e
		}
		if center != r.config.Registry {
			return errors.New("stake source MasterValidator registry backlink differs")
		}
		wrapper := r.wrapper
		if e := r.code(child, r.config.WrappedDEL, wrapper.Runtime); e != nil {
			return e
		}
		var chainLength [2]byte
		binary.BigEndian.PutUint16(chainLength[:], uint16(len(r.config.ChainID)))
		var evmID [8]byte
		binary.BigEndian.PutUint64(evmID[:], r.config.EVMChainID)
		parts := [][]byte{[]byte("mintcandy:isolated-stake-source:v1\x00"), chainLength[:], []byte(r.config.ChainID), evmID[:], r.owner.Bytes()}
		// These implementations have already passed current-release code/owner checks.
		// Their live addresses are included for freshness even for byte-identical upgrades.
		for _, role := range []struct {
			address  common.Address
			artifact contracts.Artifact
		}{{r.config.Registry, r.registry}, {r.config.Delegation, r.delegation}, {r.config.NFTCenter, r.factory}, {r.config.PositionVault, r.vault}, {r.config.MasterValidator, r.master}} {
			implementation := r.evm.GetState(child, role.address, implementationSlot)
			parts = append(parts, role.address.Bytes(), implementation.Bytes(), crypto.Keccak256Hash(role.artifact.Runtime).Bytes())
		}
		parts = append(parts, crypto.Keccak256Hash(r.proxy.Runtime).Bytes(), r.config.DelegationLibrary.Bytes(), crypto.Keccak256Hash(r.libraryRuntime).Bytes(), r.config.WrappedDEL.Bytes(), crypto.Keccak256Hash(wrapper.Runtime).Bytes())
		source = StakeSource{ChainID: r.config.ChainID, EVMChainID: r.config.EVMChainID, Gateway: r.config.Delegation, Fingerprint: crypto.Keccak256Hash(parts...)}
		return nil
	})
	if err != nil {
		return StakeSource{}, err
	}
	return source, nil
}
