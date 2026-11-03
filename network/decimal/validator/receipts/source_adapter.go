package receipts

import (
	"errors"

	sdk "github.com/cosmos/cosmos-sdk/types"
	nftview "github.com/maxwell2010/go-smart-node/network/decimal/nft/evmview"
	nftreceipts "github.com/maxwell2010/go-smart-node/network/decimal/nft/receipts"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
)

// NFTViewStakeSourceAdapter preserves the actual approved reader's original
// role/code/domain fingerprint. It never turns an observed code hash or native
// NFT record into its own approval. Monetary attestation remains a separate stage.
type NFTViewStakeSourceAdapter struct{ reader *nftview.Reader }

var _ StakeSourceAuthenticator = (*NFTViewStakeSourceAdapter)(nil)

func NewNFTViewStakeSourceAdapter(reader *nftview.Reader) (*NFTViewStakeSourceAdapter, error) {
	if reader == nil {
		return nil, errors.New("native stake source adapter requires the approved actual EVM reader")
	}
	return &NFTViewStakeSourceAdapter{reader: reader}, nil
}
func (a *NFTViewStakeSourceAdapter) AuthenticateStakeSource(ctx sdk.Context, execution nftreceipts.Execution) (StakeSourceProfile, error) {
	if a == nil || a.reader == nil {
		return StakeSourceProfile{}, errors.New("native stake source adapter is not initialized")
	}
	source, err := a.reader.AuthenticateStakeSource(ctx, execution)
	if err != nil {
		return StakeSourceProfile{}, err
	}
	return StakeSourceProfile{Domain: types.EVMSourceDomain{ChainID: source.ChainID, EVMChainID: source.EVMChainID}, Gateway: source.Gateway, Fingerprint: source.Fingerprint}, nil
}
