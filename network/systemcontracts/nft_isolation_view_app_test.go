package systemcontracts_test

import (
	"math/big"
	"os"
	"testing"

	sdkmath "cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/evm/x/vm/statedb"
	evmtypes "github.com/cosmos/evm/x/vm/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	ownedapp "github.com/maxwell2010/go-smart-node/network/app"
	nftview "github.com/maxwell2010/go-smart-node/network/decimal/nft/evmview"
	nftkeeper "github.com/maxwell2010/go-smart-node/network/decimal/nft/keeper"
	"github.com/maxwell2010/go-smart-node/network/decimal/nft/receipts"
	nfttypes "github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/settlement"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
	"github.com/stretchr/testify/require"
)

type isolationReaderMetadata map[common.Address]nfttypes.Collection

func (m isolationReaderMetadata) GetCollectionByDRC(_ sdk.Context, address string) (nfttypes.Collection, error) {
	v, ok := m[common.HexToAddress(address)]
	if !ok {
		return nfttypes.Collection{}, nftkeeper.ErrNotFound
	}
	return v, nil
}

// Only native collection admission is a metadata fixture. System-role ownership,
// linked Gateway/library, factory, origin, vault, collateral and all EVM calls are
// actual compiled code in the owned App. No transaction reaches a public network.
func runIsolatedPositionReader(t *testing.T, e isolatedBackendEnv) {
	t.Helper()
	owner := crypto.PubkeyToAddress(e.keys[0].PublicKey)
	library := e.load("MintCandyDelegationNFTLib")
	libraryAddress := crypto.CreateAddress(owner, e.app.EVMKeeper.GetNonce(e.ctx(), owner))
	creation, err := library.Deployment()
	require.NoError(t, err)
	e.send(e.keys[0], nil, new(big.Int), creation, true)
	expectedLibrary, err := contracts.ExpectedRuntime("MintCandyDelegationNFTLib", nil, map[string]common.Hash{"owned/DecimalDelegation.sol:DelegationNFTLib.library_deploy_address": common.BytesToHash(libraryAddress.Bytes())})
	require.NoError(t, err)
	require.Equal(t, expectedLibrary, e.app.EVMKeeper.GetCode(e.ctx(), e.app.EVMKeeper.GetCodeHash(e.ctx(), libraryAddress)))
	gateway, err := contracts.LoadLinked("MintCandyIsolatedDelegation", map[string]common.Address{"owned/DecimalDelegation.sol:DelegationNFTLib": libraryAddress})
	require.NoError(t, err)
	registry, proxy, bootstrap := e.load("DecimalContractCenter"), e.load("DecimalContractCenterProxy"), e.load("NewNetworkProxyBootstrap")
	factory, vault, origin, wrapper := e.load("NewNetworkNFTCenter"), e.load("MintCandyNFTPositionVault"), e.load("MintCandyIsolatedDRC1155"), e.load("MintCandyWrappedDEL")
	registryImpl, gatewayImpl, factoryImpl, vaultImpl, originImpl := e.deploy(registry), e.deploy(gateway), e.deploy(factory), e.deploy(vault), e.deploy(origin)
	wrapped := e.deploy(wrapper)
	nonce := e.app.EVMKeeper.GetNonce(e.ctx(), owner)
	registryAddress, gatewayAddress, factoryAddress, vaultAddress := crypto.CreateAddress(owner, nonce), crypto.CreateAddress(owner, nonce+1), crypto.CreateAddress(owner, nonce+2), crypto.CreateAddress(owner, nonce+3)
	var configs [][]byte
	for _, binding := range []struct {
		role    string
		address common.Address
	}{{"delegation", gatewayAddress}, {"delegation-nft", gatewayAddress}, {"nft-center", factoryAddress}, {"nft-position-vault", vaultAddress}, {"wdel", wrapped}} {
		data, err := registry.ABI.Pack("setContractAddress", binding.role, binding.address)
		require.NoError(t, err)
		configs = append(configs, data)
	}
	createProxy := func(expected, implementation common.Address, initialization []byte, configuration [][]byte) {
		data, err := bootstrap.ABI.Pack("bootstrapConfigured", e.govOwner, implementation, initialization, configuration)
		require.NoError(t, err)
		require.Equal(t, expected, e.deploy(proxy, e.setup, data))
	}
	createProxy(registryAddress, registryImpl, nil, configs)
	setRegistry, err := gateway.ABI.Pack("setContractCenter", registryAddress)
	require.NoError(t, err)
	createProxy(gatewayAddress, gatewayImpl, nil, [][]byte{setRegistry})
	initFactory, err := factory.ABI.Pack("initializeFresh", registryAddress)
	require.NoError(t, err)
	initChild, err := factory.ABI.Pack("upgradeNFT", originImpl, uint8(1), true)
	require.NoError(t, err)
	createProxy(factoryAddress, factoryImpl, initFactory, [][]byte{initChild})
	initVault, err := vault.ABI.Pack("initializeFresh", registryAddress, factoryAddress)
	require.NoError(t, err)
	createProxy(vaultAddress, vaultImpl, initVault, nil)
	call := func(key int, artifact contracts.Artifact, target common.Address, value *big.Int, success bool, method string, args ...interface{}) *evmtypes.MsgEthereumTxResponse {
		t.Helper()
		data, err := artifact.ABI.Pack(method, args...)
		require.NoError(t, err)
		return e.send(e.keys[key], &target, value, data, success)
	}
	integer := func(artifact contracts.Artifact, method string, response *evmtypes.MsgEthereumTxResponse) *big.Int {
		values, err := artifact.ABI.Unpack(method, response.Ret)
		require.NoError(t, err)
		return values[0].(*big.Int)
	}
	govSetup := func(artifact contracts.Artifact, target common.Address, method string, args ...interface{}) {
		data, err := artifact.ABI.Pack(method, args...)
		require.NoError(t, err)
		child, commit := e.ctx().CacheContext()
		child = child.WithGasMeter(storetypes.NewGasMeter(3_000_000))
		message := core.Message{From: e.govOwner, To: &target, Value: new(big.Int), GasLimit: 500000, GasPrice: new(big.Int), GasFeeCap: new(big.Int), GasTipCap: new(big.Int), Data: data, SkipNonceChecks: true, SkipFromEOACheck: true}
		response, err := e.app.EVMKeeper.ApplyMessage(child, statedb.New(child, e.app.EVMKeeper, statedb.NewEmptyTxConfig()), message, nil, true, false, true)
		require.NoError(t, err)
		require.Empty(t, response.VmError)
		commit()
	}
	type meta struct {
		Creator                   common.Address
		Symbol, Name, ContractURI string
		Refundable, Burnable      bool
	}
	created := call(0, factory, factoryAddress, new(big.Int), true, "createDRC1155", meta{owner, "VIEWISO", "Reader isolated origin", "ipfs://reader-origin", true, true})
	originAddress := integerAddress(t, factory, "createDRC1155", created)
	metadata := isolationReaderMetadata{
		originAddress: {Creator: sdk.AccAddress(owner.Bytes()).String(), Denom: "reader_origin", TypeNft: nfttypes.NftType_NFT1155, AddressDRC: originAddress.Hex()},
		vaultAddress:  {Creator: e.govOwner.Hex(), Denom: "reader_positions", TypeNft: nfttypes.NftType_NFT721, AddressDRC: vaultAddress.Hex()},
	}
	// The native creator is a bech32 account, independently of EVM ownership.
	collection := metadata[vaultAddress]
	collection.Creator = sdk.AccAddress(e.govOwner.Bytes()).String()
	metadata[vaultAddress] = collection
	deployMaster := func() (contracts.Artifact, common.Address) {
		master := e.load("NewNetworkMasterValidator")
		masterImpl := e.deploy(master)
		masterInit, err := master.ABI.Pack("initializeFresh", registryAddress)
		require.NoError(t, err)
		masterConfig, err := bootstrap.ABI.Pack("bootstrapConfigured", e.govOwner, masterImpl, masterInit, [][]byte{})
		require.NoError(t, err)
		return master, e.deploy(proxy, e.setup, masterConfig)
	}
	if dir := os.Getenv(nativeConsensusDaemonFixtureEnv); dir != "" {
		master, masterAddress := deployMaster()
		govSetup(registry, registryAddress, "setContractAddress", "master-validator", masterAddress)
		profile := ownedapp.WithdrawalProfile{
			Registry: registryAddress, Gateway: gatewayAddress, Library: libraryAddress, NFTCenter: factoryAddress,
			PositionVault: vaultAddress, MasterValidator: masterAddress, WrappedDEL: wrapped,
			SettlementOnly: true, EnableDELDelegation: true,
		}
		var nft *nativeNFTDowntimeFixture
		if os.Getenv(nativeConsensusNFTDowntimeEnv) == "1" {
			origin := mintNativeNFTDowntimeFixture(t, e, origin, originAddress, crypto.PubkeyToAddress(e.keys[3].PublicKey))
			nft = &origin
		}
		exportNativeConsensusDaemonFixture(t, e, dir, profile, gateway, master, wrapper, vault, nft, govSetup)
		return
	}
	if os.Getenv(signedNFTAdmissionPhaseEnv) == "seed" {
		master, masterAddress := deployMaster()
		govSetup(registry, registryAddress, "setContractAddress", "master-validator", masterAddress)
		profile := ownedapp.WithdrawalProfile{
			Registry: registryAddress, Gateway: gatewayAddress, Library: libraryAddress, NFTCenter: factoryAddress,
			PositionVault: vaultAddress, MasterValidator: masterAddress, WrappedDEL: wrapped,
			SettlementOnly: true, EnableDELDelegation: true, EnableNFTDelegation: true,
		}
		seedSignedNFTAdmissionGenesis(t, e, profile, gateway, master, wrapper, origin, originAddress, govSetup)
		return
	}
	config := nftview.Config{Protocol: nftview.ProfileIsolatedPositionsV1, PositionVault: vaultAddress, WrappedDEL: wrapped, ChainID: e.ctx().ChainID(), EVMChainID: 990001, Registry: registryAddress, NFTCenter: factoryAddress, Delegation: gatewayAddress, DelegationLibrary: libraryAddress, EVMGas: 100000, SDKGas: 5_000_000, MaxOwners: 8, MaxQuantity: 10000}
	reader, err := nftview.New(metadata, e.app.EVMKeeper, config)
	require.NoError(t, err)
	read := func(id *big.Int) nftview.PositionSnapshot {
		t.Helper()
		ctx := e.ctx().WithGasMeter(storetypes.NewGasMeter(15_000_000))
		p, err := reader.Position(ctx, common.BigToHash(id))
		require.NoError(t, err)
		t.Logf("isolated actual position view status=%d SDKgas=%d", p.Status, ctx.GasMeter().GasConsumed())
		return p
	}
	unit := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	ten := new(big.Int).Mul(unit, big.NewInt(10))
	twenty := new(big.Int).Mul(ten, big.NewInt(2))
	externalID := new(big.Int).Add(new(big.Int).Lsh(big.NewInt(1), 200), big.NewInt(999))
	call(0, origin, originAddress, twenty, true, "mintByDEL", owner, externalID, big.NewInt(2), "ipfs://reader-item")
	first := integer(origin, "isolate", call(0, origin, originAddress, new(big.Int), true, "isolate", externalID, big.NewInt(1), owner))
	second := integer(origin, "isolate", call(0, origin, originAddress, new(big.Int), true, "isolate", externalID, big.NewInt(1), owner))
	p := read(first)
	require.Equal(t, nftview.PositionActive, p.Status)
	require.Equal(t, common.BigToHash(externalID), p.Origin.TokenID)
	require.Equal(t, uint32(1), p.OriginQuantity)
	require.Equal(t, owner, p.Owner)
	require.Equal(t, common.BigToHash(ten), p.ReserveAmount)
	require.Equal(t, common.BigToHash(twenty), p.DeclaredCurrencyReserve)
	snapshot, err := p.Reserve()
	require.NoError(t, err)
	snapshot.Amount[0] = 1
	require.Equal(t, common.BigToHash(ten), p.ReserveAmount)
	for _, target := range []struct {
		artifact contracts.Artifact
		address  common.Address
	}{{factory, factoryAddress}, {vault, vaultAddress}} {
		govSetup(target.artifact, target.address, "pause")
		paused := read(first)
		require.Equal(t, p, paused)
		govSetup(target.artifact, target.address, "unpause")
	}
	e.send(e.keys[0], &vaultAddress, big.NewInt(7), nil, true)
	surplus := read(first)
	require.Equal(t, p.DeclaredCurrencyReserve, surplus.DeclaredCurrencyReserve)
	require.Equal(t, common.BigToHash(new(big.Int).Add(twenty, big.NewInt(7))), surplus.HeldCurrencyReserve)
	child := integer(vault, "splitPosition", call(0, vault, vaultAddress, new(big.Int), true, "splitPosition", second, big.NewInt(1), owner))
	closed := read(second)
	require.Equal(t, nftview.PositionClosed, closed.Status)
	require.Equal(t, common.Address{}, closed.Owner)
	_, err = closed.Reserve()
	require.Error(t, err)
	activeChild := read(child)
	require.Equal(t, common.BigToHash(second), activeChild.ParentPositionID)
	require.Equal(t, p.Origin, activeChild.Origin)
	bad := config
	bad.ChainID = "another-chain"
	wrong, err := nftview.New(metadata, e.app.EVMKeeper, bad)
	require.NoError(t, err)
	_, err = wrong.Position(e.ctx(), common.BigToHash(first))
	require.Error(t, err)
	bad = config
	bad.SDKGas = 1
	limited, err := nftview.New(metadata, e.app.EVMKeeper, bad)
	require.NoError(t, err)
	_, err = limited.Position(e.ctx(), common.BigToHash(first))
	require.Error(t, err)
	_, err = reader.Position(e.ctx(), common.Hash{})
	require.Error(t, err)
	_, err = reader.Position(e.ctx(), common.BigToHash(big.NewInt(999999)))
	require.Error(t, err)
	profile, known, err := reader.AuthenticateCollection(e.ctx(), vaultAddress, receipts.EntryPointSignedEthereum)
	require.NoError(t, err)
	require.True(t, known)
	recipient := crypto.PubkeyToAddress(e.keys[3].PublicKey)
	call(0, vault, vaultAddress, new(big.Int), true, exactABIMethod(t, vault.ABI, "safeTransferFrom(address,address,uint256)"), owner, recipient, first)
	ownership, err := reader.Ownership(e.ctx(), profile, receipts.AssetKey{Contract: vaultAddress, TokenID: common.BigToHash(first)}, []common.Address{owner, recipient})
	require.NoError(t, err)
	require.Equal(t, uint32(0), ownership[owner])
	require.Equal(t, uint32(1), ownership[recipient])
	for _, item := range []struct {
		id  *big.Int
		key int
	}{{first, 3}, {child, 0}} {
		to := crypto.PubkeyToAddress(e.keys[item.key].PublicKey)
		e.credit.Set(ten)
		call(item.key, vault, vaultAddress, new(big.Int), true, "redeemPosition", item.id, to)
		require.Equal(t, nftview.PositionClosed, read(item.id).Status)
	}
	call(0, wrapper, wrapped, unit, true, "deposit")
	call(0, wrapper, wrapped, new(big.Int), true, "approve", originAddress, unit)
	call(0, origin, originAddress, new(big.Int), true, "mint", owner, big.NewInt(22), big.NewInt(1), "ipfs://reader-wrapped", unit, wrapped)
	wrappedPosition := integer(origin, "isolate", call(0, origin, originAddress, new(big.Int), true, "isolate", big.NewInt(22), big.NewInt(1), owner))
	w := read(wrappedPosition)
	require.Equal(t, nfttypes.IsolatedReserveERC20, w.ReserveKind)
	require.Equal(t, wrapped, w.ReserveToken)
	require.Equal(t, common.BigToHash(unit), w.ReserveAmount)
	require.Equal(t, w.ReserveAmount, w.WrappedSupply)
	require.Equal(t, w.WrappedSupply, w.WrappedNativeBacking)
	call(0, vault, vaultAddress, new(big.Int), true, "redeemPosition", wrappedPosition, owner)
	e.credit.Set(unit)
	call(0, wrapper, wrapped, new(big.Int), true, "withdraw", unit)

	// Source authentication has no dependency on native NFT metadata or balances.
	master, masterAddress := deployMaster()
	govSetup(registry, registryAddress, "setContractAddress", "master-validator", masterAddress)
	missingMaster := receipts.Execution{EntryPoint: receipts.EntryPointSignedEthereum, Receipt: &ethtypes.Receipt{Status: ethtypes.ReceiptStatusSuccessful}}
	_, err = reader.AuthenticateStakeSource(e.ctx(), missingMaster)
	require.Error(t, err)
	config.MasterValidator = masterAddress
	sourceReader, err := nftview.New(metadata, e.app.EVMKeeper, config)
	require.NoError(t, err)
	response := call(0, origin, originAddress, unit, true, "mintByDEL", owner, big.NewInt(303), big.NewInt(1), "ipfs://source-auth")
	execution := receipts.Execution{EntryPoint: receipts.EntryPointSignedEthereum, Receipt: &ethtypes.Receipt{Status: ethtypes.ReceiptStatusSuccessful, Logs: evmtypes.LogsToEthereum(response.Logs)}}
	auth := func(reader *nftview.Reader, execution receipts.Execution) (nftview.StakeSource, uint64) {
		child := e.ctx().WithGasMeter(storetypes.NewGasMeter(15_000_000))
		source, err := reader.AuthenticateStakeSource(child, execution)
		require.NoError(t, err)
		return source, child.GasMeter().GasConsumed()
	}
	source, cost := auth(sourceReader, execution)
	require.Equal(t, config.ChainID, source.ChainID)
	require.Equal(t, config.EVMChainID, source.EVMChainID)
	require.Equal(t, gatewayAddress, source.Gateway)
	require.NotEqual(t, common.Hash{}, source.Fingerprint)
	newPosition := integer(origin, "isolate", call(0, origin, originAddress, new(big.Int), true, "isolate", big.NewInt(303), big.NewInt(1), owner))
	afterFinancial, afterFinancialCost := auth(sourceReader, execution)
	require.Equal(t, source, afterFinancial, "current NFT state cannot invalidate role-only source capture")
	larger := config
	larger.EVMGas = 200000
	largerReader, err := nftview.New(metadata, e.app.EVMKeeper, larger)
	require.NoError(t, err)
	largerSource, largerCost := auth(largerReader, execution)
	require.Equal(t, source, largerSource)
	require.Equal(t, afterFinancialCost, largerCost, "same-context internal getter billing has no cap-dependent transaction minimum")
	execution.EntryPoint = receipts.EntryPointGovernance
	execution.Receipt.TxHash = common.Hash{}
	internal, _ := auth(sourceReader, execution)
	require.Equal(t, source, internal, "zero-hash internal Gov execution uses role identity, not transaction-hash deduplication")
	execution.Receipt.Status = ethtypes.ReceiptStatusFailed
	_, err = sourceReader.AuthenticateStakeSource(e.ctx(), execution)
	require.Error(t, err)
	execution.Receipt.Status = ethtypes.ReceiptStatusSuccessful
	execution.EntryPoint = receipts.EntryPointIBC
	_, err = sourceReader.AuthenticateStakeSource(e.ctx(), execution)
	require.Error(t, err)
	execution.EntryPoint = receipts.EntryPointSignedEthereum
	govSetup(master, masterAddress, "transferOwnership", owner)
	_, err = sourceReader.AuthenticateStakeSource(e.ctx(), execution)
	require.Error(t, err, "a known-code Master owned by an EOA must fail closed")
	call(0, master, masterAddress, new(big.Int), true, "transferOwnership", e.govOwner)
	restored, _ := auth(sourceReader, execution)
	require.Equal(t, source, restored)
	e.credit.Set(unit)
	call(0, vault, vaultAddress, new(big.Int), true, "redeemPosition", newPosition, owner)
	t.Logf("actual isolated StakeSource authentication SDKgas=%d; native capture/pipeline costs separate", cost)
	if mode := os.Getenv(delLifecycleFixtureEnv); mode == "1" || mode == "hold" || mode == "reset" ||
		mode == "penalty" || mode == "recovered-deposits" || mode == "recovered-nft-deposit" ||
		mode == heldPenaltyMode || mode == heldLotBudgetMode || mode == mixedLotBudgetMode ||
		mode == "frozen-penalty-partial" || mode == "frozen-penalty-full" ||
		mode == "frozen-held-penalty" || mode == "frozen-held-penalty-full" || mode == frozenHeldLotBudgetMode ||
		mode == "nft-penalty" || mode == "nft-penalty-zero-exit" || mode == "frozen-nft-penalty" || mode == frozenNFTMaxUnitsMode || mode == frozenNFTBothEligibleMode {
		profile := ownedapp.WithdrawalProfile{Registry: registryAddress, Gateway: gatewayAddress, Library: libraryAddress, NFTCenter: factoryAddress,
			PositionVault: vaultAddress, MasterValidator: masterAddress, WrappedDEL: wrapped, SettlementOnly: true, EnableDELDelegation: true,
			EnableDELHold: mode == "hold" || mode == "reset", EnableDELHoldReset: mode == "reset"}
		switch mode {
		case "penalty":
			runSignedOrdinaryDELPenalty(t, e, profile, gateway, master, wrapper, sourceReader, govSetup, nil)
			return
		case "recovered-deposits":
			runSignedOrdinaryDELPenalty(t, e, profile, gateway, master, wrapper, sourceReader, govSetup, verifyRecoveredDELDeposits)
			return
		case "recovered-nft-deposit":
			runSignedOrdinaryDELPenalty(t, e, profile, gateway, master, wrapper, sourceReader, govSetup,
				func(t *testing.T, f signedDELFixture, executor *settlement.Executor, existing, supply sdkmath.Int) {
					verifyRecoveredNFTDeposit(t, f, executor, profile, origin, vault, originAddress, existing, supply)
				})
			return
		}
		if mode == heldPenaltyMode || mode == heldLotBudgetMode || mode == mixedLotBudgetMode {
			runSignedHeldDELPenalty(t, e, profile, gateway, master, wrapper, sourceReader, govSetup, mode)
			return
		}
		if mode == "frozen-penalty-partial" || mode == "frozen-penalty-full" || mode == "frozen-held-penalty" || mode == "frozen-held-penalty-full" || mode == frozenHeldLotBudgetMode {
			full := mode == "frozen-penalty-full" || mode == "frozen-held-penalty-full"
			held := mode == "frozen-held-penalty" || mode == "frozen-held-penalty-full" || mode == frozenHeldLotBudgetMode
			runSignedFrozenDELPenalty(t, e, profile, gateway, master, wrapper, sourceReader, govSetup, full, held, mode == frozenHeldLotBudgetMode)
			return
		}
		if mode == "nft-penalty" || mode == "nft-penalty-zero-exit" || mode == "frozen-nft-penalty" || mode == frozenNFTMaxUnitsMode || mode == frozenNFTBothEligibleMode {
			runSignedActiveNFTPenalty(t, e, profile, gateway, master, wrapper, origin, vault, originAddress, sourceReader, govSetup, mode)
			return
		}
		runSignedDELDelegationLifecycle(t, e, profile, gateway, master, wrapper, govSetup)
		return
	}
	if os.Getenv(delDelegationDaemonFixtureEnv) == "" {
		// This test deliberately leaves custom-backed positions. The
		// fresh DEL daemon fixture needs a Gateway with no such positions.
		t.Run("registered custom DRC20 minimum-supply gate", func(t *testing.T) {
			runRegisteredCustomMinimum(t, customMinimumEnv{base: e, registry: registryAddress, gateway: gatewayAddress, vault: vaultAddress, origin: originAddress, master: masterAddress, call: call, gov: govSetup, reader: sourceReader})
		})
	}
	t.Run("shared App withdrawal settlement", func(t *testing.T) {
		profile := ownedapp.WithdrawalProfile{Registry: registryAddress, Gateway: gatewayAddress, Library: libraryAddress, NFTCenter: factoryAddress,
			PositionVault: vaultAddress, MasterValidator: masterAddress, WrappedDEL: wrapped}
		validator := runSharedWithdrawalDEL(t, e, profile, gateway, master, govSetup)
		if os.Getenv(withdrawalGenesisFixtureEnv) != "" {
			govSetup(gateway, gatewayAddress, "setFreezeTime", big.NewInt(75), new(big.Int))
		}
		runSharedWithdrawalNFT(t, e, profile, gateway, origin, vault, originAddress, validator)
	})
}
