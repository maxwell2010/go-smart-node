package securitytests

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
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

func TestSTUNAddressRoundTrips(t *testing.T) {
	for _, ip := range []net.IP{net.ParseIP("192.0.2.1").To4(), net.ParseIP("2001:db8::1")} {
		message := stun.MustBuild(stun.TransactionID, stun.BindingSuccess,
			&stun.XORMappedAddress{IP: ip, Port: 3478})
		wire := append([]byte(nil), message.Raw...)
		decoded := stun.New()
		decoded.Raw = wire[:len(wire):len(wire)]
		if err := decoded.Decode(); err != nil {
			t.Fatal(err)
		}
		var actual stun.XORMappedAddress
		if err := actual.GetFrom(decoded); err != nil {
			t.Fatal(err)
		}
		if !ip.Equal(actual.IP) || actual.Port != 3478 {
			t.Fatalf("address changed: expected %v:3478, got %s", ip, actual.String())
		}
	}
}
