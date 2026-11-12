//go:build mcnd_native_sdk_migration_v1

package app

func (app *App) registerNativeGovernanceUpgradeHandlers() {
	app.UpgradeKeeper.SetUpgradeHandler(nativeSDKMigrationPlan, app.runNativeSDKMigration)
}
