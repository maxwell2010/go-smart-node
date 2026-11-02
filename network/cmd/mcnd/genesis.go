package main

import (
	"encoding/json"

	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/types/module"
	ownedapp "github.com/maxwell2010/go-smart-node/network/app"
)

type ownedGenesisBasic struct {
	module.AppModuleBasic
	module.HasGenesisBasics
	state json.RawMessage
}

func (basic ownedGenesisBasic) DefaultGenesis(codec.JSONCodec) json.RawMessage {
	return append(json.RawMessage(nil), basic.state...)
}

// SDK InitCmd obtains defaults from BasicManager. Supply the same owned defaults
// as live InitChain without creating a temporary application or EVM keeper.
func initializationBasics(cdc codec.Codec) module.BasicManager {
	basics := ownedapp.NewBasicModuleManager()
	for name, state := range ownedapp.DefaultGenesis(cdc) {
		original := basics[name]
		genesis, ok := original.(module.HasGenesisBasics)
		if !ok {
			panic("owned genesis module has no genesis validation contract: " + name)
		}
		basics[name] = ownedGenesisBasic{AppModuleBasic: original, HasGenesisBasics: genesis, state: state}
	}
	return basics
}
