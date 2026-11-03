// Copyright 2010 The Go Authors. All rights reserved.
// CRC-24 compatibility logic derives from golang.org/x/crypto/openpgp/armor,
// distributed under its BSD-style LICENSE. CRC-24 is a transport checksum,
// not an authentication or cryptographic integrity mechanism.
package armorcompat

import (
	"bufio"
	"encoding/base64"
	"io"
	"strings"

	"github.com/ProtonMail/go-crypto/openpgp/armor"
)

// Decode preserves SDK legacy optional-CRC verification while using the
// maintained armor decoder. The SDK already materializes the decoded body.
func Decode(source string) (string, map[string]string, []byte, error) {
	in := &lineInput{reader: bufio.NewReader(strings.NewReader(source))}
	block, err := armor.Decode(in)
	if err != nil {
		return "", nil, nil, err
	}
	// Read returns at most one complete line per call. Decode has consumed
	// the header delimiter, so no body bytes can be buffered before this switch.
	if len(in.pending) != 0 {
		return "", nil, nil, armor.ArmorCorrupt
	}
	in.body = true
	data, err := io.ReadAll(block.Body)
	if err != nil {
		return "", nil, nil, err
	}
	if in.hasCRC && crc24(data) != in.crc {
		return "", nil, nil, armor.ArmorCorrupt
	}
	return block.Type, block.Header, data, nil
}

type lineInput struct {
	reader       *bufio.Reader
	pending      []byte
	body, hasCRC bool
	crc          uint32
}

func (in *lineInput) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if len(in.pending) == 0 {
		line, err := in.reader.ReadString('\n')
		if len(line) == 0 {
			return 0, err
		}
		if err != nil && err != io.EOF {
			return 0, err
		}
		if in.body {
			value := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
			if len(value) == 5 && value[0] == '=' {
				decoded, decodeErr := base64.StdEncoding.DecodeString(value[1:])
				if decodeErr != nil || len(decoded) != 3 {
					return 0, armor.ArmorCorrupt
				}
				end, endErr := in.reader.ReadString('\n')
				if endErr != nil && endErr != io.EOF {
					return 0, endErr
				}
				if !strings.HasPrefix(end, "-----END ") {
					return 0, armor.ArmorCorrupt
				}
				in.hasCRC = true
				in.crc = uint32(decoded[0])<<16 | uint32(decoded[1])<<8 | uint32(decoded[2])
			} else if !strings.HasPrefix(value, "-----END ") {
				// The maintained parser trims whitespace; the legacy SDK did not.
				if len(value) > 96 || strings.TrimSpace(value) != value {
					return 0, armor.ArmorCorrupt
				}
			}
		}
		in.pending = []byte(line)
	}
	n := copy(p, in.pending)
	in.pending = in.pending[n:]
	return n, nil
}

func crc24(data []byte) uint32 {
	crc := uint32(0xb704ce)
	for _, value := range data {
		crc ^= uint32(value) << 16
		for bit := 0; bit < 8; bit++ {
			crc <<= 1
			if crc&0x1000000 != 0 {
				crc ^= 0x1864cfb
			}
		}
	}
	return crc & 0xffffff
}
