package app

import (
	"errors"
	"regexp"
	"strings"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

const (
	publicTokenNamePrefix   byte = 0x83
	maxReservelessNameBytes      = 96
)

var uppercaseTokenName = regexp.MustCompile(`^[A-Z0-9]+([ -][A-Z0-9]+)*$`)

func validPublicTokenName(name string, maxBytes int) bool {
	if len(name) > maxBytes || !uppercaseTokenName.MatchString(name) ||
		!strings.ContainsAny(name, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") {
		return false
	}
	switch name {
	case "MINTCANDY", "MSP", "DEL":
		return false
	}
	return true
}

func publicTokenNameKey(name string) []byte {
	return append([]byte{publicTokenNamePrefix}, name...)
}

func (r *customCollateralRegistry) reservePublicTokenName(ctx sdk.Context, name string) error {
	store := ctx.KVStore(r.store)
	if !store.Has(publicTokenNameKey("")) {
		if err := r.indexLegacyTokenNames(ctx); err != nil {
			return err
		}
	}
	key := publicTokenNameKey(name)
	if store.Has(key) {
		return errors.New("DRC20 name is already registered")
	}
	store.Set(key, []byte{1})
	return nil
}

// Older private networks may have admitted mixed-case names. Index all names
// that could collide with the new alphabet on the first successful issuance.
// The EVM transaction's gas meter and rollback cover the entire backfill.
func (r *customCollateralRegistry) indexLegacyTokenNames(ctx sdk.Context) error {
	manifest, err := r.manifest(ctx)
	if err != nil {
		return err
	}
	store := ctx.KVStore(r.store)
	for pos := 1; pos < len(manifest); pos += customCollateralEntrySize {
		token := common.BytesToAddress(manifest[pos : pos+common.AddressLength])
		coin, err := r.keeper.GetCoinByDRC(ctx, token.Hex())
		if err != nil {
			return err
		}
		reserveLegacyTokenName(store.Set, coin.Title)
	}
	for _, prefix := range []byte{backedTokenMembershipPrefix, reservelessMembershipPrefix} {
		it := store.Iterator([]byte{prefix}, []byte{prefix + 1})
		for ; it.Valid(); it.Next() {
			key := it.Key()
			if len(key) != 1+common.AddressLength {
				_ = it.Close()
				return errors.New("invalid legacy DRC20 membership key")
			}
			name, err := r.legacyTokenName(ctx, common.BytesToAddress(key[1:]))
			if err != nil {
				_ = it.Close()
				return err
			}
			reserveLegacyTokenName(store.Set, name)
		}
		if err := it.Close(); err != nil {
			return err
		}
	}
	store.Set(publicTokenNameKey(""), []byte{1})
	return nil
}

func reserveLegacyTokenName(set func([]byte, []byte), name string) {
	name = strings.ToUpper(strings.Join(strings.Fields(name), " "))
	if validPublicTokenName(name, maxReservelessNameBytes) {
		set(publicTokenNameKey(name), []byte{1})
	}
}

func (r *customCollateralRegistry) legacyTokenName(ctx sdk.Context, token common.Address) (string, error) {
	slot := customSlotOffset(customERC20Slot, 3)
	word := r.evm.GetState(ctx, token, slot)
	if word[31]&1 == 0 && word[31] < 64 {
		return string(word[:word[31]/2]), nil
	}
	length := word.Big()
	if word[31]&1 == 0 || !length.IsUint64() || length.Uint64() < 65 || length.Uint64() > 2*maxReservelessNameBytes+1 {
		return "", errors.New("invalid legacy DRC20 name storage")
	}
	count := int(length.Uint64() / 2)
	data := make([]byte, 0, count)
	base := crypto.Keccak256Hash(slot[:])
	for i := 0; i < count; i += common.HashLength {
		part := r.evm.GetState(ctx, token, customSlotOffset(base, int64(i/common.HashLength)))
		data = append(data, part[:min(common.HashLength, count-i)]...)
	}
	return string(data), nil
}
