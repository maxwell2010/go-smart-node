package types

import (
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	cryptocodec "github.com/cosmos/cosmos-sdk/crypto/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/msgservice"
)

// ModuleCdc is for standalone schema serialization. No message routes are installed.
var ModuleCdc = codec.NewProtoCodec(codectypes.NewInterfaceRegistry())

// NewCodec registers types after generated protobuf init functions have run.
// Avoid constructing a populated registry in a package variable initializer.
func NewCodec() *codec.ProtoCodec {
	r := codectypes.NewInterfaceRegistry()
	RegisterInterfaces(r)
	return codec.NewProtoCodec(r)
}

func RegisterInterfaces(r codectypes.InterfaceRegistry) {
	cryptocodec.RegisterInterfaces(r)
	r.RegisterImplementations((*sdk.Msg)(nil),
		&MsgCreateValidator{},
		&MsgEditValidator{},
		&MsgSetOnline{},
		&MsgSetOffline{},
		&MsgDelegate{},
		&MsgDelegateNFT{},
		&MsgRedelegate{},
		&MsgRedelegateNFT{},
		&MsgUndelegate{},
		&MsgUndelegateNFT{},
		&MsgCancelRedelegation{},
		&MsgCancelRedelegationNFT{},
		&MsgCancelUndelegation{},
		&MsgCancelUndelegationNFT{},
		&MsgHaltChain{},
		&MsgResumeChain{},
		&MsgFreezeChain{},
		&MsgUnfreezeChain{},
	)
	msgservice.RegisterMsgServiceDesc(r, &_Msg_serviceDesc)
}
