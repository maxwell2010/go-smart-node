package securitytests

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/s2"
	"github.com/klauspost/compress/zstd"
	"golang.org/x/net/html"
	"golang.org/x/net/http2"
)

func TestDependencyPatchCompressionRoundTrips(t *testing.T) {
	encoder, err := zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1))
	if err != nil {
		t.Fatal(err)
	}
	defer encoder.Close()
	decoder, err := zstd.NewReader(nil, zstd.WithDecoderConcurrency(1))
	if err != nil {
		t.Fatal(err)
	}
	defer decoder.Close()
	for _, input := range [][]byte{{}, {17}, []byte("Decimal: обычные данные IBC"), bytes.Repeat([]byte("bounded test record\n"), 64)} {
		encoded := encoder.EncodeAll(input, nil)
		decoded, err := decoder.DecodeAll(encoded, nil)
		if err != nil || !bytes.Equal(input, decoded) {
			t.Fatalf("ordinary zstd round trip failed: %v", err)
		}
		decoded, err = s2.Decode(nil, s2.Encode(nil, input))
		if err != nil || !bytes.Equal(input, decoded) {
			t.Fatalf("ordinary s2 round trip failed: %v", err)
		}
	}
}

func TestDependencyPatchHTTP2LocalConnection(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.ProtoMajor != 2 || request.Method != http.MethodGet || request.URL.Path != "/ordinary" {
			http.Error(writer, "unexpected ordinary request", http.StatusBadRequest)
			return
		}
		writer.Header().Set("Content-Type", "text/plain")
		_, _ = writer.Write([]byte("ordinary response"))
	}))
	server.EnableHTTP2 = true
	if err := http2.ConfigureServer(server.Config, &http2.Server{}); err != nil {
		t.Fatal(err)
	}
	server.StartTLS()
	defer server.Close()
	trust := x509.NewCertPool()
	trust.AddCert(server.Certificate())
	transport := &http2.Transport{TLSClientConfig: &tls.Config{RootCAs: trust, MinVersion: tls.VersionTLS12}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	response, err := client.Get(server.URL + "/ordinary")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	contents, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != http.StatusOK || response.ProtoMajor != 2 || string(contents) != "ordinary response" {
		t.Fatalf("ordinary HTTP/2 response failed: status=%d protocol=%s err=%v", response.StatusCode, response.Proto, err)
	}
}

func TestDependencyPatchOrdinaryHTMLRoundTrip(t *testing.T) {
	document, err := html.Parse(strings.NewReader("<!doctype html><html><body><p>Decimal &amp; IBC</p></body></html>"))
	if err != nil {
		t.Fatal(err)
	}
	var encoded bytes.Buffer
	if err := html.Render(&encoded, document); err != nil {
		t.Fatal(err)
	}
	decoded, err := html.Parse(&encoded)
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	var visit func(*html.Node)
	visit = func(node *html.Node) {
		if node.Type == html.TextNode {
			text.WriteString(node.Data)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(decoded)
	if text.String() != "Decimal & IBC" {
		t.Fatal("ordinary HTML content changed")
	}
}
