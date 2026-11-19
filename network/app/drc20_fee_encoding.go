package app

import (
	"github.com/cosmos/cosmos-sdk/client"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/maxwell2010/go-smart-node/network/fees/envelope"
)

// Keep the ordinary SDK builders/signing configuration. Only binary transport
// changes for the explicitly opted-in composite envelope.
type feeTxConfig struct{ client.TxConfig }

func (c feeTxConfig) TxDecoder() sdk.TxDecoder { return envelope.Decoder(c.TxConfig.TxDecoder()) }
func (c feeTxConfig) TxEncoder() sdk.TxEncoder { return envelope.Encoder(c.TxConfig.TxEncoder()) }
