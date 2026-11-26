package app

import (
	"bytes"
	"errors"
	"math/big"
	"sync"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/maxwell2010/go-smart-node/network/identity"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
)

// Membership records are consensus state, not an authority to mint bank DEL,
// pay token fees, delegate collateral, or release bridge funds. Financial
// genesis export remains unsupported for the active withdrawal profile.
const reservelessMembershipPrefix byte = 0x70

var (
	reservelessFlagsSlot      = common.HexToHash("0xed216e73b55985dc9aa89ab575f538d915b9d4d1e349fd366c9650df2b66a100")
	reservelessCapSlot        = common.HexToHash("0x0f070392f17d5f958cc1ac31867dabecfc5c9758b4a419a200803226d7155d00")
	reservelessPauseSlot      = common.HexToHash("0xcd5ed15c6e187e77e9aee88184c21f4f2182ab5827cb3b7e07fbedcd63f03300")
	reservelessCreateSelector = crypto.Keccak256([]byte("createTokenReserveless(string,string,bool,bool,uint256,uint256,string)"))[:4]
	reservelessABIs           = sync.OnceValues(func() (abi.ABI, error) { return contracts.CompiledABI("DRC20Reserveless") })
)

func reservelessMembershipKey(token common.Address) []byte {
	return append([]byte{reservelessMembershipPrefix}, token[:]...)
}

// admitReservelessToken accepts only a signed direct factory creation or a
// small ERC20 operation on a token previously admitted by that creation path.
// Nested calls, permit, ownership changes and arbitrary contract calls remain
// outside this isolated native profile.
func (c *withdrawalController) admitReservelessToken(ctx sdk.Context, sender common.Address, msg core.Message, receipt *ethtypes.Receipt) (bool, error) {
	r := c.customCollateral
	if r == nil || msg.To == nil {
		return false, nil
	}
	create := *msg.To == c.profile.TokenCenter && len(msg.Data) >= 4 && bytes.Equal(msg.Data[:4], reservelessCreateSelector)
	member := bytes.Equal(ctx.KVStore(r.store).Get(reservelessMembershipKey(*msg.To)), []byte{1})
	if !create && !member {
		return false, nil
	}
	if sender == (common.Address{}) || sender != msg.From || msg.Value == nil || msg.Value.Sign() != 0 || len(msg.SetCodeAuthorizations) != 0 ||
		len(msg.Data) < 4 || len(msg.Data) > 2048 || receipt == nil || receipt.Status != ethtypes.ReceiptStatusSuccessful || len(receipt.Logs) > 5 {
		return true, errors.New("reserveless token requires a bounded direct zero-value authenticated call")
	}
	if err := r.authenticateRoles(ctx); err != nil {
		return true, err
	}
	if r.evm.GetState(ctx, r.profile.TokenCenter, reservelessPauseSlot) != (common.Hash{}) {
		return true, errors.New("reserveless TokenCenter is paused")
	}
	beacon, err := r.authenticateReservelessFamily(ctx)
	if err != nil {
		return true, err
	}
	if create {
		return true, c.admitReservelessCreation(ctx, msg, receipt, beacon)
	}
	if err := r.authenticateReservelessProxy(ctx, *msg.To, beacon); err != nil {
		return true, err
	}
	return true, c.admitReservelessOperation(ctx, msg, receipt)
}

func (r *customCollateralRegistry) authenticateReservelessFamily(ctx sdk.Context) (common.Address, error) {
	beaconWord := r.evm.GetState(ctx, r.profile.TokenCenter, customSlotOffset(customTokenCenterSlot, 2))
	beacon := common.BytesToAddress(beaconWord[12:])
	beaconCode, err := contracts.ReservelessTokenBeaconRuntime(r.profile.TokenCenter)
	if err != nil || beaconWord != customAddressWord(beacon) || !r.exactCode(ctx, beacon, beaconCode) {
		return common.Address{}, errors.New("reserveless beacon differs from the pinned TokenCenter beacon")
	}
	logicWord := r.evm.GetState(ctx, r.profile.TokenCenter, customSlotOffset(customTokenCenterSlot, 1))
	logic := common.BytesToAddress(logicWord[12:])
	logicCode, err := contracts.ExpectedRuntime("DRC20Reserveless", nil, nil)
	if err != nil || logicWord != customAddressWord(logic) || !r.exactCode(ctx, logic, logicCode) {
		return common.Address{}, errors.New("reserveless implementation differs from the pinned runtime")
	}
	return beacon, nil
}

