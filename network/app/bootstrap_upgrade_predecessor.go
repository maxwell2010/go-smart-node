//go:build !mintcandy_bootstrap_unanimity || (!mintcandy_mainnet && !mintcandy_testnet)

package app

func (app *App) registerBootstrapUnanimityHandler() {}
