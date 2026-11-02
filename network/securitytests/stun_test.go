package securitytests

import (
	"encoding/binary"
	"errors"
	"io"
	"testing"

	"github.com/pion/stun/v3"
)

// GO-2026-6163: a peer-controlled empty XOR-MAPPED-ADDRESS must be rejected,
// including when its decoded value has no spare backing-array capacity.
func TestMalformedSTUNAttributeDoesNotPanic(t *testing.T) {
	packet := make([]byte, 24)
	binary.BigEndian.PutUint16(packet[0:2], 0x0101) // STUN binding success
	binary.BigEndian.PutUint16(packet[2:4], 4)      // one empty attribute
	binary.BigEndian.PutUint32(packet[4:8], 0x2112a442)
	binary.BigEndian.PutUint16(packet[20:22], uint16(stun.AttrXORMappedAddress))

	message := stun.New()
	message.Raw = packet[:len(packet):len(packet)]
	if err := message.Decode(); err != nil {
		t.Fatalf("valid STUN envelope: %v", err)
	}
	var address stun.XORMappedAddress
	if err := address.GetFrom(message); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("expected a rejected short address, got %v", err)
	}
}
