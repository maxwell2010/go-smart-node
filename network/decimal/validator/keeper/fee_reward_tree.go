package keeper

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math/big"

	sdkmath "cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	cmttypes "github.com/cometbft/cometbft/types"
	"github.com/cosmos/cosmos-sdk/codec/address"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

const (
	feeTreeRootPrefix       byte  = 0x79
	feeTreeNodePrefix       byte  = 0x7a
	feeTreePayPrefix        byte  = 0x7b
	feeTreeCommissionPrefix byte  = 0x7c
	feeTreeLiabilityKey     byte  = 0x7d
	feeTreeSchedulerPrefix  byte  = 0x7e
	NativeFeeRewardPeriod   int64 = 120
	MaxNativeFeeRewardPage        = 64
)

var feeTreeMarkerKey = []byte{feeTreeSchedulerPrefix, 0}
var feeTreeSweepKey = []byte{feeTreeSchedulerPrefix, 1}

const feeTreeRetainedPrefix byte = 0x7f

func (k *StateKeeper) nativeFeeRewardRetained(ctx sdk.Context, op []byte) (uint32, error) {
	b, e := k.service.OpenKVStore(ctx).Get(feeTreeKey(feeTreeRetainedPrefix, op, nil))
	if e != nil || b == nil {
		return 0, e
	}
	if len(b) != 4 || binary.BigEndian.Uint32(b) == 0 {
		return 0, errors.New("invalid native fee reward retained census")
	}
	return binary.BigEndian.Uint32(b), nil
}
func (k *StateKeeper) changeNativeFeeRewardRetained(ctx sdk.Context, op []byte, delta int) error {
	count, e := k.nativeFeeRewardRetained(ctx, op)
	if e != nil {
		return e
	}
	next := int64(count) + int64(delta)
	if next < 0 {
		return errors.New("native fee reward retained census underflow")
	}
	if uint64(next) > uint64(^uint32(0)) {
		return errors.New("native fee reward retained census overflow")
	}
	p, e := k.GetParams(ctx)
	if e != nil {
		return e
	}
	if delta > 0 && uint64(next) > uint64(p.MaxDelegations)*2 {
		return errors.New("native fee reward retained recipient capacity exceeded")
	}
	state := k.service.OpenKVStore(ctx)
	key := feeTreeKey(feeTreeRetainedPrefix, op, nil)
	if next == 0 {
		return state.Delete(key)
	}
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], uint32(next))
	return state.Set(key, b[:])
}

func (k *StateKeeper) validateNativeFeeRewardParamCapacity(ctx sdk.Context, maxDelegations uint32) error {
	return k.scan(ctx, feeTreeRetainedPrefix, func(key, b []byte) error {
		if len(key) != 21 || len(b) != 4 || binary.BigEndian.Uint32(b) == 0 {
			return errors.New("invalid native fee reward retained census")
		}
		if uint64(binary.BigEndian.Uint32(b)) > uint64(maxDelegations)*2 {
			return errors.New("new delegation limit cannot retain existing unpaid reward recipients")
		}
		return nil
	})
}
func (k *StateKeeper) prepareNativeFeeRewardCommission(ctx sdk.Context, operator, reward string) error {
	indexed, e := k.hasNativeFeeRewardTree(ctx)
	if e != nil || !indexed {
		return e
	}
	key, e := operatorKey(feeTreeRootPrefix, operator)
	if e != nil {
		return e
	}
	op := key[1:]
	recipient, e := feeRewardAddress(reward)
	if e != nil {
		return e
	}
	state := k.service.OpenKVStore(ctx)
	// Retire an old empty reserved slot before reserving a replacement, so a
	// reward-address update at a full census needs no transient extra capacity.
	previous, e := k.GetValidator(ctx, operator)
	if e != nil && !errors.Is(e, ErrNotFound) {
		return e
	}
	var oldRecipient sdk.AccAddress
	if e == nil {
		oldRecipient, e = feeRewardAddress(previous.RewardAddress)
		if e != nil {
			return e
		}
	}
	if oldRecipient != nil && !bytes.Equal(oldRecipient, recipient) {
		oldKey := feeTreeKey(feeTreeCommissionPrefix, op, oldRecipient)
		b, e := state.Get(oldKey)
		if e != nil {
			return e
		}
		if b != nil && len(b) != 32 {
			return errors.New("invalid native fee reward old commission slot")
		}
		if len(b) == 32 && new(big.Int).SetBytes(b).Sign() == 0 {
			if e = state.Delete(oldKey); e != nil {
				return e
			}
			if e = state.Delete(append([]byte{feeTreePayPrefix, 0}, append(bytes.Clone(op), oldRecipient...)...)); e != nil {
				return e
			}
			if e = k.changeNativeFeeRewardRetained(ctx, op, -1); e != nil {
				return e
			}
		}
	}
	credit := feeTreeKey(feeTreeCommissionPrefix, op, recipient)
	b, e := state.Get(credit)
	if e != nil {
		return e
	}
	if b != nil {
		if len(b) != 32 {
			return errors.New("invalid native fee reward commission slot")
		}
		return nil
	}
	if e = k.changeNativeFeeRewardRetained(ctx, op, 1); e != nil {
		return e
	}
	if e = state.Set(credit, make([]byte, 32)); e != nil {
		return e
	}
	return state.Set(append([]byte{feeTreePayPrefix, 0}, append(bytes.Clone(op), recipient...)...), []byte{1})
}

