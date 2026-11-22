package app

import (
	"bytes"
	sdkmath "cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	"errors"
	"fmt"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/cosmos/evm/x/vm/statedb"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	nftkeeper "github.com/maxwell2010/go-smart-node/network/decimal/nft/keeper"
	nfttypes "github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
	"math/big"
)

const userNFTMembershipPrefix byte = 0x81

type userNFTMetadata struct {
	Creator     common.Address
	Symbol      string
	Name        string
	ContractURI string
	Refundable  bool
	Burnable    bool
}

func userNFTKey(target common.Address) []byte {
	return append([]byte{userNFTMembershipPrefix}, target[:]...)
}
func (c *withdrawalController) userNFTRead(ctx sdk.Context, target common.Address, contract abi.ABI, method string, args ...interface{}) ([]interface{}, error) {
	data, err := contract.Pack(method, args...)
	if err != nil {
		return nil, err
	}
	child, _ := ctx.CacheContext()
	meter := storetypes.NewGasMeter(2_000_000)
	child = child.WithGasMeter(meter)
	defer func() { ctx.GasMeter().ConsumeGas(meter.GasConsumedToLimit(), "public NFT authenticated read") }()
	msg := core.Message{From: common.BytesToAddress(authtypes.NewModuleAddress("gov")), To: &target, Value: new(big.Int), Data: data, GasLimit: 500_000, GasPrice: new(big.Int), GasFeeCap: new(big.Int), GasTipCap: new(big.Int), SkipNonceChecks: true, SkipFromEOACheck: true}
	response, err := c.customCollateral.evm.ApplyMessage(child, statedb.New(child, c.customCollateral.evm, statedb.NewEmptyTxConfig()), msg, nil, false, false, true)
	if err != nil {
		return nil, err
	}
	if response.VmError != "" || len(response.Ret) > 8192 {
		return nil, errors.New("bounded NFT getter failed")
	}
	return contract.Unpack(method, response.Ret)
}
func (c *withdrawalController) authenticateUserNFTFactory(ctx sdk.Context) (abi.ABI, error) {
	r := c.customCollateral
	empty := abi.ABI{}
	if err := r.authenticateRoles(ctx); err != nil {
		return empty, err
	}
	factory, err := contracts.Load("NewNetworkNFTCenter")
	if err != nil {
		return empty, err
	}
	implementation := r.evm.GetState(ctx, c.profile.NFTCenter, customImplementationSlot)
	target := common.BytesToAddress(implementation[12:])
	if implementation != customAddressWord(target) || !r.exactCode(ctx, c.profile.NFTCenter, r.proxyCode) || !r.exactCode(ctx, target, factory.Runtime) || r.evm.GetState(ctx, c.profile.NFTCenter, customOwnableSlot) != customAddressWord(common.BytesToAddress(authtypes.NewModuleAddress("gov"))) {
		return empty, errors.New("NFT factory differs from governance-pinned code and owner")
	}
	registry, err := contracts.CompiledABI("DecimalContractCenter")
	if err != nil {
		return empty, err
	}
	values, err := c.userNFTRead(ctx, c.profile.Registry, registry, "getContractAddress", "nft-center")
	if err != nil {
		return empty, err
	}
	if values[0].(common.Address) != c.profile.NFTCenter {
		return empty, errors.New("NFT registry role differs")
	}
	values, err = c.userNFTRead(ctx, c.profile.NFTCenter, factory.ABI, "getContractCenter")
	if err != nil {
		return empty, err
	}
	if values[0].(common.Address) != c.profile.Registry {
		return empty, errors.New("NFT factory registry differs")
	}
	values, err = c.userNFTRead(ctx, c.profile.NFTCenter, factory.ABI, "paused")
	if err != nil {
		return empty, err
	}
	if values[0].(bool) {
		return empty, errors.New("NFT factory paused")
	}
	return factory.ABI, nil
}
func (c *withdrawalController) authenticateUserNFT(ctx sdk.Context, target common.Address, family byte, factory abi.ABI) (abi.ABI, error) {
	if family < 2 || family > 5 {
		return abi.ABI{}, errors.New("unknown public NFT family")
	}
	kind := uint8(0)
	if family == 3 || family == 5 {
		kind = 1
	}
	reserved := family < 4
	name := map[byte]string{2: "NewNetworkDRC721", 3: "MintCandyIsolatedDRC1155", 4: "DRC721Reserveless", 5: "DRC1155Reserveless"}[family]
	asset, err := contracts.Load(name)
	if err != nil {
		return abi.ABI{}, err
	}
	values, err := c.userNFTRead(ctx, c.profile.NFTCenter, factory, "readImplementation", family)
	if err != nil {
		return abi.ABI{}, err
	}
	if !c.customCollateral.exactCode(ctx, values[0].(common.Address), asset.Runtime) {
		return abi.ABI{}, errors.New("NFT implementation is not compiler pinned")
	}
	values, err = c.userNFTRead(ctx, c.profile.NFTCenter, factory, "beacon", kind, reserved)
	if err != nil {
		return abi.ABI{}, err
	}
	beacon := values[0].(common.Address)
	code, err := contracts.NFTBeaconRuntime(c.profile.NFTCenter, kind, reserved)
	if err != nil {
		return abi.ABI{}, err
	}
	if !c.customCollateral.exactCode(ctx, beacon, code) {
		return abi.ABI{}, errors.New("NFT beacon differs")
	}
	code, err = contracts.PauseAwareProxyRuntime(beacon, c.profile.NFTCenter, family)
	if err != nil {
		return abi.ABI{}, err
	}
	if !c.customCollateral.exactCode(ctx, target, code) || c.customCollateral.evm.GetState(ctx, target, customBeaconSlot) != customAddressWord(beacon) {
		return abi.ABI{}, errors.New("NFT proxy differs")
	}
	values, err = c.userNFTRead(ctx, c.profile.NFTCenter, factory, "getNFTState", target)
	if err != nil {
		return abi.ABI{}, err
	}
	state := abi.ConvertType(values[0], new(struct {
		Active      bool
		NftType     uint8
		WithReserve bool
	})).(*struct {
		Active      bool
		NftType     uint8
		WithReserve bool
	})
	if !state.Active || state.NftType != kind || state.WithReserve != reserved {
		return abi.ABI{}, errors.New("NFT factory membership differs")
	}
	return asset.ABI, nil
}

