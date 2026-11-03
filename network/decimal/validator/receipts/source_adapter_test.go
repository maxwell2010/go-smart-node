package receipts

import (
	sdk "github.com/cosmos/cosmos-sdk/types"
	nftreceipts "github.com/maxwell2010/go-smart-node/network/decimal/nft/receipts"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestActualSourceAdapterRequiresItsReader(t *testing.T) {
	_, err := NewNFTViewStakeSourceAdapter(nil)
	require.Error(t, err)
	var absent *NFTViewStakeSourceAdapter
	_, err = absent.AuthenticateStakeSource(sdk.Context{}, nftreceipts.Execution{})
	require.Error(t, err)
}
