package armorcompat

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ProtonMail/go-crypto/openpgp/armor"
)

// This ordinary ASCII fixture was emitted by canonical SDK v0.53.8.
const legacyInfo = "-----BEGIN TENDERMINT KEY INFO-----\ntype: Info\nversion: 0.0.0\n\nb3JkaW5hcnkgU0RLIGluZm9ybWF0aW9u\n=5yvz\n-----END TENDERMINT KEY INFO-----"

func TestLegacyOptionalCRC(t *testing.T) {
	for name, input := range map[string]string{
		"original":       legacyInfo,
		"crlf":           strings.ReplaceAll(legacyInfo, "\n", "\r\n"),
		"missing-crc":    strings.Replace(legacyInfo, "=5yvz\n", "", 1),
		"preamble":       "ignored text\n" + legacyInfo,
		"trailing-block": legacyInfo + "\n" + legacyInfo,
	} {
		t.Run(name, func(t *testing.T) {
			kind, headers, body, err := Decode(input)
			if err != nil || kind != "TENDERMINT KEY INFO" || headers["type"] != "Info" || string(body) != "ordinary SDK information" {
				t.Fatalf("SDK wire format changed: %v", err)
			}
		})
	}
	for name, input := range map[string]string{
		"corrupt-crc":           strings.Replace(legacyInfo, "=5yvz", "=Ayvz", 1),
		"invalid-crc":           strings.Replace(legacyInfo, "=5yvz", "=!!!!", 1),
		"missing-end-after-crc": strings.Split(legacyInfo, "-----END ")[0],
		"body-whitespace":       strings.Replace(legacyInfo, "\nb3J", "\n b3J", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, _, err := Decode(input); err == nil {
				t.Fatal("legacy invalid input accepted")
			}
		})
	}
}

func TestMaintainedEncodingAndBoundedOrdinaryBodies(t *testing.T) {
	for _, body := range [][]byte{nil, []byte("a"), bytes.Repeat([]byte("ordinary fixture"), 37)} {
		var encoded bytes.Buffer
		writer, err := armor.Encode(&encoded, "TENDERMINT KEY INFO", map[string]string{"version": "0.0.0"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(body); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		_, _, decoded, err := Decode(encoded.String())
		if err != nil || !bytes.Equal(decoded, body) {
			t.Fatalf("ordinary round trip failed: %v", err)
		}
	}
}