// admitUserNFT deliberately excludes callbacks, batch calls, custom collateral,
// arbitrary metadata mutation and financial burn. Each additional route needs
// independent signed receipt and native-accounting coverage.
func (c *withdrawalController) admitUserNFT(ctx sdk.Context, sender common.Address, msg core.Message, receipt *ethtypes.Receipt) (bool, error) {
	if !c.profile.EnableNFTDelegation || c.customCollateral == nil || msg.To == nil {
		return false, nil
	}
	r := c.customCollateral
	member := ctx.KVStore(r.store).Get(userNFTKey(*msg.To))
	creation := *msg.To == c.profile.NFTCenter
	if !creation && member == nil {
		return false, nil
	}
	if sender == (common.Address{}) || sender != msg.From || msg.Value == nil || msg.Value.Sign() < 0 || len(msg.SetCodeAuthorizations) != 0 || len(msg.Data) < 4 || len(msg.Data) > 8192 || receipt == nil || receipt.Status != ethtypes.ReceiptStatusSuccessful || len(receipt.Logs) > 12 {
		return true, errors.New("public NFT requires bounded signed direct call")
	}
	totalLogBytes := 0
	for _, log := range receipt.Logs {
		if log == nil {
			return true, errors.New("nil NFT receipt log")
		}
		totalLogBytes += len(log.Data) + len(log.Topics)*32
		if totalLogBytes > 16384 {
			return true, errors.New("NFT receipt exceeds bounded data")
		}
	}
	child, write := ctx.CacheContext()
	factory, err := c.authenticateUserNFTFactory(child)
	if err != nil {
		return true, err
	}
	if creation {
		err = c.admitUserNFTCreation(child, msg, receipt, factory)
	} else if len(member) != 1 {
		err = errors.New("corrupt public NFT membership")
	} else {
		err = c.admitUserNFTOperation(child, msg, receipt, factory, member[0])
	}
	if err == nil {
		write()
	}
	return true, err
}
func (c *withdrawalController) admitUserNFTCreation(ctx sdk.Context, msg core.Message, receipt *ethtypes.Receipt, factory abi.ABI) error {
	method, err := factory.MethodById(msg.Data[:4])
	if err != nil {
		return err
	}
	families := map[string]byte{"createDRC721": 2, "createDRC1155": 3, "createDRC721Reserveless": 4, "createDRC1155Reserveless": 5}
	family, ok := families[method.RawName]
	if !ok || msg.Value.Sign() != 0 {
		return errors.New("unsupported public NFT factory route")
	}
	args, err := canonicalReservelessArgs(*method, msg.Data)
	if err != nil {
		return err
	}
	meta := abi.ConvertType(args[0], new(userNFTMetadata)).(*userNFTMetadata)
	if meta.Creator != msg.From || !identity.ValidDisplayName(meta.Name, 96) || !identity.ValidDisplayName(meta.Symbol, 32) || !identity.ValidDisplayName(meta.ContractURI, 2048) {
		return errors.New("invalid signed NFT creator or metadata")
	}
	event := factory.Events["NFTCreated"]
	var target common.Address
	count := 0
	kind := uint8(0)
	if family == 3 || family == 5 {
		kind = 1
	}
	for _, log := range receipt.Logs {
		if log == nil {
			return errors.New("nil NFT creation log")
		}
		if log.Address == c.profile.NFTCenter {
			if len(log.Topics) != 2 || log.Topics[0] != event.ID || log.Topics[1] != common.BigToHash(new(big.Int).SetUint64(uint64(kind))) {
				return errors.New("unexpected NFT factory event")
			}
			values, err := event.Inputs.NonIndexed().Unpack(log.Data)
			if err != nil {
				return err
			}
			target = values[0].(common.Address)
			data, err := event.Inputs.NonIndexed().Pack(target, *meta)
			if err != nil || !bytes.Equal(data, log.Data) {
				return errors.New("NFT creation event differs from signed metadata")
			}
			count++
		}
	}
	if count != 1 || target == (common.Address{}) || ctx.KVStore(c.customCollateral.store).Has(userNFTKey(target)) {
		return errors.New("NFT creation must identify one fresh collection")
	}
	nonce := c.customCollateral.evm.GetNonce(ctx, c.profile.NFTCenter)
	if nonce == 0 || crypto.CreateAddress(c.profile.NFTCenter, nonce-1) != target {
		return errors.New("NFT creation address differs from factory CREATE")
	}
	asset, err := c.authenticateUserNFT(ctx, target, family, factory)
	if err != nil {
		return err
	}
	values, err := c.userNFTRead(ctx, target, asset, "creator")
	if err != nil {
		return err
	}
	if values[0].(common.Address) != meta.Creator {
		return errors.New("NFT creator getter differs")
	}
	for _, field := range []struct{ name, want string }{{"name", meta.Name}, {"symbol", meta.Symbol}, {"contractURI", meta.ContractURI}} {
		values, err := c.userNFTRead(ctx, target, asset, field.name)
		if err != nil {
			return err
		}
		if values[0].(string) != field.want {
			return errors.New("NFT deployed metadata differs")
		}
	}
	beacon := common.BytesToAddress(c.customCollateral.evm.GetState(ctx, target, customBeaconSlot).Bytes())
	expected, err := userNFTCreationLogs(factory, c.profile.NFTCenter, target, beacon, kind, *meta)
	if err != nil {
		return err
	}
	if err := requireUserNFTLogs(receipt.Logs, expected); err != nil {
		return err
	}
	owner := sdk.AccAddress(meta.Creator[:]).String()
	typ := nfttypes.NftType_NFT721
	if kind == 1 {
		typ = nfttypes.NftType_NFT1155
	}
	collection := nfttypes.Collection{Creator: owner, Denom: "user_" + fmt.Sprintf("%x", target[:]), AddressDRC: target.Hex(), TypeNft: typ, ReserveLess: family >= 4, ExternalName: meta.Name, ExternalSymbol: meta.Symbol}
	if err = c.nft.AdmitUserCollection(ctx, collection); err != nil {
		return err
	}
	ctx.KVStore(c.customCollateral.store).Set(userNFTKey(target), []byte{family})
	return nil
}

