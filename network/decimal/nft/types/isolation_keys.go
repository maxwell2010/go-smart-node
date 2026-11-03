package types

import (
	"encoding/binary"
	helpers "github.com/maxwell2010/go-smart-node/network/decimal/compat"
)

func GetIsolatedVaultsKey() []byte            { return []byte{0x42} }
func GetIsolatedVaultKey(vault []byte) []byte { return append(GetIsolatedVaultsKey(), vault...) }
func GetIsolatedPositionsKey() []byte         { return []byte{0x43} }
func GetIsolatedPositionKey(vault, id []byte) []byte {
	return append(append(GetIsolatedPositionsKey(), vault...), id...)
}
func GetIsolatedReceiptIndexKey(nativeID string) []byte {
	return append([]byte{0xA6}, helpers.CalcHashSHA256(nativeID)...)
}
func GetIsolatedOriginUnitKey(nativeID string, id uint32) []byte {
	value := make([]byte, 4)
	binary.BigEndian.PutUint32(value, id)
	return append(append([]byte{0xA7}, helpers.CalcHashSHA256(nativeID)...), value...)
}
