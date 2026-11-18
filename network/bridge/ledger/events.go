package ledger

import (
	"encoding/hex"
	"encoding/json"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/gogoproto/jsonpb"
	"github.com/cosmos/gogoproto/proto"
)

// EventVerifiedDeposit is an internal typed accounting event, not a message.
// It intentionally has no signer, service descriptor or transaction route.
type EventVerifiedDeposit struct {
	TransferID    string `protobuf:"bytes,1,opt,name=transfer_id,json=transferId,proto3" json:"transfer_id"`
	OriginChainID string `protobuf:"bytes,2,opt,name=origin_chain_id,json=originChainId,proto3" json:"origin_chain_id"`
	OriginDenom   string `protobuf:"bytes,3,opt,name=origin_denom,json=originDenom,proto3" json:"origin_denom"`
	VoucherDenom  string `protobuf:"bytes,4,opt,name=voucher_denom,json=voucherDenom,proto3" json:"voucher_denom"`
	RecipientHex  string `protobuf:"bytes,5,opt,name=recipient_hex,json=recipientHex,proto3" json:"recipient_hex"`
	Amount        string `protobuf:"bytes,6,opt,name=amount,proto3" json:"amount"`
}

func (event *EventVerifiedDeposit) Reset()         { *event = EventVerifiedDeposit{} }
func (event *EventVerifiedDeposit) String() string { return proto.CompactTextString(event) }
func (*EventVerifiedDeposit) ProtoMessage()        {}

// MarshalJSONPB supplies a stable typed-event representation without requiring
// a public proto message-service or routing registration.
func (event *EventVerifiedDeposit) MarshalJSONPB(_ *jsonpb.Marshaler) ([]byte, error) {
	type eventJSON EventVerifiedDeposit
	return json.Marshal((*eventJSON)(event))
}

func init() {
	proto.RegisterType((*EventVerifiedDeposit)(nil), "mintcandy.bridge.v1.EventVerifiedDeposit")
}

func newDepositEvent(id [32]byte, recipient sdk.AccAddress, amount sdkmath.Int) *EventVerifiedDeposit {
	return &EventVerifiedDeposit{TransferID: hex.EncodeToString(id[:]), OriginChainID: OriginChainID,
		OriginDenom: OriginDenom, VoucherDenom: VoucherDenom,
		RecipientHex: hex.EncodeToString(recipient), Amount: amount.String()}
}