func (c *withdrawalController) admitUserNFTOperation(ctx sdk.Context, msg core.Message, receipt *ethtypes.Receipt, factory abi.ABI, family byte) error {
	target := *msg.To
	asset, err := c.authenticateUserNFT(ctx, target, family, factory)
	if err != nil {
		return err
	}
	method, err := asset.MethodById(msg.Data[:4])
	if err != nil {
		return err
	}
	args, err := canonicalReservelessArgs(*method, msg.Data)
	if err != nil {
		return err
	}
	switch method.RawName {
	case "approve", "setApprovalForAll":
		if msg.Value.Sign() != 0 || len(receipt.Logs) != 1 || receipt.Logs[0].Address != target {
			return errors.New("invalid direct NFT approval receipt")
		}
		log := receipt.Logs[0]
		if method.RawName == "setApprovalForAll" {
			event := asset.Events["ApprovalForAll"]
			data, err := event.Inputs.NonIndexed().Pack(args[1])
			if err != nil {
				return err
			}
			if len(log.Topics) != 3 || log.Topics[0] != event.ID || log.Topics[1] != customAddressWord(msg.From) || log.Topics[2] != customAddressWord(args[0].(common.Address)) || !bytes.Equal(log.Data, data) {
				return errors.New("NFT operator approval event differs")
			}
		} else {
			values, err := c.userNFTRead(ctx, target, asset, "ownerOf", args[1])
			if err != nil {
				return err
			}
			if len(log.Topics) != 4 || log.Topics[0] != asset.Events["Approval"].ID || log.Topics[1] != customAddressWord(values[0].(common.Address)) || log.Topics[2] != customAddressWord(args[0].(common.Address)) || log.Topics[3] != common.BigToHash(args[1].(*big.Int)) || len(log.Data) != 0 {
				return errors.New("NFT token approval event differs")
			}
		}
		return nil
	case "transferFrom", "safeTransferFrom":
		if msg.Value.Sign() != 0 {
			return errors.New("NFT transfer cannot carry native funds")
		}
		if len(args) > 3 {
			if data, ok := args[len(args)-1].([]byte); ok && len(data) != 0 {
				return errors.New("NFT transfer callback data is unsupported")
			}
		}
		if len(c.customCollateral.evm.GetCode(ctx, c.customCollateral.evm.GetCodeHash(ctx, args[1].(common.Address)))) != 0 {
			return errors.New("NFT receiver callbacks are unsupported")
		}
		return c.assets.ProjectWalletTransfer(ctx, receipt, target, c.profile.Gateway, c.profile.PositionVault)
	case "mint", "mintByDEL":
		return c.admitUserNFTMint(ctx, msg, receipt, asset, family, args, method.RawName)
	default:
		return errors.New("NFT route has no reviewed public native projection")
	}
}
func (c *withdrawalController) admitUserNFTMint(ctx sdk.Context, msg core.Message, receipt *ethtypes.Receipt, asset abi.ABI, family byte, args []interface{}, method string) error {
	target := *msg.To
	reserved := family < 4
	is1155 := family == 3 || family == 5
	if reserved && method != "mintByDEL" || !reserved && method != "mint" {
		return errors.New("public NFT mint supports native reserve only")
	}
	recipient := args[0].(common.Address)
	if recipient == (common.Address{}) || recipient == c.profile.Gateway || recipient == c.profile.PositionVault || len(c.customCollateral.evm.GetCode(ctx, c.customCollateral.evm.GetCodeHash(ctx, recipient))) != 0 {
		return errors.New("NFT mint requires an ordinary recipient")
	}
	quantity := uint32(1)
	uriIndex := 1
	var id common.Hash
	if is1155 {
		id = common.BigToHash(args[1].(*big.Int))
		q := args[2].(*big.Int)
		if !q.IsUint64() || q.Uint64() == 0 || q.Uint64() > 128 {
			return errors.New("NFT mint quantity exceeds bound")
		}
		quantity = uint32(q.Uint64())
		uriIndex = 3
	}
	uri := args[uriIndex].(string)
	if !identity.ValidDisplayName(uri, 2048) {
		return errors.New("invalid NFT metadata URI")
	}
	if !reserved && (msg.Value.Sign() != 0 || args[len(args)-2].(*big.Int).Sign() != 0 || args[len(args)-1].(common.Address) != (common.Address{})) {
		return errors.New("reserveless NFT cannot carry collateral")
	}
	transfers := 0
	for _, log := range receipt.Logs {
		if log == nil || log.Address != target || len(log.Topics) == 0 {
			return errors.New("NFT mint includes foreign events")
		}
		if is1155 && log.Topics[0] == crypto.Keccak256Hash([]byte("TransferSingle(address,address,address,uint256,uint256)")) {
			if len(log.Topics) != 4 || log.Topics[1] != customAddressWord(msg.From) || log.Topics[2] != (common.Hash{}) || log.Topics[3] != customAddressWord(recipient) || len(log.Data) != 64 || common.BytesToHash(log.Data[:32]) != id || new(big.Int).SetBytes(log.Data[32:]).Cmp(new(big.Int).SetUint64(uint64(quantity))) != 0 {
				return errors.New("NFT minted transfer differs")
			}
			transfers++
		} else if !is1155 && log.Topics[0] == crypto.Keccak256Hash([]byte("Transfer(address,address,uint256)")) {
			if len(log.Topics) != 4 || log.Topics[1] != (common.Hash{}) || log.Topics[2] != customAddressWord(recipient) || len(log.Data) != 0 {
				return errors.New("NFT minted transfer differs")
			}
			id = log.Topics[3]
			transfers++
		}
	}
	if transfers != 1 {
		return errors.New("NFT mint must emit exactly one direct movement")
	}
	methodURI := "tokenURI"
	fresh := true
	if is1155 {
		methodURI = "uri"
	}
	values, err := c.userNFTRead(ctx, target, asset, methodURI, id.Big())
	if err != nil {
		return err
	}
	if values[0].(string) != uri {
		return errors.New("NFT final URI differs")
	}
	if is1155 {
		values, err = c.userNFTRead(ctx, target, asset, "balanceOf", recipient, id.Big())
	} else {
		values, err = c.userNFTRead(ctx, target, asset, "ownerOf", id.Big())
	}
	if err != nil {
		return err
	}
	if !is1155 && values[0].(common.Address) != recipient {
		return errors.New("NFT final owner differs")
	}
	if is1155 {
		expected := uint64(quantity)
		binding, lookupErr := c.nft.ResolveExternalNFT(ctx, target[:], id[:])
		if lookupErr == nil {
			fresh = false
			count, countErr := c.nft.GetActiveSubTokenCount(ctx, binding.NativeTokenID, sdk.AccAddress(recipient[:]).String())
			if countErr != nil {
				return countErr
			}
			expected += uint64(count)
		} else if !errors.Is(lookupErr, nftkeeper.ErrNotFound) {
			return lookupErr
		}
		if values[0].(*big.Int).Cmp(new(big.Int).SetUint64(expected)) != 0 {
			return errors.New("NFT final balance differs from indexed prestate and mint")
		}
	}

	reserve := sdk.NewCoin(identity.BaseDenom, sdkmath.ZeroInt())
	if reserved {
		q := new(big.Int).SetUint64(uint64(quantity))
		unit, rem := new(big.Int).QuoRem(msg.Value, q, new(big.Int))
		if unit.Sign() <= 0 || rem.Sign() != 0 {
			return errors.New("NFT reserve must be uniform native units")
		}
		reserveMethod := "getReserve"
		if family == 3 {
			reserveMethod = "getIsolationUnitReserve"
		}
		values, err = c.userNFTRead(ctx, target, asset, reserveMethod, id.Big())
		if err != nil {
			return err
		}
		r := abi.ConvertType(values[0], new(userNFTReserve)).(*userNFTReserve)
		if r.Token != (common.Address{}) || r.ReserveType != 1 || r.Amount.Cmp(unit) != 0 {
			return errors.New("NFT final reserve differs from native payment")
		}
		reserve = sdk.NewCoin(identity.BaseDenom, sdkmath.NewIntFromBigInt(unit))
	}
	var postSupply *big.Int
	if reserved && is1155 {
		supplyMethod := ""
		for name, method := range asset.Methods {
			if method.Sig == "totalSupply(uint256)" {
				supplyMethod = name
				break
			}
		}
		if supplyMethod == "" {
			return errors.New("missing pinned ERC1155 supply getter")
		}
		values, err := c.userNFTRead(ctx, target, asset, supplyMethod, id.Big())
		if err != nil {
			return err
		}
		postSupply = values[0].(*big.Int)
	}
	expectedLogs, err := userNFTMintLogs(asset, target, msg.From, recipient, family, id, quantity, uri, msg.Value, fresh, postSupply)
	if err != nil {
		return err
	}
	if err := requireUserNFTLogs(receipt.Logs, expectedLogs); err != nil {
		return err
	}
	return c.nft.AdmitUserMint(ctx, target, id, uri, sdk.AccAddress(recipient[:]).String(), quantity, reserve)
}