// A compressed radix tree has at most two nodes per retained recipient.
// Pending integer atoms are pushed using OLD subtree weights before mutation.
// Rounding is hierarchical (two-child largest remainder), with the lower raw
// address prefix winning ties. It is not global per-block largest remainder.
type feeTreeNode struct {
	weight, pending *big.Int
	left, right     []byte
}

func feeTreeRef(owner []byte, depth int) []byte {
	ref := make([]byte, 21)
	ref[0] = byte(depth)
	copy(ref[1:], owner)
	for bit := depth; bit < 160; bit++ {
		ref[1+bit/8] &= ^byte(1 << uint(7-bit%8))
	}
	return ref
}
func feeTreeBit(owner []byte, depth int) byte { return (owner[depth/8] >> uint(7-depth%8)) & 1 }
func feeTreeCommon(a, b []byte) int {
	for i := 0; i < 160; i++ {
		if feeTreeBit(a, i) != feeTreeBit(b, i) {
			return i
		}
	}
	return 160
}
func feeTreeKey(prefix byte, operator, ref []byte) []byte {
	k := append([]byte{prefix}, operator...)
	return append(k, ref...)
}
func feeTreePayKey(operator, owner []byte) []byte {
	k := append([]byte{feeTreePayPrefix, 1}, operator...)
	return append(k, owner...)
}
func feeTreeRefValid(ref []byte) bool {
	return len(ref) == 21 && int(ref[0]) <= 160 && bytes.Equal(ref, feeTreeRef(ref[1:], int(ref[0])))
}
func (k *StateKeeper) hasNativeFeeRewardTree(ctx sdk.Context) (bool, error) {
	b, e := k.service.OpenKVStore(ctx).Get(feeTreeMarkerKey)
	if e != nil || b == nil {
		return false, e
	}
	if !bytes.Equal(b, []byte{1}) {
		return false, errors.New("invalid native fee reward tree marker")
	}
	return true, nil
}
func (k *StateKeeper) feeTreeRead(ctx sdk.Context, op, ref []byte) (feeTreeNode, error) {
	n := feeTreeNode{}
	if len(op) != 20 || !feeTreeRefValid(ref) {
		return n, errors.New("invalid native fee reward tree reference")
	}
	b, e := k.service.OpenKVStore(ctx).Get(feeTreeKey(feeTreeNodePrefix, op, ref))
	if e != nil {
		return n, e
	}
	if len(b) != 107 || b[0] != 1 {
		return n, errors.New("missing or invalid native fee reward tree node")
	}
	n.weight = new(big.Int).SetBytes(b[1:33])
	n.pending = new(big.Int).SetBytes(b[33:65])
	if ref[0] == 160 {
		if !bytes.Equal(b[65:], make([]byte, 42)) {
			return n, errors.New("invalid fee reward leaf children")
		}
		return n, nil
	}
	n.left = bytes.Clone(b[65:86])
	n.right = bytes.Clone(b[86:107])
	for side, child := range [][]byte{n.left, n.right} {
		if !feeTreeRefValid(child) || child[0] <= ref[0] || feeTreeCommon(child[1:], ref[1:]) < int(ref[0]) || feeTreeBit(child[1:], int(ref[0])) != byte(side) {
			return n, errors.New("invalid native fee reward child reference")
		}
	}
	return n, nil
}
func (k *StateKeeper) feeTreeWrite(ctx sdk.Context, op, ref []byte, n feeTreeNode) error {
	if n.weight == nil || n.pending == nil || n.weight.Sign() < 0 || n.pending.Sign() < 0 || n.weight.BitLen() > 256 || n.pending.BitLen() > 256 {
		return errors.New("native fee reward tree amount exceeds uint256")
	}
	b := make([]byte, 107)
	b[0] = 1
	n.weight.FillBytes(b[1:33])
	n.pending.FillBytes(b[33:65])
	copy(b[65:86], n.left)
	copy(b[86:107], n.right)
	return k.service.OpenKVStore(ctx).Set(feeTreeKey(feeTreeNodePrefix, op, ref), b)
}
func (k *StateKeeper) feeTreeRoot(ctx sdk.Context, op []byte) ([]byte, error) {
	b, e := k.service.OpenKVStore(ctx).Get(feeTreeKey(feeTreeRootPrefix, op, nil))
	if e != nil || b == nil {
		return b, e
	}
	if !feeTreeRefValid(b) {
		return nil, errors.New("invalid native fee reward root")
	}
	return b, nil
}
func (k *StateKeeper) feeTreePush(ctx sdk.Context, op, ref []byte, n *feeTreeNode) error {
	if ref[0] == 160 || n.pending.Sign() == 0 {
		return nil
	}
	l, e := k.feeTreeRead(ctx, op, n.left)
	if e != nil {
		return e
	}
	r, e := k.feeTreeRead(ctx, op, n.right)
	if e != nil {
		return e
	}
	total := new(big.Int).Add(l.weight, r.weight)
	if total.Cmp(n.weight) != 0 || total.Sign() == 0 {
		return errors.New("native fee reward tree weight mismatch")
	}
	shares := splitNativeFeeRewards(n.pending, map[string]*big.Int{"0": l.weight, "1": r.weight})
	for _, s := range shares {
		if s.recipient == "0" {
			l.pending.Add(l.pending, s.amount)
		} else {
			r.pending.Add(r.pending, s.amount)
		}
	}
	if e = k.feeTreeWrite(ctx, op, n.left, l); e != nil {
		return e
	}
	if e = k.feeTreeWrite(ctx, op, n.right, r); e != nil {
		return e
	}
	n.pending.SetInt64(0)
	return k.feeTreeWrite(ctx, op, ref, *n)
}
func (k *StateKeeper) feeTreeSetAt(ctx sdk.Context, op, ref, owner []byte, old, next *big.Int) ([]byte, error) {
	state := k.service.OpenKVStore(ctx)
	create := func() ([]byte, error) {
		if old.Sign() != 0 || next.Sign() == 0 {
			return nil, errors.New("native fee reward leaf prestate mismatch")
		}
		if e := k.changeNativeFeeRewardRetained(ctx, op, 1); e != nil {
			return nil, e
		}
		leaf := feeTreeRef(owner, 160)
		if e := k.feeTreeWrite(ctx, op, leaf, feeTreeNode{weight: new(big.Int).Set(next), pending: new(big.Int)}); e != nil {
			return nil, e
		}
		if e := state.Set(feeTreePayKey(op, owner), []byte{1}); e != nil {
			return nil, e
		}
		return leaf, nil
	}
	if ref == nil {
		return create()
	}
	n, e := k.feeTreeRead(ctx, op, ref)
	if e != nil {
		return nil, e
	}
	common := feeTreeCommon(ref[1:], owner)
	if common < int(ref[0]) {
		leaf, e := create()
		if e != nil {
			return nil, e
		}
		parent := feeTreeRef(owner, common)
		left, right := ref, leaf
		if feeTreeBit(owner, common) == 0 {
			left, right = leaf, ref
		}
		e = k.feeTreeWrite(ctx, op, parent, feeTreeNode{weight: new(big.Int).Add(n.weight, next), pending: new(big.Int), left: left, right: right})
		return parent, e
	}
	if ref[0] == 160 {
		if n.weight.Cmp(old) != 0 {
			return nil, errors.New("native fee reward leaf weight prestate mismatch")
		}
		n.weight.Set(next)
		if next.Sign() == 0 && n.pending.Sign() == 0 {
			if e = state.Delete(feeTreeKey(feeTreeNodePrefix, op, ref)); e != nil {
				return nil, e
			}
			if e = state.Delete(feeTreePayKey(op, owner)); e != nil {
				return nil, e
			}
			if e = k.changeNativeFeeRewardRetained(ctx, op, -1); e != nil {
				return nil, e
			}
			return nil, nil
		}
		return ref, k.feeTreeWrite(ctx, op, ref, n)
	}
	if e = k.feeTreePush(ctx, op, ref, &n); e != nil {
		return nil, e
	}
	side := feeTreeBit(owner, int(ref[0]))
	target := n.left
	if side == 1 {
		target = n.right
	}
	updated, e := k.feeTreeSetAt(ctx, op, target, owner, old, next)
	if e != nil {
		return nil, e
	}
	if updated == nil {
		remaining := n.right
		if side == 1 {
			remaining = n.left
		}
		if e = state.Delete(feeTreeKey(feeTreeNodePrefix, op, ref)); e != nil {
			return nil, e
		}
		return remaining, nil
	}
	if side == 0 {
		n.left = updated
	} else {
		n.right = updated
	}
	l, e := k.feeTreeRead(ctx, op, n.left)
	if e != nil {
		return nil, e
	}
	r, e := k.feeTreeRead(ctx, op, n.right)
	if e != nil {
		return nil, e
	}
	n.weight.Add(l.weight, r.weight)
	return ref, k.feeTreeWrite(ctx, op, ref, n)
}
func (k *StateKeeper) setNativeFeeRewardTreeWeight(ctx sdk.Context, op, owner []byte, old, next *big.Int) error {
	indexed, e := k.hasNativeFeeRewardTree(ctx)
	if e != nil || !indexed {
		return e
	}
	root, e := k.feeTreeRoot(ctx, op)
	if e != nil {
		return e
	}
	if root == nil && old.Sign() == 0 && next.Sign() == 0 {
		return nil
	}
	root, e = k.feeTreeSetAt(ctx, op, root, owner, old, next)
	if e != nil {
		return e
	}
	key := feeTreeKey(feeTreeRootPrefix, op, nil)
	if root == nil {
		return k.service.OpenKVStore(ctx).Delete(key)
	}
	return k.service.OpenKVStore(ctx).Set(key, root)
}
func (k *StateKeeper) feeTreeSettle(ctx sdk.Context, op, owner []byte) (feeTreeNode, []byte, error) {
	root, e := k.feeTreeRoot(ctx, op)
	if e != nil {
		return feeTreeNode{}, nil, e
	}
	for root != nil {
		n, e := k.feeTreeRead(ctx, op, root)
		if e != nil {
			return n, nil, e
		}
		if feeTreeCommon(root[1:], owner) < int(root[0]) {
			return n, nil, errors.New("native fee reward payout leaf absent")
		}
		if root[0] == 160 {
			return n, root, nil
		}
		if e = k.feeTreePush(ctx, op, root, &n); e != nil {
			return n, nil, e
		}
		if feeTreeBit(owner, int(root[0])) == 0 {
			root = n.left
		} else {
			root = n.right
		}
	}
	return feeTreeNode{}, nil, errors.New("native fee reward payout root absent")
}

