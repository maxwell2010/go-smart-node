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
    error InvalidConfiguration();

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

    /// @notice Configure verified owner-only methods and hand off authority
    /// atomically. The temporary owner exists only during proxy construction.
    /// Configuration calldata must be generated from reviewed compiled ABIs.
    function bootstrapConfigured(
        address finalOwner,
        address finalImplementation,
        bytes calldata initialization,
        bytes[] calldata configuration
    ) external payable initializer {
        if (address(this).code.length != 0) revert ConstructorOnly();
        if (msg.value != 0) revert UnexpectedValue();
        if (finalOwner == address(0) || configuration.length > 32) {
            revert InvalidConfiguration();
        }
        address caller = msg.sender;
        __Ownable_init(caller);
        ERC1967Utils.upgradeToAndCall(finalImplementation, initialization);
        _executeConfiguration(caller, finalImplementation, configuration);
        _transferOwnership(finalOwner);
    }

    function _executeConfiguration(address caller, address finalImplementation, bytes[] calldata configuration) private {
        uint256 totalBytes;
        _checkConfigurationAuthority(caller, finalImplementation);
        for (uint256 i; i < configuration.length; ++i) {
            bytes calldata data = configuration[i];
            totalBytes += data.length;
            if (data.length < 4 || totalBytes > 32768) {
                revert InvalidConfiguration();
            }
            (bool success, bytes memory reason) = finalImplementation.delegatecall(data);
            if (!success) {
                assembly ("memory-safe") {
                    revert(add(reason, 32), mload(reason))
                }
            }
            _checkConfigurationAuthority(caller, finalImplementation);
        }
    }

    function _checkConfigurationAuthority(address caller, address implementation) private view {
        if (owner() != caller || ERC1967Utils.getImplementation() != implementation) {
            revert InvalidConfiguration();
        }
    }
}
