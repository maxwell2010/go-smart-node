// SPDX-License-Identifier: MIT
pragma solidity 0.8.20;

import {OwnableUpgradeable} from "./sources/DecimalContractCenter.sol";

interface ITestRegistry {
    function setContractAddress(string calldata slug, address value) external;
}

// Test fixture only: observes rollback of an earlier external configuration.
contract ConfiguredBootstrapTestTarget is OwnableUpgradeable {
    error RejectedConfiguration();
    constructor() { _disableInitializers(); }
    function configureExternal(address registry, string calldata slug, address value) external onlyOwner {
        ITestRegistry(registry).setContractAddress(slug, value);
    }
    function failConfiguration() external pure { revert RejectedConfiguration(); }
}