// feeTreeSettleForPayment reads one bounded path, then pushes at most eight
// pending nodes, deepest first. Older deep credits progress even if new fees
// arrive at the root every block. Only the amount already reaching the leaf
// is paid; all other integer atoms remain in ancestor escrow liabilities.
func (k *StateKeeper) feeTreeSettleForPayment(ctx sdk.Context, op, owner []byte) (feeTreeNode, []byte, error) {
	ref, e := k.feeTreeRoot(ctx, op)
	if e != nil {
		return feeTreeNode{}, nil, e
	}
	var refs [][]byte
	var nodes []feeTreeNode
	for ref != nil {
		n, e := k.feeTreeRead(ctx, op, ref)
		if e != nil {
			return n, nil, e
		}
		if feeTreeCommon(ref[1:], owner) < int(ref[0]) {
			return n, nil, errors.New("native fee reward payout leaf absent")
		}
		refs = append(refs, ref)
		nodes = append(nodes, n)
		if ref[0] == 160 {
			break
		}
		if feeTreeBit(owner, int(ref[0])) == 0 {
			ref = n.left
		} else {
			ref = n.right
		}
	}
	if len(refs) == 0 || refs[len(refs)-1][0] != 160 {
		return feeTreeNode{}, nil, errors.New("native fee reward payout root absent")
	}
	for work := 0; work < 8; work++ {
		index := -1
		for i := len(nodes) - 2; i >= 0; i-- {
			if nodes[i].pending.Sign() > 0 {
				index = i
				break
			}
		}
		if index < 0 {
			break
		}
		if e = k.feeTreePush(ctx, op, refs[index], &nodes[index]); e != nil {
			return feeTreeNode{}, nil, e
		}
		nodes[index+1], e = k.feeTreeRead(ctx, op, refs[index+1])
		if e != nil {
			return feeTreeNode{}, nil, e
		}
	}
	return nodes[len(nodes)-1], refs[len(refs)-1], nil
}

