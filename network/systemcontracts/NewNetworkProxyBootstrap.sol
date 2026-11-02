// SPDX-License-Identifier: MIT
pragma solidity 0.8.20;

import {OwnableUpgradeable} from "sources/DecimalContractCenter.sol";
import {ERC1967Utils} from "sources/DecimalContractCenterProxy.sol";

/// @notice Explicit-owner bootstrap for a new network's ERC1967 proxy.
/// @dev This is new source, not the unverified historical Decimal setup code.
///      It uses the verified implementation's own initialization/storage code.
contract NewNetworkProxyBootstrap is OwnableUpgradeable {
    error ConstructorOnly();
    error UnexpectedValue();

    constructor() {
        _disableInitializers();
    }

    /// @dev Invoke only as the initialization delegatecall of a new proxy's
    ///      constructor. After it returns, the proxy executes finalImplementation.
    ///      No authority, registry address or allocation is inherited from mainnet.
    function bootstrap(
        address initialOwner,
        address finalImplementation,
        bytes calldata initialization
    ) external payable initializer {
        if (address(this).code.length != 0) revert ConstructorOnly();
        if (msg.value != 0) revert UnexpectedValue();
        __Ownable_init(initialOwner);
        ERC1967Utils.upgradeToAndCall(finalImplementation, initialization);
    }
}
