package types

// MaxIsolatedOriginUnits is shared by native lineage, validator bindings and
// contract admission. A receipt with more units must never be created in a wallet.
// The compiler-pinned vault getter is checked against this value in signed tests.
const MaxIsolatedOriginUnits uint32 = 1024