func (k *StateKeeper) initNativeFeeRewardTree(ctx sdk.Context) error {
	state := k.service.OpenKVStore(ctx)
	if e := state.Set(feeTreeMarkerKey, []byte{1}); e != nil {
		return e
	}
	if e := k.scan(ctx, feeRewardOwnerPrefix, func(key, b []byte) error {
		if len(key) != 41 || len(b) != 32 {
			return errors.New("invalid native reward owner migration row")
		}
		return k.setNativeFeeRewardTreeWeight(ctx, key[1:21], key[21:], new(big.Int), new(big.Int).SetBytes(b))
	}); e != nil {
		return e
	}
	validators, e := k.GetValidators(ctx)
	if e != nil {
		return e
	}
	for _, v := range validators {
		if e = k.prepareNativeFeeRewardCommission(ctx, v.OperatorAddress, v.RewardAddress); e != nil {
			return e
		}
	}
	return state.Set([]byte{feeTreeLiabilityKey}, make([]byte, 32))
}

// BuildNativeFeeRewardTree initializes an older owner-index store atomically.
func (k *StateKeeper) BuildNativeFeeRewardTree(ctx sdk.Context) error {
	child, commit := ctx.CacheContext()
	ok, e := k.hasNativeFeeRewardTree(child)
	if e != nil {
		return e
	}
	if ok {
		return errors.New("native fee reward tree already initialized")
	}
	if _, e = k.ExportGenesis(child); e != nil {
		return e
	}
	for _, prefix := range []byte{feeTreeRootPrefix, feeTreeNodePrefix, feeTreePayPrefix, feeTreeCommissionPrefix, feeTreeLiabilityKey, feeTreeSchedulerPrefix, feeTreeRetainedPrefix} {
		if e = k.scan(child, prefix, func(_, _ []byte) error { return errors.New("orphan native fee reward tree migration state") }); e != nil {
			return e
		}
	}
	if e = k.initNativeFeeRewardTree(child); e != nil {
		return e
	}
	commit()
	return nil
}

