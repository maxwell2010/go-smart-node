package keeper

import (
	"bytes"
	"errors"
	"strings"

	"cosmossdk.io/core/store"
	"github.com/ethereum/go-ethereum/common"
	"github.com/maxwell2010/go-smart-node/network/decimal/coin/types"
)

const (
	legacyAddressIndexPrefix  byte = 0x13
	compactAddressIndexPrefix byte = 0x14
	addressIndexSchemaPrefix  byte = 0x15
)

var compactAddressIndexSchema = []byte{2}

type addressIndex struct {
	prefix       byte
	hasSchemaRow bool
}

func activeAddressIndex(state store.KVStore) (addressIndex, error) {
	legacy := addressIndex{prefix: legacyAddressIndexPrefix}
	if !coinIndexMigrationEnabled {
		return legacy, nil
	}
	version, err := state.Get([]byte{addressIndexSchemaPrefix})
	if err != nil {
		return addressIndex{}, err
	}
	if version == nil {
		return legacy, nil
	}
	if !bytes.Equal(version, compactAddressIndexSchema) {
		return addressIndex{}, errors.New("unsupported coin address index schema")
	}
	return addressIndex{prefix: compactAddressIndexPrefix, hasSchemaRow: true}, nil
}

func initAddressIndex(state store.KVStore) error {
	if !coinIndexMigrationEnabled {
		return nil
	}
	return state.Set([]byte{addressIndexSchemaPrefix}, compactAddressIndexSchema)
}

func (i addressIndex) key(address string) []byte {
	if i.prefix == legacyAddressIndexPrefix {
		return addressKey(address)
	}
	return append([]byte{compactAddressIndexPrefix}, common.HexToAddress(address).Bytes()...)
}

func (i addressIndex) address(value []byte) (string, error) {
	if i.prefix == compactAddressIndexPrefix && len(value) == common.AddressLength {
		address := common.BytesToAddress(value)
		if address != (common.Address{}) {
			return address.Hex(), nil
		}
	}
	if i.prefix == legacyAddressIndexPrefix {
		address := string(value)
		if address == strings.ToLower(address) && types.ValidContractAddress(address) {
			return address, nil
		}
	}
	return "", errors.New("non-canonical coin address index")
}
