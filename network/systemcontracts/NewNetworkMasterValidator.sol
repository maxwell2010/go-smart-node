// SPDX-License-Identifier: MIT
pragma solidity 0.8.20;

import {DecimalMasterValidator} from "./sources/DecimalMasterValidator.sol";

/// @notice Fresh-network initialization extension using verified native accessors.
contract NewNetworkMasterValidator is DecimalMasterValidator {
    error InvalidFreshInitialization();
    error InvalidPenaltyReadRange();

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

    /// @notice Fixed-size validator header; never copies the penalty array.
    function getValidatorState(address validator) external view returns (
        ValidatorStatus status, bool paused, uint256 penaltyCount
    ) {
        Validator storage state = _getValidatorStorage()._validators[validator];
        return (state.status, state.paused, state.penaltyPercantages.length);
    }

    /// @notice Bounded readers for native reconciliation; no monetary change.
    function getPenaltyCount(address validator) external view returns (uint256) {
        return _getValidatorStorage()._validators[validator].penaltyPercantages.length;
    }

    function getPenaltiesRange(
        address validator,
        uint256 start,
        uint256 limit
    ) external view returns (uint16[] memory result) {
        uint16[] storage penalties = _getValidatorStorage()._validators[validator].penaltyPercantages;
        if (limit > 256 || start > penalties.length) revert InvalidPenaltyReadRange();
        uint256 remaining = penalties.length - start;
        uint256 count = limit < remaining ? limit : remaining;
        result = new uint16[](count);
        for (uint256 i; i < count; ++i) {
            result[i] = penalties[start + i];
        }
    }
}