// MigrateNativeFeeRewardState is called only by an authenticated upgrade
// handler, never by ordinary startup or transaction admission. Repeating a
// completed upgrade audits existing data instead of resetting earned credits.
func (k *StateKeeper) MigrateNativeFeeRewardState(ctx sdk.Context) error {
	child, commit := ctx.CacheContext()
	indexed, err := k.hasNativeFeeRewardIndex(child)
	if err != nil {
		return err
	}
	if !indexed {
		if err = k.BuildNativeFeeRewardIndex(child); err != nil {
			return err
		}
	} else {
		tree, err := k.hasNativeFeeRewardTree(child)
		if err != nil {
			return err
		}
		if !tree {
			if err = k.BuildNativeFeeRewardTree(child); err != nil {
				return err
			}
		}
	}
	if _, err = k.ExportGenesis(child); err != nil {
		return err
	}
	commit()
	return nil
}
func (k *StateKeeper) nativeFeeRewardLiability(ctx sdk.Context) (*big.Int, error) {
	b, e := k.service.OpenKVStore(ctx).Get([]byte{feeTreeLiabilityKey})
	if e != nil {
		return nil, e
	}
	if len(b) != 32 {
		return nil, errors.New("invalid native fee reward escrow liability")
	}
	return new(big.Int).SetBytes(b), nil
}
func (k *StateKeeper) AggregateNativeFeeRewardLiability(ctx sdk.Context) (sdkmath.Int, error) {
	v, e := k.nativeFeeRewardLiability(ctx)
	if e != nil {
		return sdkmath.Int{}, e
	}
	return sdkmath.NewIntFromBigInt(v), nil
}
func (k *StateKeeper) writeNativeFeeRewardLiability(ctx sdk.Context, v *big.Int) error {
	if v.Sign() < 0 || v.BitLen() > 256 {
		return errors.New("native fee reward liability exceeds uint256")
	}
	return k.service.OpenKVStore(ctx).Set([]byte{feeTreeLiabilityKey}, v.FillBytes(make([]byte, 32)))
}
func (k *StateKeeper) creditNativeFeeRewardCommission(ctx sdk.Context, op []byte, recipient sdk.AccAddress, amount *big.Int) error {
	if amount.Sign() == 0 {
		return nil
	}
	state := k.service.OpenKVStore(ctx)
	key := feeTreeKey(feeTreeCommissionPrefix, op, recipient)
	b, e := state.Get(key)
	if e != nil {
		return e
	}
	if len(b) != 32 {
		return errors.New("native fee reward current commission slot missing")
	}
	total := new(big.Int).Add(new(big.Int).SetBytes(b), amount)
	if total.BitLen() > 256 {
		return errors.New("native fee reward commission exceeds uint256")
	}
	return state.Set(key, total.FillBytes(make([]byte, 32)))
}

// AccrueNativeFeeRewards records only existing DEL as escrow liabilities. The
// caller must move EXACTLY fees to its reward escrow within the same cache.
func (k *StateKeeper) AccrueNativeFeeRewards(ctx sdk.Context, fees sdkmath.Int, votes []NativeFeeRewardVote) error {
	if fees.IsNil() || fees.IsNegative() {
		return errors.New("invalid native fee reward accrual")
	}
	if fees.IsZero() {
		return nil
	}
	child, commit := ctx.CacheContext()
	ok, e := k.hasNativeFeeRewardTree(child)
	if e != nil {
		return e
	}
	if !ok {
		return errors.New("native fee reward tree requires explicit migration")
	}
	p, e := k.GetParams(child)
	if e != nil {
		return e
	}
	if len(votes) > int(p.MaxValidators) {
		return errors.New("native fee reward voter limit exceeded")
	}
	type allocation struct {
		root, op   []byte
		node       feeTreeNode
		commission sdkmath.LegacyDec
		recipient  sdk.AccAddress
	}
	allocations := map[string]allocation{}
	weights := map[string]*big.Int{}
	seen := map[string]bool{}
	var totalPower int64
	for _, v := range votes {
		if v.Power <= 0 || v.Power > cmttypes.MaxTotalVotingPower-totalPower {
			return errors.New("invalid native fee reward committing power")
		}
		totalPower += v.Power
		key, e := operatorKey(feeTreeRootPrefix, v.Operator)
		if e != nil {
			return e
		}
		op := key[1:]
		if seen[string(op)] {
			return errors.New("duplicate native fee reward committing operator")
		}
		seen[string(op)] = true
		validator, e := k.GetValidator(child, v.Operator)
		if e != nil {
			return e
		}
		active, e := k.readActiveDELReserve(child, v.Operator)
		if e != nil {
			return e
		}
		root, e := k.feeTreeRoot(child, op)
		if e != nil {
			return e
		}
		if root == nil {
			if active.Sign() != 0 {
				return errors.New("missing active native fee reward tree")
			}
			continue
		}
		n, e := k.feeTreeRead(child, op, root)
		if e != nil {
			return e
		}
		if n.weight.Cmp(active) != 0 {
			return errors.New("native fee reward root differs from active DEL")
		}
		if active.Sign() == 0 {
			continue
		}
		recipient, e := feeRewardAddress(validator.RewardAddress)
		if e != nil {
			return e
		}
		allocations[string(op)] = allocation{root, bytes.Clone(op), n, validator.Commission, recipient}
		weights[string(op)] = big.NewInt(v.Power)
	}
	if len(weights) == 0 {
		return ErrNoNativeFeeRewardRecipients
	}
	for _, share := range splitNativeFeeRewards(fees.BigInt(), weights) {
		a := allocations[share.recipient]
		commission := new(big.Int).Quo(new(big.Int).Mul(share.amount, a.commission.BigInt()), sdkmath.LegacyOneDec().BigInt())
		if e = k.creditNativeFeeRewardCommission(child, a.op, a.recipient, commission); e != nil {
			return e
		}
		a.node.pending.Add(a.node.pending, new(big.Int).Sub(share.amount, commission))
		if e = k.feeTreeWrite(child, a.op, a.root, a.node); e != nil {
			return e
		}
	}
	liability, e := k.nativeFeeRewardLiability(child)
	if e != nil {
		return e
	}
	liability.Add(liability, fees.BigInt())
	if e = k.writeNativeFeeRewardLiability(child, liability); e != nil {
		return e
	}
	commit()
	return nil
}

var ErrNoNativeFeeRewardRecipients = errors.New("no active native fee reward recipients")

