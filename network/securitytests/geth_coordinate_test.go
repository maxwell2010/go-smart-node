package securitytests

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/crypto"
)

// These are ordinary local curve/encoding/signature checks on synthetic values.
// There is no network input, exploit sequence or EVM execution context here.
func TestOwnedGethCoordinateValidation(t *testing.T) {
	curve := crypto.S256()
	p := curve.Params()
	x2, y2 := curve.ScalarBaseMult([]byte{2})
	for _, tc := range []struct {
		name string
		x, y *big.Int
		want bool
	}{
		{"generator", p.Gx, p.Gy, true},
		{"double", x2, y2, true},
		{"x at field modulus", p.P, p.Gy, false},
		{"y at field modulus", p.Gx, p.P, false},
		{"x beyond field modulus", new(big.Int).Add(p.P, p.Gx), p.Gy, false},
		{"y beyond field modulus", p.Gx, new(big.Int).Add(p.P, p.Gy), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := curve.IsOnCurve(tc.x, tc.y); got != tc.want {
				t.Fatalf("field-coordinate validation: got %v, want %v", got, tc.want)
			}
		})
	}
	xAdd, yAdd := curve.Add(p.Gx, p.Gy, p.Gx, p.Gy)
	if xAdd.Cmp(x2) != 0 || yAdd.Cmp(y2) != 0 {
		t.Fatal("ordinary generator addition differs from scalar multiplication")
	}
}

func TestOwnedGethSignatureRoundTrip(t *testing.T) {
	// Public, synthetic test scalar. It is never a funded account or stored key.
	material := make([]byte, 32)
	material[31] = 2
	key, err := crypto.ToECDSA(material)
	if err != nil {
		t.Fatal(err)
	}
	digest := crypto.Keccak256([]byte("ordinary owned-geth signature validation"))
	signature, err := crypto.Sign(digest, key)
	if err != nil {
		t.Fatal(err)
	}
	if len(signature) != crypto.SignatureLength {
		t.Fatal("unexpected signature encoding length")
	}
	recovered, err := crypto.SigToPub(digest, signature)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.X.Cmp(key.X) != 0 || recovered.Y.Cmp(key.Y) != 0 {
		t.Fatal("signature recovered a different synthetic public key")
	}
	for _, public := range [][]byte{crypto.FromECDSAPub(&key.PublicKey), crypto.CompressPubkey(&key.PublicKey)} {
		if !crypto.VerifySignature(public, digest, signature[:crypto.RecoveryIDOffset]) {
			t.Fatal("ordinary signature verification failed")
		}
	}
	encoded := crypto.FromECDSAPub(&key.PublicKey)
	decoded, err := crypto.UnmarshalPubkey(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.X.Cmp(key.X) != 0 || decoded.Y.Cmp(key.Y) != 0 {
		t.Fatal("public key changed during ordinary encoding round trip")
	}
}
