//go:build !mintcandy_mainnet || !mintcandy_drc20_issuance

package app

func (app *App) registerDRC20IssuanceHandler() {}