type NativeFeeRewardPage struct {
	Visited, Paid, Failed int
	Amount                sdkmath.Int
	Active, OutOfGas      bool
}
type feeRewardSweep struct {
	height      int64
	cursor, end []byte
}

func (k *StateKeeper) readNativeFeeRewardSweep(ctx sdk.Context) (feeRewardSweep, error) {
	b, e := k.service.OpenKVStore(ctx).Get(feeTreeSweepKey)
	s := feeRewardSweep{}
	if e != nil || b == nil {
		return s, e
	}
	if len(b) < 12 {
		return s, errors.New("invalid native fee reward sweep")
	}
	s.height = int64(binary.BigEndian.Uint64(b[:8]))
	size := int(binary.BigEndian.Uint16(b[8:10]))
	if s.height < 0 || len(b) < 12+size {
		return s, errors.New("invalid native fee reward sweep cursor")
	}
	s.cursor = bytes.Clone(b[10 : 10+size])
	endSize := int(binary.BigEndian.Uint16(b[10+size : 12+size]))
	if len(b) != 12+size+endSize || endSize > 42 || size > 42 {
		return s, errors.New("invalid native fee reward sweep boundary")
	}
	s.end = bytes.Clone(b[12+size:])
	return s, nil
}
func (k *StateKeeper) writeNativeFeeRewardSweep(ctx sdk.Context, s feeRewardSweep) error {
	b := make([]byte, 12+len(s.cursor)+len(s.end))
	binary.BigEndian.PutUint64(b[:8], uint64(s.height))
	binary.BigEndian.PutUint16(b[8:10], uint16(len(s.cursor)))
	copy(b[10:], s.cursor)
	at := 10 + len(s.cursor)
	binary.BigEndian.PutUint16(b[at:at+2], uint16(len(s.end)))
	copy(b[at+2:], s.end)
	return k.service.OpenKVStore(ctx).Set(feeTreeSweepKey, b)
}

// NativeFeeRewardPageBudget is a deterministic gas headroom gate. It does not
// estimate wall time; the caller controls a bounded SDK gas meter for this page.
const NativeFeeRewardPageBudget uint64 = 2_000_000

// ProcessNativeFeeRewards automatically visits a finite sweep opened every
// 120 blocks. Payments use independent caches; a failed recipient retains its
// liability and the cursor progresses so it cannot starve other recipients.
// New keys after the captured upper bound wait for a later sweep.
func (k *StateKeeper) ProcessNativeFeeRewards(ctx sdk.Context, height int64, maxRecipients int, pay func(sdk.Context, sdk.AccAddress, sdkmath.Int) error) (NativeFeeRewardPage, error) {
	stats := NativeFeeRewardPage{Amount: sdkmath.ZeroInt()}
	if height < 0 || maxRecipients < 1 || maxRecipients > MaxNativeFeeRewardPage || pay == nil {
		return stats, errors.New("invalid native fee reward page parameters")
	}
	ok, e := k.hasNativeFeeRewardTree(ctx)
	if e != nil {
		return stats, e
	}
	if !ok {
		return stats, errors.New("native fee reward tree requires explicit migration")
	}
	s, e := k.readNativeFeeRewardSweep(ctx)
	if e != nil {
		return stats, e
	}
	if len(s.end) == 0 {
		if height < s.height || height-s.height < NativeFeeRewardPeriod {
			return stats, nil
		}
		s.height = height
		s.cursor = nil
		it, e := k.service.OpenKVStore(ctx).ReverseIterator([]byte{feeTreePayPrefix}, []byte{feeTreePayPrefix + 1})
		if e != nil {
			return stats, e
		}
		if it.Valid() {
			if e = it.Error(); e == nil {
				s.end = bytes.Clone(it.Key())
			}
		}
		closeErr := it.Close()
		if e != nil {
			return stats, e
		}
		if closeErr != nil {
			return stats, closeErr
		}
		if len(s.end) == 0 {
			return stats, k.writeNativeFeeRewardSweep(ctx, s)
		}
	}
	start := []byte{feeTreePayPrefix}
	if len(s.cursor) > 0 {
		start = append(bytes.Clone(s.cursor), 0)
	}
	end := append(bytes.Clone(s.end), 0)
	it, e := k.service.OpenKVStore(ctx).Iterator(start, end)
	if e != nil {
		return stats, e
	}
	var keys [][]byte
	for ; it.Valid() && len(keys) < maxRecipients; it.Next() {
		if e = it.Error(); e != nil {
			break
		}
		if !bytes.Equal(it.Value(), []byte{1}) {
			e = errors.New("invalid native fee reward payout index")
			break
		}
		keys = append(keys, bytes.Clone(it.Key()))
	}
	closeErr := it.Close()
	if e != nil {
		return stats, e
	}
	if closeErr != nil {
		return stats, closeErr
	}
	for _, key := range keys {
		meter := ctx.GasMeter()
		if meter.Limit() > 0 && (meter.GasConsumedToLimit() >= meter.Limit() || meter.Limit()-meter.GasConsumedToLimit() < NativeFeeRewardPageBudget+50_000) {
			stats.OutOfGas = true
			break
		}
		paid, amount, outOfGas, e := k.payNativeFeeRewardOne(ctx, key, pay)
		if outOfGas {
			stats.OutOfGas = true
			stats.Visited++
			stats.Failed++
			s.cursor = key
			continue
		}
		if e != nil {
			return stats, e
		}
		stats.Visited++
		if paid {
			stats.Paid++
			stats.Amount = stats.Amount.Add(amount)
		} else if amount.IsPositive() {
			stats.Failed++
		}
		s.cursor = key
	}
	// A partial page due to gas retains its cursor; ordinary exhaustion closes
	// this finite sweep, without chasing recipients inserted during the sweep.
	if !stats.OutOfGas && (len(keys) < maxRecipients || bytes.Equal(s.cursor, s.end)) {
		s.cursor = nil
		s.end = nil
	}
	stats.Active = len(s.end) > 0
	return stats, k.writeNativeFeeRewardSweep(ctx, s)
}

