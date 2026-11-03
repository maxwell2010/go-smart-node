// Package module wires native nft metadata and read-only queries.
package module

import (
	"context"
	autocliv1 "cosmossdk.io/api/cosmos/autocli/v1"
	"cosmossdk.io/core/store"
	"encoding/json"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkmodule "github.com/cosmos/cosmos-sdk/types/module"
	"github.com/grpc-ecosystem/grpc-gateway/runtime"
	"github.com/maxwell2010/go-smart-node/network/decimal/nft/keeper"
	"github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
)

type AppModuleBasic struct{}

func (AppModuleBasic) Name() string                                { return types.ModuleName }
func (AppModuleBasic) RegisterLegacyAminoCodec(*codec.LegacyAmino) {}
func (AppModuleBasic) RegisterInterfaces(registry codectypes.InterfaceRegistry) {
	types.RegisterInterfaces(registry)
}
func (AppModuleBasic) RegisterGRPCGatewayRoutes(ctx client.Context, mux *runtime.ServeMux) {
	if err := types.RegisterQueryHandlerClient(context.Background(), mux, types.NewQueryClient(ctx)); err != nil {
		panic(err)
	}
}
func (AppModuleBasic) DefaultGenesis(cdc codec.JSONCodec) json.RawMessage {
	return cdc.MustMarshalJSON(types.DefaultGenesisState())
}
func (AppModuleBasic) ValidateGenesis(cdc codec.JSONCodec, _ client.TxEncodingConfig, data json.RawMessage) error {
	var g types.GenesisState
	if err := cdc.UnmarshalJSON(data, &g); err != nil {
		return err
	}
	return g.Validate()
}

type AppModule struct {
	AppModuleBasic
	keeper  *keeper.StateKeeper
	service store.KVStoreService
}

func NewAppModule(k *keeper.StateKeeper, s store.KVStoreService) AppModule {
	return AppModule{keeper: k, service: s}
}
func (AppModule) IsOnePerModuleType()      {}
func (AppModule) IsAppModule()             {}
func (AppModule) ConsensusVersion() uint64 { return 1 }
func (m AppModule) InitGenesis(ctx sdk.Context, cdc codec.JSONCodec, data json.RawMessage) {
	var g types.GenesisState
	cdc.MustUnmarshalJSON(data, &g)
	if err := m.keeper.InitGenesis(ctx, &g); err != nil {
		panic(err)
	}
}
func (m AppModule) ExportGenesis(ctx sdk.Context, cdc codec.JSONCodec) json.RawMessage {
	g, err := m.keeper.ExportGenesis(ctx)
	if err != nil {
		panic(err)
	}
	return cdc.MustMarshalJSON(g)
}
func (m AppModule) RegisterServices(cfg sdkmodule.Configurator) {
	types.RegisterQueryServer(cfg.QueryServer(), queryServer{keeper: m.keeper, service: m.service})
}

func (AppModuleBasic) AutoCLIOptions() *autocliv1.ModuleOptions {
	return &autocliv1.ModuleOptions{Query: &autocliv1.ServiceCommandDescriptor{Service: "decimal.nft.v1.Query"}}
}
