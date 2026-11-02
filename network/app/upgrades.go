// Derived from cosmos/evm/evmd at c3ae9067a7eadcdea55a5192226916deb44c71c4.
// Licensed under Apache-2.0; see LICENSE.

package app

// RegisterUpgradeHandlers has no legacy sample migrations. Coordinated upgrades
// for the separate network must be added explicitly with their own state tests.
func (app App) RegisterUpgradeHandlers() {}
