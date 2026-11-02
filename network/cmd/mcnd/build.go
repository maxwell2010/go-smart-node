package main

import sdkversion "github.com/cosmos/cosmos-sdk/version"

// Release automation can set these with -X main.buildVersion / buildCommit.
// Local builds identify themselves as development builds, not stable releases.
var (
	buildVersion = "0.0.0-dev"
	buildCommit  = "unknown"
)

func init() {
	sdkversion.Name = "MintCandy"
	sdkversion.AppName = "mcnd"
	sdkversion.Version = buildVersion
	sdkversion.Commit = buildCommit
}
