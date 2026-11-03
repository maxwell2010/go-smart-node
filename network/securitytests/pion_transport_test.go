package securitytests

import (
	"bytes"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/p2p/nat"
	"github.com/pion/dtls/v3"
	"github.com/pion/stun/v3"
)

func TestPionTransportCodec(t *testing.T) {
	for _, address := range []net.IP{net.ParseIP("192.0.2.17").To4(), net.ParseIP("2001:db8::17")} {
		message := stun.MustBuild(stun.TransactionID, stun.BindingSuccess, &stun.XORMappedAddress{IP: address, Port: 41000}, stun.Fingerprint)
		decoded := stun.New()
		decoded.Raw = bytes.Clone(message.Raw)
		if err := decoded.Decode(); err != nil {
			t.Fatal(err)
		}
		if err := stun.Fingerprint.Check(decoded); err != nil {
			t.Fatal(err)
		}
		var mapped stun.XORMappedAddress
		if err := mapped.GetFrom(decoded); err != nil {
			t.Fatal(err)
		}
		if !mapped.IP.Equal(address) || mapped.Port != 41000 || decoded.TransactionID != message.TransactionID {
			t.Fatal("valid STUN address or transaction ID changed in codec round trip")
		}
	}
}

func TestPionTransportClientClose(t *testing.T) {
	local, peer := net.Pipe()
	defer peer.Close()
	client, err := stun.NewClient(local)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if err := client.Close(); !errors.Is(err, stun.ErrClientClosed) {
		t.Fatalf("second Close: got %v", err)
	}
	message := stun.MustBuild(stun.TransactionID, stun.BindingRequest)
	if err := client.Do(message, func(stun.Event) {}); !errors.Is(err, stun.ErrClientClosed) {
		t.Fatalf("closed client accepted another transaction: %v", err)
	}
}

func TestPionTransportUDPAndActualGethNAT(t *testing.T) {
	server, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	if err := server.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	mappedIP := net.ParseIP("203.0.113.17").To4()
	go func() {
		for range 2 {
			buffer := make([]byte, 1500)
			n, peer, err := server.ReadFrom(buffer)
			if err != nil {
				done <- err
				return
			}
			request := stun.New()
			request.Raw = buffer[:n]
			if err := request.Decode(); err != nil {
				done <- err
				return
			}
			if request.Type != stun.BindingRequest {
				done <- errors.New("expected an ordinary STUN binding request")
				return
			}
			response := stun.MustBuild(stun.NewTransactionIDSetter(request.TransactionID), stun.BindingSuccess, &stun.XORMappedAddress{IP: mappedIP, Port: peer.(*net.UDPAddr).Port})
			if _, err := server.WriteTo(response.Raw, peer); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	client, err := stun.Dial("udp4", server.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	var mapped stun.XORMappedAddress
	var responseError error
	if err := client.Do(stun.MustBuild(stun.TransactionID, stun.BindingRequest), func(event stun.Event) {
		responseError = event.Error
		if responseError == nil {
			responseError = mapped.GetFrom(event.Message)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if responseError != nil || !mapped.IP.Equal(mappedIP) || mapped.Port == 0 {
		t.Fatalf("ordinary UDP client response failed: %v", responseError)
	}
	// Exercise the actual owned geth NAT implementation, rather than only the
	// replacement library. No default server list or external network is used.
	mapper, err := nat.Parse("stun:" + server.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	ip, err := mapper.ExternalIP()
	if err != nil || !ip.Equal(mappedIP) {
		t.Fatalf("actual geth NAT response: ip=%v err=%v", ip, err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("local UDP responder did not finish")
	}
}

func TestPionTransportDTLSLocalConnection(t *testing.T) {
	// Artificial local PSK, with no certificate bypass or real account material.
	configuration := func() *dtls.Config {
		return &dtls.Config{PSK: func([]byte) ([]byte, error) { return bytes.Repeat([]byte{17}, 16), nil }, PSKIdentityHint: []byte("local-transport-test"), CipherSuites: []dtls.CipherSuiteID{dtls.TLS_PSK_WITH_AES_128_GCM_SHA256}, ExtendedMasterSecret: dtls.RequireExtendedMasterSecret}
	}
	listener, err := dtls.Listen("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")}, configuration())
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan error, 1)
	go func() {
		connection, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer connection.Close()
		if err := connection.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
			done <- err
			return
		}
		payload := make([]byte, 4)
		if _, err := io.ReadFull(connection, payload); err != nil {
			done <- err
			return
		}
		if !bytes.Equal(payload, []byte("ping")) {
			done <- errors.New("ordinary DTLS payload changed")
			return
		}
		_, err = connection.Write([]byte("pong"))
		done <- err
	}()
	client, err := dtls.Dial("udp4", listener.Addr().(*net.UDPAddr), configuration())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	payload := make([]byte, 4)
	if _, err := io.ReadFull(client, payload); err != nil || !bytes.Equal(payload, []byte("pong")) {
		t.Fatalf("ordinary DTLS response failed: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("local DTLS responder did not finish")
	}
}
