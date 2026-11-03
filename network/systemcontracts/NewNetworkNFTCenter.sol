// SPDX-License-Identifier: MIT
pragma solidity 0.8.20;

import {DecimalNFTCenter, BeaconNFT, NFTType, NFTState, DecimalTokenCenter, IDecimalContractCenter, IERC20Metadata} from "./owned/DecimalNFTCenter.sol";

import {NewNetworkNFTReadableProxy} from "./NewNetworkReadableProxies.sol";

/// @notice Fresh factory initialization, not historical deployment setup.
contract NewNetworkNFTCenter is DecimalNFTCenter {
    error InvalidFreshInitialization();
    constructor() { _disableInitializers(); }

    function initializeFresh(address registry) external onlyInitializing {
        NFTCenterStorage storage state = _getNFTCenterStorage();
        if (address(this).code.length != 0 || registry == address(0) ||
            registry.code.length == 0 || owner() == address(0) ||
            state._contractCenter != address(0)) revert InvalidFreshInitialization();
        state._contractCenter = registry;
        for (uint8 kind; kind < 2; ++kind) {
            for (uint8 reserved; reserved < 2; ++reserved) {
                bool withReserve = reserved == 1;
                if (state._beacon[NFTType(kind)][withReserve] != address(0)) {
                    revert InvalidFreshInitialization();
                }
                state._beacon[NFTType(kind)][withReserve] =
                    address(new BeaconNFT(address(this), NFTType(kind), withReserve));
            }
        }
    }
    // A matching symbol is not a binding to a particular deployed token.
    // Canonical historical code is retained; only this fresh factory overrides it.
    function checkToken(address token) public view override {
        address center = IDecimalContractCenter(getContractCenter()).getContractAddress("token-center");
        if (token == address(0) || DecimalTokenCenter(payable(center)).tokens(IERC20Metadata(token).symbol()) != token) {
            revert InvalidToken();
        }
    }

    function readImplementation(uint8 family) external view returns (address) {
        if (family < 2 || family > 5) revert InvalidFreshInitialization();
        NFTType kind = family == 3 || family == 5 ? NFTType.DRC1155 : NFTType.DRC721;
        return _getNFTCenterStorage()._implementation[kind][family < 4];
    }

    function _create(NFTType kind, NFT memory nft, bool withReserve) internal override returns (address nftAddress) {
        NFTCenterStorage storage state = _getNFTCenterStorage();
        uint8 family = withReserve ? (kind == NFTType.DRC721 ? 2 : 3) : (kind == NFTType.DRC721 ? 4 : 5);
        nftAddress = address(new NewNetworkNFTReadableProxy(state._beacon[kind][withReserve],
            _getInitArgs(nft), address(this), family));
        if (nftAddress == address(0)) revert AlreadyDeployed();
        state._nfts[nftAddress] = NFTState({active:true,nftType:kind,withReserve:withReserve});
        emit NFTCreated(nftAddress, kind, nft);
    }

}
