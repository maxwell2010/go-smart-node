// SPDX-License-Identifier: MIT
pragma solidity 0.8.20;

import {DecimalMasterValidator} from "./sources/DecimalMasterValidator.sol";

/// @notice Fresh-network initialization extension using verified native accessors.
contract NewNetworkMasterValidator is DecimalMasterValidator {
    error InvalidFreshInitialization();

    constructor() { _disableInitializers(); }

    // Executed by NewNetworkProxyBootstrap while its constructor-only
    // initializer is active and after explicit owner initialization.
    function initializeFresh(address registry) external onlyInitializing {
        if (address(this).code.length != 0 || registry == address(0) ||
            registry.code.length == 0 || owner() == address(0) ||
            _getValidatorStorage()._contractCenter != address(0)) {
            revert InvalidFreshInitialization();
        }
        _getValidatorStorage()._contractCenter = registry;
    }

    function contractCenter() external view returns (address) {
        return _getValidatorStorage()._contractCenter;
    }
}
