package integration_test

import (
	"context"
	"encoding/json"
	"math/big"
	"testing"
	"time"

	"cosmossdk.io/log"
	sdkmath "cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	abci "github.com/cometbft/cometbft/abci/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	cmttypes "github.com/cometbft/cometbft/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/client/flags"
	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/cosmos/cosmos-sdk/x/gov"
	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types"
	govv1 "github.com/cosmos/cosmos-sdk/x/gov/types/v1"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	ethsecp "github.com/cosmos/evm/crypto/ethsecp256k1"
	srvflags "github.com/cosmos/evm/server/flags"
	"github.com/cosmos/evm/x/vm/statedb"
	evmtypes "github.com/cosmos/evm/x/vm/types"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	ownedapp "github.com/maxwell2010/go-smart-node/network/app"
	"github.com/maxwell2010/go-smart-node/network/authority"
	authoritytypes "github.com/maxwell2010/go-smart-node/network/authority/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
	"github.com/stretchr/testify/require"
)

type options map[string]interface{}

func (o options) Get(key string) interface{} { return o[key] }

// This wrapper is test-only. It retains the unmodified canonical SDK proposal
// keeper, tally and EndBlock, adding the reviewed authority work session.
type sessionGovModule struct{ gov.AppModule }

func (m sessionGovModule) EndBlock(ctx context.Context) error {
	return authority.RunGovernance(sdk.UnwrapSDKContext(ctx), func(c sdk.Context) error { return m.AppModule.EndBlock(c) })
}

type noFinancialHook struct{}

func (noFinancialHook) PostTxProcessing(_ sdk.Context, sender common.Address, message core.Message, receipt *ethtypes.Receipt) error {
	if sender != common.BytesToAddress(authtypes.NewModuleAddress("gov")) || message.Value.Sign() != 0 || receipt.Status != ethtypes.ReceiptStatusSuccessful {
		panic("invalid authority integration hook context")
	}
	return nil
}

