// Derived from cosmos/ibc-go v10.7.0 testing/events.go. Apache-2.0; see LICENSE.
package ibctesting

import (
	"encoding/hex"
	"errors"
	"fmt"

	"strconv"

	abci "github.com/cometbft/cometbft/abci/types"

	clienttypes "github.com/cosmos/ibc-go/v10/modules/core/02-client/types"
	connectiontypes "github.com/cosmos/ibc-go/v10/modules/core/03-connection/types"
	channeltypes "github.com/cosmos/ibc-go/v10/modules/core/04-channel/types"
)

// ParseClientIDFromEvents parses events emitted from a MsgCreateClient and returns the
// client identifier.
func ParseClientIDFromEvents(events []abci.Event) (string, error) {
	for _, ev := range events {
		if ev.Type == clienttypes.EventTypeCreateClient {
			if attribute, found := attributeByKey(ev.Attributes, clienttypes.AttributeKeyClientID); found {
				return attribute.Value, nil
			}
		}
	}
	return "", errors.New("client identifier event attribute not found")
}

// ParseConnectionIDFromEvents parses events emitted from a MsgConnectionOpenInit or
// MsgConnectionOpenTry and returns the connection identifier.
func ParseConnectionIDFromEvents(events []abci.Event) (string, error) {
	for _, ev := range events {
		if ev.Type == connectiontypes.EventTypeConnectionOpenInit ||
			ev.Type == connectiontypes.EventTypeConnectionOpenTry {
			if attribute, found := attributeByKey(ev.Attributes, connectiontypes.AttributeKeyConnectionID); found {
				return attribute.Value, nil
			}
		}
	}
	return "", errors.New("connection identifier event attribute not found")
}

// ParseChannelIDFromEvents parses events emitted from a MsgChannelOpenInit or
// MsgChannelOpenTry or a MsgCreateChannel and returns the channel identifier.
func ParseChannelIDFromEvents(events []abci.Event) (string, error) {
	for _, ev := range events {
		if ev.Type == channeltypes.EventTypeChannelOpenInit || ev.Type == channeltypes.EventTypeChannelOpenTry {
			if attribute, found := attributeByKey(ev.Attributes, channeltypes.AttributeKeyChannelID); found {
				return attribute.Value, nil
			}
		}
	}
	return "", errors.New("channel identifier event attribute not found")
}

// ParseV1PacketFromEvents parses events emitted from a send packet and returns
// the first EventTypeSendPacket packet found.
// Returns an error if no packet is found.
func ParseV1PacketFromEvents(events []abci.Event) (channeltypes.Packet, error) {
	packets, err := ParseIBCV1Packets(channeltypes.EventTypeSendPacket, events)
	if err != nil {
		return channeltypes.Packet{}, err
	}
	return packets[0], nil
}

// ParseRecvV1PacketFromEvents parses events emitted from a MsgRecvPacket and returns
// the first EventTypeRecvPacket packet found.
// Returns an error if no packet is found.
func ParseRecvV1PacketFromEvents(events []abci.Event) (channeltypes.Packet, error) {
	packets, err := ParseIBCV1Packets(channeltypes.EventTypeRecvPacket, events)
	if err != nil {
		return channeltypes.Packet{}, err
	}
	return packets[0], nil
}

// ParseIBCV1Packets parses events and returns all the v1 packets found.
// Returns an error if no v1 packet is found.
func ParseIBCV1Packets(eventType string, events []abci.Event) ([]channeltypes.Packet, error) {
	ferr := func(err error) ([]channeltypes.Packet, error) {
		return nil, fmt.Errorf("ibctesting.ParseIBCV1Packets: %w", err)
	}
	var packets []channeltypes.Packet
	for _, ev := range events {
		if ev.Type != eventType {
			continue
		}

		var packet channeltypes.Packet
		for _, attr := range ev.Attributes {
			switch attr.Key {
			case channeltypes.AttributeKeyDataHex:
				data, err := hex.DecodeString(attr.Value)
				if err != nil {
					return ferr(err)
				}
				packet.Data = data
			case channeltypes.AttributeKeySequence:
				seq, err := strconv.ParseUint(attr.Value, 10, 64)
				if err != nil {
					return ferr(err)
				}

				packet.Sequence = seq

			case channeltypes.AttributeKeySrcPort:
				packet.SourcePort = attr.Value

			case channeltypes.AttributeKeySrcChannel:
				packet.SourceChannel = attr.Value

			case channeltypes.AttributeKeyDstPort:
				packet.DestinationPort = attr.Value

			case channeltypes.AttributeKeyDstChannel:
				packet.DestinationChannel = attr.Value

			case channeltypes.AttributeKeyTimeoutHeight:
				height, err := clienttypes.ParseHeight(attr.Value)
				if err != nil {
					return ferr(err)
				}

				packet.TimeoutHeight = height

			case channeltypes.AttributeKeyTimeoutTimestamp:
				timestamp, err := strconv.ParseUint(attr.Value, 10, 64)
				if err != nil {
					return ferr(err)
				}

				packet.TimeoutTimestamp = timestamp

			default:
				continue
			}
		}

		packets = append(packets, packet)
	}
	if len(packets) == 0 {
		return ferr(errors.New("acknowledgement event attribute not found"))
	}
	return packets, nil
}

// ParseIBCV2Packets parses events and returns all the v2 packets found.
// Returns an error if no v2 packet is found.
func ParseAckFromEvents(events []abci.Event) ([]byte, error) {
	for _, ev := range events {
		if ev.Type == channeltypes.EventTypeWriteAck {
			if attribute, found := attributeByKey(ev.Attributes, channeltypes.AttributeKeyAckHex); found {
				value, err := hex.DecodeString(attribute.Value)
				if err != nil {
					return nil, err
				}
				return value, nil
			}
		}
	}
	return nil, errors.New("acknowledgement event attribute not found")
}

// ParseProposalIDFromEvents parses events emitted from MsgSubmitProposal and returns proposalID
func ParsePacketFromEvents(events []abci.Event) (channeltypes.Packet, error) {
	return ParseV1PacketFromEvents(events)
}

// ParseRecvPacketFromEvents parses events emitted from a MsgRecvPacket and returns
// the first EventTypeRecvPacket packet found.
// Returns an error if no packet is found.
//
// Deprecated: This function will be removed in the next major release. Use
// ParseRecvV1PacketFromEvents instead
func ParseRecvPacketFromEvents(events []abci.Event) (channeltypes.Packet, error) {
	return ParseRecvV1PacketFromEvents(events)
}

// ParsePacketsFromEvents parses events emitted from a MsgRecvPacket and returns
// all the packets found.
// Returns an error if no packet is found.
//
// Deprecated: This function will be removed in the next major release. Use ParseIBCV1Packets instead.
func ParsePacketsFromEvents(eventType string, events []abci.Event) ([]channeltypes.Packet, error) {
	return ParseIBCV1Packets(eventType, events)
}

func attributeByKey(attrs []abci.EventAttribute, key string) (abci.EventAttribute, bool) {
	for _, a := range attrs {
		if a.Key == key {
			return a, true
		}
	}
	return abci.EventAttribute{}, false
}