func (r *customCollateralRegistry) authenticateReservelessProxy(ctx sdk.Context, token, beacon common.Address) error {
	code, err := contracts.PauseAwareProxyRuntime(beacon, r.profile.TokenCenter, 1)
	if err != nil || !r.exactCode(ctx, token, code) || r.evm.GetState(ctx, token, customBeaconSlot) != customAddressWord(beacon) ||
		r.evm.GetState(ctx, token, customImplementationSlot) != (common.Hash{}) {
		return errors.New("token is not the pinned family-1 reserveless proxy")
	}
	return nil
}

func canonicalReservelessArgs(method abi.Method, data []byte) ([]interface{}, error) {
	if len(data) < 4 || !bytes.Equal(method.ID, data[:4]) {
		return nil, errors.New("unsupported reserveless method")
	}
	args, err := method.Inputs.Unpack(data[4:])
	if err != nil {
		return nil, err
	}
	encoded, err := method.Inputs.Pack(args...)
	if err != nil || !bytes.Equal(encoded, data[4:]) {
		return nil, errors.New("noncanonical reserveless calldata")
	}
	return args, nil
}

func reservedPublicTokenSymbol(symbol string) bool {
	switch symbol {
	case "del", "msp", "wmsp":
		return true
	case "tmsp", "wtmsp":
		return identity.ProfileChainID != ""
	default:
		return false
	}
}

func (c *withdrawalController) admitReservelessCreation(ctx sdk.Context, msg core.Message, receipt *ethtypes.Receipt, beacon common.Address) error {
	r := c.customCollateral
	centerABI, err := contracts.CompiledABI("NewNetworkTokenCenter")
	if err != nil {
		return err
	}
	args, err := canonicalReservelessArgs(centerABI.Methods["createTokenReserveless"], r.issuanceCalldata(ctx, msg.Data))
	if err != nil {
		return err
	}
	name, symbol, tokenIdentity := args[0].(string), args[1].(string), args[6].(string)
	mintable, burnable := args[2].(bool), args[3].(bool)
	initial, cap := args[4].(*big.Int), args[5].(*big.Int)
	if !validPublicTokenName(name, maxReservelessNameBytes) || len(symbol) < 3 || len(symbol) > 10 || reservedPublicTokenSymbol(symbol) || !identity.ValidPublicText(tokenIdentity, 256) ||
		(cap.Sign() > 0 && (!mintable || initial.Cmp(cap) > 0)) || ctx.BlockTime().Unix() < 0 {
		return errors.New("invalid reserveless name, symbol, identity, flags or cap")
	}
	tokenABI, err := reservelessABIs()
	if err != nil {
		return err
	}
	initialization, err := tokenABI.Pack("initialize", name, symbol, msg.From, mintable, burnable, initial, cap, tokenIdentity)
	if err != nil {
		return err
	}
	proxy, err := contracts.Load("NewNetworkTokenReadableProxy")
	if err != nil {
		return err
	}
	constructor, err := proxy.ABI.Constructor.Inputs.Pack(beacon, initialization, r.profile.TokenCenter, uint8(1))
	if err != nil {
		return err
	}
	salt := crypto.Keccak256Hash(common.BigToHash(big.NewInt(ctx.BlockTime().Unix())).Bytes(), []byte(name))
	token := crypto.CreateAddress2(r.profile.TokenCenter, [32]byte(salt), crypto.Keccak256(append(proxy.Creation, constructor...)))
	key := reservelessMembershipKey(token)
	if ctx.KVStore(r.store).Has(key) {
		return errors.New("reserveless token already admitted")
	}
	if err := r.authenticateReservelessProxy(ctx, token, beacon); err != nil {
		return err
	}
	flags := common.Hash{}
	if mintable {
		flags[31] = 1
	}
	if burnable {
		flags[30] = 1
	}
	effectiveCap := new(big.Int).Set(cap)
	if cap.Sign() == 0 {
		effectiveCap.Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
	}
	if r.evm.GetState(ctx, token, customOwnableSlot) != customAddressWord(msg.From) ||
		r.evm.GetState(ctx, token, reservelessFlagsSlot) != flags ||
		r.evm.GetState(ctx, token, reservelessCapSlot).Big().Cmp(effectiveCap) != 0 ||
		r.evm.GetState(ctx, token, customSlotOffset(customERC20Slot, 2)).Big().Cmp(initial) != 0 ||
		reservelessBalance(r, ctx, token, msg.From).Cmp(initial) != 0 {
		return errors.New("reserveless created owner, flags, cap, supply or balance differs from the signed creation")
	}
	expected := []*ethtypes.Log{
		{Address: r.profile.TokenCenter, Topics: []common.Hash{crypto.Keccak256Hash([]byte("TokenReservelessDeployed(address)"))}, Data: customAddressWord(token).Bytes()},
		{Address: token, Topics: []common.Hash{crypto.Keccak256Hash([]byte("BeaconUpgraded(address)")), customAddressWord(beacon)}},
		{Address: token, Topics: []common.Hash{crypto.Keccak256Hash([]byte("OwnershipTransferred(address,address)")), {}, customAddressWord(msg.From)}},
	}
	if initial.Sign() > 0 {
		expected = append(expected, reservelessLog(token, "Transfer", common.Address{}, msg.From, initial))
	}
	expected = append(expected, &ethtypes.Log{Address: token, Topics: []common.Hash{crypto.Keccak256Hash([]byte("Initialized(uint64)"))}, Data: common.BigToHash(big.NewInt(1)).Bytes()})
	if !equalReservelessLogs(receipt.Logs, expected) {
		return errors.New("reserveless creation logs differ from the signed factory initialization")
	}
	if err := r.reservePublicTokenName(ctx, name); err != nil {
		return err
	}
	if err := r.settleIssuance(ctx, msg.From, token, symbol, msg.Data, new(big.Int)); err != nil {
		return err
	}
	ctx.KVStore(r.store).Set(key, []byte{1})
	return nil
}

