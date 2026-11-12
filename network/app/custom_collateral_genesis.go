package app

import (
	"bytes"
	"errors"
	"fmt"
	"math/big"

	"github.com/cosmos/cosmos-sdk/codec/address"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	cointypes "github.com/maxwell2010/go-smart-node/network/decimal/coin/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

func (r *customCollateralRegistry) attestGenesis(ctx sdk.Context) error {
	if r == nil {
		return nil
	}
	genesis, err := r.keeper.ExportGenesis(ctx)
	if err != nil {
		return err
	}
	if err := validateCustomCollateralGenesis(*genesis); err != nil {
		return err
	}
	if !genesis.Params.BaseVolume.Equal(r.bank.GetSupply(ctx, identity.BaseDenom).Amount) {
		return errors.New("DRC20 genesis DEL supply differs from the reviewed bank allocation")
	}
	manifest, err := customCollateralManifest(genesis.Coins)
	if err != nil {
		return err
	}
	store := ctx.KVStore(r.store)
	if store.Has([]byte{customCollateralManifestKey}) {
		return errors.New("DRC20 genesis manifest is already initialized")
	}
	if err := r.verifyManifest(ctx, manifest); err != nil {
		return err
	}
	store.Set([]byte{customCollateralManifestKey}, manifest)
	return nil
}

func (r *customCollateralRegistry) verifyRestored(ctx sdk.Context, expected []byte) error {
	if r == nil {
		return nil
	}
	manifest, err := r.manifest(ctx)
	if err != nil {
		return err
	}
	if expected != nil && !bytes.Equal(manifest, expected) {
		return errors.New("restored DRC20 manifest differs from the configured genesis")
	}
	return r.verifyManifest(ctx, manifest)
}

func (r *customCollateralRegistry) verifyManifest(ctx sdk.Context, manifest []byte) error {
	if err := r.authenticateRoles(ctx); err != nil {
		return err
	}
	var previous common.Address
	for pos := 1; pos < len(manifest); pos += customCollateralEntrySize {
		entry := manifest[pos : pos+customCollateralEntrySize]
		token := common.BytesToAddress(entry[:common.AddressLength])
		if bytes.Compare(previous[:], token[:]) >= 0 {
			return errors.New("approved DRC20 manifest addresses must be nonzero, unique and sorted")
		}
		previous = token
		coin, err := r.keeper.GetCoinByDRC(ctx, token.Hex())
		if err != nil {
			return err
		}
		actual, err := customCollateralEntry(coin)
		if err != nil {
			return err
		}
		if !bytes.Equal(entry, actual[:]) {
			return fmt.Errorf("approved DRC20 metadata differs for %s", coin.Denom)
		}
		live, err := r.authenticateCoin(ctx, token, coin)
		if err != nil {
			return err
		}
		if !live.Volume.Equal(coin.Volume) || !live.Reserve.Equal(coin.Reserve) {
			return fmt.Errorf("approved DRC20 supply or bank DEL reserve differs for %s", coin.Denom)
		}
		if !r.storageStringEquals(ctx, token, customSlotOffset(customERC20Slot, 3), coin.Title) ||
			!r.storageStringEquals(ctx, token, customSlotOffset(customERC20Slot, 4), coin.Denom) ||
			!r.storageStringEquals(ctx, token, customSlotOffset(customTokenSlot, 3), coin.Identity) {
			return fmt.Errorf("approved DRC20 name, symbol or identity differs for %s", coin.Denom)
		}
	}
	return nil
}

func (r *customCollateralRegistry) verifyCurveMetadata(ctx sdk.Context, token common.Address, metadata cointypes.Coin) error {
	creator, err := address.NewBech32Codec(identity.Bech32PrefixAccAddr).StringToBytes(metadata.Creator)
	if err != nil || len(creator) != common.AddressLength || common.BytesToAddress(creator) == (common.Address{}) {
		return errors.New("approved DRC20 creator must be a nonzero account")
	}
	// TokenStorage places crr in the first word, min and max in the next
	// two, identity in the fourth, and the creator in the fifth.
	if r.evm.GetState(ctx, token, customTokenSlot) != common.BigToHash(new(big.Int).SetUint64(uint64(metadata.CRR))) ||
		r.evm.GetState(ctx, token, customSlotOffset(customTokenSlot, 1)) != common.BigToHash(metadata.MinVolume.BigInt()) ||
		r.evm.GetState(ctx, token, customSlotOffset(customTokenSlot, 2)) != common.BigToHash(metadata.LimitVolume.BigInt()) ||
		r.evm.GetState(ctx, token, customSlotOffset(customTokenSlot, 4)) != common.BytesToHash(creator) {
		return errors.New("approved DRC20 creator or curve parameters differ from native metadata")
	}
	return nil
}

// Solidity stores short strings left-aligned in the word, with length*2
// in the last byte. Long strings put length*2+1 in the word and start
// their data at keccak256(slot). The reviewed expected length bounds reads.
func (r *customCollateralRegistry) storageStringEquals(ctx sdk.Context, target common.Address, slot common.Hash, expected string) bool {
	word := r.evm.GetState(ctx, target, slot)
	if len(expected) < common.HashLength {
		var want common.Hash
		copy(want[:], expected)
		want[common.HashLength-1] = byte(2 * len(expected))
		return word == want
	}
	if word != common.BigToHash(big.NewInt(int64(2*len(expected)+1))) {
		return false
	}
	data := crypto.Keccak256Hash(slot[:])
	for i := 0; i < len(expected); i += common.HashLength {
		var want common.Hash
		copy(want[:], expected[i:min(i+common.HashLength, len(expected))])
		if r.evm.GetState(ctx, target, customSlotOffset(data, int64(i/common.HashLength))) != want {
			return false
		}
	}
	return true
}
