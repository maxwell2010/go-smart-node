//go:build !mintcandy_mainnet && !mintcandy_testnet

package identity

const (
	ProfileChainID    = ""
	BaseDenom         = "del"
	NativeTokenName   = "MintCandy"
	NativeTokenSymbol = "MSP"
	WrappedMSPName    = "MintCandy Wrapped MSP"
	WrappedMSPSymbol  = "WMSP"
)