func reservelessBalance(r *customCollateralRegistry, ctx sdk.Context, token, owner common.Address) *big.Int {
	return r.evm.GetState(ctx, token, crypto.Keccak256Hash(customAddressWord(owner).Bytes(), customERC20Slot[:])).Big()
}

func reservelessLog(token common.Address, event string, from, to common.Address, amount *big.Int) *ethtypes.Log {
	return &ethtypes.Log{Address: token, Topics: []common.Hash{crypto.Keccak256Hash([]byte(event + "(address,address,uint256)")), customAddressWord(from), customAddressWord(to)}, Data: common.BigToHash(amount).Bytes()}
}

func equalReservelessLogs(actual, expected []*ethtypes.Log) bool {
	if len(actual) != len(expected) {
		return false
	}
	for i, log := range actual {
		if log == nil || log.Address != expected[i].Address || len(log.Topics) != len(expected[i].Topics) || !bytes.Equal(log.Data, expected[i].Data) {
			return false
		}
		for j, topic := range log.Topics {
			if topic != expected[i].Topics[j] {
				return false
			}
		}
	}
	return true
}

func (c *withdrawalController) admitReservelessOperation(ctx sdk.Context, msg core.Message, receipt *ethtypes.Receipt) error {
	tokenABI, err := reservelessABIs()
	if err != nil {
		return err
	}
	method, err := tokenABI.MethodById(msg.Data[:4])
	if err != nil {
		return err
	}
	args, err := canonicalReservelessArgs(*method, msg.Data)
	if err != nil {
		return err
	}
	from, to, event := msg.From, common.Address{}, "Transfer"
	var amount *big.Int
	r, token := c.customCollateral, *msg.To
	flags := r.evm.GetState(ctx, token, reservelessFlagsSlot)
	switch method.Sig {
	case "mint(address,uint256)":
		if flags[31] != 1 || r.evm.GetState(ctx, token, customOwnableSlot) != customAddressWord(msg.From) {
			return errors.New("reserveless mint requires the current owner and mintable token")
		}
		from, to, amount = common.Address{}, args[0].(common.Address), args[1].(*big.Int)
	case "burn(uint256)":
		if flags[30] != 1 {
			return errors.New("reserveless token is not burnable")
		}
		amount = args[0].(*big.Int)
	case "burnFrom(address,uint256)":
		if flags[30] != 1 {
			return errors.New("reserveless token is not burnable")
		}
		from, amount = args[0].(common.Address), args[1].(*big.Int)
	case "transfer(address,uint256)", "approve(address,uint256)":
		to, amount = args[0].(common.Address), args[1].(*big.Int)
		if method.Name == "approve" {
			event = "Approval"
		}
	case "transferFrom(address,address,uint256)":
		from, to, amount = args[0].(common.Address), args[1].(common.Address), args[2].(*big.Int)
	default:
		return errors.New("reserveless method is outside the admitted ERC20 lifecycle")
	}
	if (method.Name != "burn" && method.Name != "burnFrom" && to == (common.Address{})) ||
		(method.Name != "mint" && from == (common.Address{})) {
		return errors.New("invalid reserveless operation address")
	}
	if !equalReservelessLogs(receipt.Logs, []*ethtypes.Log{reservelessLog(token, event, from, to, amount)}) {
		return errors.New("reserveless operation logs differ from signed calldata")
	}
	return nil
}