// Exactly one default-build App belongs to this package's test binary. All setup
// transactions are genuinely signed and delivered to ABCI; no node/RPC is used.
func TestCanonicalGovernanceVaultAndUnifiedGatewayRoles(t *testing.T) {
	const chainID = "mintcandy_990001-1"
	a := ownedapp.NewApp(log.NewNopLogger(), dbm.NewMemDB(), true, options{flags.FlagHome: t.TempDir(), flags.FlagChainID: chainID, srvflags.EVMChainID: uint64(990001)})
	t.Cleanup(func() { require.NoError(t, a.Close()) })
	key, err := ethsecp.GenerateKey()
	require.NoError(t, err)
	private, err := crypto.ToECDSA(key.Bytes())
	require.NoError(t, err)
	account := authtypes.NewBaseAccount(key.PubKey().Address().Bytes(), key.PubKey(), 0, 0)
	funds := sdk.NewCoins(sdk.NewCoin(identity.BaseDenom, sdkmath.NewInt(10000).Mul(sdkmath.NewInt(1_000_000_000_000_000_000))))
	validator, _ := cmttypes.RandValidator(false, 1)
	validators := cmttypes.NewValidatorSet([]*cmttypes.Validator{validator})
	defaults := a.DefaultGenesis()
	genesis, err := simtestutil.GenesisStateWithValSet(a.AppCodec(), defaults, validators, []authtypes.GenesisAccount{account}, banktypes.Balance{Address: account.GetAddress().String(), Coins: funds})
	require.NoError(t, err)
	// The SDK fixture helper mutates the supplied genesis map. Restore the actual
	// application defaults from a fresh map before reconciling the funded pool.
	defaults = a.DefaultGenesis()
	var originalBank, bank banktypes.GenesisState
	var originalStaking, staking stakingtypes.GenesisState
	a.AppCodec().MustUnmarshalJSON(defaults[banktypes.ModuleName], &originalBank)
	a.AppCodec().MustUnmarshalJSON(genesis[banktypes.ModuleName], &bank)
	a.AppCodec().MustUnmarshalJSON(defaults[stakingtypes.ModuleName], &originalStaking)
	a.AppCodec().MustUnmarshalJSON(genesis[stakingtypes.ModuleName], &staking)
	staking.Params = originalStaking.Params
	bank.DenomMetadata, bank.Supply = originalBank.DenomMetadata, sdk.NewCoins()
	for index, balance := range bank.Balances {
		coins := sdk.NewCoins()
		for _, coin := range balance.Coins {
			if coin.Denom == sdk.DefaultBondDenom {
				coin.Denom = identity.BaseDenom
			}
			coins = coins.Add(coin)
		}
		bank.Balances[index].Coins = coins
		bank.Supply = bank.Supply.Add(coins...)
	}
	genesis[banktypes.ModuleName] = a.AppCodec().MustMarshalJSON(&bank)
	genesis[stakingtypes.ModuleName] = a.AppCodec().MustMarshalJSON(&staking)
	require.NoError(t, ownedapp.ReconcileNativeGenesisAllocation(a.AppCodec(), genesis))
	encoded, err := json.Marshal(genesis)
	require.NoError(t, err)
	now := time.Unix(1900000000, 0).UTC()
	_, err = a.InitChain(&abci.RequestInitChain{ChainId: chainID, Time: now, AppStateBytes: encoded, ConsensusParams: simtestutil.DefaultConsensusParams})
	require.NoError(t, err)
	block := func(txs ...[]byte) *abci.ResponseFinalizeBlock {
		t.Helper()
		height := a.LastBlockHeight() + 1
		response, err := a.FinalizeBlock(&abci.RequestFinalizeBlock{Height: height, Time: now.Add(time.Duration(height) * time.Second), NextValidatorsHash: validators.Hash(), ProposerAddress: validators.Proposer.Address, Txs: txs})
		require.NoError(t, err)
		_, err = a.Commit()
		require.NoError(t, err)
		return response
	}
	block()
	ctx := func() sdk.Context {
		return a.NewUncachedContext(false, cmtproto.Header{ChainID: chainID, Height: a.LastBlockHeight() + 1, Time: now.Add(time.Duration(a.LastBlockHeight()+1) * time.Second), ProposerAddress: validators.Proposer.Address})
	}
	send := func(to *common.Address, data []byte) *evmtypes.MsgEthereumTxResponse {
		t.Helper()
		from := crypto.PubkeyToAddress(private.PublicKey)
		nonce := a.EVMKeeper.GetNonce(ctx(), from)
		raw := ethtypes.NewTx(&ethtypes.LegacyTx{Nonce: nonce, To: to, Value: new(big.Int), Gas: 8_000_000, GasPrice: big.NewInt(1_000_000_000_000), Data: data})
		signer := ethtypes.NewEIP155Signer(big.NewInt(990001))
		signed, err := ethtypes.SignTx(raw, signer, private)
		require.NoError(t, err)
		message := &evmtypes.MsgEthereumTx{}
		require.NoError(t, message.FromSignedEthereumTx(signed, signer))
		tx, err := message.BuildTx(a.TxConfig().NewTxBuilder(), identity.BaseDenom)
		require.NoError(t, err)
		encoded, err := a.TxConfig().TxEncoder()(tx)
		require.NoError(t, err)
		response := block(encoded)
		require.Len(t, response.TxResults, 1)
		require.Zero(t, response.TxResults[0].Code, response.TxResults[0].Log)
		var result sdk.TxMsgData
		require.NoError(t, a.AppCodec().Unmarshal(response.TxResults[0].Data, &result))
		require.Len(t, result.MsgResponses, 1)
		evmResult := &evmtypes.MsgEthereumTxResponse{}
		require.NoError(t, evmResult.Unmarshal(result.MsgResponses[0].Value))
		reason, _ := abi.UnpackRevert(evmResult.Ret)
		require.Empty(t, evmResult.VmError, reason)
		require.Positive(t, evmResult.GasUsed)
		require.Equal(t, nonce+1, a.EVMKeeper.GetNonce(ctx(), from))
		return evmResult
	}
	load := func(name string) contracts.Artifact {
		t.Helper()
		artifact, err := contracts.Load(name)
		require.NoError(t, err)
		return artifact
	}
	deploy := func(artifact contracts.Artifact, args ...interface{}) common.Address {
		t.Helper()
		address := crypto.CreateAddress(crypto.PubkeyToAddress(private.PublicKey), a.EVMKeeper.GetNonce(ctx(), crypto.PubkeyToAddress(private.PublicKey)))
		creation, err := artifact.Deployment(args...)
		require.NoError(t, err)
		send(nil, creation)
		require.Equal(t, artifact.Runtime, a.EVMKeeper.GetCode(ctx(), a.EVMKeeper.GetCodeHash(ctx(), address)))
		return address
	}
	registry, factory, vault := load("DecimalContractCenter"), load("NewNetworkNFTCenter"), load("MintCandyNFTPositionVault")
	proxy, bootstrap := load("DecimalContractCenterProxy"), load("NewNetworkProxyBootstrap")
	var gateways [2]contracts.Artifact
	var gatewayImpls [2]common.Address
	for index := range gateways {
		library := load("MintCandyDelegationNFTLib")
		libraryAddress := crypto.CreateAddress(crypto.PubkeyToAddress(private.PublicKey), a.EVMKeeper.GetNonce(ctx(), crypto.PubkeyToAddress(private.PublicKey)))
		creation, err := library.Deployment()
		require.NoError(t, err)
		send(nil, creation)
		expected, err := contracts.ExpectedRuntime("MintCandyDelegationNFTLib", nil, map[string]common.Hash{"owned/DecimalDelegation.sol:DelegationNFTLib.library_deploy_address": common.BytesToHash(libraryAddress.Bytes())})
		require.NoError(t, err)
		require.Equal(t, expected, a.EVMKeeper.GetCode(ctx(), a.EVMKeeper.GetCodeHash(ctx(), libraryAddress)))
		gateways[index], err = contracts.LoadLinked("MintCandyIsolatedDelegation", map[string]common.Address{"owned/DecimalDelegation.sol:DelegationNFTLib": libraryAddress})
		require.NoError(t, err)
		gatewayImpls[index] = deploy(gateways[index])
	}
	registryImpl, factoryImpl, vaultImpl := deploy(registry), deploy(factory), deploy(vault)
	vaultReplacement := deploy(vault)
	setup := deploy(bootstrap)
	owner := common.BytesToAddress(authtypes.NewModuleAddress("gov"))
	from := crypto.PubkeyToAddress(private.PublicKey)
	nonce := a.EVMKeeper.GetNonce(ctx(), from)
	registryAddress := crypto.CreateAddress(from, nonce)
	gatewayAddress := crypto.CreateAddress(from, nonce+1)
	factoryAddress := crypto.CreateAddress(from, nonce+2)
	vaultAddress := crypto.CreateAddress(from, nonce+3)
	pack := func(artifact contracts.Artifact, method string, args ...interface{}) []byte {
		t.Helper()
		data, err := artifact.ABI.Pack(method, args...)
		require.NoError(t, err)
		return data
	}
	var bindings [][]byte
	for _, binding := range []struct {
		role    string
		address common.Address
	}{{"delegation", gatewayAddress}, {"delegation-nft", gatewayAddress}, {"nft-center", factoryAddress}, {"nft-position-vault", vaultAddress}} {
		bindings = append(bindings, pack(registry, "setContractAddress", binding.role, binding.address))
	}
	creation := pack(bootstrap, "bootstrapConfigured", owner, registryImpl, []byte{}, bindings)
	require.Equal(t, registryAddress, deploy(proxy, setup, creation))
	creation = pack(bootstrap, "bootstrapConfigured", owner, gatewayImpls[0], []byte{}, [][]byte{pack(gateways[0], "setContractCenter", registryAddress)})
	require.Equal(t, gatewayAddress, deploy(proxy, setup, creation))
	creation = pack(bootstrap, "bootstrapConfigured", owner, factoryImpl, pack(factory, "initializeFresh", registryAddress), [][]byte{})
	require.Equal(t, factoryAddress, deploy(proxy, setup, creation))
	creation = pack(bootstrap, "bootstrapConfigured", owner, vaultImpl, pack(vault, "initializeFresh", registryAddress, factoryAddress), [][]byte{})
	require.Equal(t, vaultAddress, deploy(proxy, setup, creation))
	proxyHash := crypto.Keccak256Hash(proxy.Runtime)
	gatewayHashes := []common.Hash{crypto.Keccak256Hash(gateways[0].Runtime), crypto.Keccak256Hash(gateways[1].Runtime)}
	require.NotEqual(t, gatewayHashes[0], gatewayHashes[1], "two approved linked implementations must genuinely differ")
	manifest := authority.Manifest{Registry: authority.Role{Address: registryAddress, ProxyCodeHash: proxyHash, ApprovedImplementations: []common.Hash{crypto.Keccak256Hash(registry.Runtime)}}, Roles: map[string]authority.Role{
		"delegation":         {Address: gatewayAddress, ProxyCodeHash: proxyHash, ApprovedImplementations: gatewayHashes},
		"delegation-nft":     {Address: gatewayAddress, ProxyCodeHash: proxyHash, ApprovedImplementations: []common.Hash{gatewayHashes[1], gatewayHashes[0]}},
		"nft-position-vault": {Address: vaultAddress, ProxyCodeHash: proxyHash, ApprovedImplementations: []common.Hash{crypto.Keccak256Hash(vault.Runtime)}},
	}}
	bridge, err := authority.NewKeeper(a.EVMKeeper, manifest, noFinancialHook{})
	require.NoError(t, err)
	authoritytypes.RegisterInterfaces(a.InterfaceRegistry())
	authoritytypes.RegisterMsgServer(a.MsgServiceRouter(), bridge)
	original, ok := a.ModuleManager.Modules[govtypes.ModuleName].(gov.AppModule)
	require.True(t, ok)
	a.ModuleManager.Modules[govtypes.ModuleName] = sessionGovModule{original}
	params, err := a.GovKeeper.Params.Get(ctx())
	require.NoError(t, err)
	period, expedited := time.Second, 500*time.Millisecond
	params.VotingPeriod, params.ExpeditedVotingPeriod = &period, &expedited
	params.MinDeposit = sdk.NewCoins(sdk.NewInt64Coin(identity.BaseDenom, 1))
	require.NoError(t, a.GovKeeper.Params.Set(ctx(), params))
	var failureReason string
	propose := func(messages ...sdk.Msg) govv1.ProposalStatus {
		t.Helper()
		proposal, err := a.GovKeeper.SubmitProposal(ctx(), messages, "", "Reviewed system administration", "Pinned vault and Gateway authority", account.GetAddress(), false)
		require.NoError(t, err)
		active, err := a.GovKeeper.AddDeposit(ctx(), proposal.Id, account.GetAddress(), params.MinDeposit)
		require.NoError(t, err)
		require.True(t, active)
		require.NoError(t, a.GovKeeper.AddVote(ctx(), proposal.Id, sdk.AccAddress(validators.Proposer.Address), govv1.NewNonSplitVoteOption(govv1.OptionYes), ""))
		block()
		block()
		updated, err := a.GovKeeper.Proposals.Get(ctx(), proposal.Id)
		require.NoError(t, err)
		failureReason = updated.FailedReason
		return updated.Status
	}
	implementation := func(target common.Address) common.Address {
		word := a.EVMKeeper.GetState(ctx(), target, common.HexToHash("0x360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc"))
		return common.BytesToAddress(word[12:])
	}
	query := func(artifact contracts.Artifact, target common.Address, method string) []interface{} {
		t.Helper()
		child, _ := ctx().CacheContext()
		child = child.WithGasMeter(storetypes.NewGasMeter(3_000_000))
		message := core.Message{From: owner, To: &target, Value: new(big.Int), GasLimit: 100000, GasPrice: new(big.Int), GasFeeCap: new(big.Int), GasTipCap: new(big.Int), Data: pack(artifact, method), SkipNonceChecks: true, SkipFromEOACheck: true}
		response, err := a.EVMKeeper.ApplyMessage(child, statedb.New(child, a.EVMKeeper, statedb.NewEmptyTxConfig()), message, nil, false, false, true)
		require.NoError(t, err)
		require.Empty(t, response.VmError)
		values, err := artifact.ABI.Unpack(method, response.Ret)
		require.NoError(t, err)
		return values
	}
	request := func(role, method string, args ...interface{}) *authoritytypes.MsgExecuteSystemContract {
		t.Helper()
		target, artifact := vaultAddress, vault
		if role != "nft-position-vault" {
			target, artifact = gatewayAddress, gateways[0]
		}
		current := implementation(target)
		call := &authoritytypes.SystemCall{Role: role, Target: target.Bytes(), TargetCodeHash: proxyHash.Bytes(), Implementation: current.Bytes(), ImplementationCodeHash: a.EVMKeeper.GetCodeHash(ctx(), current).Bytes(), Data: pack(artifact, method, args...), GasLimit: 300000}
		if method == "upgrade" {
			call.ReplacementCodeHash = a.EVMKeeper.GetCodeHash(ctx(), args[0].(common.Address)).Bytes()
		}
		return &authoritytypes.MsgExecuteSystemContract{Authority: authtypes.NewModuleAddress("gov").String(), Calls: []*authoritytypes.SystemCall{call}}
	}
	for _, role := range []string{"nft-position-vault", "delegation", "delegation-nft"} {
		t.Run(role+" canonical pause and unpause", func(t *testing.T) {
			target, artifact := vaultAddress, vault
			if role != "nft-position-vault" {
				target, artifact = gatewayAddress, gateways[0]
			}
			require.Equal(t, govv1.StatusPassed, propose(request(role, "pause")))
			require.Equal(t, true, query(artifact, target, "paused")[0])
			require.Equal(t, govv1.StatusPassed, propose(request(role, "unpause")))
			require.Equal(t, false, query(artifact, target, "paused")[0])
		})
	}
	require.Equal(t, govv1.StatusPassed, propose(request("nft-position-vault", "upgrade", vaultReplacement, []byte{})))
	require.Equal(t, vaultReplacement, implementation(vaultAddress))
	require.Equal(t, govv1.StatusPassed, propose(request("delegation", "upgrade", gatewayImpls[1], []byte{})))
	require.Equal(t, gatewayImpls[1], implementation(gatewayAddress))
	// The second alias has exactly the same reviewed replacement policy.
	require.Equal(t, govv1.StatusPassed, propose(request("delegation-nft", "upgrade", gatewayImpls[0], []byte{})))
	require.Equal(t, gatewayImpls[0], implementation(gatewayAddress))
	t.Run("ordinary failed approved proposals cannot widen authority", func(t *testing.T) {
		for _, financial := range []*authoritytypes.MsgExecuteSystemContract{
			request("nft-position-vault", "splitPosition", big.NewInt(1), big.NewInt(1), from),
			request("nft-position-vault", "redeemPosition", big.NewInt(1), from),
		} {
			require.Equal(t, govv1.StatusFailed, propose(financial))
			require.Contains(t, failureReason, "system method is not approved for authority execution")
		}
		for _, invalid := range []*authoritytypes.MsgExecuteSystemContract{
			request("nft-position-vault", "transferOwnership", from),
			request("nft-position-vault", "upgrade", registryImpl, []byte{}),
			request("nft-position-vault", "upgrade", vaultImpl, []byte{1}),
		} {
			require.Equal(t, govv1.StatusFailed, propose(invalid))
			require.Equal(t, vaultReplacement, implementation(vaultAddress))
			require.Equal(t, owner, query(vault, vaultAddress, "owner")[0])
			require.Equal(t, false, query(vault, vaultAddress, "paused")[0])
		}
		// A later forbidden selector rolls back a valid earlier pause in the
		// canonical SDK proposal cache, not merely in a direct keeper call.
		require.Equal(t, govv1.StatusFailed, propose(request("nft-position-vault", "pause"), request("nft-position-vault", "transferOwnership", from)))
		require.Equal(t, false, query(vault, vaultAddress, "paused")[0])
	})
}
