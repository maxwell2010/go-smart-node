package integrationconfig

// AllowedChainID deliberately excludes all production and public testnet IDs.
func AllowedChainID(id string) bool {
	return id == "decimal_900001-1" || id == "decimal_900002-1"
}