func (k *StateKeeper) payNativeFeeRewardOne(ctx sdk.Context, key []byte, pay func(sdk.Context, sdk.AccAddress, sdkmath.Int) error) (paid bool, amount sdkmath.Int, outOfGas bool, err error) {
	amount = sdkmath.ZeroInt()
	parent := ctx
	meter := storetypes.NewGasMeter(NativeFeeRewardPageBudget)
	var commit func()
	shouldCommit := false
	defer func() {
		if recovered := recover(); recovered != nil {
			switch recovered.(type) {
			case storetypes.ErrorOutOfGas, storetypes.ErrorGasOverflow:
				outOfGas = true
				err = nil
				paid = false
			default:
				panic(recovered)
			}
		}
		parent.GasMeter().ConsumeGas(meter.GasConsumedToLimit(), "bounded native fee reward recipient")
		if shouldCommit && err == nil && !outOfGas {
			commit()
		}
	}()
	child, write := ctx.WithGasMeter(meter).CacheContext()
	commit = write
	state := k.service.OpenKVStore(child)
	var recipient sdk.AccAddress
	var pending *big.Int
	var node feeTreeNode
	var ref, op, owner, creditKey []byte
	switch {
	case len(key) == 42 && key[0] == feeTreePayPrefix && key[1] == 0:
		op = key[2:22]
		recipient = sdk.AccAddress(key[22:])
		creditKey = feeTreeKey(feeTreeCommissionPrefix, op, recipient)
		b, e := state.Get(creditKey)
		if e != nil {
			return false, amount, false, e
		}
		if len(b) != 32 {
			return false, amount, false, errors.New("invalid native fee reward commission payout")
		}
		pending = new(big.Int).SetBytes(b)
	case len(key) == 42 && key[0] == feeTreePayPrefix && key[1] == 1:
		op, owner = key[2:22], key[22:]
		recipient = sdk.AccAddress(owner)
		node, ref, err = k.feeTreeSettleForPayment(child, op, owner)
		if err != nil {
			return false, amount, false, err
		}
		if ref == nil {
			shouldCommit = true
			return false, amount, false, nil
		}
		pending = node.pending
	default:
		return false, amount, false, errors.New("invalid native fee reward payout key")
	}
	amount = sdkmath.NewIntFromBigInt(pending)
	if pending.Sign() > 0 {
		payment, paidWrite := child.CacheContext()
		if e := pay(payment, recipient, amount); e != nil {
			shouldCommit = true
			return false, amount, false, nil
		}
		paidWrite()
		liability, e := k.nativeFeeRewardLiability(child)
		if e != nil {
			return false, amount, false, e
		}
		if liability.Cmp(pending) < 0 {
			return false, amount, false, errors.New("native fee reward payout exceeds liability")
		}
		liability.Sub(liability, pending)
		if e = k.writeNativeFeeRewardLiability(child, liability); e != nil {
			return false, amount, false, e
		}
		paid = true
	}
	if creditKey != nil {
		operator, e := address.NewBech32Codec(identity.Bech32PrefixValAddr).BytesToString(op)
		if e != nil {
			return false, amount, false, e
		}
		v, e := k.GetValidator(child, operator)
		if e != nil {
			return false, amount, false, e
		}
		current, e := feeRewardAddress(v.RewardAddress)
		if e != nil {
			return false, amount, false, e
		}
		if bytes.Equal(current, recipient) {
			err = state.Set(creditKey, make([]byte, 32))
		} else {
			err = state.Delete(creditKey)
			if err == nil {
				err = state.Delete(key)
			}
			if err == nil {
				err = k.changeNativeFeeRewardRetained(child, op, -1)
			}
		}
		if err != nil {
			return false, amount, false, err
		}
	} else {
		node.pending = new(big.Int)
		if err = k.feeTreeWrite(child, op, ref, node); err != nil {
			return false, amount, false, err
		}
		if node.weight.Sign() == 0 {
			if err = k.setNativeFeeRewardTreeWeight(child, op, owner, new(big.Int), new(big.Int)); err != nil {
				return false, amount, false, err
			}
		}
	}
	shouldCommit = true
	return paid, amount, false, nil
}

