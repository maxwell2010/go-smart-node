// SPDX-License-Identifier: MIT
pragma solidity 0.8.20;

import {DecimalNFTCenter, BeaconNFT, NFTType} from "./sources/DecimalNFTCenter.sol";

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
}
