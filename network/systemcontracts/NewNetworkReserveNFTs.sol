// SPDX-License-Identifier: MIT
pragma solidity 0.8.20;

import {DRC721, DRC1155} from "./owned/DecimalNFTCenter.sol";

/// @notice Fresh reserve assets use acknowledged native destruction, not locking.
contract NewNetworkDRC721 is DRC721 {
    constructor() { _disableInitializers(); }
}
contract NewNetworkDRC1155 is DRC1155 {
    constructor() { _disableInitializers(); }
}
