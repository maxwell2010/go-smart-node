//go:build !mcnd_native_sdk_migration_v1

package app

// The predecessor recognizes the proposal manifest but leaves its handler
// absent so the SDK stops at the approved height and writes the upgrade hint.
func (app *App) registerNativeGovernanceUpgradeHandlers() {}