// auditNativeFeeRewardTree is a cold reconciliation, never a transaction or
// BeginBlock path. It detects orphan nodes, incorrect weights and lost atoms.
func (k *StateKeeper) auditNativeFeeRewardTree(ctx sdk.Context) error {
	indexed, err := k.hasNativeFeeRewardTree(ctx)
	if err != nil {
		return err
	}
	if !indexed {
		for _, prefix := range []byte{feeTreeRootPrefix, feeTreeNodePrefix, feeTreePayPrefix, feeTreeCommissionPrefix, feeTreeLiabilityKey, feeTreeSchedulerPrefix, feeTreeRetainedPrefix} {
			if err = k.scan(ctx, prefix, func(_, _ []byte) error { return errors.New("native fee reward tree rows exist without marker") }); err != nil {
				return err
			}
		}
		return nil
	}
	seen := map[string]bool{}
	leaves := map[string]bool{}
	retained := map[string]uint32{}
	sum := new(big.Int)
	var walk func([]byte, []byte) (*big.Int, error)
	walk = func(op, ref []byte) (*big.Int, error) {
		key := feeTreeKey(feeTreeNodePrefix, op, ref)
		if seen[string(key)] {
			return nil, errors.New("native fee reward tree duplicate child")
		}
		seen[string(key)] = true
		n, err := k.feeTreeRead(ctx, op, ref)
		if err != nil {
			return nil, err
		}
		sum.Add(sum, n.pending)
		if ref[0] == 160 {
			ownerKey := append(append([]byte{feeRewardOwnerPrefix}, op...), ref[1:]...)
			b, err := k.service.OpenKVStore(ctx).Get(ownerKey)
			if err != nil {
				return nil, err
			}
			if b != nil && len(b) != 32 {
				return nil, errors.New("native fee reward owner weight malformed")
			}
			if new(big.Int).SetBytes(b).Cmp(n.weight) != 0 {
				return nil, errors.New("native fee reward tree differs from owner index")
			}
			if n.weight.Sign() == 0 && n.pending.Sign() == 0 {
				return nil, errors.New("empty native fee reward tree leaf retained")
			}
			leaves[string(feeTreePayKey(op, ref[1:]))] = true
			retained[string(op)]++
			return n.weight, nil
		}
		left, err := walk(op, n.left)
		if err != nil {
			return nil, err
		}
		right, err := walk(op, n.right)
		if err != nil {
			return nil, err
		}
		if new(big.Int).Add(left, right).Cmp(n.weight) != 0 {
			return nil, errors.New("native fee reward subtree weight mismatch")
		}
		return n.weight, nil
	}
	if err = k.scan(ctx, feeTreeRootPrefix, func(key, ref []byte) error {
		if len(key) != 21 {
			return errors.New("invalid native fee reward root key")
		}
		_, err := walk(key[1:], ref)
		return err
	}); err != nil {
		return err
	}
	if err = k.scan(ctx, feeTreeNodePrefix, func(key, _ []byte) error {
		if !seen[string(key)] {
			return errors.New("orphan native fee reward tree node")
		}
		return nil
	}); err != nil {
		return err
	}
	if err = k.scan(ctx, feeRewardOwnerPrefix, func(key, _ []byte) error {
		if len(key) != 41 || !seen[string(feeTreeKey(feeTreeNodePrefix, key[1:21], feeTreeRef(key[21:], 160)))] {
			return errors.New("native fee reward owner absent from tree")
		}
		return nil
	}); err != nil {
		return err
	}
	if err = k.scan(ctx, feeTreeCommissionPrefix, func(key, b []byte) error {
		if len(key) != 41 || len(b) != 32 {
			return errors.New("invalid native fee reward commission liability")
		}
		sum.Add(sum, new(big.Int).SetBytes(b))
		retained[string(key[1:21])]++
		if new(big.Int).SetBytes(b).Sign() == 0 {
			operator, e := address.NewBech32Codec(identity.Bech32PrefixValAddr).BytesToString(key[1:21])
			if e != nil {
				return e
			}
			v, e := k.GetValidator(ctx, operator)
			if e != nil {
				return e
			}
			current, e := feeRewardAddress(v.RewardAddress)
			if e != nil {
				return e
			}
			if !bytes.Equal(current, key[21:]) {
				return errors.New("empty historical native fee reward commission slot")
			}
		}
		leaves[string(append([]byte{feeTreePayPrefix, 0}, key[1:]...))] = true
		return nil
	}); err != nil {
		return err
	}
	if err = k.scan(ctx, feeTreePayPrefix, func(key, b []byte) error {
		if !leaves[string(key)] || !bytes.Equal(b, []byte{1}) {
			return errors.New("orphan native fee reward payout index")
		}
		delete(leaves, string(key))
		return nil
	}); err != nil {
		return err
	}
	if len(leaves) > 0 {
		return errors.New("missing native fee reward payout index")
	}
	params, e := k.GetParams(ctx)
	if e != nil {
		return e
	}
	if err = k.scan(ctx, feeTreeRetainedPrefix, func(key, b []byte) error {
		if len(key) != 21 || len(b) != 4 || binary.BigEndian.Uint32(b) == 0 || binary.BigEndian.Uint32(b) != retained[string(key[1:])] || uint64(binary.BigEndian.Uint32(b)) > uint64(params.MaxDelegations)*2 {
			return errors.New("native fee reward retained census mismatch")
		}
		delete(retained, string(key[1:]))
		return nil
	}); err != nil {
		return err
	}
	if len(retained) > 0 {
		return errors.New("missing native fee reward retained census")
	}
	liability, err := k.nativeFeeRewardLiability(ctx)
	if err != nil {
		return err
	}
	if liability.Cmp(sum) != 0 {
		return errors.New("native fee reward liabilities do not conserve DEL")
	}
	return nil
}
