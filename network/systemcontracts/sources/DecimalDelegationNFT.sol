// Sources flattened with hardhat v2.22.6 https://hardhat.org

// SPDX-License-Identifier: MIT

// File @decimal/contract-center/contracts/interfaces/IDecimalContractCenter.sol@v0.1.0

// Original license: SPDX_License_Identifier: MIT

pragma solidity 0.8.20;

interface IDecimalContractCenter {
    function setAddress(string memory symbol, address contractAddress) external;
    function setContractAddress(
        string memory symbol,
        address contractAddress
    ) external;

    function getAddress(string memory symbol) external view returns (address);
    function getContractAddress(
        string memory symbol
    ) external view returns (address);

    function upgrade(address newImpl, bytes memory data) external;
}


// File @decimal/contract-center/contracts/interfaces/IDecimalContractCenterMigration.sol@v0.1.0

// Original license: SPDX_License_Identifier: MIT

pragma solidity 0.8.20;

struct Reserve {
    address contractAddress;
    uint256 amount;
}

interface IDecimalContractCenterMigration is IDecimalContractCenter {
    function isMigrating() external view returns (bool);

    function getMarkedReserve(
        address contractAddress
    ) external view returns (uint256);

    function isReserveMigrated(
        Reserve memory reserve
    ) external view returns (bool);

    function markReserve(Reserve memory reserve) external;
}


// File @openzeppelin/contracts/proxy/beacon/IBeacon.sol@v5.0.2

// Original license: SPDX_License_Identifier: MIT
// OpenZeppelin Contracts (last updated v5.0.0) (proxy/beacon/IBeacon.sol)

pragma solidity ^0.8.20;

/**
 * @dev This is the interface that {BeaconProxy} expects of its beacon.
 */
interface IBeacon {
    /**
     * @dev Must return an address that can be used as a delegate call target.
     *
     * {UpgradeableBeacon} will check that this address is a contract.
     */
    function implementation() external view returns (address);
}


// File @openzeppelin/contracts/utils/Address.sol@v5.0.2

// Original license: SPDX_License_Identifier: MIT
// OpenZeppelin Contracts (last updated v5.0.0) (utils/Address.sol)

pragma solidity ^0.8.20;

/**
 * @dev Collection of functions related to the address type
 */
library Address {
    /**
     * @dev The ETH balance of the account is not enough to perform the operation.
     */
    error AddressInsufficientBalance(address account);

    /**
     * @dev There's no code at `target` (it is not a contract).
     */
    error AddressEmptyCode(address target);

    /**
     * @dev A call to an address target failed. The target may have reverted.
     */
    error FailedInnerCall();

    /**
     * @dev Replacement for Solidity's `transfer`: sends `amount` wei to
     * `recipient`, forwarding all available gas and reverting on errors.
     *
     * https://eips.ethereum.org/EIPS/eip-1884[EIP1884] increases the gas cost
     * of certain opcodes, possibly making contracts go over the 2300 gas limit
     * imposed by `transfer`, making them unable to receive funds via
     * `transfer`. {sendValue} removes this limitation.
     *
     * https://consensys.net/diligence/blog/2019/09/stop-using-soliditys-transfer-now/[Learn more].
     *
     * IMPORTANT: because control is transferred to `recipient`, care must be
     * taken to not create reentrancy vulnerabilities. Consider using
     * {ReentrancyGuard} or the
     * https://solidity.readthedocs.io/en/v0.8.20/security-considerations.html#use-the-checks-effects-interactions-pattern[checks-effects-interactions pattern].
     */
    function sendValue(address payable recipient, uint256 amount) internal {
        if (address(this).balance < amount) {
            revert AddressInsufficientBalance(address(this));
        }

        (bool success, ) = recipient.call{value: amount}("");
        if (!success) {
            revert FailedInnerCall();
        }
    }

    /**
     * @dev Performs a Solidity function call using a low level `call`. A
     * plain `call` is an unsafe replacement for a function call: use this
     * function instead.
     *
     * If `target` reverts with a revert reason or custom error, it is bubbled
     * up by this function (like regular Solidity function calls). However, if
     * the call reverted with no returned reason, this function reverts with a
     * {FailedInnerCall} error.
     *
     * Returns the raw returned data. To convert to the expected return value,
     * use https://solidity.readthedocs.io/en/latest/units-and-global-variables.html?highlight=abi.decode#abi-encoding-and-decoding-functions[`abi.decode`].
     *
     * Requirements:
     *
     * - `target` must be a contract.
     * - calling `target` with `data` must not revert.
     */
    function functionCall(address target, bytes memory data) internal returns (bytes memory) {
        return functionCallWithValue(target, data, 0);
    }

    /**
     * @dev Same as {xref-Address-functionCall-address-bytes-}[`functionCall`],
     * but also transferring `value` wei to `target`.
     *
     * Requirements:
     *
     * - the calling contract must have an ETH balance of at least `value`.
     * - the called Solidity function must be `payable`.
     */
    function functionCallWithValue(address target, bytes memory data, uint256 value) internal returns (bytes memory) {
        if (address(this).balance < value) {
            revert AddressInsufficientBalance(address(this));
        }
        (bool success, bytes memory returndata) = target.call{value: value}(data);
        return verifyCallResultFromTarget(target, success, returndata);
    }

    /**
     * @dev Same as {xref-Address-functionCall-address-bytes-}[`functionCall`],
     * but performing a static call.
     */
    function functionStaticCall(address target, bytes memory data) internal view returns (bytes memory) {
        (bool success, bytes memory returndata) = target.staticcall(data);
        return verifyCallResultFromTarget(target, success, returndata);
    }

    /**
     * @dev Same as {xref-Address-functionCall-address-bytes-}[`functionCall`],
     * but performing a delegate call.
     */
    function functionDelegateCall(address target, bytes memory data) internal returns (bytes memory) {
        (bool success, bytes memory returndata) = target.delegatecall(data);
        return verifyCallResultFromTarget(target, success, returndata);
    }

    /**
     * @dev Tool to verify that a low level call to smart-contract was successful, and reverts if the target
     * was not a contract or bubbling up the revert reason (falling back to {FailedInnerCall}) in case of an
     * unsuccessful call.
     */
    function verifyCallResultFromTarget(
        address target,
        bool success,
        bytes memory returndata
    ) internal view returns (bytes memory) {
        if (!success) {
            _revert(returndata);
        } else {
            // only check if target is a contract if the call was successful and the return data is empty
            // otherwise we already know that it was a contract
            if (returndata.length == 0 && target.code.length == 0) {
                revert AddressEmptyCode(target);
            }
            return returndata;
        }
    }

    /**
     * @dev Tool to verify that a low level call was successful, and reverts if it wasn't, either by bubbling the
     * revert reason or with a default {FailedInnerCall} error.
     */
    function verifyCallResult(bool success, bytes memory returndata) internal pure returns (bytes memory) {
        if (!success) {
            _revert(returndata);
        } else {
            return returndata;
        }
    }

    /**
     * @dev Reverts with returndata if present. Otherwise reverts with {FailedInnerCall}.
     */
    function _revert(bytes memory returndata) private pure {
        // Look for revert reason and bubble it up if present
        if (returndata.length > 0) {
            // The easiest way to bubble the revert reason is using memory via assembly
            /// @solidity memory-safe-assembly
            assembly {
                let returndata_size := mload(returndata)
                revert(add(32, returndata), returndata_size)
            }
        } else {
            revert FailedInnerCall();
        }
    }
}


// File @openzeppelin/contracts/utils/StorageSlot.sol@v5.0.2

// Original license: SPDX_License_Identifier: MIT
// OpenZeppelin Contracts (last updated v5.0.0) (utils/StorageSlot.sol)
// This file was procedurally generated from scripts/generate/templates/StorageSlot.js.

pragma solidity ^0.8.20;

/**
 * @dev Library for reading and writing primitive types to specific storage slots.
 *
 * Storage slots are often used to avoid storage conflict when dealing with upgradeable contracts.
 * This library helps with reading and writing to such slots without the need for inline assembly.
 *
 * The functions in this library return Slot structs that contain a `value` member that can be used to read or write.
 *
 * Example usage to set ERC1967 implementation slot:
 * ```solidity
 * contract ERC1967 {
 *     bytes32 internal constant _IMPLEMENTATION_SLOT = 0x360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc;
 *
 *     function _getImplementation() internal view returns (address) {
 *         return StorageSlot.getAddressSlot(_IMPLEMENTATION_SLOT).value;
 *     }
 *
 *     function _setImplementation(address newImplementation) internal {
 *         require(newImplementation.code.length > 0);
 *         StorageSlot.getAddressSlot(_IMPLEMENTATION_SLOT).value = newImplementation;
 *     }
 * }
 * ```
 */
library StorageSlot {
    struct AddressSlot {
        address value;
    }

    struct BooleanSlot {
        bool value;
    }

    struct Bytes32Slot {
        bytes32 value;
    }

    struct Uint256Slot {
        uint256 value;
    }

    struct StringSlot {
        string value;
    }

    struct BytesSlot {
        bytes value;
    }

    /**
     * @dev Returns an `AddressSlot` with member `value` located at `slot`.
     */
    function getAddressSlot(bytes32 slot) internal pure returns (AddressSlot storage r) {
        /// @solidity memory-safe-assembly
        assembly {
            r.slot := slot
        }
    }

    /**
     * @dev Returns an `BooleanSlot` with member `value` located at `slot`.
     */
    function getBooleanSlot(bytes32 slot) internal pure returns (BooleanSlot storage r) {
        /// @solidity memory-safe-assembly
        assembly {
            r.slot := slot
        }
    }

    /**
     * @dev Returns an `Bytes32Slot` with member `value` located at `slot`.
     */
    function getBytes32Slot(bytes32 slot) internal pure returns (Bytes32Slot storage r) {
        /// @solidity memory-safe-assembly
        assembly {
            r.slot := slot
        }
    }

    /**
     * @dev Returns an `Uint256Slot` with member `value` located at `slot`.
     */
    function getUint256Slot(bytes32 slot) internal pure returns (Uint256Slot storage r) {
        /// @solidity memory-safe-assembly
        assembly {
            r.slot := slot
        }
    }

    /**
     * @dev Returns an `StringSlot` with member `value` located at `slot`.
     */
    function getStringSlot(bytes32 slot) internal pure returns (StringSlot storage r) {
        /// @solidity memory-safe-assembly
        assembly {
            r.slot := slot
        }
    }

    /**
     * @dev Returns an `StringSlot` representation of the string storage pointer `store`.
     */
    function getStringSlot(string storage store) internal pure returns (StringSlot storage r) {
        /// @solidity memory-safe-assembly
        assembly {
            r.slot := store.slot
        }
    }

    /**
     * @dev Returns an `BytesSlot` with member `value` located at `slot`.
     */
    function getBytesSlot(bytes32 slot) internal pure returns (BytesSlot storage r) {
        /// @solidity memory-safe-assembly
        assembly {
            r.slot := slot
        }
    }

    /**
     * @dev Returns an `BytesSlot` representation of the bytes storage pointer `store`.
     */
    function getBytesSlot(bytes storage store) internal pure returns (BytesSlot storage r) {
        /// @solidity memory-safe-assembly
        assembly {
            r.slot := store.slot
        }
    }
}


// File @openzeppelin/contracts/proxy/ERC1967/ERC1967Utils.sol@v5.0.2

// Original license: SPDX_License_Identifier: MIT
// OpenZeppelin Contracts (last updated v5.0.0) (proxy/ERC1967/ERC1967Utils.sol)

pragma solidity ^0.8.20;



/**
 * @dev This abstract contract provides getters and event emitting update functions for
 * https://eips.ethereum.org/EIPS/eip-1967[EIP1967] slots.
 */
library ERC1967Utils {
    // We re-declare ERC-1967 events here because they can't be used directly from IERC1967.
    // This will be fixed in Solidity 0.8.21. At that point we should remove these events.
    /**
     * @dev Emitted when the implementation is upgraded.
     */
    event Upgraded(address indexed implementation);

    /**
     * @dev Emitted when the admin account has changed.
     */
    event AdminChanged(address previousAdmin, address newAdmin);

    /**
     * @dev Emitted when the beacon is changed.
     */
    event BeaconUpgraded(address indexed beacon);

    /**
     * @dev Storage slot with the address of the current implementation.
     * This is the keccak-256 hash of "eip1967.proxy.implementation" subtracted by 1.
     */
    // solhint-disable-next-line private-vars-leading-underscore
    bytes32 internal constant IMPLEMENTATION_SLOT = 0x360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc;

    /**
     * @dev The `implementation` of the proxy is invalid.
     */
    error ERC1967InvalidImplementation(address implementation);

    /**
     * @dev The `admin` of the proxy is invalid.
     */
    error ERC1967InvalidAdmin(address admin);

    /**
     * @dev The `beacon` of the proxy is invalid.
     */
    error ERC1967InvalidBeacon(address beacon);

    /**
     * @dev An upgrade function sees `msg.value > 0` that may be lost.
     */
    error ERC1967NonPayable();

    /**
     * @dev Returns the current implementation address.
     */
    function getImplementation() internal view returns (address) {
        return StorageSlot.getAddressSlot(IMPLEMENTATION_SLOT).value;
    }

    /**
     * @dev Stores a new address in the EIP1967 implementation slot.
     */
    function _setImplementation(address newImplementation) private {
        if (newImplementation.code.length == 0) {
            revert ERC1967InvalidImplementation(newImplementation);
        }
        StorageSlot.getAddressSlot(IMPLEMENTATION_SLOT).value = newImplementation;
    }

    /**
     * @dev Performs implementation upgrade with additional setup call if data is nonempty.
     * This function is payable only if the setup call is performed, otherwise `msg.value` is rejected
     * to avoid stuck value in the contract.
     *
     * Emits an {IERC1967-Upgraded} event.
     */
    function upgradeToAndCall(address newImplementation, bytes memory data) internal {
        _setImplementation(newImplementation);
        emit Upgraded(newImplementation);

        if (data.length > 0) {
            Address.functionDelegateCall(newImplementation, data);
        } else {
            _checkNonPayable();
        }
    }

    /**
     * @dev Storage slot with the admin of the contract.
     * This is the keccak-256 hash of "eip1967.proxy.admin" subtracted by 1.
     */
    // solhint-disable-next-line private-vars-leading-underscore
    bytes32 internal constant ADMIN_SLOT = 0xb53127684a568b3173ae13b9f8a6016e243e63b6e8ee1178d6a717850b5d6103;

    /**
     * @dev Returns the current admin.
     *
     * TIP: To get this value clients can read directly from the storage slot shown below (specified by EIP1967) using
     * the https://eth.wiki/json-rpc/API#eth_getstorageat[`eth_getStorageAt`] RPC call.
     * `0xb53127684a568b3173ae13b9f8a6016e243e63b6e8ee1178d6a717850b5d6103`
     */
    function getAdmin() internal view returns (address) {
        return StorageSlot.getAddressSlot(ADMIN_SLOT).value;
    }

    /**
     * @dev Stores a new address in the EIP1967 admin slot.
     */
    function _setAdmin(address newAdmin) private {
        if (newAdmin == address(0)) {
            revert ERC1967InvalidAdmin(address(0));
        }
        StorageSlot.getAddressSlot(ADMIN_SLOT).value = newAdmin;
    }

    /**
     * @dev Changes the admin of the proxy.
     *
     * Emits an {IERC1967-AdminChanged} event.
     */
    function changeAdmin(address newAdmin) internal {
        emit AdminChanged(getAdmin(), newAdmin);
        _setAdmin(newAdmin);
    }

    /**
     * @dev The storage slot of the UpgradeableBeacon contract which defines the implementation for this proxy.
     * This is the keccak-256 hash of "eip1967.proxy.beacon" subtracted by 1.
     */
    // solhint-disable-next-line private-vars-leading-underscore
    bytes32 internal constant BEACON_SLOT = 0xa3f0ad74e5423aebfd80d3ef4346578335a9a72aeaee59ff6cb3582b35133d50;

    /**
     * @dev Returns the current beacon.
     */
    function getBeacon() internal view returns (address) {
        return StorageSlot.getAddressSlot(BEACON_SLOT).value;
    }

    /**
     * @dev Stores a new beacon in the EIP1967 beacon slot.
     */
    function _setBeacon(address newBeacon) private {
        if (newBeacon.code.length == 0) {
            revert ERC1967InvalidBeacon(newBeacon);
        }

        StorageSlot.getAddressSlot(BEACON_SLOT).value = newBeacon;

        address beaconImplementation = IBeacon(newBeacon).implementation();
        if (beaconImplementation.code.length == 0) {
            revert ERC1967InvalidImplementation(beaconImplementation);
        }
    }

    /**
     * @dev Change the beacon and trigger a setup call if data is nonempty.
     * This function is payable only if the setup call is performed, otherwise `msg.value` is rejected
     * to avoid stuck value in the contract.
     *
     * Emits an {IERC1967-BeaconUpgraded} event.
     *
     * CAUTION: Invoking this function has no effect on an instance of {BeaconProxy} since v5, since
     * it uses an immutable beacon without looking at the value of the ERC-1967 beacon slot for
     * efficiency.
     */
    function upgradeBeaconToAndCall(address newBeacon, bytes memory data) internal {
        _setBeacon(newBeacon);
        emit BeaconUpgraded(newBeacon);

        if (data.length > 0) {
            Address.functionDelegateCall(IBeacon(newBeacon).implementation(), data);
        } else {
            _checkNonPayable();
        }
    }

    /**
     * @dev Reverts if `msg.value` is not zero. It can be used to avoid `msg.value` stuck in the contract
     * if an upgrade doesn't perform an initialization call.
     */
    function _checkNonPayable() private {
        if (msg.value > 0) {
            revert ERC1967NonPayable();
        }
    }
}


// File @decimal/contract-center/contracts/libraries/DecimalImpl.sol@v0.1.0

// Original license: SPDX_License_Identifier: MIT
pragma solidity 0.8.20;

abstract contract DecimalImpl {
    function getImpl() external view returns (address) {
        return ERC1967Utils.getImplementation();
    }
}


// File @decimal/contract-center/contracts/interfaces/IDecimalMigration.sol@v0.1.0

// Original license: SPDX_License_Identifier: MIT
pragma solidity 0.8.20;

interface IDecimalMigration {
    function isMigrating() external view returns (bool);
}


// File @openzeppelin/contracts-upgradeable/proxy/utils/Initializable.sol@v5.0.2

// Original license: SPDX_License_Identifier: MIT
// OpenZeppelin Contracts (last updated v5.0.0) (proxy/utils/Initializable.sol)

pragma solidity ^0.8.20;

/**
 * @dev This is a base contract to aid in writing upgradeable contracts, or any kind of contract that will be deployed
 * behind a proxy. Since proxied contracts do not make use of a constructor, it's common to move constructor logic to an
 * external initializer function, usually called `initialize`. It then becomes necessary to protect this initializer
 * function so it can only be called once. The {initializer} modifier provided by this contract will have this effect.
 *
 * The initialization functions use a version number. Once a version number is used, it is consumed and cannot be
 * reused. This mechanism prevents re-execution of each "step" but allows the creation of new initialization steps in
 * case an upgrade adds a module that needs to be initialized.
 *
 * For example:
 *
 * [.hljs-theme-light.nopadding]
 * ```solidity
 * contract MyToken is ERC20Upgradeable {
 *     function initialize() initializer public {
 *         __ERC20_init("MyToken", "MTK");
 *     }
 * }
 *
 * contract MyTokenV2 is MyToken, ERC20PermitUpgradeable {
 *     function initializeV2() reinitializer(2) public {
 *         __ERC20Permit_init("MyToken");
 *     }
 * }
 * ```
 *
 * TIP: To avoid leaving the proxy in an uninitialized state, the initializer function should be called as early as
 * possible by providing the encoded function call as the `_data` argument to {ERC1967Proxy-constructor}.
 *
 * CAUTION: When used with inheritance, manual care must be taken to not invoke a parent initializer twice, or to ensure
 * that all initializers are idempotent. This is not verified automatically as constructors are by Solidity.
 *
 * [CAUTION]
 * ====
 * Avoid leaving a contract uninitialized.
 *
 * An uninitialized contract can be taken over by an attacker. This applies to both a proxy and its implementation
 * contract, which may impact the proxy. To prevent the implementation contract from being used, you should invoke
 * the {_disableInitializers} function in the constructor to automatically lock it when it is deployed:
 *
 * [.hljs-theme-light.nopadding]
 * ```
 * /// @custom:oz-upgrades-unsafe-allow constructor
 * constructor() {
 *     _disableInitializers();
 * }
 * ```
 * ====
 */
abstract contract Initializable {
    /**
     * @dev Storage of the initializable contract.
     *
     * It's implemented on a custom ERC-7201 namespace to reduce the risk of storage collisions
     * when using with upgradeable contracts.
     *
     * @custom:storage-location erc7201:openzeppelin.storage.Initializable
     */
    struct InitializableStorage {
        /**
         * @dev Indicates that the contract has been initialized.
         */
        uint64 _initialized;
        /**
         * @dev Indicates that the contract is in the process of being initialized.
         */
        bool _initializing;
    }

    // keccak256(abi.encode(uint256(keccak256("openzeppelin.storage.Initializable")) - 1)) & ~bytes32(uint256(0xff))
    bytes32 private constant INITIALIZABLE_STORAGE = 0xf0c57e16840df040f15088dc2f81fe391c3923bec73e23a9662efc9c229c6a00;

    /**
     * @dev The contract is already initialized.
     */
    error InvalidInitialization();

    /**
     * @dev The contract is not initializing.
     */
    error NotInitializing();

    /**
     * @dev Triggered when the contract has been initialized or reinitialized.
     */
    event Initialized(uint64 version);

    /**
     * @dev A modifier that defines a protected initializer function that can be invoked at most once. In its scope,
     * `onlyInitializing` functions can be used to initialize parent contracts.
     *
     * Similar to `reinitializer(1)`, except that in the context of a constructor an `initializer` may be invoked any
     * number of times. This behavior in the constructor can be useful during testing and is not expected to be used in
     * production.
     *
     * Emits an {Initialized} event.
     */
    modifier initializer() {
        // solhint-disable-next-line var-name-mixedcase
        InitializableStorage storage $ = _getInitializableStorage();

        // Cache values to avoid duplicated sloads
        bool isTopLevelCall = !$._initializing;
        uint64 initialized = $._initialized;

        // Allowed calls:
        // - initialSetup: the contract is not in the initializing state and no previous version was
        //                 initialized
        // - construction: the contract is initialized at version 1 (no reininitialization) and the
        //                 current contract is just being deployed
        bool initialSetup = initialized == 0 && isTopLevelCall;
        bool construction = initialized == 1 && address(this).code.length == 0;

        if (!initialSetup && !construction) {
            revert InvalidInitialization();
        }
        $._initialized = 1;
        if (isTopLevelCall) {
            $._initializing = true;
        }
        _;
        if (isTopLevelCall) {
            $._initializing = false;
            emit Initialized(1);
        }
    }

    /**
     * @dev A modifier that defines a protected reinitializer function that can be invoked at most once, and only if the
     * contract hasn't been initialized to a greater version before. In its scope, `onlyInitializing` functions can be
     * used to initialize parent contracts.
     *
     * A reinitializer may be used after the original initialization step. This is essential to configure modules that
     * are added through upgrades and that require initialization.
     *
     * When `version` is 1, this modifier is similar to `initializer`, except that functions marked with `reinitializer`
     * cannot be nested. If one is invoked in the context of another, execution will revert.
     *
     * Note that versions can jump in increments greater than 1; this implies that if multiple reinitializers coexist in
     * a contract, executing them in the right order is up to the developer or operator.
     *
     * WARNING: Setting the version to 2**64 - 1 will prevent any future reinitialization.
     *
     * Emits an {Initialized} event.
     */
    modifier reinitializer(uint64 version) {
        // solhint-disable-next-line var-name-mixedcase
        InitializableStorage storage $ = _getInitializableStorage();

        if ($._initializing || $._initialized >= version) {
            revert InvalidInitialization();
        }
        $._initialized = version;
        $._initializing = true;
        _;
        $._initializing = false;
        emit Initialized(version);
    }

    /**
     * @dev Modifier to protect an initialization function so that it can only be invoked by functions with the
     * {initializer} and {reinitializer} modifiers, directly or indirectly.
     */
    modifier onlyInitializing() {
        _checkInitializing();
        _;
    }

    /**
     * @dev Reverts if the contract is not in an initializing state. See {onlyInitializing}.
     */
    function _checkInitializing() internal view virtual {
        if (!_isInitializing()) {
            revert NotInitializing();
        }
    }

    /**
     * @dev Locks the contract, preventing any future reinitialization. This cannot be part of an initializer call.
     * Calling this in the constructor of a contract will prevent that contract from being initialized or reinitialized
     * to any version. It is recommended to use this to lock implementation contracts that are designed to be called
     * through proxies.
     *
     * Emits an {Initialized} event the first time it is successfully executed.
     */
    function _disableInitializers() internal virtual {
        // solhint-disable-next-line var-name-mixedcase
        InitializableStorage storage $ = _getInitializableStorage();

        if ($._initializing) {
            revert InvalidInitialization();
        }
        if ($._initialized != type(uint64).max) {
            $._initialized = type(uint64).max;
            emit Initialized(type(uint64).max);
        }
    }

    /**
     * @dev Returns the highest version that has been initialized. See {reinitializer}.
     */
    function _getInitializedVersion() internal view returns (uint64) {
        return _getInitializableStorage()._initialized;
    }

    /**
     * @dev Returns `true` if the contract is currently initializing. See {onlyInitializing}.
     */
    function _isInitializing() internal view returns (bool) {
        return _getInitializableStorage()._initializing;
    }

    /**
     * @dev Returns a pointer to the storage namespace.
     */
    // solhint-disable-next-line var-name-mixedcase
    function _getInitializableStorage() private pure returns (InitializableStorage storage $) {
        assembly {
            $.slot := INITIALIZABLE_STORAGE
        }
    }
}


// File @openzeppelin/contracts-upgradeable/utils/ContextUpgradeable.sol@v5.0.2

// Original license: SPDX_License_Identifier: MIT
// OpenZeppelin Contracts (last updated v5.0.1) (utils/Context.sol)

pragma solidity ^0.8.20;

/**
 * @dev Provides information about the current execution context, including the
 * sender of the transaction and its data. While these are generally available
 * via msg.sender and msg.data, they should not be accessed in such a direct
 * manner, since when dealing with meta-transactions the account sending and
 * paying for execution may not be the actual sender (as far as an application
 * is concerned).
 *
 * This contract is only required for intermediate, library-like contracts.
 */
abstract contract ContextUpgradeable is Initializable {
    function __Context_init() internal onlyInitializing {
    }

    function __Context_init_unchained() internal onlyInitializing {
    }
    function _msgSender() internal view virtual returns (address) {
        return msg.sender;
    }

    function _msgData() internal view virtual returns (bytes calldata) {
        return msg.data;
    }

    function _contextSuffixLength() internal view virtual returns (uint256) {
        return 0;
    }
}


// File @openzeppelin/contracts-upgradeable/access/OwnableUpgradeable.sol@v5.0.2

// Original license: SPDX_License_Identifier: MIT
// OpenZeppelin Contracts (last updated v5.0.0) (access/Ownable.sol)

pragma solidity ^0.8.20;


/**
 * @dev Contract module which provides a basic access control mechanism, where
 * there is an account (an owner) that can be granted exclusive access to
 * specific functions.
 *
 * The initial owner is set to the address provided by the deployer. This can
 * later be changed with {transferOwnership}.
 *
 * This module is used through inheritance. It will make available the modifier
 * `onlyOwner`, which can be applied to your functions to restrict their use to
 * the owner.
 */
abstract contract OwnableUpgradeable is Initializable, ContextUpgradeable {
    /// @custom:storage-location erc7201:openzeppelin.storage.Ownable
    struct OwnableStorage {
        address _owner;
    }

    // keccak256(abi.encode(uint256(keccak256("openzeppelin.storage.Ownable")) - 1)) & ~bytes32(uint256(0xff))
    bytes32 private constant OwnableStorageLocation = 0x9016d09d72d40fdae2fd8ceac6b6234c7706214fd39c1cd1e609a0528c199300;

    function _getOwnableStorage() private pure returns (OwnableStorage storage $) {
        assembly {
            $.slot := OwnableStorageLocation
        }
    }

    /**
     * @dev The caller account is not authorized to perform an operation.
     */
    error OwnableUnauthorizedAccount(address account);

    /**
     * @dev The owner is not a valid owner account. (eg. `address(0)`)
     */
    error OwnableInvalidOwner(address owner);

    event OwnershipTransferred(address indexed previousOwner, address indexed newOwner);

    /**
     * @dev Initializes the contract setting the address provided by the deployer as the initial owner.
     */
    function __Ownable_init(address initialOwner) internal onlyInitializing {
        __Ownable_init_unchained(initialOwner);
    }

    function __Ownable_init_unchained(address initialOwner) internal onlyInitializing {
        if (initialOwner == address(0)) {
            revert OwnableInvalidOwner(address(0));
        }
        _transferOwnership(initialOwner);
    }

    /**
     * @dev Throws if called by any account other than the owner.
     */
    modifier onlyOwner() {
        _checkOwner();
        _;
    }

    /**
     * @dev Returns the address of the current owner.
     */
    function owner() public view virtual returns (address) {
        OwnableStorage storage $ = _getOwnableStorage();
        return $._owner;
    }

    /**
     * @dev Throws if the sender is not the owner.
     */
    function _checkOwner() internal view virtual {
        if (owner() != _msgSender()) {
            revert OwnableUnauthorizedAccount(_msgSender());
        }
    }

    /**
     * @dev Leaves the contract without owner. It will not be possible to call
     * `onlyOwner` functions. Can only be called by the current owner.
     *
     * NOTE: Renouncing ownership will leave the contract without an owner,
     * thereby disabling any functionality that is only available to the owner.
     */
    function renounceOwnership() public virtual onlyOwner {
        _transferOwnership(address(0));
    }

    /**
     * @dev Transfers ownership of the contract to a new account (`newOwner`).
     * Can only be called by the current owner.
     */
    function transferOwnership(address newOwner) public virtual onlyOwner {
        if (newOwner == address(0)) {
            revert OwnableInvalidOwner(address(0));
        }
        _transferOwnership(newOwner);
    }

    /**
     * @dev Transfers ownership of the contract to a new account (`newOwner`).
     * Internal function without access restriction.
     */
    function _transferOwnership(address newOwner) internal virtual {
        OwnableStorage storage $ = _getOwnableStorage();
        address oldOwner = $._owner;
        $._owner = newOwner;
        emit OwnershipTransferred(oldOwner, newOwner);
    }
}


// File @decimal/contract-center/contracts/libraries/DecimalMigration.sol@v0.1.0

// Original license: SPDX_License_Identifier: MIT
pragma solidity 0.8.20;



abstract contract DecimalMigration is IDecimalMigration {
    error DisabledDuringMigration();
    error AvailableOnlyDuringMigration();
    error AvailableOnlyForDecimal();

    function _getContractCenterMigration()
        internal
        view
        virtual
        returns (IDecimalContractCenterMigration);

    function isMigrating() public view virtual returns (bool) {
        return _getContractCenterMigration().isMigrating();
    }

    function _onlyMigration() internal view {
        if (!isMigrating()) revert AvailableOnlyDuringMigration();
    }

    function _onlyNotMigration() internal view {
        if (isMigrating()) revert DisabledDuringMigration();
    }

    modifier onlyNotMigration() {
        _onlyNotMigration();
        _;
    }

    modifier onlyMigration() {
        _onlyMigration();
        _;
    }

    function isDecimal() public view virtual returns (bool) {
        bool isTokenCenter = _getContractCenterMigration().getContractAddress(
            "token-center"
        ) == msg.sender;
        bool isNFTCenter = _getContractCenterMigration().getContractAddress(
            "nft-center"
        ) == msg.sender;
        bool isDelegation = _getContractCenterMigration().getContractAddress(
            "delegation"
        ) == msg.sender;
        bool isDelegationNFT = _getContractCenterMigration().getContractAddress(
            "delegation-nft"
        ) == msg.sender;
        bool isMasterValidator = _getContractCenterMigration()
            .getContractAddress("master-validator") == msg.sender;
        address owner = OwnableUpgradeable(
            address(_getContractCenterMigration())
        ).owner();
        bool isOwner = owner == msg.sender || owner == tx.origin;
        return
            isTokenCenter ||
            isNFTCenter ||
            isDelegation ||
            isDelegationNFT ||
            isMasterValidator ||
            isOwner;
    }

    function _checkDecimal() internal view {
        if (!isDecimal()) {
            revert AvailableOnlyForDecimal();
        }
    }

    modifier onlyDecimal() {
        _checkDecimal();
        _;
    }
}


// File @decimal/delegation/contracts/interfaces/IDecimalDelegationCommon.sol@v0.1.0

// Original license: SPDX_License_Identifier: MIT

pragma solidity 0.8.20;

interface IDecimalDelegationCommon {
    /* ========== STORAGE ========== */

    struct DelegationStorage {
        address _contractCenter;
        mapping(bytes32 stakeId => Stake stake) _stakes;
        mapping(bytes32 stakeId => uint256 penaltyIndex) _stakePenaltyIndex;
        FrozenStake[] _frozenStakes;
        mapping(FreezeType freezeType => uint256 timestamp) _freezeTime;
        mapping(address validator => mapping(bytes32 hashedTokenId => ValidatorReserve)) _validatorTokens;
    }

    /* ========== ERRORS ========== */

    error InvalidValidator();
    error InvalidAmount();
    error FrozenStakesQueueIsEmpty();
    error NoPenaltyToApply();
    error StakeAlreadyUnfrozen();
    error UnfreezeTimestampError(
        uint256 unfreezeTimestamp,
        uint256 currentTimestamp
    );
    error InvalidHoldTimestamp(uint256 providedHoldTimestamp);
    error HoldNotExpired(uint256 holdTimestamp, uint256 currentTimestamp);
    error WithdrawFailed(address recipient);
    error NoCompletableFrozenStakes();
    error InvalidFrozenType();
    error ZeroAddress();
    error ZeroAmount();
    error StakeInactive();
    error InvalidTimestamp();
    error InvalidTokenType();

    /* ========== EVENTS ========== */

    event StakeUpdated(bytes32 stakeId, Stake stake, bool isNew);
    event StakeAmountUpdated(bytes32 stakeId, int256 changedAmount);
    event StakeHolded(bytes32 stakeId, Stake stake, bool isNew);
    event StakeReset(bytes32 stakeId, Stake stake);
    event WithdrawRequest(
        bytes32 stakeId,
        uint256 stakeIndex,
        FrozenStake frozenStake
    );
    event TransferRequest(
        bytes32 stakeId,
        uint256 stakeIndex,
        FrozenStake frozenStake
    );
    event WithdrawCompleted(uint256 stakeIndex);
    event TransferCompleted(uint256 stakeIndex);

    event PenaltyAppliedToValidator(
        address indexed validator,
        address token,
        TokenType tokenType,
        uint256 tokenId,
        uint256 penaltyIndex,
        uint256 amountToPenalty
    );

    event PenaltyAppliedToStake(bytes32 indexed stakeId, uint256 newPenaltyIndex);

    event FreezeTimeUpdated(
        uint256 withdrawFreezeTime,
        uint256 transferFreezeTime
    );

    /* ========== STRUCTS ========== */

    enum FreezeType {
        None, // default
        Withdraw,
        Transfer
    }

    enum FreezeStatus {
        Completed, // default
        Frozen
    }

    enum Member {
        None,
        Delegator,
        Validator
    }

    struct StakeMeta {
        Stake stake;
        uint256 penaltyIndex;
    }

    struct ValidatorReserve {
        uint256 penaltyIndex;
        uint256 reserve;
    }

    struct ValidatorToken {
        address validator;
        address token;
        uint256 tokenId;
    }

    enum TokenType {
        None,
        DRC20,
        DRC721,
        DRC1155,
        DEL
    }

    struct Stake {
        address validator;
        address delegator;
        address token;
        uint256 amount; // always 1 for DRC721, but any for DRC20 and DRC1155
        uint256 tokenId; // always 0 for DRC20, but any for DRC721 and DRC1155
        TokenType tokenType;
        uint256 holdTimestamp; // if not zero => hold stake
    }

    struct FrozenStake {
        Stake stake;
        FreezeStatus freezeStatus;
        FreezeType freezeType;
        uint256 unfreezeTimestamp;
    }
}


// File @decimal/delegation/contracts/interfaces/IDecimalMasterValidator.sol@v0.1.0

// Original license: SPDX_License_Identifier: MIT
pragma solidity 0.8.20;

interface IDecimalMasterValidator {
    /* ========== STRUCTS ========== */

    enum ValidatorStatus {
        Deleted,
        Candidate,
        Approved
    }

    /* ========== GETTERS ========== */

    function getValidatorStatus(
        address validator
    ) external view returns (ValidatorStatus);

    function getValidatorPenalties(
        address validator
    ) external view returns (uint16[] memory);

    function isActive(address validator) external view returns (bool);
}


// File @openzeppelin/contracts-upgradeable/utils/PausableUpgradeable.sol@v5.0.2

// Original license: SPDX_License_Identifier: MIT
// OpenZeppelin Contracts (last updated v5.0.0) (utils/Pausable.sol)

pragma solidity ^0.8.20;


/**
 * @dev Contract module which allows children to implement an emergency stop
 * mechanism that can be triggered by an authorized account.
 *
 * This module is used through inheritance. It will make available the
 * modifiers `whenNotPaused` and `whenPaused`, which can be applied to
 * the functions of your contract. Note that they will not be pausable by
 * simply including this module, only once the modifiers are put in place.
 */
abstract contract PausableUpgradeable is Initializable, ContextUpgradeable {
    /// @custom:storage-location erc7201:openzeppelin.storage.Pausable
    struct PausableStorage {
        bool _paused;
    }

    // keccak256(abi.encode(uint256(keccak256("openzeppelin.storage.Pausable")) - 1)) & ~bytes32(uint256(0xff))
    bytes32 private constant PausableStorageLocation = 0xcd5ed15c6e187e77e9aee88184c21f4f2182ab5827cb3b7e07fbedcd63f03300;

    function _getPausableStorage() private pure returns (PausableStorage storage $) {
        assembly {
            $.slot := PausableStorageLocation
        }
    }

    /**
     * @dev Emitted when the pause is triggered by `account`.
     */
    event Paused(address account);

    /**
     * @dev Emitted when the pause is lifted by `account`.
     */
    event Unpaused(address account);

    /**
     * @dev The operation failed because the contract is paused.
     */
    error EnforcedPause();

    /**
     * @dev The operation failed because the contract is not paused.
     */
    error ExpectedPause();

    /**
     * @dev Initializes the contract in unpaused state.
     */
    function __Pausable_init() internal onlyInitializing {
        __Pausable_init_unchained();
    }

    function __Pausable_init_unchained() internal onlyInitializing {
        PausableStorage storage $ = _getPausableStorage();
        $._paused = false;
    }

    /**
     * @dev Modifier to make a function callable only when the contract is not paused.
     *
     * Requirements:
     *
     * - The contract must not be paused.
     */
    modifier whenNotPaused() {
        _requireNotPaused();
        _;
    }

    /**
     * @dev Modifier to make a function callable only when the contract is paused.
     *
     * Requirements:
     *
     * - The contract must be paused.
     */
    modifier whenPaused() {
        _requirePaused();
        _;
    }

    /**
     * @dev Returns true if the contract is paused, and false otherwise.
     */
    function paused() public view virtual returns (bool) {
        PausableStorage storage $ = _getPausableStorage();
        return $._paused;
    }

    /**
     * @dev Throws if the contract is paused.
     */
    function _requireNotPaused() internal view virtual {
        if (paused()) {
            revert EnforcedPause();
        }
    }

    /**
     * @dev Throws if the contract is not paused.
     */
    function _requirePaused() internal view virtual {
        if (!paused()) {
            revert ExpectedPause();
        }
    }

    /**
     * @dev Triggers stopped state.
     *
     * Requirements:
     *
     * - The contract must not be paused.
     */
    function _pause() internal virtual whenNotPaused {
        PausableStorage storage $ = _getPausableStorage();
        $._paused = true;
        emit Paused(_msgSender());
    }

    /**
     * @dev Returns to normal state.
     *
     * Requirements:
     *
     * - The contract must be paused.
     */
    function _unpause() internal virtual whenPaused {
        PausableStorage storage $ = _getPausableStorage();
        $._paused = false;
        emit Unpaused(_msgSender());
    }
}


// File @decimal/delegation/contracts/DecimalDelegationCommon.sol@v0.1.0

// Original license: SPDX_License_Identifier: MIT

pragma solidity 0.8.20;






abstract contract DecimalDelegationCommon is
    IDecimalDelegationCommon,
    OwnableUpgradeable,
    PausableUpgradeable,
    DecimalImpl
{
    function _getDelegationStorage()
        internal
        pure
        returns (DelegationStorage storage $)
    {
        assembly {
            $.slot := DECIMAL_DELEGATION_COMMON_STORAGE_LOCATION
        }
    }

    // keccak256(abi.encode(uint256(keccak256("DECIMAL_DELEGATION_COMMON_STORAGE_LOCATION")) - 1)) & ~bytes32(uint256(0xff));
    bytes32 internal constant DECIMAL_DELEGATION_COMMON_STORAGE_LOCATION =
        0xc1dae510251b57b62087f142cc8746564a134308f8810580b66b08a29b6def00;

    function _addStake(Stake memory stake) internal virtual {
        bytes32 stakeId = _getStakeId(stake);
        _applyPenalties(stakeId);
        _checkToken(stake.token, stake.tokenType);
        _checkStake(stake);

        Stake storage currentStake = _getStake(stakeId);
        bool isNewStake = currentStake.amount == 0;
        if (isNewStake) {
            uint256 currentPenaltyIndex = _getValidatorPenalties(stake.validator)
                .length;

            currentStake.amount = stake.amount;
            currentStake.validator = stake.validator;
            currentStake.delegator = stake.delegator;
            currentStake.token = stake.token;
            currentStake.tokenId = stake.tokenId;
            currentStake.tokenType = stake.tokenType;
            currentStake.holdTimestamp = stake.holdTimestamp;
            _setStakePenaltyIndex(stakeId, currentPenaltyIndex);
        } else {
            currentStake.amount += stake.amount;
        }

        _addValidatorReserve(stake);

        emit StakeAmountUpdated(stakeId, int256(stake.amount));
        emit StakeUpdated(stakeId, _getStake(stakeId), isNewStake);
    }

    function _addValidatorReserve(Stake memory stake) internal {
        ValidatorReserve storage validatorReserve = _getValidatorReserve(
            stake.validator,
            stake.token,
            stake.tokenId
        );
        validatorReserve.reserve += stake.amount;
    }

    function _reduceValidatorReserve(
        Stake memory stake,
        uint256 amountToRemove
    ) internal {
        ValidatorReserve storage validatorReserve = _getValidatorReserve(
            stake.validator,
            stake.token,
            stake.tokenId
        );
        validatorReserve.reserve -= amountToRemove;
    }

    function _holdWithReset(
        address validator,
        address token,
        uint256 tokenId,
        uint256 amountToHold,
        uint256 newHoldTimestamp,
        uint256[] memory holdTimestampsToReset
    ) internal {
        _resetHolds(
            validator,
            msg.sender,
            token,
            tokenId,
            holdTimestampsToReset
        );
        _hold(
            validator,
            token,
            tokenId,
            amountToHold,
            0, // not used
            newHoldTimestamp
        );
    }

    function _hold(
        address validator,
        address token,
        uint256 tokenId,
        uint256 amountToHold,
        uint256,
        uint256 newHoldTimestamp
    ) internal virtual {
        bool timestampIsZero = newHoldTimestamp == 0;
        bool timestampIsExpired = newHoldTimestamp <= block.timestamp;
        // bool timestampIsBeforeOld = newHoldTimestamp <= oldHoldTimestamp;
        if (
            timestampIsZero || timestampIsExpired /* || timestampIsBeforeOld */
        ) {
            revert InvalidTimestamp();
        }
        uint256 holdTimestamp = 0;
        bytes32 stakeId = _getStakeId(
            validator,
            msg.sender,
            token,
            tokenId,
            holdTimestamp
        );
        _applyPenalties(stakeId);

        Stake memory holdStake = Stake({
            validator: validator,
            delegator: msg.sender,
            token: token,
            tokenId: tokenId,
            amount: amountToHold,
            tokenType: _getStake(stakeId).tokenType,
            holdTimestamp: newHoldTimestamp
        });

        Stake memory existedHoldStake = _getStake(_getStakeId(holdStake));
        bool isNewHoldStake = existedHoldStake.amount == 0;

        _removeStake(stakeId, amountToHold);

        _addStake(holdStake);

        emit StakeHolded(stakeId, holdStake, isNewHoldStake);
    }

    function _resetHolds(
        address validator,
        address delegator,
        address token,
        uint256 tokenId,
        uint256[] memory holdTimestampsToReset
    ) internal virtual {
        Stake memory stake;
        for (uint256 i; i < holdTimestampsToReset.length; i++) {
            uint256 holdTimestamp = holdTimestampsToReset[i];
            bytes32 holdStakeId = _getStakeId(
                validator,
                delegator,
                token,
                tokenId,
                holdTimestamp
            );

            _applyPenalties(holdStakeId);

            Stake memory holdStake = _getStake(holdStakeId);

            if (holdStake.amount == 0) {
                revert StakeInactive();
            }
            if (holdStake.holdTimestamp == 0) {
                // TODO already empty hold
                revert InvalidHoldTimestamp(holdStake.holdTimestamp);
            }
            bool isHoldExpired = holdStake.holdTimestamp <= block.timestamp;
            if (!isHoldExpired) {
                revert HoldNotExpired(holdStake.holdTimestamp, block.timestamp);
            }

            _removeStake(holdStakeId, holdStake.amount);

            emit StakeReset(holdStakeId, holdStake);

            if (i == 0) {
                stake = holdStake;
                stake.holdTimestamp = 0;
            } else {
                stake.amount += holdStake.amount;
            }
        }

        _addStake(stake);
    }

    function _completeTransfer(uint256 stakeIndex) internal {
        DelegationStorage storage $ = _getDelegationStorage();
        FrozenStake storage frozenStake = $._frozenStakes[stakeIndex];
        _checkFreezeTime(frozenStake.unfreezeTimestamp);
        if (frozenStake.freezeStatus == FreezeStatus.Completed) {
            revert StakeAlreadyUnfrozen();
        }
        frozenStake.freezeStatus = FreezeStatus.Completed;

        bool isHoldExpired = frozenStake.stake.holdTimestamp <= block.timestamp;
        if (isHoldExpired) {
            frozenStake.stake.holdTimestamp = 0;
        }

        _addStake(frozenStake.stake);
        emit TransferCompleted(stakeIndex);
    }

    function _completeWithdraw(uint256 stakeIndex) internal virtual;

    function complete(uint256[] memory indexes) public virtual whenNotPaused {
        for (uint256 i; i < indexes.length; i++) {
            uint256 stakeIndex = indexes[i];
            FrozenStake memory frozenStake = getFrozenStake(stakeIndex);
            if (frozenStake.freezeType == FreezeType.Withdraw) {
                _completeWithdraw(stakeIndex);
            }
            if (frozenStake.freezeType == FreezeType.Transfer) {
                _completeTransfer(stakeIndex);
            }
        }
    }

    function _transferWithReset(
        address oldValidator,
        address token,
        uint256 tokenId,
        uint256 amount,
        address newValidator,
        uint256[] memory holdTimestampsToReset
    ) internal virtual {
        _resetHolds(
            oldValidator,
            msg.sender,
            token,
            tokenId,
            holdTimestampsToReset
        );
        uint256 holdTimestamp = 0;
        _transfer(
            oldValidator,
            token,
            tokenId,
            amount,
            holdTimestamp,
            newValidator
        );
    }

    function _transfer(
        address oldValidator,
        address token,
        uint256 tokenId,
        uint256 amount,
        uint256 holdTimestamp,
        address newValidator
    ) internal virtual {
        if (newValidator == oldValidator) revert InvalidValidator();
        if (!_isValidatorActive(newValidator)) revert InvalidValidator();

        bytes32 oldStakeId = _getStakeId(
            oldValidator,
            msg.sender,
            token,
            tokenId,
            holdTimestamp
        );
        FrozenStake memory frozenStake = _freezeStake(
            oldStakeId,
            amount,
            FreezeType.Transfer
        );

        FrozenStake[] storage frozenStakes = _getDelegationStorage()
            ._frozenStakes;

        frozenStake.stake.validator = newValidator;
        frozenStakes.push(frozenStake);

        uint256 stakeIndex = frozenStakes.length - 1;
        emit TransferRequest(oldStakeId, stakeIndex, frozenStake);
    }

    function _withdrawWithReset(
        address validator,
        address token,
        uint256 tokenId,
        uint256 amount,
        uint256[] memory holdTimestampsToReset
    ) internal {
        _resetHolds(
            validator,
            msg.sender,
            token,
            tokenId,
            holdTimestampsToReset
        );
        uint256 holdTimestamp = 0;
        _withdraw(validator, token, tokenId, amount, holdTimestamp);
    }

    function _withdraw(
        address validator,
        address token,
        uint256 tokenId,
        uint256 amount,
        uint256 holdTimestamp
    ) internal virtual {
        bool isHoldExpired = holdTimestamp == 0 ||
            holdTimestamp < block.timestamp;
        if (!isHoldExpired) {
            revert HoldNotExpired(holdTimestamp, block.timestamp);
        }

        bytes32 stakeId = _getStakeId(
            validator,
            msg.sender,
            token,
            tokenId,
            holdTimestamp
        );
        FrozenStake memory frozenStake = _freezeStake(
            stakeId,
            amount,
            FreezeType.Withdraw
        );
        FrozenStake[] storage frozenStakes = _getDelegationStorage()
            ._frozenStakes;

        frozenStakes.push(frozenStake);

        uint256 stakeIndex = frozenStakes.length - 1;
        emit WithdrawRequest(stakeId, stakeIndex, frozenStake);
    }

    function _freezeStake(
        bytes32 stakeId,
        uint256 amountToFreeze,
        FreezeType freezeType
    ) internal virtual returns (FrozenStake memory) {
        _applyPenalties(stakeId);

        Stake memory stake = _getStake(stakeId);

        if (stake.amount == 0) {
            revert StakeInactive();
        }
        if (stake.amount < amountToFreeze) {
            revert InvalidAmount();
        }

        if (freezeType == FreezeType.None) {
            revert InvalidFrozenType();
        }

        _removeStake(stakeId, amountToFreeze);

        stake.amount = amountToFreeze;

        uint256 unfreezeTimestamp = block.timestamp + getFreezeTime(freezeType);
        return
            FrozenStake({
                stake: stake,
                freezeStatus: FreezeStatus.Frozen,
                unfreezeTimestamp: unfreezeTimestamp,
                freezeType: freezeType
            });
    }

    function _removeStake(bytes32 stakeId, uint256 amountToRemove) internal {
        Stake storage stake = _getStake(stakeId);

        if (stake.amount == 0) {
            revert StakeInactive();
        }

        bool isEnoughStakeAmount = amountToRemove <= stake.amount;

        if (!isEnoughStakeAmount) {
            revert InvalidAmount();
        }

        stake.amount -= amountToRemove;

        bool isNewStake = false;
        emit StakeAmountUpdated(stakeId, -int256(amountToRemove));
        emit StakeUpdated(stakeId, _getStake(stakeId), isNewStake);

        _reduceValidatorReserve(stake, amountToRemove);
    }

    function _checkFreezeTime(uint256 unfreezeTimestamp) internal view {
        if (unfreezeTimestamp > block.timestamp || unfreezeTimestamp == 0) {
            revert UnfreezeTimestampError(unfreezeTimestamp, block.timestamp);
        }
    }

    /* ========== PENALTY ========== */

    function _getAmountToPenalty(
        uint256 totalAmount,
        uint16 penaltyPercentage
    ) internal pure returns (uint256) {
        return (totalAmount * uint256(penaltyPercentage)) / 100;
    }

    function _applyPenalties(bytes32 stakeId) internal returns (bool) {
        bool stakeApplied = _applyPenaltiesToStake(stakeId);
        Stake memory stake = _getStake(stakeId);
        bool validatorApplied = _applyPenaltiesToValidator(
            stake.validator,
            stake.token,
            stake.tokenId
        );
        return stakeApplied || validatorApplied;
    }

    function _applyPenaltiesToStake(
        bytes32 stakeId
    ) internal virtual returns (bool);

    function _applyPenaltiesToValidator(
        address validator,
        address token,
        uint256 tokenId
    ) internal virtual returns (bool);

    function applyPenaltiesToValidator(
        ValidatorToken[] memory validatorTokens
    ) public virtual whenNotPaused {
        bool isAppliedAtLeastOnce = false;
        for (uint256 i; i < validatorTokens.length; i += 1) {
            ValidatorToken memory validatorToken = validatorTokens[i];
            bool isApplied = _applyPenaltiesToValidator(
                validatorToken.validator,
                validatorToken.token,
                validatorToken.tokenId
            );
            if (isApplied) {
                isAppliedAtLeastOnce = true;
            }
        }
        if (!isAppliedAtLeastOnce) revert NoPenaltyToApply();
    }

    /* ========== GETTERS ========== */

    function _getValidatorPenalties(
        address validator
    ) internal view virtual returns (uint16[] memory) {
        return _masterValidator().getValidatorPenalties(validator);
    }

    function _checkStake(Stake memory stake) internal view {
        if (
            stake.holdTimestamp != 0 && stake.holdTimestamp <= block.timestamp
        ) {
            revert InvalidHoldTimestamp(stake.holdTimestamp);
        }
        if (stake.token == address(0)) revert ZeroAddress();
        if (stake.amount == 0) revert ZeroAmount();
        if (stake.tokenType == TokenType.None) revert InvalidTokenType();
    }

    function _checkToken(
        address token,
        TokenType tokenType
    ) internal view virtual;

    function getFrozenStakes(
        uint256[] memory stakeIndexes
    ) public view returns (FrozenStake[] memory) {
        FrozenStake[] memory frozenStakes = new FrozenStake[](
            stakeIndexes.length
        );
        for (uint256 i; i < stakeIndexes.length; i++) {
            uint256 stakeIndex = stakeIndexes[i];
            frozenStakes[i] = getFrozenStake(stakeIndex);
        }
        return frozenStakes;
    }

    function getFrozenStake(
        uint256 index
    ) public view returns (FrozenStake memory) {
        return _getDelegationStorage()._frozenStakes[index];
    }

    function getStakes(
        bytes32[] memory stakeIds
    ) public view returns (Stake[] memory stakes) {
        return _getStakes(stakeIds);
    }

    function _getStakes(
        bytes32[] memory stakeIds
    ) internal view returns (Stake[] memory stakes) {
        stakes = new Stake[](stakeIds.length);
        for (uint256 i; i < stakeIds.length; i++) {
            bytes32 stakeId = stakeIds[i];
            stakes[i] = _getStake(stakeId);
        }
        return stakes;
    }

    function _getValidatorReserve(
        address validator,
        address token,
        uint256 tokenId
    ) internal view returns (ValidatorReserve storage) {
        bytes32 hashedTokenId = _getHashedTokenId(token, tokenId);
        return
            _getDelegationStorage()._validatorTokens[validator][hashedTokenId];
    }

    function _isValidatorActive(
        address validator
    ) internal view returns (bool) {
        if (validator == address(0)) {
            return false;
        }
        return _masterValidator().isActive(validator);
    }

    function _getHashedTokenId(
        address token,
        uint256 tokenId
    ) internal pure returns (bytes32) {
        return keccak256(abi.encodePacked(token, tokenId));
    }

    function getFreezeTime(
        FreezeType freezeType
    ) public view returns (uint256) {
        return _getDelegationStorage()._freezeTime[freezeType];
    }

    function _getAddress(string memory symbol) internal view returns (address) {
        return
            IDecimalContractCenter(_getDelegationStorage()._contractCenter)
                .getAddress(symbol);
    }

    function _masterValidator()
        internal
        view
        returns (IDecimalMasterValidator)
    {
        return IDecimalMasterValidator(_getAddress("master-validator"));
    }

    function _getStakeId(
        address validator,
        address delegator,
        address token,
        uint256 tokenId,
        uint256 holdTimestamp
    ) internal pure returns (bytes32) {
        return
            keccak256(
                abi.encodePacked(
                    validator,
                    delegator,
                    token,
                    tokenId,
                    holdTimestamp
                )
            );
    }

    function _getStakeId(Stake memory stake) internal pure returns (bytes32) {
        return
            _getStakeId(
                stake.validator,
                stake.delegator,
                stake.token,
                stake.tokenId,
                stake.holdTimestamp
            );
    }

    function getStakePenaltyIndex(bytes32 stakeId) public view returns (uint256) {
        return _getDelegationStorage()._stakePenaltyIndex[stakeId];
    }

    function _getStake(bytes32 stakeId) internal view returns (Stake storage) {
        return _getDelegationStorage()._stakes[stakeId];
    }

    function getContractCenter() public view returns (address) {
        return _getDelegationStorage()._contractCenter;
    }

    /* ========== SETTERS ========== */

    function _setStakePenaltyIndex(bytes32 stakeId, uint256 penaltyIndex) internal {
        _getDelegationStorage()._stakePenaltyIndex[stakeId] = penaltyIndex;
    }

    function setFreezeTime(
        uint256 withdrawFreezeTime,
        uint256 transferFreezeTime
    ) public onlyOwner whenNotPaused {
        _setFreezeTime(withdrawFreezeTime, transferFreezeTime);
    }

    function _setFreezeTime(
        uint256 withdrawFreezeTime,
        uint256 transferFreezeTime
    ) internal {
        DelegationStorage storage $ = _getDelegationStorage();
        $._freezeTime[FreezeType.Withdraw] = withdrawFreezeTime;
        $._freezeTime[FreezeType.Transfer] = transferFreezeTime;
        emit FreezeTimeUpdated(withdrawFreezeTime, transferFreezeTime);
    }

    function setContractCenter(address contractCenter) public onlyOwner {
        _getDelegationStorage()._contractCenter = contractCenter;
    }

    /* ========== UPGRADE ========== */

    function upgrade(address newImpl, bytes memory data) public onlyOwner {
        ERC1967Utils.upgradeToAndCall(newImpl, data);
    }

    /* ========== PAUSABLE ========== */

    function pause() public onlyOwner {
        _pause();
    }

    function unpause() public onlyOwner {
        _unpause();
    }
}


// File @decimal/nft-center/contracts/interfaces/IDecimalNFTCenter.sol@v0.1.0

// Original license: SPDX_License_Identifier: MIT

pragma solidity 0.8.20;

enum NFTType {
    DRC721,
    DRC1155
}

struct NFTState {
    bool active;
    NFTType nftType;
    bool withReserve;
}

interface IDecimalNFTCenter {
    /* ========== STRUCTS ========== */

    struct NFT {
        address creator;
        string symbol;
        string name;
        string contractURI;
        bool refundable;
        bool burnable;
    }

    /* ========== EVENTS ========== */

    event NFTCreated(address tokenAddress, NFTType indexed nftType, NFT nft);

    event UpgradedNFT(
        address indexed oldContract,
        address indexed newContract,
        NFTType indexed nftType,
        bool withReserve
    );

    /* ========== ERRORS ========== */

    error InvalidPayloadLength();
    error InvalidInitialMint();
    error InvalidAddress();
    error AlreadyDeployed();
    error InvalidAllowMint();
    error InvalidToken();

    //     /* ========== DECIMAL NFT CENTER ========== */

    function createDRC721(NFT memory meta) external returns (address);
    function createDRC1155(NFT memory meta) external returns (address);
    function createDRC721Reserveless(
        NFT memory meta
    ) external returns (address);
    function createDRC1155Reserveless(
        NFT memory meta
    ) external returns (address);

    function getNFTBySalt(bytes memory salt) external view returns (address);
    function getNFTState(address nft) external view returns (NFTState memory);
    function beacon(
        NFTType nftType,
        bool withReserve
    ) external view returns (address);
    function implementation(
        NFTType nftType,
        bool withReserve
    ) external view returns (address);
    function getContractCenter() external view returns (address);
    function checkToken(address token) external view;

    function setContractCenter(address addressContractCenter) external;
    function upgrade(address newImpl, bytes memory data) external;
    function upgradeNFT(
        address newNFTImpl,
        NFTType nftType,
        bool withReserve
    ) external;
}


// File @decimal/nft-center/contracts/interfaces/IBeaconNFT.sol@v0.1.0

// Original license: SPDX_License_Identifier: MIT
pragma solidity 0.8.20;

interface IBeaconNFT {
    function NFT_CENTER() external view returns (IDecimalNFTCenter);
    function TYPE_NFT() external view returns (NFTType);
    function WITH_RESERVE() external view returns (bool);

    function implementation() external view returns (address);
}


// File @decimal/nft-center/contracts/BeaconNFT.sol@v0.1.0

// Original license: SPDX_License_Identifier: MIT
pragma solidity 0.8.20;


contract BeaconNFT is IBeaconNFT {
    IDecimalNFTCenter public immutable NFT_CENTER;
    NFTType public immutable TYPE_NFT;
    bool public immutable WITH_RESERVE;

    constructor(
        address initialNFTCenter,
        NFTType initialNFTType,
        bool initialWithReserve
    ) {
        NFT_CENTER = IDecimalNFTCenter(initialNFTCenter);
        TYPE_NFT = initialNFTType;
        WITH_RESERVE = initialWithReserve;
    }

    function implementation() public view virtual returns (address) {
        return NFT_CENTER.implementation(TYPE_NFT, WITH_RESERVE);
    }
}


// File @openzeppelin/contracts/proxy/Proxy.sol@v5.0.2

// Original license: SPDX_License_Identifier: MIT
// OpenZeppelin Contracts (last updated v5.0.0) (proxy/Proxy.sol)

pragma solidity ^0.8.20;

/**
 * @dev This abstract contract provides a fallback function that delegates all calls to another contract using the EVM
 * instruction `delegatecall`. We refer to the second contract as the _implementation_ behind the proxy, and it has to
 * be specified by overriding the virtual {_implementation} function.
 *
 * Additionally, delegation to the implementation can be triggered manually through the {_fallback} function, or to a
 * different contract through the {_delegate} function.
 *
 * The success and return data of the delegated call will be returned back to the caller of the proxy.
 */
abstract contract Proxy {
    /**
     * @dev Delegates the current call to `implementation`.
     *
     * This function does not return to its internal call site, it will return directly to the external caller.
     */
    function _delegate(address implementation) internal virtual {
        assembly {
            // Copy msg.data. We take full control of memory in this inline assembly
            // block because it will not return to Solidity code. We overwrite the
            // Solidity scratch pad at memory position 0.
            calldatacopy(0, 0, calldatasize())

            // Call the implementation.
            // out and outsize are 0 because we don't know the size yet.
            let result := delegatecall(gas(), implementation, 0, calldatasize(), 0, 0)

            // Copy the returned data.
            returndatacopy(0, 0, returndatasize())

            switch result
            // delegatecall returns 0 on error.
            case 0 {
                revert(0, returndatasize())
            }
            default {
                return(0, returndatasize())
            }
        }
    }

    /**
     * @dev This is a virtual function that should be overridden so it returns the address to which the fallback
     * function and {_fallback} should delegate.
     */
    function _implementation() internal view virtual returns (address);

    /**
     * @dev Delegates the current call to the address returned by `_implementation()`.
     *
     * This function does not return to its internal call site, it will return directly to the external caller.
     */
    function _fallback() internal virtual {
        _delegate(_implementation());
    }

    /**
     * @dev Fallback function that delegates calls to the address returned by `_implementation()`. Will run if no other
     * function in the contract matches the call data.
     */
    fallback() external payable virtual {
        _fallback();
    }
}


// File @openzeppelin/contracts/proxy/beacon/BeaconProxy.sol@v5.0.2

// Original license: SPDX_License_Identifier: MIT
// OpenZeppelin Contracts (last updated v5.0.0) (proxy/beacon/BeaconProxy.sol)

pragma solidity ^0.8.20;



/**
 * @dev This contract implements a proxy that gets the implementation address for each call from an {UpgradeableBeacon}.
 *
 * The beacon address can only be set once during construction, and cannot be changed afterwards. It is stored in an
 * immutable variable to avoid unnecessary storage reads, and also in the beacon storage slot specified by
 * https://eips.ethereum.org/EIPS/eip-1967[EIP1967] so that it can be accessed externally.
 *
 * CAUTION: Since the beacon address can never be changed, you must ensure that you either control the beacon, or trust
 * the beacon to not upgrade the implementation maliciously.
 *
 * IMPORTANT: Do not use the implementation logic to modify the beacon storage slot. Doing so would leave the proxy in
 * an inconsistent state where the beacon storage slot does not match the beacon address.
 */
contract BeaconProxy is Proxy {
    // An immutable address for the beacon to avoid unnecessary SLOADs before each delegate call.
    address private immutable _beacon;

    /**
     * @dev Initializes the proxy with `beacon`.
     *
     * If `data` is nonempty, it's used as data in a delegate call to the implementation returned by the beacon. This
     * will typically be an encoded function call, and allows initializing the storage of the proxy like a Solidity
     * constructor.
     *
     * Requirements:
     *
     * - `beacon` must be a contract with the interface {IBeacon}.
     * - If `data` is empty, `msg.value` must be zero.
     */
    constructor(address beacon, bytes memory data) payable {
        ERC1967Utils.upgradeBeaconToAndCall(beacon, data);
        _beacon = beacon;
    }

    /**
     * @dev Returns the current implementation address of the associated beacon.
     */
    function _implementation() internal view virtual override returns (address) {
        return IBeacon(_getBeacon()).implementation();
    }

    /**
     * @dev Returns the beacon.
     */
    function _getBeacon() internal view virtual returns (address) {
        return _beacon;
    }
}


// File @decimal/nft-center/contracts/DecimalNFTBeaconProxy.sol@v0.1.0

// Original license: SPDX_License_Identifier: MIT
pragma solidity 0.8.20;

contract DecimalNFTBeaconProxy is BeaconProxy {
    constructor(
        address _beacon,
        bytes memory _data
    ) payable BeaconProxy(_beacon, _data) {}

    function getBeacon() external view returns (address) {
        return _getBeacon();
    }

    receive() external payable {}
}


// File @openzeppelin/contracts/utils/introspection/IERC165.sol@v5.0.2

// Original license: SPDX_License_Identifier: MIT
// OpenZeppelin Contracts (last updated v5.0.0) (utils/introspection/IERC165.sol)

pragma solidity ^0.8.20;

/**
 * @dev Interface of the ERC165 standard, as defined in the
 * https://eips.ethereum.org/EIPS/eip-165[EIP].
 *
 * Implementers can declare support of contract interfaces, which can then be
 * queried by others ({ERC165Checker}).
 *
 * For an implementation, see {ERC165}.
 */
interface IERC165 {
    /**
     * @dev Returns true if this contract implements the interface defined by
     * `interfaceId`. See the corresponding
     * https://eips.ethereum.org/EIPS/eip-165#how-interfaces-are-identified[EIP section]
     * to learn more about how these ids are created.
     *
     * This function call must use less than 30 000 gas.
     */
    function supportsInterface(bytes4 interfaceId) external view returns (bool);
}


// File @openzeppelin/contracts/token/ERC1155/IERC1155.sol@v5.0.2

// Original license: SPDX_License_Identifier: MIT
// OpenZeppelin Contracts (last updated v5.0.1) (token/ERC1155/IERC1155.sol)

pragma solidity ^0.8.20;

/**
 * @dev Required interface of an ERC1155 compliant contract, as defined in the
 * https://eips.ethereum.org/EIPS/eip-1155[EIP].
 */
interface IERC1155 is IERC165 {
    /**
     * @dev Emitted when `value` amount of tokens of type `id` are transferred from `from` to `to` by `operator`.
     */
    event TransferSingle(address indexed operator, address indexed from, address indexed to, uint256 id, uint256 value);

    /**
     * @dev Equivalent to multiple {TransferSingle} events, where `operator`, `from` and `to` are the same for all
     * transfers.
     */
    event TransferBatch(
        address indexed operator,
        address indexed from,
        address indexed to,
        uint256[] ids,
        uint256[] values
    );

    /**
     * @dev Emitted when `account` grants or revokes permission to `operator` to transfer their tokens, according to
     * `approved`.
     */
    event ApprovalForAll(address indexed account, address indexed operator, bool approved);

    /**
     * @dev Emitted when the URI for token type `id` changes to `value`, if it is a non-programmatic URI.
     *
     * If an {URI} event was emitted for `id`, the standard
     * https://eips.ethereum.org/EIPS/eip-1155#metadata-extensions[guarantees] that `value` will equal the value
     * returned by {IERC1155MetadataURI-uri}.
     */
    event URI(string value, uint256 indexed id);

    /**
     * @dev Returns the value of tokens of token type `id` owned by `account`.
     *
     * Requirements:
     *
     * - `account` cannot be the zero address.
     */
    function balanceOf(address account, uint256 id) external view returns (uint256);

    /**
     * @dev xref:ROOT:erc1155.adoc#batch-operations[Batched] version of {balanceOf}.
     *
     * Requirements:
     *
     * - `accounts` and `ids` must have the same length.
     */
    function balanceOfBatch(
        address[] calldata accounts,
        uint256[] calldata ids
    ) external view returns (uint256[] memory);

    /**
     * @dev Grants or revokes permission to `operator` to transfer the caller's tokens, according to `approved`,
     *
     * Emits an {ApprovalForAll} event.
     *
     * Requirements:
     *
     * - `operator` cannot be the caller.
     */
    function setApprovalForAll(address operator, bool approved) external;

    /**
     * @dev Returns true if `operator` is approved to transfer ``account``'s tokens.
     *
     * See {setApprovalForAll}.
     */
    function isApprovedForAll(address account, address operator) external view returns (bool);

    /**
     * @dev Transfers a `value` amount of tokens of type `id` from `from` to `to`.
     *
     * WARNING: This function can potentially allow a reentrancy attack when transferring tokens
     * to an untrusted contract, when invoking {onERC1155Received} on the receiver.
     * Ensure to follow the checks-effects-interactions pattern and consider employing
     * reentrancy guards when interacting with untrusted contracts.
     *
     * Emits a {TransferSingle} event.
     *
     * Requirements:
     *
     * - `to` cannot be the zero address.
     * - If the caller is not `from`, it must have been approved to spend ``from``'s tokens via {setApprovalForAll}.
     * - `from` must have a balance of tokens of type `id` of at least `value` amount.
     * - If `to` refers to a smart contract, it must implement {IERC1155Receiver-onERC1155Received} and return the
     * acceptance magic value.
     */
    function safeTransferFrom(address from, address to, uint256 id, uint256 value, bytes calldata data) external;

    /**
     * @dev xref:ROOT:erc1155.adoc#batch-operations[Batched] version of {safeTransferFrom}.
     *
     * WARNING: This function can potentially allow a reentrancy attack when transferring tokens
     * to an untrusted contract, when invoking {onERC1155BatchReceived} on the receiver.
     * Ensure to follow the checks-effects-interactions pattern and consider employing
     * reentrancy guards when interacting with untrusted contracts.
     *
     * Emits either a {TransferSingle} or a {TransferBatch} event, depending on the length of the array arguments.
     *
     * Requirements:
     *
     * - `ids` and `values` must have the same length.
     * - If `to` refers to a smart contract, it must implement {IERC1155Receiver-onERC1155BatchReceived} and return the
     * acceptance magic value.
     */
    function safeBatchTransferFrom(
        address from,
        address to,
        uint256[] calldata ids,
        uint256[] calldata values,
        bytes calldata data
    ) external;
}


// File @decimal/nft-center/contracts/interfaces/IDRC1155Reserveless.sol@v0.1.0

// Original license: SPDX_License_Identifier: MIT

pragma solidity 0.8.20;

interface IDRC1155Reserveless is IERC1155 {
    struct NFTStorage {
        mapping(address owner => uint256 nonce) _nonces;
        string _name;
        string _symbol;
    }

    function mint(
        address recipient,
        uint256 tokenId,
        uint256 amountToMint,
        string memory tokenURI,
        uint256,
        address
    ) external;

    function burn(uint256 tokenId, uint256 amount) external;

    function name() external view returns (string memory);
    function symbol() external view returns (string memory);
    function nonces(address owner) external view returns (uint256);

    function permit(
        address owner,
        address spender,
        uint256 deadline,
        uint8 v,
        bytes32 r,
        bytes32 s
    ) external;
}


// File @decimal/nft-center/contracts/interfaces/IERC7572.sol@v0.1.0

// Original license: SPDX_License_Identifier: MIT
pragma solidity 0.8.20;

interface IERC7572 {
    function contractURI() external view returns (string memory);

    event ContractURIUpdated();
}


// File @decimal/nft-center/contracts/interfaces/INFTCommon.sol@v0.1.0

// Original license: SPDX_License_Identifier: MIT
pragma solidity 0.8.20;

interface INFTCommon {
    /* ========== ERRORS ========== */

    error NotOwner();
    error PermitExpired();
    error PermitInvalidSpender();
    error PermitUnauthorized();
    error PermitInvalidSignature();
    error MintNotAllowed();
    error OnlyCreator();
    error BurnDisabled();

    /* ========== EVENTS ========== */

    event DisabledMint();

    /* ========== METHODS ========== */

    function initialize(
        string memory initialSymbol,
        string memory initialName,
        string memory contractURI,
        address initialCreator,
        bool initialRefundable,
        bool initialBurnable
    ) external;

    function withReserve() external returns (bool);

    function getNFTType() external pure returns (NFTType);

    function DOMAIN_SEPARATOR() external view returns (bytes32);

    struct NFTCommonStorage {
        string _contractURI;
        address _creator;
        bool _allowMint;
        bool _burnable;
        string _version;
    }

    function disableMint() external;
    function getBurnable() external view returns (bool);
    function creator() external view returns (address);
    function getAllowMint() external view returns (bool);
}


// File @decimal/nft-center/contracts/NFTCommon.sol@v0.1.0

// Original license: SPDX_License_Identifier: MIT
pragma solidity 0.8.20;



abstract contract NFTCommon is INFTCommon, IERC7572, ContextUpgradeable {
    /* ========== STORAGE ========== */

    // keccak256(abi.encode(uint256(keccak256("DECIMAL_NFT_COMMON_STORAGE_LOCATION")) - 1)) & ~bytes32(uint256(0xff));
    bytes32 private constant DECIMAL_NFT_COMMON_STORAGE_LOCATION =
        0xdbcc0f1deb48fb93df0827a612a633dfca175380300989db7fc4b63e1da35e00;

    function _getNFTCommonStorage()
        internal
        pure
        returns (NFTCommonStorage storage $)
    {
        assembly {
            $.slot := DECIMAL_NFT_COMMON_STORAGE_LOCATION
        }
    }

    /* ========== METHODS ========== */

    function _setCreator(address newCreator) internal onlyInitializing {
        NFTCommonStorage storage $ = _getNFTCommonStorage();
        $._creator = newCreator;
    }

    function _setBurnable(bool newBurnable) internal onlyInitializing {
        NFTCommonStorage storage $ = _getNFTCommonStorage();
        $._burnable = newBurnable;
    }

    function _setContractURI(
        string memory newContractURI
    ) internal onlyInitializing {
        NFTCommonStorage storage $ = _getNFTCommonStorage();
        $._contractURI = newContractURI;
        emit ContractURIUpdated();
    }

    function disableMint() public virtual onlyCreator {
        _getNFTCommonStorage()._allowMint = false;
        emit DisabledMint();
    }

    modifier onlyCreator() {
        if (_msgSender() != creator()) {
            revert OnlyCreator();
        }
        _;
    }

    /* ========== GETTERS ========== */

    function getBurnable() public view returns (bool) {
        return _getNFTCommonStorage()._burnable;
    }

    function contractURI() external view returns (string memory) {
        return _getNFTCommonStorage()._contractURI;
    }

    function creator() public view returns (address) {
        return _getNFTCommonStorage()._creator;
    }

    function getAllowMint() public view returns (bool) {
        return _getNFTCommonStorage()._allowMint;
    }
}


// File @openzeppelin/contracts-upgradeable/utils/introspection/ERC165Upgradeable.sol@v5.0.2

// Original license: SPDX_License_Identifier: MIT
// OpenZeppelin Contracts (last updated v5.0.0) (utils/introspection/ERC165.sol)

pragma solidity ^0.8.20;


/**
 * @dev Implementation of the {IERC165} interface.
 *
 * Contracts that want to implement ERC165 should inherit from this contract and override {supportsInterface} to check
 * for the additional interface id that will be supported. For example:
 *
 * ```solidity
 * function supportsInterface(bytes4 interfaceId) public view virtual override returns (bool) {
 *     return interfaceId == type(MyInterface).interfaceId || super.supportsInterface(interfaceId);
 * }
 * ```
 */
abstract contract ERC165Upgradeable is Initializable, IERC165 {
    function __ERC165_init() internal onlyInitializing {
    }

    function __ERC165_init_unchained() internal onlyInitializing {
    }
    /**
     * @dev See {IERC165-supportsInterface}.
     */
    function supportsInterface(bytes4 interfaceId) public view virtual returns (bool) {
        return interfaceId == type(IERC165).interfaceId;
    }
}


// File @openzeppelin/contracts/interfaces/draft-IERC6093.sol@v5.0.2

// Original license: SPDX_License_Identifier: MIT
// OpenZeppelin Contracts (last updated v5.0.0) (interfaces/draft-IERC6093.sol)
pragma solidity ^0.8.20;

/**
 * @dev Standard ERC20 Errors
 * Interface of the https://eips.ethereum.org/EIPS/eip-6093[ERC-6093] custom errors for ERC20 tokens.
 */
interface IERC20Errors {
    /**
     * @dev Indicates an error related to the current `balance` of a `sender`. Used in transfers.
     * @param sender Address whose tokens are being transferred.
     * @param balance Current balance for the interacting account.
     * @param needed Minimum amount required to perform a transfer.
     */
    error ERC20InsufficientBalance(address sender, uint256 balance, uint256 needed);

    /**
     * @dev Indicates a failure with the token `sender`. Used in transfers.
     * @param sender Address whose tokens are being transferred.
     */
    error ERC20InvalidSender(address sender);

    /**
     * @dev Indicates a failure with the token `receiver`. Used in transfers.
     * @param receiver Address to which tokens are being transferred.
     */
    error ERC20InvalidReceiver(address receiver);

    /**
     * @dev Indicates a failure with the `spender`’s `allowance`. Used in transfers.
     * @param spender Address that may be allowed to operate on tokens without being their owner.
     * @param allowance Amount of tokens a `spender` is allowed to operate with.
     * @param needed Minimum amount required to perform a transfer.
     */
    error ERC20InsufficientAllowance(address spender, uint256 allowance, uint256 needed);

    /**
     * @dev Indicates a failure with the `approver` of a token to be approved. Used in approvals.
     * @param approver Address initiating an approval operation.
     */
    error ERC20InvalidApprover(address approver);

    /**
     * @dev Indicates a failure with the `spender` to be approved. Used in approvals.
     * @param spender Address that may be allowed to operate on tokens without being their owner.
     */
    error ERC20InvalidSpender(address spender);
}

/**
 * @dev Standard ERC721 Errors
 * Interface of the https://eips.ethereum.org/EIPS/eip-6093[ERC-6093] custom errors for ERC721 tokens.
 */
interface IERC721Errors {
    /**
     * @dev Indicates that an address can't be an owner. For example, `address(0)` is a forbidden owner in EIP-20.
     * Used in balance queries.
     * @param owner Address of the current owner of a token.
     */
    error ERC721InvalidOwner(address owner);

    /**
     * @dev Indicates a `tokenId` whose `owner` is the zero address.
     * @param tokenId Identifier number of a token.
     */
    error ERC721NonexistentToken(uint256 tokenId);

    /**
     * @dev Indicates an error related to the ownership over a particular token. Used in transfers.
     * @param sender Address whose tokens are being transferred.
     * @param tokenId Identifier number of a token.
     * @param owner Address of the current owner of a token.
     */
    error ERC721IncorrectOwner(address sender, uint256 tokenId, address owner);

    /**
     * @dev Indicates a failure with the token `sender`. Used in transfers.
     * @param sender Address whose tokens are being transferred.
     */
    error ERC721InvalidSender(address sender);

    /**
     * @dev Indicates a failure with the token `receiver`. Used in transfers.
     * @param receiver Address to which tokens are being transferred.
     */
    error ERC721InvalidReceiver(address receiver);

    /**
     * @dev Indicates a failure with the `operator`’s approval. Used in transfers.
     * @param operator Address that may be allowed to operate on tokens without being their owner.
     * @param tokenId Identifier number of a token.
     */
    error ERC721InsufficientApproval(address operator, uint256 tokenId);

    /**
     * @dev Indicates a failure with the `approver` of a token to be approved. Used in approvals.
     * @param approver Address initiating an approval operation.
     */
    error ERC721InvalidApprover(address approver);

    /**
     * @dev Indicates a failure with the `operator` to be approved. Used in approvals.
     * @param operator Address that may be allowed to operate on tokens without being their owner.
     */
    error ERC721InvalidOperator(address operator);
}

/**
 * @dev Standard ERC1155 Errors
 * Interface of the https://eips.ethereum.org/EIPS/eip-6093[ERC-6093] custom errors for ERC1155 tokens.
 */
interface IERC1155Errors {
    /**
     * @dev Indicates an error related to the current `balance` of a `sender`. Used in transfers.
     * @param sender Address whose tokens are being transferred.
     * @param balance Current balance for the interacting account.
     * @param needed Minimum amount required to perform a transfer.
     * @param tokenId Identifier number of a token.
     */
    error ERC1155InsufficientBalance(address sender, uint256 balance, uint256 needed, uint256 tokenId);

    /**
     * @dev Indicates a failure with the token `sender`. Used in transfers.
     * @param sender Address whose tokens are being transferred.
     */
    error ERC1155InvalidSender(address sender);

    /**
     * @dev Indicates a failure with the token `receiver`. Used in transfers.
     * @param receiver Address to which tokens are being transferred.
     */
    error ERC1155InvalidReceiver(address receiver);

    /**
     * @dev Indicates a failure with the `operator`’s approval. Used in transfers.
     * @param operator Address that may be allowed to operate on tokens without being their owner.
     * @param owner Address of the current owner of a token.
     */
    error ERC1155MissingApprovalForAll(address operator, address owner);

    /**
     * @dev Indicates a failure with the `approver` of a token to be approved. Used in approvals.
     * @param approver Address initiating an approval operation.
     */
    error ERC1155InvalidApprover(address approver);

    /**
     * @dev Indicates a failure with the `operator` to be approved. Used in approvals.
     * @param operator Address that may be allowed to operate on tokens without being their owner.
     */
    error ERC1155InvalidOperator(address operator);

    /**
     * @dev Indicates an array length mismatch between ids and values in a safeBatchTransferFrom operation.
     * Used in batch transfers.
     * @param idsLength Length of the array of token identifiers
     * @param valuesLength Length of the array of token amounts
     */
    error ERC1155InvalidArrayLength(uint256 idsLength, uint256 valuesLength);
}


// File @openzeppelin/contracts/token/ERC1155/extensions/IERC1155MetadataURI.sol@v5.0.2

// Original license: SPDX_License_Identifier: MIT
// OpenZeppelin Contracts (last updated v5.0.0) (token/ERC1155/extensions/IERC1155MetadataURI.sol)

pragma solidity ^0.8.20;

/**
 * @dev Interface of the optional ERC1155MetadataExtension interface, as defined
 * in the https://eips.ethereum.org/EIPS/eip-1155#metadata-extensions[EIP].
 */
interface IERC1155MetadataURI is IERC1155 {
    /**
     * @dev Returns the URI for token type `id`.
     *
     * If the `\{id\}` substring is present in the URI, it must be replaced by
     * clients with the actual token type ID.
     */
    function uri(uint256 id) external view returns (string memory);
}


// File @openzeppelin/contracts/token/ERC1155/IERC1155Receiver.sol@v5.0.2

// Original license: SPDX_License_Identifier: MIT
// OpenZeppelin Contracts (last updated v5.0.0) (token/ERC1155/IERC1155Receiver.sol)

pragma solidity ^0.8.20;

/**
 * @dev Interface that must be implemented by smart contracts in order to receive
 * ERC-1155 token transfers.
 */
interface IERC1155Receiver is IERC165 {
    /**
     * @dev Handles the receipt of a single ERC1155 token type. This function is
     * called at the end of a `safeTransferFrom` after the balance has been updated.
     *
     * NOTE: To accept the transfer, this must return
     * `bytes4(keccak256("onERC1155Received(address,address,uint256,uint256,bytes)"))`
     * (i.e. 0xf23a6e61, or its own function selector).
     *
     * @param operator The address which initiated the transfer (i.e. msg.sender)
     * @param from The address which previously owned the token
     * @param id The ID of the token being transferred
     * @param value The amount of tokens being transferred
     * @param data Additional data with no specified format
     * @return `bytes4(keccak256("onERC1155Received(address,address,uint256,uint256,bytes)"))` if transfer is allowed
     */
    function onERC1155Received(
        address operator,
        address from,
        uint256 id,
        uint256 value,
        bytes calldata data
    ) external returns (bytes4);

    /**
     * @dev Handles the receipt of a multiple ERC1155 token types. This function
     * is called at the end of a `safeBatchTransferFrom` after the balances have
     * been updated.
     *
     * NOTE: To accept the transfer(s), this must return
     * `bytes4(keccak256("onERC1155BatchReceived(address,address,uint256[],uint256[],bytes)"))`
     * (i.e. 0xbc197c81, or its own function selector).
     *
     * @param operator The address which initiated the batch transfer (i.e. msg.sender)
     * @param from The address which previously owned the token
     * @param ids An array containing ids of each token being transferred (order and length must match values array)
     * @param values An array containing amounts of each token being transferred (order and length must match ids array)
     * @param data Additional data with no specified format
     * @return `bytes4(keccak256("onERC1155BatchReceived(address,address,uint256[],uint256[],bytes)"))` if transfer is allowed
     */
    function onERC1155BatchReceived(
        address operator,
        address from,
        uint256[] calldata ids,
        uint256[] calldata values,
        bytes calldata data
    ) external returns (bytes4);
}


// File @openzeppelin/contracts/utils/math/Math.sol@v5.0.2

// Original license: SPDX_License_Identifier: MIT
// OpenZeppelin Contracts (last updated v5.0.0) (utils/math/Math.sol)

pragma solidity ^0.8.20;

/**
 * @dev Standard math utilities missing in the Solidity language.
 */
library Math {
    /**
     * @dev Muldiv operation overflow.
     */
    error MathOverflowedMulDiv();

    enum Rounding {
        Floor, // Toward negative infinity
        Ceil, // Toward positive infinity
        Trunc, // Toward zero
        Expand // Away from zero
    }

    /**
     * @dev Returns the addition of two unsigned integers, with an overflow flag.
     */
    function tryAdd(uint256 a, uint256 b) internal pure returns (bool, uint256) {
        unchecked {
            uint256 c = a + b;
            if (c < a) return (false, 0);
            return (true, c);
        }
    }

    /**
     * @dev Returns the subtraction of two unsigned integers, with an overflow flag.
     */
    function trySub(uint256 a, uint256 b) internal pure returns (bool, uint256) {
        unchecked {
            if (b > a) return (false, 0);
            return (true, a - b);
        }
    }

    /**
     * @dev Returns the multiplication of two unsigned integers, with an overflow flag.
     */
    function tryMul(uint256 a, uint256 b) internal pure returns (bool, uint256) {
        unchecked {
            // Gas optimization: this is cheaper than requiring 'a' not being zero, but the
            // benefit is lost if 'b' is also tested.
            // See: https://github.com/OpenZeppelin/openzeppelin-contracts/pull/522
            if (a == 0) return (true, 0);
            uint256 c = a * b;
            if (c / a != b) return (false, 0);
            return (true, c);
        }
    }

    /**
     * @dev Returns the division of two unsigned integers, with a division by zero flag.
     */
    function tryDiv(uint256 a, uint256 b) internal pure returns (bool, uint256) {
        unchecked {
            if (b == 0) return (false, 0);
            return (true, a / b);
        }
    }

    /**
     * @dev Returns the remainder of dividing two unsigned integers, with a division by zero flag.
     */
    function tryMod(uint256 a, uint256 b) internal pure returns (bool, uint256) {
        unchecked {
            if (b == 0) return (false, 0);
            return (true, a % b);
        }
    }

    /**
     * @dev Returns the largest of two numbers.
     */
    function max(uint256 a, uint256 b) internal pure returns (uint256) {
        return a > b ? a : b;
    }

    /**
     * @dev Returns the smallest of two numbers.
     */
    function min(uint256 a, uint256 b) internal pure returns (uint256) {
        return a < b ? a : b;
    }

    /**
     * @dev Returns the average of two numbers. The result is rounded towards
     * zero.
     */
    function average(uint256 a, uint256 b) internal pure returns (uint256) {
        // (a + b) / 2 can overflow.
        return (a & b) + (a ^ b) / 2;
    }

    /**
     * @dev Returns the ceiling of the division of two numbers.
     *
     * This differs from standard division with `/` in that it rounds towards infinity instead
     * of rounding towards zero.
     */
    function ceilDiv(uint256 a, uint256 b) internal pure returns (uint256) {
        if (b == 0) {
            // Guarantee the same behavior as in a regular Solidity division.
            return a / b;
        }

        // (a + b - 1) / b can overflow on addition, so we distribute.
        return a == 0 ? 0 : (a - 1) / b + 1;
    }

    /**
     * @notice Calculates floor(x * y / denominator) with full precision. Throws if result overflows a uint256 or
     * denominator == 0.
     * @dev Original credit to Remco Bloemen under MIT license (https://xn--2-umb.com/21/muldiv) with further edits by
     * Uniswap Labs also under MIT license.
     */
    function mulDiv(uint256 x, uint256 y, uint256 denominator) internal pure returns (uint256 result) {
        unchecked {
            // 512-bit multiply [prod1 prod0] = x * y. Compute the product mod 2^256 and mod 2^256 - 1, then use
            // use the Chinese Remainder Theorem to reconstruct the 512 bit result. The result is stored in two 256
            // variables such that product = prod1 * 2^256 + prod0.
            uint256 prod0 = x * y; // Least significant 256 bits of the product
            uint256 prod1; // Most significant 256 bits of the product
            assembly {
                let mm := mulmod(x, y, not(0))
                prod1 := sub(sub(mm, prod0), lt(mm, prod0))
            }

            // Handle non-overflow cases, 256 by 256 division.
            if (prod1 == 0) {
                // Solidity will revert if denominator == 0, unlike the div opcode on its own.
                // The surrounding unchecked block does not change this fact.
                // See https://docs.soliditylang.org/en/latest/control-structures.html#checked-or-unchecked-arithmetic.
                return prod0 / denominator;
            }

            // Make sure the result is less than 2^256. Also prevents denominator == 0.
            if (denominator <= prod1) {
                revert MathOverflowedMulDiv();
            }

            ///////////////////////////////////////////////
            // 512 by 256 division.
            ///////////////////////////////////////////////

            // Make division exact by subtracting the remainder from [prod1 prod0].
            uint256 remainder;
            assembly {
                // Compute remainder using mulmod.
                remainder := mulmod(x, y, denominator)

                // Subtract 256 bit number from 512 bit number.
                prod1 := sub(prod1, gt(remainder, prod0))
                prod0 := sub(prod0, remainder)
            }

            // Factor powers of two out of denominator and compute largest power of two divisor of denominator.
            // Always >= 1. See https://cs.stackexchange.com/q/138556/92363.

            uint256 twos = denominator & (0 - denominator);
            assembly {
                // Divide denominator by twos.
                denominator := div(denominator, twos)

                // Divide [prod1 prod0] by twos.
                prod0 := div(prod0, twos)

                // Flip twos such that it is 2^256 / twos. If twos is zero, then it becomes one.
                twos := add(div(sub(0, twos), twos), 1)
            }

            // Shift in bits from prod1 into prod0.
            prod0 |= prod1 * twos;

            // Invert denominator mod 2^256. Now that denominator is an odd number, it has an inverse modulo 2^256 such
            // that denominator * inv = 1 mod 2^256. Compute the inverse by starting with a seed that is correct for
            // four bits. That is, denominator * inv = 1 mod 2^4.
            uint256 inverse = (3 * denominator) ^ 2;

            // Use the Newton-Raphson iteration to improve the precision. Thanks to Hensel's lifting lemma, this also
            // works in modular arithmetic, doubling the correct bits in each step.
            inverse *= 2 - denominator * inverse; // inverse mod 2^8
            inverse *= 2 - denominator * inverse; // inverse mod 2^16
            inverse *= 2 - denominator * inverse; // inverse mod 2^32
            inverse *= 2 - denominator * inverse; // inverse mod 2^64
            inverse *= 2 - denominator * inverse; // inverse mod 2^128
            inverse *= 2 - denominator * inverse; // inverse mod 2^256

            // Because the division is now exact we can divide by multiplying with the modular inverse of denominator.
            // This will give us the correct result modulo 2^256. Since the preconditions guarantee that the outcome is
            // less than 2^256, this is the final result. We don't need to compute the high bits of the result and prod1
            // is no longer required.
            result = prod0 * inverse;
            return result;
        }
    }

    /**
     * @notice Calculates x * y / denominator with full precision, following the selected rounding direction.
     */
    function mulDiv(uint256 x, uint256 y, uint256 denominator, Rounding rounding) internal pure returns (uint256) {
        uint256 result = mulDiv(x, y, denominator);
        if (unsignedRoundsUp(rounding) && mulmod(x, y, denominator) > 0) {
            result += 1;
        }
        return result;
    }

    /**
     * @dev Returns the square root of a number. If the number is not a perfect square, the value is rounded
     * towards zero.
     *
     * Inspired by Henry S. Warren, Jr.'s "Hacker's Delight" (Chapter 11).
     */
    function sqrt(uint256 a) internal pure returns (uint256) {
        if (a == 0) {
            return 0;
        }

        // For our first guess, we get the biggest power of 2 which is smaller than the square root of the target.
        //
        // We know that the "msb" (most significant bit) of our target number `a` is a power of 2 such that we have
        // `msb(a) <= a < 2*msb(a)`. This value can be written `msb(a)=2**k` with `k=log2(a)`.
        //
        // This can be rewritten `2**log2(a) <= a < 2**(log2(a) + 1)`
        // → `sqrt(2**k) <= sqrt(a) < sqrt(2**(k+1))`
        // → `2**(k/2) <= sqrt(a) < 2**((k+1)/2) <= 2**(k/2 + 1)`
        //
        // Consequently, `2**(log2(a) / 2)` is a good first approximation of `sqrt(a)` with at least 1 correct bit.
        uint256 result = 1 << (log2(a) >> 1);

        // At this point `result` is an estimation with one bit of precision. We know the true value is a uint128,
        // since it is the square root of a uint256. Newton's method converges quadratically (precision doubles at
        // every iteration). We thus need at most 7 iteration to turn our partial result with one bit of precision
        // into the expected uint128 result.
        unchecked {
            result = (result + a / result) >> 1;
            result = (result + a / result) >> 1;
            result = (result + a / result) >> 1;
            result = (result + a / result) >> 1;
            result = (result + a / result) >> 1;
            result = (result + a / result) >> 1;
            result = (result + a / result) >> 1;
            return min(result, a / result);
        }
    }

    /**
     * @notice Calculates sqrt(a), following the selected rounding direction.
     */
    function sqrt(uint256 a, Rounding rounding) internal pure returns (uint256) {
        unchecked {
            uint256 result = sqrt(a);
            return result + (unsignedRoundsUp(rounding) && result * result < a ? 1 : 0);
        }
    }

    /**
     * @dev Return the log in base 2 of a positive value rounded towards zero.
     * Returns 0 if given 0.
     */
    function log2(uint256 value) internal pure returns (uint256) {
        uint256 result = 0;
        unchecked {
            if (value >> 128 > 0) {
                value >>= 128;
                result += 128;
            }
            if (value >> 64 > 0) {
                value >>= 64;
                result += 64;
            }
            if (value >> 32 > 0) {
                value >>= 32;
                result += 32;
            }
            if (value >> 16 > 0) {
                value >>= 16;
                result += 16;
            }
            if (value >> 8 > 0) {
                value >>= 8;
                result += 8;
            }
            if (value >> 4 > 0) {
                value >>= 4;
                result += 4;
            }
            if (value >> 2 > 0) {
                value >>= 2;
                result += 2;
            }
            if (value >> 1 > 0) {
                result += 1;
            }
        }
        return result;
    }

    /**
     * @dev Return the log in base 2, following the selected rounding direction, of a positive value.
     * Returns 0 if given 0.
     */
    function log2(uint256 value, Rounding rounding) internal pure returns (uint256) {
        unchecked {
            uint256 result = log2(value);
            return result + (unsignedRoundsUp(rounding) && 1 << result < value ? 1 : 0);
        }
    }

    /**
     * @dev Return the log in base 10 of a positive value rounded towards zero.
     * Returns 0 if given 0.
     */
    function log10(uint256 value) internal pure returns (uint256) {
        uint256 result = 0;
        unchecked {
            if (value >= 10 ** 64) {
                value /= 10 ** 64;
                result += 64;
            }
            if (value >= 10 ** 32) {
                value /= 10 ** 32;
                result += 32;
            }
            if (value >= 10 ** 16) {
                value /= 10 ** 16;
                result += 16;
            }
            if (value >= 10 ** 8) {
                value /= 10 ** 8;
                result += 8;
            }
            if (value >= 10 ** 4) {
                value /= 10 ** 4;
                result += 4;
            }
            if (value >= 10 ** 2) {
                value /= 10 ** 2;
                result += 2;
            }
            if (value >= 10 ** 1) {
                result += 1;
            }
        }
        return result;
    }

    /**
     * @dev Return the log in base 10, following the selected rounding direction, of a positive value.
     * Returns 0 if given 0.
     */
    function log10(uint256 value, Rounding rounding) internal pure returns (uint256) {
        unchecked {
            uint256 result = log10(value);
            return result + (unsignedRoundsUp(rounding) && 10 ** result < value ? 1 : 0);
        }
    }

    /**
     * @dev Return the log in base 256 of a positive value rounded towards zero.
     * Returns 0 if given 0.
     *
     * Adding one to the result gives the number of pairs of hex symbols needed to represent `value` as a hex string.
     */
    function log256(uint256 value) internal pure returns (uint256) {
        uint256 result = 0;
        unchecked {
            if (value >> 128 > 0) {
                value >>= 128;
                result += 16;
            }
            if (value >> 64 > 0) {
                value >>= 64;
                result += 8;
            }
            if (value >> 32 > 0) {
                value >>= 32;
                result += 4;
            }
            if (value >> 16 > 0) {
                value >>= 16;
                result += 2;
            }
            if (value >> 8 > 0) {
                result += 1;
            }
        }
        return result;
    }

    /**
     * @dev Return the log in base 256, following the selected rounding direction, of a positive value.
     * Returns 0 if given 0.
     */
    function log256(uint256 value, Rounding rounding) internal pure returns (uint256) {
        unchecked {
            uint256 result = log256(value);
            return result + (unsignedRoundsUp(rounding) && 1 << (result << 3) < value ? 1 : 0);
        }
    }

    /**
     * @dev Returns whether a provided rounding mode is considered rounding up for unsigned integers.
     */
    function unsignedRoundsUp(Rounding rounding) internal pure returns (bool) {
        return uint8(rounding) % 2 == 1;
    }
}


// File @openzeppelin/contracts/utils/Arrays.sol@v5.0.2

// Original license: SPDX_License_Identifier: MIT
// OpenZeppelin Contracts (last updated v5.0.0) (utils/Arrays.sol)

pragma solidity ^0.8.20;


/**
 * @dev Collection of functions related to array types.
 */
library Arrays {
    using StorageSlot for bytes32;

    /**
     * @dev Searches a sorted `array` and returns the first index that contains
     * a value greater or equal to `element`. If no such index exists (i.e. all
     * values in the array are strictly less than `element`), the array length is
     * returned. Time complexity O(log n).
     *
     * `array` is expected to be sorted in ascending order, and to contain no
     * repeated elements.
     */
    function findUpperBound(uint256[] storage array, uint256 element) internal view returns (uint256) {
        uint256 low = 0;
        uint256 high = array.length;

        if (high == 0) {
            return 0;
        }

        while (low < high) {
            uint256 mid = Math.average(low, high);

            // Note that mid will always be strictly less than high (i.e. it will be a valid array index)
            // because Math.average rounds towards zero (it does integer division with truncation).
            if (unsafeAccess(array, mid).value > element) {
                high = mid;
            } else {
                low = mid + 1;
            }
        }

        // At this point `low` is the exclusive upper bound. We will return the inclusive upper bound.
        if (low > 0 && unsafeAccess(array, low - 1).value == element) {
            return low - 1;
        } else {
            return low;
        }
    }

    /**
     * @dev Access an array in an "unsafe" way. Skips solidity "index-out-of-range" check.
     *
     * WARNING: Only use if you are certain `pos` is lower than the array length.
     */
    function unsafeAccess(address[] storage arr, uint256 pos) internal pure returns (StorageSlot.AddressSlot storage) {
        bytes32 slot;
        // We use assembly to calculate the storage slot of the element at index `pos` of the dynamic array `arr`
        // following https://docs.soliditylang.org/en/v0.8.20/internals/layout_in_storage.html#mappings-and-dynamic-arrays.

        /// @solidity memory-safe-assembly
        assembly {
            mstore(0, arr.slot)
            slot := add(keccak256(0, 0x20), pos)
        }
        return slot.getAddressSlot();
    }

    /**
     * @dev Access an array in an "unsafe" way. Skips solidity "index-out-of-range" check.
     *
     * WARNING: Only use if you are certain `pos` is lower than the array length.
     */
    function unsafeAccess(bytes32[] storage arr, uint256 pos) internal pure returns (StorageSlot.Bytes32Slot storage) {
        bytes32 slot;
        // We use assembly to calculate the storage slot of the element at index `pos` of the dynamic array `arr`
        // following https://docs.soliditylang.org/en/v0.8.20/internals/layout_in_storage.html#mappings-and-dynamic-arrays.

        /// @solidity memory-safe-assembly
        assembly {
            mstore(0, arr.slot)
            slot := add(keccak256(0, 0x20), pos)
        }
        return slot.getBytes32Slot();
    }

    /**
     * @dev Access an array in an "unsafe" way. Skips solidity "index-out-of-range" check.
     *
     * WARNING: Only use if you are certain `pos` is lower than the array length.
     */
    function unsafeAccess(uint256[] storage arr, uint256 pos) internal pure returns (StorageSlot.Uint256Slot storage) {
        bytes32 slot;
        // We use assembly to calculate the storage slot of the element at index `pos` of the dynamic array `arr`
        // following https://docs.soliditylang.org/en/v0.8.20/internals/layout_in_storage.html#mappings-and-dynamic-arrays.

        /// @solidity memory-safe-assembly
        assembly {
            mstore(0, arr.slot)
            slot := add(keccak256(0, 0x20), pos)
        }
        return slot.getUint256Slot();
    }

    /**
     * @dev Access an array in an "unsafe" way. Skips solidity "index-out-of-range" check.
     *
     * WARNING: Only use if you are certain `pos` is lower than the array length.
     */
    function unsafeMemoryAccess(uint256[] memory arr, uint256 pos) internal pure returns (uint256 res) {
        assembly {
            res := mload(add(add(arr, 0x20), mul(pos, 0x20)))
        }
    }

    /**
     * @dev Access an array in an "unsafe" way. Skips solidity "index-out-of-range" check.
     *
     * WARNING: Only use if you are certain `pos` is lower than the array length.
     */
    function unsafeMemoryAccess(address[] memory arr, uint256 pos) internal pure returns (address res) {
        assembly {
            res := mload(add(add(arr, 0x20), mul(pos, 0x20)))
        }
    }
}


// File @openzeppelin/contracts-upgradeable/token/ERC1155/ERC1155Upgradeable.sol@v5.0.2

// Original license: SPDX_License_Identifier: MIT
// OpenZeppelin Contracts (last updated v5.0.0) (token/ERC1155/ERC1155.sol)

pragma solidity ^0.8.20;









/**
 * @dev Implementation of the basic standard multi-token.
 * See https://eips.ethereum.org/EIPS/eip-1155
 * Originally based on code by Enjin: https://github.com/enjin/erc-1155
 */
abstract contract ERC1155Upgradeable is Initializable, ContextUpgradeable, ERC165Upgradeable, IERC1155, IERC1155MetadataURI, IERC1155Errors {
    using Arrays for uint256[];
    using Arrays for address[];

    /// @custom:storage-location erc7201:openzeppelin.storage.ERC1155
    struct ERC1155Storage {
        mapping(uint256 id => mapping(address account => uint256)) _balances;

        mapping(address account => mapping(address operator => bool)) _operatorApprovals;

        // Used as the URI for all token types by relying on ID substitution, e.g. https://token-cdn-domain/{id}.json
        string _uri;
    }

    // keccak256(abi.encode(uint256(keccak256("openzeppelin.storage.ERC1155")) - 1)) & ~bytes32(uint256(0xff))
    bytes32 private constant ERC1155StorageLocation = 0x88be536d5240c274a3b1d3a1be54482fd9caa294f08c62a7cde569f49a3c4500;

    function _getERC1155Storage() private pure returns (ERC1155Storage storage $) {
        assembly {
            $.slot := ERC1155StorageLocation
        }
    }

    /**
     * @dev See {_setURI}.
     */
    function __ERC1155_init(string memory uri_) internal onlyInitializing {
        __ERC1155_init_unchained(uri_);
    }

    function __ERC1155_init_unchained(string memory uri_) internal onlyInitializing {
        _setURI(uri_);
    }

    /**
     * @dev See {IERC165-supportsInterface}.
     */
    function supportsInterface(bytes4 interfaceId) public view virtual override(ERC165Upgradeable, IERC165) returns (bool) {
        return
            interfaceId == type(IERC1155).interfaceId ||
            interfaceId == type(IERC1155MetadataURI).interfaceId ||
            super.supportsInterface(interfaceId);
    }

    /**
     * @dev See {IERC1155MetadataURI-uri}.
     *
     * This implementation returns the same URI for *all* token types. It relies
     * on the token type ID substitution mechanism
     * https://eips.ethereum.org/EIPS/eip-1155#metadata[defined in the EIP].
     *
     * Clients calling this function must replace the `\{id\}` substring with the
     * actual token type ID.
     */
    function uri(uint256 /* id */) public view virtual returns (string memory) {
        ERC1155Storage storage $ = _getERC1155Storage();
        return $._uri;
    }

    /**
     * @dev See {IERC1155-balanceOf}.
     */
    function balanceOf(address account, uint256 id) public view virtual returns (uint256) {
        ERC1155Storage storage $ = _getERC1155Storage();
        return $._balances[id][account];
    }

    /**
     * @dev See {IERC1155-balanceOfBatch}.
     *
     * Requirements:
     *
     * - `accounts` and `ids` must have the same length.
     */
    function balanceOfBatch(
        address[] memory accounts,
        uint256[] memory ids
    ) public view virtual returns (uint256[] memory) {
        if (accounts.length != ids.length) {
            revert ERC1155InvalidArrayLength(ids.length, accounts.length);
        }

        uint256[] memory batchBalances = new uint256[](accounts.length);

        for (uint256 i = 0; i < accounts.length; ++i) {
            batchBalances[i] = balanceOf(accounts.unsafeMemoryAccess(i), ids.unsafeMemoryAccess(i));
        }

        return batchBalances;
    }

    /**
     * @dev See {IERC1155-setApprovalForAll}.
     */
    function setApprovalForAll(address operator, bool approved) public virtual {
        _setApprovalForAll(_msgSender(), operator, approved);
    }

    /**
     * @dev See {IERC1155-isApprovedForAll}.
     */
    function isApprovedForAll(address account, address operator) public view virtual returns (bool) {
        ERC1155Storage storage $ = _getERC1155Storage();
        return $._operatorApprovals[account][operator];
    }

    /**
     * @dev See {IERC1155-safeTransferFrom}.
     */
    function safeTransferFrom(address from, address to, uint256 id, uint256 value, bytes memory data) public virtual {
        address sender = _msgSender();
        if (from != sender && !isApprovedForAll(from, sender)) {
            revert ERC1155MissingApprovalForAll(sender, from);
        }
        _safeTransferFrom(from, to, id, value, data);
    }

    /**
     * @dev See {IERC1155-safeBatchTransferFrom}.
     */
    function safeBatchTransferFrom(
        address from,
        address to,
        uint256[] memory ids,
        uint256[] memory values,
        bytes memory data
    ) public virtual {
        address sender = _msgSender();
        if (from != sender && !isApprovedForAll(from, sender)) {
            revert ERC1155MissingApprovalForAll(sender, from);
        }
        _safeBatchTransferFrom(from, to, ids, values, data);
    }

    /**
     * @dev Transfers a `value` amount of tokens of type `id` from `from` to `to`. Will mint (or burn) if `from`
     * (or `to`) is the zero address.
     *
     * Emits a {TransferSingle} event if the arrays contain one element, and {TransferBatch} otherwise.
     *
     * Requirements:
     *
     * - If `to` refers to a smart contract, it must implement either {IERC1155Receiver-onERC1155Received}
     *   or {IERC1155Receiver-onERC1155BatchReceived} and return the acceptance magic value.
     * - `ids` and `values` must have the same length.
     *
     * NOTE: The ERC-1155 acceptance check is not performed in this function. See {_updateWithAcceptanceCheck} instead.
     */
    function _update(address from, address to, uint256[] memory ids, uint256[] memory values) internal virtual {
        ERC1155Storage storage $ = _getERC1155Storage();
        if (ids.length != values.length) {
            revert ERC1155InvalidArrayLength(ids.length, values.length);
        }

        address operator = _msgSender();

        for (uint256 i = 0; i < ids.length; ++i) {
            uint256 id = ids.unsafeMemoryAccess(i);
            uint256 value = values.unsafeMemoryAccess(i);

            if (from != address(0)) {
                uint256 fromBalance = $._balances[id][from];
                if (fromBalance < value) {
                    revert ERC1155InsufficientBalance(from, fromBalance, value, id);
                }
                unchecked {
                    // Overflow not possible: value <= fromBalance
                    $._balances[id][from] = fromBalance - value;
                }
            }

            if (to != address(0)) {
                $._balances[id][to] += value;
            }
        }

        if (ids.length == 1) {
            uint256 id = ids.unsafeMemoryAccess(0);
            uint256 value = values.unsafeMemoryAccess(0);
            emit TransferSingle(operator, from, to, id, value);
        } else {
            emit TransferBatch(operator, from, to, ids, values);
        }
    }

    /**
     * @dev Version of {_update} that performs the token acceptance check by calling
     * {IERC1155Receiver-onERC1155Received} or {IERC1155Receiver-onERC1155BatchReceived} on the receiver address if it
     * contains code (eg. is a smart contract at the moment of execution).
     *
     * IMPORTANT: Overriding this function is discouraged because it poses a reentrancy risk from the receiver. So any
     * update to the contract state after this function would break the check-effect-interaction pattern. Consider
     * overriding {_update} instead.
     */
    function _updateWithAcceptanceCheck(
        address from,
        address to,
        uint256[] memory ids,
        uint256[] memory values,
        bytes memory data
    ) internal virtual {
        _update(from, to, ids, values);
        if (to != address(0)) {
            address operator = _msgSender();
            if (ids.length == 1) {
                uint256 id = ids.unsafeMemoryAccess(0);
                uint256 value = values.unsafeMemoryAccess(0);
                _doSafeTransferAcceptanceCheck(operator, from, to, id, value, data);
            } else {
                _doSafeBatchTransferAcceptanceCheck(operator, from, to, ids, values, data);
            }
        }
    }

    /**
     * @dev Transfers a `value` tokens of token type `id` from `from` to `to`.
     *
     * Emits a {TransferSingle} event.
     *
     * Requirements:
     *
     * - `to` cannot be the zero address.
     * - `from` must have a balance of tokens of type `id` of at least `value` amount.
     * - If `to` refers to a smart contract, it must implement {IERC1155Receiver-onERC1155Received} and return the
     * acceptance magic value.
     */
    function _safeTransferFrom(address from, address to, uint256 id, uint256 value, bytes memory data) internal {
        if (to == address(0)) {
            revert ERC1155InvalidReceiver(address(0));
        }
        if (from == address(0)) {
            revert ERC1155InvalidSender(address(0));
        }
        (uint256[] memory ids, uint256[] memory values) = _asSingletonArrays(id, value);
        _updateWithAcceptanceCheck(from, to, ids, values, data);
    }

    /**
     * @dev xref:ROOT:erc1155.adoc#batch-operations[Batched] version of {_safeTransferFrom}.
     *
     * Emits a {TransferBatch} event.
     *
     * Requirements:
     *
     * - If `to` refers to a smart contract, it must implement {IERC1155Receiver-onERC1155BatchReceived} and return the
     * acceptance magic value.
     * - `ids` and `values` must have the same length.
     */
    function _safeBatchTransferFrom(
        address from,
        address to,
        uint256[] memory ids,
        uint256[] memory values,
        bytes memory data
    ) internal {
        if (to == address(0)) {
            revert ERC1155InvalidReceiver(address(0));
        }
        if (from == address(0)) {
            revert ERC1155InvalidSender(address(0));
        }
        _updateWithAcceptanceCheck(from, to, ids, values, data);
    }

    /**
     * @dev Sets a new URI for all token types, by relying on the token type ID
     * substitution mechanism
     * https://eips.ethereum.org/EIPS/eip-1155#metadata[defined in the EIP].
     *
     * By this mechanism, any occurrence of the `\{id\}` substring in either the
     * URI or any of the values in the JSON file at said URI will be replaced by
     * clients with the token type ID.
     *
     * For example, the `https://token-cdn-domain/\{id\}.json` URI would be
     * interpreted by clients as
     * `https://token-cdn-domain/000000000000000000000000000000000000000000000000000000000004cce0.json`
     * for token type ID 0x4cce0.
     *
     * See {uri}.
     *
     * Because these URIs cannot be meaningfully represented by the {URI} event,
     * this function emits no events.
     */
    function _setURI(string memory newuri) internal virtual {
        ERC1155Storage storage $ = _getERC1155Storage();
        $._uri = newuri;
    }

    /**
     * @dev Creates a `value` amount of tokens of type `id`, and assigns them to `to`.
     *
     * Emits a {TransferSingle} event.
     *
     * Requirements:
     *
     * - `to` cannot be the zero address.
     * - If `to` refers to a smart contract, it must implement {IERC1155Receiver-onERC1155Received} and return the
     * acceptance magic value.
     */
    function _mint(address to, uint256 id, uint256 value, bytes memory data) internal {
        if (to == address(0)) {
            revert ERC1155InvalidReceiver(address(0));
        }
        (uint256[] memory ids, uint256[] memory values) = _asSingletonArrays(id, value);
        _updateWithAcceptanceCheck(address(0), to, ids, values, data);
    }

    /**
     * @dev xref:ROOT:erc1155.adoc#batch-operations[Batched] version of {_mint}.
     *
     * Emits a {TransferBatch} event.
     *
     * Requirements:
     *
     * - `ids` and `values` must have the same length.
     * - `to` cannot be the zero address.
     * - If `to` refers to a smart contract, it must implement {IERC1155Receiver-onERC1155BatchReceived} and return the
     * acceptance magic value.
     */
    function _mintBatch(address to, uint256[] memory ids, uint256[] memory values, bytes memory data) internal {
        if (to == address(0)) {
            revert ERC1155InvalidReceiver(address(0));
        }
        _updateWithAcceptanceCheck(address(0), to, ids, values, data);
    }

    /**
     * @dev Destroys a `value` amount of tokens of type `id` from `from`
     *
     * Emits a {TransferSingle} event.
     *
     * Requirements:
     *
     * - `from` cannot be the zero address.
     * - `from` must have at least `value` amount of tokens of type `id`.
     */
    function _burn(address from, uint256 id, uint256 value) internal {
        if (from == address(0)) {
            revert ERC1155InvalidSender(address(0));
        }
        (uint256[] memory ids, uint256[] memory values) = _asSingletonArrays(id, value);
        _updateWithAcceptanceCheck(from, address(0), ids, values, "");
    }

    /**
     * @dev xref:ROOT:erc1155.adoc#batch-operations[Batched] version of {_burn}.
     *
     * Emits a {TransferBatch} event.
     *
     * Requirements:
     *
     * - `from` cannot be the zero address.
     * - `from` must have at least `value` amount of tokens of type `id`.
     * - `ids` and `values` must have the same length.
     */
    function _burnBatch(address from, uint256[] memory ids, uint256[] memory values) internal {
        if (from == address(0)) {
            revert ERC1155InvalidSender(address(0));
        }
        _updateWithAcceptanceCheck(from, address(0), ids, values, "");
    }

    /**
     * @dev Approve `operator` to operate on all of `owner` tokens
     *
     * Emits an {ApprovalForAll} event.
     *
     * Requirements:
     *
     * - `operator` cannot be the zero address.
     */
    function _setApprovalForAll(address owner, address operator, bool approved) internal virtual {
        ERC1155Storage storage $ = _getERC1155Storage();
        if (operator == address(0)) {
            revert ERC1155InvalidOperator(address(0));
        }
        $._operatorApprovals[owner][operator] = approved;
        emit ApprovalForAll(owner, operator, approved);
    }

    /**
     * @dev Performs an acceptance check by calling {IERC1155-onERC1155Received} on the `to` address
     * if it contains code at the moment of execution.
     */
    function _doSafeTransferAcceptanceCheck(
        address operator,
        address from,
        address to,
        uint256 id,
        uint256 value,
        bytes memory data
    ) private {
        if (to.code.length > 0) {
            try IERC1155Receiver(to).onERC1155Received(operator, from, id, value, data) returns (bytes4 response) {
                if (response != IERC1155Receiver.onERC1155Received.selector) {
                    // Tokens rejected
                    revert ERC1155InvalidReceiver(to);
                }
            } catch (bytes memory reason) {
                if (reason.length == 0) {
                    // non-ERC1155Receiver implementer
                    revert ERC1155InvalidReceiver(to);
                } else {
                    /// @solidity memory-safe-assembly
                    assembly {
                        revert(add(32, reason), mload(reason))
                    }
                }
            }
        }
    }

    /**
     * @dev Performs a batch acceptance check by calling {IERC1155-onERC1155BatchReceived} on the `to` address
     * if it contains code at the moment of execution.
     */
    function _doSafeBatchTransferAcceptanceCheck(
        address operator,
        address from,
        address to,
        uint256[] memory ids,
        uint256[] memory values,
        bytes memory data
    ) private {
        if (to.code.length > 0) {
            try IERC1155Receiver(to).onERC1155BatchReceived(operator, from, ids, values, data) returns (
                bytes4 response
            ) {
                if (response != IERC1155Receiver.onERC1155BatchReceived.selector) {
                    // Tokens rejected
                    revert ERC1155InvalidReceiver(to);
                }
            } catch (bytes memory reason) {
                if (reason.length == 0) {
                    // non-ERC1155Receiver implementer
                    revert ERC1155InvalidReceiver(to);
                } else {
                    /// @solidity memory-safe-assembly
                    assembly {
                        revert(add(32, reason), mload(reason))
                    }
                }
            }
        }
    }

    /**
     * @dev Creates an array in memory with only one value for each of the elements provided.
     */
    function _asSingletonArrays(
        uint256 element1,
        uint256 element2
    ) private pure returns (uint256[] memory array1, uint256[] memory array2) {
        /// @solidity memory-safe-assembly
        assembly {
            // Load the free memory pointer
            array1 := mload(0x40)
            // Set array length to 1
            mstore(array1, 1)
            // Store the single element at the next word after the length (where content starts)
            mstore(add(array1, 0x20), element1)

            // Repeat for next array locating it right after the first array
            array2 := add(array1, 0x40)
            mstore(array2, 1)
            mstore(add(array2, 0x20), element2)

            // Update the free memory pointer by pointing after the second array
            mstore(0x40, add(array2, 0x40))
        }
    }
}


// File @openzeppelin/contracts-upgradeable/token/ERC1155/extensions/ERC1155SupplyUpgradeable.sol@v5.0.2

// Original license: SPDX_License_Identifier: MIT
// OpenZeppelin Contracts (last updated v5.0.0) (token/ERC1155/extensions/ERC1155Supply.sol)

pragma solidity ^0.8.20;


/**
 * @dev Extension of ERC1155 that adds tracking of total supply per id.
 *
 * Useful for scenarios where Fungible and Non-fungible tokens have to be
 * clearly identified. Note: While a totalSupply of 1 might mean the
 * corresponding is an NFT, there is no guarantees that no other token with the
 * same id are not going to be minted.
 *
 * NOTE: This contract implies a global limit of 2**256 - 1 to the number of tokens
 * that can be minted.
 *
 * CAUTION: This extension should not be added in an upgrade to an already deployed contract.
 */
abstract contract ERC1155SupplyUpgradeable is Initializable, ERC1155Upgradeable {
    /// @custom:storage-location erc7201:openzeppelin.storage.ERC1155Supply
    struct ERC1155SupplyStorage {
        mapping(uint256 id => uint256) _totalSupply;
        uint256 _totalSupplyAll;
    }

    // keccak256(abi.encode(uint256(keccak256("openzeppelin.storage.ERC1155Supply")) - 1)) & ~bytes32(uint256(0xff))
    bytes32 private constant ERC1155SupplyStorageLocation = 0x4a593662ee04d27b6a00ebb31be7fe0c102c2ade82a7c5d764f2df05dc4e2800;

    function _getERC1155SupplyStorage() private pure returns (ERC1155SupplyStorage storage $) {
        assembly {
            $.slot := ERC1155SupplyStorageLocation
        }
    }

    function __ERC1155Supply_init() internal onlyInitializing {
    }

    function __ERC1155Supply_init_unchained() internal onlyInitializing {
    }
    /**
     * @dev Total value of tokens in with a given id.
     */
    function totalSupply(uint256 id) public view virtual returns (uint256) {
        ERC1155SupplyStorage storage $ = _getERC1155SupplyStorage();
        return $._totalSupply[id];
    }

    /**
     * @dev Total value of tokens.
     */
    function totalSupply() public view virtual returns (uint256) {
        ERC1155SupplyStorage storage $ = _getERC1155SupplyStorage();
        return $._totalSupplyAll;
    }

    /**
     * @dev Indicates whether any token exist with a given id, or not.
     */
    function exists(uint256 id) public view virtual returns (bool) {
        return totalSupply(id) > 0;
    }

    /**
     * @dev See {ERC1155-_update}.
     */
    function _update(
        address from,
        address to,
        uint256[] memory ids,
        uint256[] memory values
    ) internal virtual override {
        ERC1155SupplyStorage storage $ = _getERC1155SupplyStorage();
        super._update(from, to, ids, values);

        if (from == address(0)) {
            uint256 totalMintValue = 0;
            for (uint256 i = 0; i < ids.length; ++i) {
                uint256 value = values[i];
                // Overflow check required: The rest of the code assumes that totalSupply never overflows
                $._totalSupply[ids[i]] += value;
                totalMintValue += value;
            }
            // Overflow check required: The rest of the code assumes that totalSupplyAll never overflows
            $._totalSupplyAll += totalMintValue;
        }

        if (to == address(0)) {
            uint256 totalBurnValue = 0;
            for (uint256 i = 0; i < ids.length; ++i) {
                uint256 value = values[i];

                unchecked {
                    // Overflow not possible: values[i] <= balanceOf(from, ids[i]) <= totalSupply(ids[i])
                    $._totalSupply[ids[i]] -= value;
                    // Overflow not possible: sum_i(values[i]) <= sum_i(totalSupply(ids[i])) <= totalSupplyAll
                    totalBurnValue += value;
                }
            }
            unchecked {
                // Overflow not possible: totalBurnValue = sum_i(values[i]) <= sum_i(totalSupply(ids[i])) <= totalSupplyAll
                $._totalSupplyAll -= totalBurnValue;
            }
        }
    }
}


// File @openzeppelin/contracts/utils/math/SignedMath.sol@v5.0.2

// Original license: SPDX_License_Identifier: MIT
// OpenZeppelin Contracts (last updated v5.0.0) (utils/math/SignedMath.sol)

pragma solidity ^0.8.20;

/**
 * @dev Standard signed math utilities missing in the Solidity language.
 */
library SignedMath {
    /**
     * @dev Returns the largest of two signed numbers.
     */
    function max(int256 a, int256 b) internal pure returns (int256) {
        return a > b ? a : b;
    }

    /**
     * @dev Returns the smallest of two signed numbers.
     */
    function min(int256 a, int256 b) internal pure returns (int256) {
        return a < b ? a : b;
    }

    /**
     * @dev Returns the average of two signed numbers without overflow.
     * The result is rounded towards zero.
     */
    function average(int256 a, int256 b) internal pure returns (int256) {
        // Formula from the book "Hacker's Delight"
        int256 x = (a & b) + ((a ^ b) >> 1);
        return x + (int256(uint256(x) >> 255) & (a ^ b));
    }

    /**
     * @dev Returns the absolute unsigned value of a signed value.
     */
    function abs(int256 n) internal pure returns (uint256) {
        unchecked {
            // must be unchecked in order to support `n = type(int256).min`
            return uint256(n >= 0 ? n : -n);
        }
    }
}


// File @openzeppelin/contracts/utils/Strings.sol@v5.0.2

// Original license: SPDX_License_Identifier: MIT
// OpenZeppelin Contracts (last updated v5.0.0) (utils/Strings.sol)

pragma solidity ^0.8.20;


/**
 * @dev String operations.
 */
library Strings {
    bytes16 private constant HEX_DIGITS = "0123456789abcdef";
    uint8 private constant ADDRESS_LENGTH = 20;

    /**
     * @dev The `value` string doesn't fit in the specified `length`.
     */
    error StringsInsufficientHexLength(uint256 value, uint256 length);

    /**
     * @dev Converts a `uint256` to its ASCII `string` decimal representation.
     */
    function toString(uint256 value) internal pure returns (string memory) {
        unchecked {
            uint256 length = Math.log10(value) + 1;
            string memory buffer = new string(length);
            uint256 ptr;
            /// @solidity memory-safe-assembly
            assembly {
                ptr := add(buffer, add(32, length))
            }
            while (true) {
                ptr--;
                /// @solidity memory-safe-assembly
                assembly {
                    mstore8(ptr, byte(mod(value, 10), HEX_DIGITS))
                }
                value /= 10;
                if (value == 0) break;
            }
            return buffer;
        }
    }

    /**
     * @dev Converts a `int256` to its ASCII `string` decimal representation.
     */
    function toStringSigned(int256 value) internal pure returns (string memory) {
        return string.concat(value < 0 ? "-" : "", toString(SignedMath.abs(value)));
    }

    /**
     * @dev Converts a `uint256` to its ASCII `string` hexadecimal representation.
     */
    function toHexString(uint256 value) internal pure returns (string memory) {
        unchecked {
            return toHexString(value, Math.log256(value) + 1);
        }
    }

    /**
     * @dev Converts a `uint256` to its ASCII `string` hexadecimal representation with fixed length.
     */
    function toHexString(uint256 value, uint256 length) internal pure returns (string memory) {
        uint256 localValue = value;
        bytes memory buffer = new bytes(2 * length + 2);
        buffer[0] = "0";
        buffer[1] = "x";
        for (uint256 i = 2 * length + 1; i > 1; --i) {
            buffer[i] = HEX_DIGITS[localValue & 0xf];
            localValue >>= 4;
        }
        if (localValue != 0) {
            revert StringsInsufficientHexLength(value, length);
        }
        return string(buffer);
    }

    /**
     * @dev Converts an `address` with fixed length of 20 bytes to its not checksummed ASCII `string` hexadecimal
     * representation.
     */
    function toHexString(address addr) internal pure returns (string memory) {
        return toHexString(uint256(uint160(addr)), ADDRESS_LENGTH);
    }

    /**
     * @dev Returns true if the two strings are equal.
     */
    function equal(string memory a, string memory b) internal pure returns (bool) {
        return bytes(a).length == bytes(b).length && keccak256(bytes(a)) == keccak256(bytes(b));
    }
}


// File @openzeppelin/contracts-upgradeable/token/ERC1155/extensions/ERC1155URIStorageUpgradeable.sol@v5.0.2

// Original license: SPDX_License_Identifier: MIT
// OpenZeppelin Contracts (last updated v5.0.0) (token/ERC1155/extensions/ERC1155URIStorage.sol)

pragma solidity ^0.8.20;



/**
 * @dev ERC1155 token with storage based token URI management.
 * Inspired by the ERC721URIStorage extension
 */
abstract contract ERC1155URIStorageUpgradeable is Initializable, ERC1155Upgradeable {
    using Strings for uint256;

    /// @custom:storage-location erc7201:openzeppelin.storage.ERC1155URIStorage
    struct ERC1155URIStorageStorage {
        // Optional base URI
        string _baseURI;

        // Optional mapping for token URIs
        mapping(uint256 tokenId => string) _tokenURIs;
    }

    // keccak256(abi.encode(uint256(keccak256("openzeppelin.storage.ERC1155URIStorage")) - 1)) & ~bytes32(uint256(0xff))
    bytes32 private constant ERC1155URIStorageStorageLocation = 0x89fc852226e759c7c636cf34d732f0198fc56a54876b2374a52beb7b0c558600;

    function _getERC1155URIStorageStorage() private pure returns (ERC1155URIStorageStorage storage $) {
        assembly {
            $.slot := ERC1155URIStorageStorageLocation
        }
    }

    function __ERC1155URIStorage_init() internal onlyInitializing {
        __ERC1155URIStorage_init_unchained();
    }

    function __ERC1155URIStorage_init_unchained() internal onlyInitializing {
        ERC1155URIStorageStorage storage $ = _getERC1155URIStorageStorage();
        $._baseURI = "";
    }
    /**
     * @dev See {IERC1155MetadataURI-uri}.
     *
     * This implementation returns the concatenation of the `_baseURI`
     * and the token-specific uri if the latter is set
     *
     * This enables the following behaviors:
     *
     * - if `_tokenURIs[tokenId]` is set, then the result is the concatenation
     *   of `_baseURI` and `_tokenURIs[tokenId]` (keep in mind that `_baseURI`
     *   is empty per default);
     *
     * - if `_tokenURIs[tokenId]` is NOT set then we fallback to `super.uri()`
     *   which in most cases will contain `ERC1155._uri`;
     *
     * - if `_tokenURIs[tokenId]` is NOT set, and if the parents do not have a
     *   uri value set, then the result is empty.
     */
    function uri(uint256 tokenId) public view virtual override returns (string memory) {
        ERC1155URIStorageStorage storage $ = _getERC1155URIStorageStorage();
        string memory tokenURI = $._tokenURIs[tokenId];

        // If token URI is set, concatenate base URI and tokenURI (via string.concat).
        return bytes(tokenURI).length > 0 ? string.concat($._baseURI, tokenURI) : super.uri(tokenId);
    }

    /**
     * @dev Sets `tokenURI` as the tokenURI of `tokenId`.
     */
    function _setURI(uint256 tokenId, string memory tokenURI) internal virtual {
        ERC1155URIStorageStorage storage $ = _getERC1155URIStorageStorage();
        $._tokenURIs[tokenId] = tokenURI;
        emit URI(uri(tokenId), tokenId);
    }

    /**
     * @dev Sets `baseURI` as the `_baseURI` for all tokens
     */
    function _setBaseURI(string memory baseURI) internal virtual {
        ERC1155URIStorageStorage storage $ = _getERC1155URIStorageStorage();
        $._baseURI = baseURI;
    }
}


// File @openzeppelin/contracts/interfaces/IERC1271.sol@v5.0.2

// Original license: SPDX_License_Identifier: MIT
// OpenZeppelin Contracts (last updated v5.0.0) (interfaces/IERC1271.sol)

pragma solidity ^0.8.20;

/**
 * @dev Interface of the ERC1271 standard signature validation method for
 * contracts as defined in https://eips.ethereum.org/EIPS/eip-1271[ERC-1271].
 */
interface IERC1271 {
    /**
     * @dev Should return whether the signature provided is valid for the provided data
     * @param hash      Hash of the data to be signed
     * @param signature Signature byte array associated with _data
     */
    function isValidSignature(bytes32 hash, bytes memory signature) external view returns (bytes4 magicValue);
}


// File @decimal/nft-center/contracts/DRC1155Reserveless.sol@v0.1.0

// Original license: SPDX_License_Identifier: MIT
pragma solidity 0.8.20;







contract DRC1155Reserveless is
    NFTCommon,
    ERC1155SupplyUpgradeable,
    ERC1155URIStorageUpgradeable,
    IDRC1155Reserveless
{
    /* ========== STORAGE ========== */

    // keccak256(abi.encode(uint256(keccak256("DRC1155_STORAGE_LOCATION")) - 1)) & ~bytes32(uint256(0xff));
    bytes32 private constant DRC1155_STORAGE_LOCATION =
        0xe33a23e62772425d13924e9b300b603f656e7df5cea8cbeeb1d0a21ab276fc00;

    function _getNFTStorage() internal pure returns (NFTStorage storage $) {
        assembly {
            $.slot := DRC1155_STORAGE_LOCATION
        }
    }

    /* ========== METHODS ========== */

    function initialize(
        string memory initialSymbol,
        string memory initialName,
        string memory initialContractURI,
        address initialCreator,
        bool,
        bool initialBurnable
    ) public virtual initializer {
        __DRC1155Reserveless_init(
            initialSymbol,
            initialName,
            initialContractURI,
            initialCreator,
            false,
            initialBurnable
        );
    }

    function __DRC1155Reserveless_init(
        string memory initialSymbol,
        string memory initialName,
        string memory initialContractURI,
        address initialCreator,
        bool,
        bool initialBurnable
    ) internal onlyInitializing {
        __ERC1155_init("");
        __ERC1155URIStorage_init();

        _setCreator(initialCreator);
        _setContractURI(initialContractURI);
        _setBurnable(initialBurnable);

        NFTCommonStorage storage $ = _getNFTCommonStorage();

        $._allowMint = true;
        $._version = "1";

        _getNFTStorage()._name = initialName;
        _getNFTStorage()._symbol = initialSymbol;
    }

    function _update(
        address from,
        address to,
        uint256[] memory ids,
        uint256[] memory values
    ) internal virtual override(ERC1155Upgradeable, ERC1155SupplyUpgradeable) {
        if (to == address(0) && !getBurnable()) {
            revert BurnDisabled();
        }
        ERC1155SupplyUpgradeable._update(from, to, ids, values);
    }

    function uri(
        uint256 tokenId
    )
        public
        view
        virtual
        override(ERC1155Upgradeable, ERC1155URIStorageUpgradeable)
        returns (string memory)
    {
        return ERC1155URIStorageUpgradeable.uri(tokenId);
    }

    function mint(
        address recipient,
        uint256 tokenId,
        uint256 amountToMint,
        string memory tokenURI,
        uint256,
        address
    ) public virtual onlyCreator {
        if (!getAllowMint()) revert MintNotAllowed();
        _setURI(tokenId, tokenURI);
        _mint(recipient, tokenId, amountToMint, "");
    }

    function burn(uint256 tokenId, uint256 amount) external virtual {
        _burn(msg.sender, tokenId, amount);
    }

    function getNFTType() external pure returns (NFTType) {
        return NFTType.DRC1155;
    }

    /* ========== GETTERS ========== */

    function name() public view virtual returns (string memory) {
        return _getNFTStorage()._name;
    }

    function symbol() public view returns (string memory) {
        return _getNFTStorage()._symbol;
    }

    function nonces(address owner) external view virtual returns (uint256) {
        return _getNFTStorage()._nonces[owner];
    }

    /* ========== Permit ========== */

    // solhint-disable-next-line func-name-mixedcase
    function DOMAIN_SEPARATOR() public view virtual returns (bytes32) {
        return
            keccak256(
                abi.encode(
                    // keccak256('EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)')
                    0x8b73c3c69bb8fe3d512ecc4cf759cc79239f7b179b0ffacaa9a75d522b39400f,
                    keccak256(bytes(name())),
                    keccak256(bytes(_getNFTCommonStorage()._version)),
                    block.chainid,
                    address(this)
                )
            );
    }

    /// @dev Value is equal recipient keccak256("Permit(address spender,uint256 nonce,uint256 deadline)");
    bytes32 private constant PERMIT_TYPEHASH =
        0x29db08c6fa5ea37f368ae6abb1beb48d7730e405512bf86d618d142b0c7a8731;

    function permit(
        address owner,
        address spender,
        uint256 deadline,
        uint8 v,
        bytes32 r,
        bytes32 s
    ) external {
        if (block.timestamp > deadline) revert PermitExpired();

        bytes32 digest = keccak256(
            abi.encodePacked(
                "\x19\x01",
                DOMAIN_SEPARATOR(),
                keccak256(
                    abi.encode(
                        PERMIT_TYPEHASH,
                        spender,
                        _getNFTStorage()._nonces[owner]++,
                        deadline
                    )
                )
            )
        );
        if (spender == owner) revert PermitInvalidSpender();

        if (address(owner).code.length > 0) {
            if (
                IERC1271(owner).isValidSignature(
                    digest,
                    abi.encodePacked(r, s, v)
                ) != 0x1626ba7e
            ) revert PermitUnauthorized();
        } else {
            address recoveredAddress = ecrecover(digest, v, r, s);
            if (recoveredAddress == address(0)) revert PermitInvalidSignature();
            if (recoveredAddress != owner) revert PermitUnauthorized();
        }
        _setApprovalForAll(owner, spender, true);
    }

    function withReserve() external pure virtual returns (bool) {
        return false;
    }
}


// File @decimal/nft-center/contracts/interfaces/INFTReserve.sol@v0.1.0

// Original license: SPDX_License_Identifier: MIT
pragma solidity 0.8.20;

interface INFTReserve {
    /* ========== STRUCTS ========== */

    struct Reserve {
        address token;
        uint256 amount;
        ReserveType reserveType;
    }

    enum ReserveType {
        None,
        DEL,
        DRC20
    }

    /* ========== ERRORS ========== */

    error InvalidMinReserve();
    error InvalidReserve();
    error InvalidReserveType();
    error ReserveAlreadyInitialized();
    error OnlyNFTDelegation();
}


// File @decimal/nft-center/contracts/interfaces/IDRC1155.sol@v0.1.0

// Original license: SPDX_License_Identifier: MIT

pragma solidity 0.8.20;


interface IDRC1155 is IDRC1155Reserveless, INFTReserve {
    event ReserveUpdated(uint256 tokenId, uint256 totalSupply, Reserve reserve);

    function mintByDEL(
        address recipient,
        uint256 tokenId,
        uint256 amountToMint,
        string memory tokenURI
    ) external payable;

    function mintByPermit(
        address recipient,
        uint256 tokenId,
        uint256 amountToMint,
        string memory tokenURI,
        uint256 reserveAmount,
        address reserveToken,
        uint256 deadline,
        uint8 v,
        bytes32 r,
        bytes32 s
    ) external;

    function rate(uint256 tokenId) external view returns (uint256 amount);

    function penalty(
        uint256 tokenId,
        uint256 value,
        uint16 percentage
    ) external returns (uint256 penaltyAmount);
}


// File @openzeppelin/contracts/token/ERC20/IERC20.sol@v5.0.2

// Original license: SPDX_License_Identifier: MIT
// OpenZeppelin Contracts (last updated v5.0.0) (token/ERC20/IERC20.sol)

pragma solidity ^0.8.20;

/**
 * @dev Interface of the ERC20 standard as defined in the EIP.
 */
interface IERC20 {
    /**
     * @dev Emitted when `value` tokens are moved from one account (`from`) to
     * another (`to`).
     *
     * Note that `value` may be zero.
     */
    event Transfer(address indexed from, address indexed to, uint256 value);

    /**
     * @dev Emitted when the allowance of a `spender` for an `owner` is set by
     * a call to {approve}. `value` is the new allowance.
     */
    event Approval(address indexed owner, address indexed spender, uint256 value);

    /**
     * @dev Returns the value of tokens in existence.
     */
    function totalSupply() external view returns (uint256);

    /**
     * @dev Returns the value of tokens owned by `account`.
     */
    function balanceOf(address account) external view returns (uint256);

    /**
     * @dev Moves a `value` amount of tokens from the caller's account to `to`.
     *
     * Returns a boolean value indicating whether the operation succeeded.
     *
     * Emits a {Transfer} event.
     */
    function transfer(address to, uint256 value) external returns (bool);

    /**
     * @dev Returns the remaining number of tokens that `spender` will be
     * allowed to spend on behalf of `owner` through {transferFrom}. This is
     * zero by default.
     *
     * This value changes when {approve} or {transferFrom} are called.
     */
    function allowance(address owner, address spender) external view returns (uint256);

    /**
     * @dev Sets a `value` amount of tokens as the allowance of `spender` over the
     * caller's tokens.
     *
     * Returns a boolean value indicating whether the operation succeeded.
     *
     * IMPORTANT: Beware that changing an allowance with this method brings the risk
     * that someone may use both the old and the new allowance by unfortunate
     * transaction ordering. One possible solution to mitigate this race
     * condition is to first reduce the spender's allowance to 0 and set the
     * desired value afterwards:
     * https://github.com/ethereum/EIPs/issues/20#issuecomment-263524729
     *
     * Emits an {Approval} event.
     */
    function approve(address spender, uint256 value) external returns (bool);

    /**
     * @dev Moves a `value` amount of tokens from `from` to `to` using the
     * allowance mechanism. `value` is then deducted from the caller's
     * allowance.
     *
     * Returns a boolean value indicating whether the operation succeeded.
     *
     * Emits a {Transfer} event.
     */
    function transferFrom(address from, address to, uint256 value) external returns (bool);
}


// File @openzeppelin/contracts/token/ERC20/extensions/IERC20Metadata.sol@v5.0.2

// Original license: SPDX_License_Identifier: MIT
// OpenZeppelin Contracts (last updated v5.0.0) (token/ERC20/extensions/IERC20Metadata.sol)

pragma solidity ^0.8.20;

/**
 * @dev Interface for the optional metadata functions from the ERC20 standard.
 */
interface IERC20Metadata is IERC20 {
    /**
     * @dev Returns the name of the token.
     */
    function name() external view returns (string memory);

    /**
     * @dev Returns the symbol of the token.
     */
    function symbol() external view returns (string memory);

    /**
     * @dev Returns the decimals places of the token.
     */
    function decimals() external view returns (uint8);
}


// File @openzeppelin/contracts/utils/Context.sol@v5.0.2

// Original license: SPDX_License_Identifier: MIT
// OpenZeppelin Contracts (last updated v5.0.1) (utils/Context.sol)

pragma solidity ^0.8.20;

/**
 * @dev Provides information about the current execution context, including the
 * sender of the transaction and its data. While these are generally available
 * via msg.sender and msg.data, they should not be accessed in such a direct
 * manner, since when dealing with meta-transactions the account sending and
 * paying for execution may not be the actual sender (as far as an application
 * is concerned).
 *
 * This contract is only required for intermediate, library-like contracts.
 */
abstract contract Context {
    function _msgSender() internal view virtual returns (address) {
        return msg.sender;
    }

    function _msgData() internal view virtual returns (bytes calldata) {
        return msg.data;
    }

    function _contextSuffixLength() internal view virtual returns (uint256) {
        return 0;
    }
}


// File @openzeppelin/contracts/token/ERC20/ERC20.sol@v5.0.2

// Original license: SPDX_License_Identifier: MIT
// OpenZeppelin Contracts (last updated v5.0.0) (token/ERC20/ERC20.sol)

pragma solidity ^0.8.20;




/**
 * @dev Implementation of the {IERC20} interface.
 *
 * This implementation is agnostic to the way tokens are created. This means
 * that a supply mechanism has to be added in a derived contract using {_mint}.
 *
 * TIP: For a detailed writeup see our guide
 * https://forum.openzeppelin.com/t/how-to-implement-erc20-supply-mechanisms/226[How
 * to implement supply mechanisms].
 *
 * The default value of {decimals} is 18. To change this, you should override
 * this function so it returns a different value.
 *
 * We have followed general OpenZeppelin Contracts guidelines: functions revert
 * instead returning `false` on failure. This behavior is nonetheless
 * conventional and does not conflict with the expectations of ERC20
 * applications.
 *
 * Additionally, an {Approval} event is emitted on calls to {transferFrom}.
 * This allows applications to reconstruct the allowance for all accounts just
 * by listening to said events. Other implementations of the EIP may not emit
 * these events, as it isn't required by the specification.
 */
abstract contract ERC20 is Context, IERC20, IERC20Metadata, IERC20Errors {
    mapping(address account => uint256) private _balances;

    mapping(address account => mapping(address spender => uint256)) private _allowances;

    uint256 private _totalSupply;

    string private _name;
    string private _symbol;

    /**
     * @dev Sets the values for {name} and {symbol}.
     *
     * All two of these values are immutable: they can only be set once during
     * construction.
     */
    constructor(string memory name_, string memory symbol_) {
        _name = name_;
        _symbol = symbol_;
    }

    /**
     * @dev Returns the name of the token.
     */
    function name() public view virtual returns (string memory) {
        return _name;
    }

    /**
     * @dev Returns the symbol of the token, usually a shorter version of the
     * name.
     */
    function symbol() public view virtual returns (string memory) {
        return _symbol;
    }

    /**
     * @dev Returns the number of decimals used to get its user representation.
     * For example, if `decimals` equals `2`, a balance of `505` tokens should
     * be displayed to a user as `5.05` (`505 / 10 ** 2`).
     *
     * Tokens usually opt for a value of 18, imitating the relationship between
     * Ether and Wei. This is the default value returned by this function, unless
     * it's overridden.
     *
     * NOTE: This information is only used for _display_ purposes: it in
     * no way affects any of the arithmetic of the contract, including
     * {IERC20-balanceOf} and {IERC20-transfer}.
     */
    function decimals() public view virtual returns (uint8) {
        return 18;
    }

    /**
     * @dev See {IERC20-totalSupply}.
     */
    function totalSupply() public view virtual returns (uint256) {
        return _totalSupply;
    }

    /**
     * @dev See {IERC20-balanceOf}.
     */
    function balanceOf(address account) public view virtual returns (uint256) {
        return _balances[account];
    }

    /**
     * @dev See {IERC20-transfer}.
     *
     * Requirements:
     *
     * - `to` cannot be the zero address.
     * - the caller must have a balance of at least `value`.
     */
    function transfer(address to, uint256 value) public virtual returns (bool) {
        address owner = _msgSender();
        _transfer(owner, to, value);
        return true;
    }

    /**
     * @dev See {IERC20-allowance}.
     */
    function allowance(address owner, address spender) public view virtual returns (uint256) {
        return _allowances[owner][spender];
    }

    /**
     * @dev See {IERC20-approve}.
     *
     * NOTE: If `value` is the maximum `uint256`, the allowance is not updated on
     * `transferFrom`. This is semantically equivalent to an infinite approval.
     *
     * Requirements:
     *
     * - `spender` cannot be the zero address.
     */
    function approve(address spender, uint256 value) public virtual returns (bool) {
        address owner = _msgSender();
        _approve(owner, spender, value);
        return true;
    }

    /**
     * @dev See {IERC20-transferFrom}.
     *
     * Emits an {Approval} event indicating the updated allowance. This is not
     * required by the EIP. See the note at the beginning of {ERC20}.
     *
     * NOTE: Does not update the allowance if the current allowance
     * is the maximum `uint256`.
     *
     * Requirements:
     *
     * - `from` and `to` cannot be the zero address.
     * - `from` must have a balance of at least `value`.
     * - the caller must have allowance for ``from``'s tokens of at least
     * `value`.
     */
    function transferFrom(address from, address to, uint256 value) public virtual returns (bool) {
        address spender = _msgSender();
        _spendAllowance(from, spender, value);
        _transfer(from, to, value);
        return true;
    }

    /**
     * @dev Moves a `value` amount of tokens from `from` to `to`.
     *
     * This internal function is equivalent to {transfer}, and can be used to
     * e.g. implement automatic token fees, slashing mechanisms, etc.
     *
     * Emits a {Transfer} event.
     *
     * NOTE: This function is not virtual, {_update} should be overridden instead.
     */
    function _transfer(address from, address to, uint256 value) internal {
        if (from == address(0)) {
            revert ERC20InvalidSender(address(0));
        }
        if (to == address(0)) {
            revert ERC20InvalidReceiver(address(0));
        }
        _update(from, to, value);
    }

    /**
     * @dev Transfers a `value` amount of tokens from `from` to `to`, or alternatively mints (or burns) if `from`
     * (or `to`) is the zero address. All customizations to transfers, mints, and burns should be done by overriding
     * this function.
     *
     * Emits a {Transfer} event.
     */
    function _update(address from, address to, uint256 value) internal virtual {
        if (from == address(0)) {
            // Overflow check required: The rest of the code assumes that totalSupply never overflows
            _totalSupply += value;
        } else {
            uint256 fromBalance = _balances[from];
            if (fromBalance < value) {
                revert ERC20InsufficientBalance(from, fromBalance, value);
            }
            unchecked {
                // Overflow not possible: value <= fromBalance <= totalSupply.
                _balances[from] = fromBalance - value;
            }
        }

        if (to == address(0)) {
            unchecked {
                // Overflow not possible: value <= totalSupply or value <= fromBalance <= totalSupply.
                _totalSupply -= value;
            }
        } else {
            unchecked {
                // Overflow not possible: balance + value is at most totalSupply, which we know fits into a uint256.
                _balances[to] += value;
            }
        }

        emit Transfer(from, to, value);
    }

    /**
     * @dev Creates a `value` amount of tokens and assigns them to `account`, by transferring it from address(0).
     * Relies on the `_update` mechanism
     *
     * Emits a {Transfer} event with `from` set to the zero address.
     *
     * NOTE: This function is not virtual, {_update} should be overridden instead.
     */
    function _mint(address account, uint256 value) internal {
        if (account == address(0)) {
            revert ERC20InvalidReceiver(address(0));
        }
        _update(address(0), account, value);
    }

    /**
     * @dev Destroys a `value` amount of tokens from `account`, lowering the total supply.
     * Relies on the `_update` mechanism.
     *
     * Emits a {Transfer} event with `to` set to the zero address.
     *
     * NOTE: This function is not virtual, {_update} should be overridden instead
     */
    function _burn(address account, uint256 value) internal {
        if (account == address(0)) {
            revert ERC20InvalidSender(address(0));
        }
        _update(account, address(0), value);
    }

    /**
     * @dev Sets `value` as the allowance of `spender` over the `owner` s tokens.
     *
     * This internal function is equivalent to `approve`, and can be used to
     * e.g. set automatic allowances for certain subsystems, etc.
     *
     * Emits an {Approval} event.
     *
     * Requirements:
     *
     * - `owner` cannot be the zero address.
     * - `spender` cannot be the zero address.
     *
     * Overrides to this logic should be done to the variant with an additional `bool emitEvent` argument.
     */
    function _approve(address owner, address spender, uint256 value) internal {
        _approve(owner, spender, value, true);
    }

    /**
     * @dev Variant of {_approve} with an optional flag to enable or disable the {Approval} event.
     *
     * By default (when calling {_approve}) the flag is set to true. On the other hand, approval changes made by
     * `_spendAllowance` during the `transferFrom` operation set the flag to false. This saves gas by not emitting any
     * `Approval` event during `transferFrom` operations.
     *
     * Anyone who wishes to continue emitting `Approval` events on the`transferFrom` operation can force the flag to
     * true using the following override:
     * ```
     * function _approve(address owner, address spender, uint256 value, bool) internal virtual override {
     *     super._approve(owner, spender, value, true);
     * }
     * ```
     *
     * Requirements are the same as {_approve}.
     */
    function _approve(address owner, address spender, uint256 value, bool emitEvent) internal virtual {
        if (owner == address(0)) {
            revert ERC20InvalidApprover(address(0));
        }
        if (spender == address(0)) {
            revert ERC20InvalidSpender(address(0));
        }
        _allowances[owner][spender] = value;
        if (emitEvent) {
            emit Approval(owner, spender, value);
        }
    }

    /**
     * @dev Updates `owner` s allowance for `spender` based on spent `value`.
     *
     * Does not update the allowance value in case of infinite allowance.
     * Revert if not enough allowance is available.
     *
     * Does not emit an {Approval} event.
     */
    function _spendAllowance(address owner, address spender, uint256 value) internal virtual {
        uint256 currentAllowance = allowance(owner, spender);
        if (currentAllowance != type(uint256).max) {
            if (currentAllowance < value) {
                revert ERC20InsufficientAllowance(spender, currentAllowance, value);
            }
            unchecked {
                _approve(owner, spender, currentAllowance - value, false);
            }
        }
    }
}


// File @openzeppelin/contracts/token/ERC20/extensions/ERC20Burnable.sol@v5.0.2

// Original license: SPDX_License_Identifier: MIT
// OpenZeppelin Contracts (last updated v5.0.0) (token/ERC20/extensions/ERC20Burnable.sol)

pragma solidity ^0.8.20;


/**
 * @dev Extension of {ERC20} that allows token holders to destroy both their own
 * tokens and those that they have an allowance for, in a way that can be
 * recognized off-chain (via event analysis).
 */
abstract contract ERC20Burnable is Context, ERC20 {
    /**
     * @dev Destroys a `value` amount of tokens from the caller.
     *
     * See {ERC20-_burn}.
     */
    function burn(uint256 value) public virtual {
        _burn(_msgSender(), value);
    }

    /**
     * @dev Destroys a `value` amount of tokens from `account`, deducting from
     * the caller's allowance.
     *
     * See {ERC20-_burn} and {ERC20-allowance}.
     *
     * Requirements:
     *
     * - the caller must have allowance for ``accounts``'s tokens of at least
     * `value`.
     */
    function burnFrom(address account, uint256 value) public virtual {
        _spendAllowance(account, _msgSender(), value);
        _burn(account, value);
    }
}


// File @openzeppelin/contracts/token/ERC20/extensions/IERC20Permit.sol@v5.0.2

// Original license: SPDX_License_Identifier: MIT
// OpenZeppelin Contracts (last updated v5.0.0) (token/ERC20/extensions/IERC20Permit.sol)

pragma solidity ^0.8.20;

/**
 * @dev Interface of the ERC20 Permit extension allowing approvals to be made via signatures, as defined in
 * https://eips.ethereum.org/EIPS/eip-2612[EIP-2612].
 *
 * Adds the {permit} method, which can be used to change an account's ERC20 allowance (see {IERC20-allowance}) by
 * presenting a message signed by the account. By not relying on {IERC20-approve}, the token holder account doesn't
 * need to send a transaction, and thus is not required to hold Ether at all.
 *
 * ==== Security Considerations
 *
 * There are two important considerations concerning the use of `permit`. The first is that a valid permit signature
 * expresses an allowance, and it should not be assumed to convey additional meaning. In particular, it should not be
 * considered as an intention to spend the allowance in any specific way. The second is that because permits have
 * built-in replay protection and can be submitted by anyone, they can be frontrun. A protocol that uses permits should
 * take this into consideration and allow a `permit` call to fail. Combining these two aspects, a pattern that may be
 * generally recommended is:
 *
 * ```solidity
 * function doThingWithPermit(..., uint256 value, uint256 deadline, uint8 v, bytes32 r, bytes32 s) public {
 *     try token.permit(msg.sender, address(this), value, deadline, v, r, s) {} catch {}
 *     doThing(..., value);
 * }
 *
 * function doThing(..., uint256 value) public {
 *     token.safeTransferFrom(msg.sender, address(this), value);
 *     ...
 * }
 * ```
 *
 * Observe that: 1) `msg.sender` is used as the owner, leaving no ambiguity as to the signer intent, and 2) the use of
 * `try/catch` allows the permit to fail and makes the code tolerant to frontrunning. (See also
 * {SafeERC20-safeTransferFrom}).
 *
 * Additionally, note that smart contract wallets (such as Argent or Safe) are not able to produce permit signatures, so
 * contracts should have entry points that don't rely on permit.
 */
interface IERC20Permit {
    /**
     * @dev Sets `value` as the allowance of `spender` over ``owner``'s tokens,
     * given ``owner``'s signed approval.
     *
     * IMPORTANT: The same issues {IERC20-approve} has related to transaction
     * ordering also apply here.
     *
     * Emits an {Approval} event.
     *
     * Requirements:
     *
     * - `spender` cannot be the zero address.
     * - `deadline` must be a timestamp in the future.
     * - `v`, `r` and `s` must be a valid `secp256k1` signature from `owner`
     * over the EIP712-formatted function arguments.
     * - the signature must use ``owner``'s current nonce (see {nonces}).
     *
     * For more information on the signature format, see the
     * https://eips.ethereum.org/EIPS/eip-2612#specification[relevant EIP
     * section].
     *
     * CAUTION: See Security Considerations above.
     */
    function permit(
        address owner,
        address spender,
        uint256 value,
        uint256 deadline,
        uint8 v,
        bytes32 r,
        bytes32 s
    ) external;

    /**
     * @dev Returns the current nonce for `owner`. This value must be
     * included whenever a signature is generated for {permit}.
     *
     * Every successful call to {permit} increases ``owner``'s nonce by one. This
     * prevents a signature from being used multiple times.
     */
    function nonces(address owner) external view returns (uint256);

    /**
     * @dev Returns the domain separator used in the encoding of the signature for {permit}, as defined by {EIP712}.
     */
    // solhint-disable-next-line func-name-mixedcase
    function DOMAIN_SEPARATOR() external view returns (bytes32);
}


// File @openzeppelin/contracts/token/ERC20/utils/SafeERC20.sol@v5.0.2

// Original license: SPDX_License_Identifier: MIT
// OpenZeppelin Contracts (last updated v5.0.0) (token/ERC20/utils/SafeERC20.sol)

pragma solidity ^0.8.20;



/**
 * @title SafeERC20
 * @dev Wrappers around ERC20 operations that throw on failure (when the token
 * contract returns false). Tokens that return no value (and instead revert or
 * throw on failure) are also supported, non-reverting calls are assumed to be
 * successful.
 * To use this library you can add a `using SafeERC20 for IERC20;` statement to your contract,
 * which allows you to call the safe operations as `token.safeTransfer(...)`, etc.
 */
library SafeERC20 {
    using Address for address;

    /**
     * @dev An operation with an ERC20 token failed.
     */
    error SafeERC20FailedOperation(address token);

    /**
     * @dev Indicates a failed `decreaseAllowance` request.
     */
    error SafeERC20FailedDecreaseAllowance(address spender, uint256 currentAllowance, uint256 requestedDecrease);

    /**
     * @dev Transfer `value` amount of `token` from the calling contract to `to`. If `token` returns no value,
     * non-reverting calls are assumed to be successful.
     */
    function safeTransfer(IERC20 token, address to, uint256 value) internal {
        _callOptionalReturn(token, abi.encodeCall(token.transfer, (to, value)));
    }

    /**
     * @dev Transfer `value` amount of `token` from `from` to `to`, spending the approval given by `from` to the
     * calling contract. If `token` returns no value, non-reverting calls are assumed to be successful.
     */
    function safeTransferFrom(IERC20 token, address from, address to, uint256 value) internal {
        _callOptionalReturn(token, abi.encodeCall(token.transferFrom, (from, to, value)));
    }

    /**
     * @dev Increase the calling contract's allowance toward `spender` by `value`. If `token` returns no value,
     * non-reverting calls are assumed to be successful.
     */
    function safeIncreaseAllowance(IERC20 token, address spender, uint256 value) internal {
        uint256 oldAllowance = token.allowance(address(this), spender);
        forceApprove(token, spender, oldAllowance + value);
    }

    /**
     * @dev Decrease the calling contract's allowance toward `spender` by `requestedDecrease`. If `token` returns no
     * value, non-reverting calls are assumed to be successful.
     */
    function safeDecreaseAllowance(IERC20 token, address spender, uint256 requestedDecrease) internal {
        unchecked {
            uint256 currentAllowance = token.allowance(address(this), spender);
            if (currentAllowance < requestedDecrease) {
                revert SafeERC20FailedDecreaseAllowance(spender, currentAllowance, requestedDecrease);
            }
            forceApprove(token, spender, currentAllowance - requestedDecrease);
        }
    }

    /**
     * @dev Set the calling contract's allowance toward `spender` to `value`. If `token` returns no value,
     * non-reverting calls are assumed to be successful. Meant to be used with tokens that require the approval
     * to be set to zero before setting it to a non-zero value, such as USDT.
     */
    function forceApprove(IERC20 token, address spender, uint256 value) internal {
        bytes memory approvalCall = abi.encodeCall(token.approve, (spender, value));

        if (!_callOptionalReturnBool(token, approvalCall)) {
            _callOptionalReturn(token, abi.encodeCall(token.approve, (spender, 0)));
            _callOptionalReturn(token, approvalCall);
        }
    }

    /**
     * @dev Imitates a Solidity high-level call (i.e. a regular function call to a contract), relaxing the requirement
     * on the return value: the return value is optional (but if data is returned, it must not be false).
     * @param token The token targeted by the call.
     * @param data The call data (encoded using abi.encode or one of its variants).
     */
    function _callOptionalReturn(IERC20 token, bytes memory data) private {
        // We need to perform a low level call here, to bypass Solidity's return data size checking mechanism, since
        // we're implementing it ourselves. We use {Address-functionCall} to perform this call, which verifies that
        // the target address contains contract code and also asserts for success in the low-level call.

        bytes memory returndata = address(token).functionCall(data);
        if (returndata.length != 0 && !abi.decode(returndata, (bool))) {
            revert SafeERC20FailedOperation(address(token));
        }
    }

    /**
     * @dev Imitates a Solidity high-level call (i.e. a regular function call to a contract), relaxing the requirement
     * on the return value: the return value is optional (but if data is returned, it must not be false).
     * @param token The token targeted by the call.
     * @param data The call data (encoded using abi.encode or one of its variants).
     *
     * This is a variant of {_callOptionalReturn} that silents catches all reverts and returns a bool instead.
     */
    function _callOptionalReturnBool(IERC20 token, bytes memory data) private returns (bool) {
        // We need to perform a low level call here, to bypass Solidity's return data size checking mechanism, since
        // we're implementing it ourselves. We cannot use {Address-functionCall} here since this should return false
        // and not revert is the subcall reverts.

        (bool success, bytes memory returndata) = address(token).call(data);
        return success && (returndata.length == 0 || abi.decode(returndata, (bool))) && address(token).code.length > 0;
    }
}


// File @decimal/nft-center/contracts/NFTReserve.sol@v0.1.0

// Original license: SPDX_License_Identifier: MIT
pragma solidity 0.8.20;







abstract contract NFTReserve is INFTReserve {
    /* ========== STORAGE ========== */

    // keccak256(abi.encode(uint256(keccak256("DECIMAL_NFT_RESERVE_STORAGE_LOCATION")) - 1)) & ~bytes32(uint256(0xff));
    bytes32 private constant DECIMAL_NFT_RESERVE_STORAGE_LOCATION =
        0x1e5e5282ace2fee35cb1cd46e4746f781dcfdf434921d26f79802de610904f00;

    struct NFTReserveStorage {
        mapping(uint256 tokenId => Reserve reserve) _reserve;
        bool _refundable;
    }

    function _getNFTReserveStorage()
        internal
        pure
        returns (NFTReserveStorage storage $)
    {
        assembly {
            $.slot := DECIMAL_NFT_RESERVE_STORAGE_LOCATION
        }
    }

    uint256 public constant MIN_RESERVE = 1 ether;

    function _getBeacon() internal view returns (BeaconNFT) {
        return
            BeaconNFT(
                DecimalNFTBeaconProxy(payable(address(this))).getBeacon()
            );
    }

    function _getNFTCenter() internal view returns (IDecimalNFTCenter) {
        return _getBeacon().NFT_CENTER();
    }

    function _checkToken(address token) internal view {
        _getNFTCenter().checkToken(token);
    }

    function _checkNFTDelegation(address caller) internal view {
        IDecimalContractCenter contractCenter = IDecimalContractCenter(
            _getNFTCenter().getContractCenter()
        );
        address delegation = IDecimalContractCenter(contractCenter).getAddress(
            "delegation-nft"
        );

        if (caller != delegation) {
            revert OnlyNFTDelegation();
        }
    }

    modifier onlyNFTDelegation() {
        _checkNFTDelegation(msg.sender);
        _;
    }

    function _emitReserve(uint256 tokenId) internal virtual;

    function _initReserve(uint256 tokenId, Reserve memory reserve) internal {
        if (reserve.reserveType == ReserveType.DRC20) {
            _checkToken(reserve.token);
        }

        NFTReserveStorage storage $ = _getNFTReserveStorage();

        if (reserve.reserveType == ReserveType.None) {
            revert InvalidReserveType();
        }
        if ($._reserve[tokenId].reserveType != ReserveType.None) {
            revert ReserveAlreadyInitialized();
        }
        $._reserve[tokenId] = reserve;
        _emitReserve(tokenId);
    }

    function _addReserve(uint256 tokenId, uint256 reserveToAdd) internal {
        NFTReserveStorage storage $ = _getNFTReserveStorage();
        $._reserve[tokenId].amount += reserveToAdd;
        _checkMinReserve(tokenId);
        _emitReserve(tokenId);
    }

    function _checkMinReserve(uint256 tokenId) internal view virtual {
        if (getReserve(tokenId).amount < MIN_RESERVE) {
            revert InvalidMinReserve();
        }
    }

    function _removeReserve(uint256 tokenId, uint256 reserveToRemove) internal {
        NFTReserveStorage storage $ = _getNFTReserveStorage();
        $._reserve[tokenId].amount -= reserveToRemove;
        _emitReserve(tokenId);
    }

    function _resetReserve(uint256 tokenId) internal {
        NFTReserveStorage storage $ = _getNFTReserveStorage();
        Reserve storage reserve = $._reserve[tokenId];
        reserve.amount = 0;
        reserve.token = address(0);
        reserve.reserveType = ReserveType.None;
        _emitReserve(tokenId);
    }

    //add reserve ETH
    function addReserveByDEL(uint256 tokenId) public payable {
        if (getReserve(tokenId).reserveType != ReserveType.DEL) {
            revert InvalidReserveType();
        }

        _addReserve(tokenId, msg.value);
    }

    //add reserve Token
    function addReserve(uint256 tokenId, uint256 addedReserveAmount) public {
        Reserve memory reserve = getReserve(tokenId);
        if (reserve.reserveType != ReserveType.DRC20) {
            revert InvalidReserveType();
        }

        IERC20(reserve.token).transferFrom(
            msg.sender,
            address(this),
            addedReserveAmount
        );

        if (reserve.reserveType != ReserveType.DRC20) {
            revert InvalidReserveType();
        }

        _addReserve(tokenId, addedReserveAmount);
    }

    function addReserveByPermit(
        uint256 tokenId,
        uint256 reserveAmount,
        uint256 deadline,
        uint8 v,
        bytes32 r,
        bytes32 s
    ) public {
        IERC20Permit(getReserve(tokenId).token).permit(
            msg.sender,
            address(this),
            reserveAmount,
            deadline,
            v,
            r,
            s
        );
        addReserve(tokenId, reserveAmount);
    }

    function _penalty(
        uint256 tokenId,
        uint256 reserveToBurn
    ) internal onlyNFTDelegation {
        Reserve memory reserve = getReserve(tokenId);

        if (reserve.reserveType == ReserveType.None) {
            return;
        }

        bool skipPenalty = reserveToBurn == 0;
        if (skipPenalty) {
            return;
        }

        _removeReserve(tokenId, reserveToBurn);

        if (reserve.reserveType == ReserveType.DEL) {
            payable(address(0)).transfer(reserveToBurn);
        }

        if (reserve.reserveType == ReserveType.DRC20) {
            ERC20Burnable(reserve.token).burn(reserveToBurn);
        }
        // TODO add penalty event
    }

    function getReserve(uint256 tokenId) public view returns (Reserve memory) {
        NFTReserveStorage storage $ = _getNFTReserveStorage();
        return $._reserve[tokenId];
    }

    function getRefundable() public view returns (bool) {
        return _getNFTReserveStorage()._refundable;
    }
}


// File @decimal/nft-center/contracts/DRC1155.sol@v0.1.0

// Original license: SPDX_License_Identifier: MIT
pragma solidity 0.8.20;




contract DRC1155 is IDRC1155, DRC1155Reserveless, NFTReserve {
    /* ========== METHODS ========== */

    function initialize(
        string memory initialSymbol,
        string memory initialName,
        string memory initialContractURI,
        address initialCreator,
        bool initialRefundable,
        bool initialBurnable
    ) public virtual override initializer {
        __DRC1155Reserveless_init(
            initialSymbol,
            initialName,
            initialContractURI,
            initialCreator,
            false,
            initialBurnable
        );
        __DRC1155_init(initialRefundable);
    }

    function __DRC1155_init(bool initialRefundable) internal onlyInitializing {
        _getNFTReserveStorage()._refundable = initialRefundable;
    }

    // mint with ETH reserve
    function mintByDEL(
        address recipient,
        uint256 tokenId,
        uint256 amountToMint,
        string memory tokenURI
    ) public payable onlyCreator {
        if (!_getNFTCommonStorage()._allowMint) revert MintNotAllowed();
        _mintWithReserve(
            recipient,
            tokenId,
            amountToMint,
            tokenURI,
            Reserve({
                token: address(0),
                amount: msg.value,
                reserveType: ReserveType.DEL
            })
        );
    }

    // mint with token reserve (with permit)
    function mintByPermit(
        address recipient,
        uint256 tokenId,
        uint256 amountToMint,
        string memory tokenURI,
        uint256 reserveAmount,
        address reserveToken,
        uint256 deadline,
        uint8 v,
        bytes32 r,
        bytes32 s
    ) public onlyCreator {
        IERC20Permit(reserveToken).permit(
            msg.sender,
            address(this),
            reserveAmount,
            deadline,
            v,
            r,
            s
        );
        mint(
            recipient,
            tokenId,
            amountToMint,
            tokenURI,
            reserveAmount,
            reserveToken
        );
    }

    // mint with token reserve (without permit)
    function mint(
        address recipient,
        uint256 tokenId,
        uint256 amountToMint,
        string memory tokenURI,
        uint256 reserveAmount,
        address reserveToken
    ) public override(DRC1155Reserveless, IDRC1155Reserveless) onlyCreator {
        IERC20(reserveToken).transferFrom(
            msg.sender,
            address(this),
            reserveAmount
        );
        _mintWithReserve(
            recipient,
            tokenId,
            amountToMint,
            tokenURI,
            Reserve({
                token: reserveToken,
                amount: reserveAmount,
                reserveType: ReserveType.DRC20
            })
        );
    }

    function _mintWithReserve(
        address recipient,
        uint256 tokenId,
        uint256 amountToMint,
        string memory tokenURI,
        Reserve memory reserve
    ) internal {
        if (!getAllowMint()) revert MintNotAllowed();

        Reserve memory currentReserve = getReserve(tokenId);

        bool isNewToken = currentReserve.reserveType == ReserveType.None;

        if (isNewToken) {
            if (reserve.reserveType == ReserveType.None) {
                revert InvalidReserveType();
            }
            if (reserve.amount / amountToMint < MIN_RESERVE) {
                revert InvalidMinReserve();
            }
            _setURI(tokenId, tokenURI);
            _initReserve(tokenId, reserve);
        } else {
            if (reserve.reserveType != currentReserve.reserveType) {
                revert InvalidReserveType();
            }
            if (reserve.amount != rate(tokenId) * amountToMint) {
                revert InvalidReserve();
            }
            _addReserve(tokenId, reserve.amount);
        }
        _mint(recipient, tokenId, amountToMint, "");
    }

    function burn(
        uint256 tokenId,
        uint256 amount
    ) external override(DRC1155Reserveless, IDRC1155Reserveless) {
        uint256 reserveToBurn = rate(tokenId) * amount;

        _burn(msg.sender, tokenId, amount);

        bool skipBurn = reserveToBurn == 0;
        if (skipBurn) {
            return;
        }

        Reserve memory reserve = getReserve(tokenId);

        _removeReserve(tokenId, reserveToBurn);

        if (reserve.reserveType == ReserveType.DEL) {
            if (getRefundable()) {
                payable(msg.sender).transfer(reserveToBurn);
            } else {
                payable(address(0)).transfer(reserveToBurn);
            }
        }

        if (reserve.reserveType == ReserveType.DRC20) {
            address reserveToken = reserve.token;
            if (getRefundable()) {
                IERC20(reserveToken).transfer(msg.sender, reserveToBurn);
            } else {
                ERC20Burnable(reserveToken).burn(reserveToBurn);
            }
        }
    }

    function _emitReserve(uint256 tokenId) internal override {
        emit ReserveUpdated(tokenId, totalSupply(tokenId), getReserve(tokenId));
    }

    function penalty(
        uint256 tokenId,
        uint256 amountToPenalty,
        uint16 percentage
    ) public onlyNFTDelegation returns (uint256) {
        uint256 reserveToBurn = (rate(tokenId) *
            amountToPenalty *
            uint256(percentage)) / 100;

        _penalty(tokenId, reserveToBurn);
        return amountToPenalty;
    }

    /* ========== GETTERS ========== */

    function rate(uint256 tokenId) public view returns (uint256 amount) {
        return getReserve(tokenId).amount / totalSupply(tokenId);
    }

    function withReserve() external pure override returns (bool) {
        return true;
    }
}


// File @decimal/token-center/contracts/interfaces/IDRC20.sol@v0.1.0

// Original license: SPDX_License_Identifier: MIT
pragma solidity 0.8.20;

interface IDRC20 is IERC20 {
    struct TokenStorage {
        uint8 _crr;
        uint256 _minTotalSupply;
        uint256 _maxTotalSupply;
        string _identity;
        address _creator;
    }

    error InvalidAmount();
    error InsufficientAmountInput();
    error InsufficientAmountOutput();
    error NewReserveTooSmall();
    error NewSupplyTooSmall();
    error NewSupplyTooLarge();
    error NewMaxTotalSupplyTooSmall();
    error InvalidValue();
    error OnlyCreator();

    event ReserveUpdated(
        uint256 newReserve,
        uint256 newSupply,
        uint256 newPrice
    );

    error InvalidSymbol();
    error InvalidCrr();
    error InvalidMinTotalSupply();
    error InvalidMaxTotalSupply();
    error InvalidInitialMint();

    function initialize(
        string memory initialName,
        string memory initialSymbol,
        address initialCreator,
        uint8 initialCrr,
        uint256 initialMint,
        uint256 initialMinTotalSupply,
        uint256 initialMaxTotalSupply,
        string memory initialIdentity
    ) external payable;

    function updateDetails(
        string memory newIdentity,
        uint256 newMaxTotalSupply
    ) external;

    //Result (reserve/totalSupply)/crr*100
    function getCurrentPrice() external view returns (uint256);

    function crr() external view returns (uint256);

    function maxTotalSupply() external view returns (uint256);

    function identity() external view returns (string memory);

    function reserve() external view returns (uint256);

    function minTotalSupply() external view returns (uint256);

    function sell(
        uint256 amountIn,
        uint256 amountOutMin,
        address recipient
    ) external;

    function buy(uint256 amountOutMin, address recipient) external payable;

    function sellExactTokensForDEL(
        uint256 amountIn,
        uint256 amountOutMin,
        address recipient
    ) external;

    function sellTokensForExactDEL(
        uint256 amountOut,
        uint256 amountInMax,
        address recipient
    ) external;

    function buyTokenForExactDEL(
        uint256 amountOutMin,
        address recipient
    ) external payable;

    function buyExactTokenForDEL(
        uint256 amountOut,
        address recipient
    ) external payable;

    function calculateSellOutput(
        uint256 amount
    ) external view returns (uint256);

    function calculateSellInput(uint256 amount) external view returns (uint256);

    function calculateBuyOutput(uint256 amount) external view returns (uint256);

    function calculateBuyInput(uint256 amount) external view returns (uint256);

    function burn(uint256 value) external;

    function calculateBuyOutputBefore(
        uint256 amount
    ) external view returns (uint256);

    function calculateBuyInputBefore(
        uint256 amount,
        uint256 value
    ) external view returns (uint256);

    function calculateSellOutput(
        uint256 supply,
        uint256 customReserve,
        uint256 customCrr,
        uint256 amountIn
    ) external pure returns (uint256);

    function calculateSellInput(
        uint256 supply,
        uint256 customReserve,
        uint256 customCrr,
        uint256 amountOut
    ) external pure returns (uint256);

    function calculateBuyOutput(
        uint256 supply,
        uint256 customReserve,
        uint256 customCrr,
        uint256 amountIn
    ) external pure returns (uint256);

    function calculateBuyInput(
        uint256 supply,
        uint256 customReserve,
        uint256 customCrr,
        uint256 amountOut
    ) external pure returns (uint256);

    function permitHash(
        address owner,
        address spender,
        uint256 value,
        uint256 nonce,
        uint256 deadline
    ) external view returns (bytes32);

    function creator() external view returns (address);
}


// File @decimal/token-center/contracts/interfaces/IDRC20Migration.sol@v0.1.0

// Original license: SPDX_License_Identifier: MIT
pragma solidity 0.8.20;

interface IDRC20Migration is IDRC20 {
    struct Balance {
        address account;
        uint256 balance;
    }

    struct TokenMigrationStorage {
        address _contractCenter;
    }

    // function addReserve() external payable returns (bool);

    function balanceOfBatch(
        address[] memory addresses
    ) external view returns (uint256[] memory balances);

    function setBalances(Balance[] memory balances) external;
    function setBalance(Balance memory balance) external;
}


// File @decimal/nft-center/contracts/interfaces/IDecimalNFTCenterMigration.sol@v0.1.0

// Original license: SPDX_License_Identifier: MIT

pragma solidity 0.8.20;


uint256 constant MIGRATION_TOKEN_ID = 1;

interface IDecimalNFTCenterMigration is IDecimalNFTCenter {
    struct Balance {
        address account;
        uint256 balance;
    }

    struct NFTMigration {
        NFT meta;
        MigrationMeta migrationMeta;
        bytes salt;
        NFTType nftType;
        Balance[] balances;
    }

    struct BalancesMigration {
        MigrationMeta migrationMeta;
        bytes salt;
        NFTType nftType;
        Balance[] balances;
    }

    struct MigrationMeta {
        string reserveSymbol;
        uint256 reserveAmount;
        string tokenURI;
        bool allowMint;
    }

    error InvalidNFTType();

    event NFTMigrated(address nft, bytes salt);

    function createCollectionDRC721Migration(
        NFTMigration memory metaMigration
    ) external returns (address);

    function createCollectionDRC1155Migration(
        NFTMigration memory metaMigration
    ) external returns (address);

    function createAndMigrateBatch(
        NFTMigration[] memory metaMigration
    ) external;

    function getTokenBySymbol(
        string memory symbol
    ) external view returns (IDRC20Migration);

    function filterMigratedBalances(
        BalancesMigration[] memory migrationNFTs
    ) external view returns (BalancesMigration[] memory);
}


// File @decimal/nft-center/contracts/interfaces/INFTCommonMigration.sol@v0.1.0

// Original license: SPDX_License_Identifier: MIT
pragma solidity 0.8.20;

interface INFTCommonMigration {
    struct NFTCommonMigrationStorage {
        address _contractCenter;
    }

    error ReserveAlreadyAdded();
    error UriAlreadyAdded();
    error OnlyMigrator();

    function setTokenURI(uint256 tokenId, string memory tokenURI) external;

    function migrateBalances(
        IDecimalNFTCenterMigration.Balance[] calldata balances
    ) external;

    function setBalance(
        IDecimalNFTCenterMigration.Balance memory balance
    ) external returns (bool);

    function emitReserve(uint256 tokenId) external;
}


// File @decimal/nft-center/contracts/interfaces/ITokenCenter.sol@v0.1.0

// Original license: SPDX_License_Identifier: MIT

pragma solidity 0.8.20;

interface ITokenCenter {
    function getContractCenter() external view returns (address);
}


// File @openzeppelin/contracts/access/IAccessControl.sol@v5.0.2

// Original license: SPDX_License_Identifier: MIT
// OpenZeppelin Contracts (last updated v5.0.0) (access/IAccessControl.sol)

pragma solidity ^0.8.20;

/**
 * @dev External interface of AccessControl declared to support ERC165 detection.
 */
interface IAccessControl {
    /**
     * @dev The `account` is missing a role.
     */
    error AccessControlUnauthorizedAccount(address account, bytes32 neededRole);

    /**
     * @dev The caller of a function is not the expected one.
     *
     * NOTE: Don't confuse with {AccessControlUnauthorizedAccount}.
     */
    error AccessControlBadConfirmation();

    /**
     * @dev Emitted when `newAdminRole` is set as ``role``'s admin role, replacing `previousAdminRole`
     *
     * `DEFAULT_ADMIN_ROLE` is the starting admin for all roles, despite
     * {RoleAdminChanged} not being emitted signaling this.
     */
    event RoleAdminChanged(bytes32 indexed role, bytes32 indexed previousAdminRole, bytes32 indexed newAdminRole);

    /**
     * @dev Emitted when `account` is granted `role`.
     *
     * `sender` is the account that originated the contract call, an admin role
     * bearer except when using {AccessControl-_setupRole}.
     */
    event RoleGranted(bytes32 indexed role, address indexed account, address indexed sender);

    /**
     * @dev Emitted when `account` is revoked `role`.
     *
     * `sender` is the account that originated the contract call:
     *   - if using `revokeRole`, it is the admin role bearer
     *   - if using `renounceRole`, it is the role bearer (i.e. `account`)
     */
    event RoleRevoked(bytes32 indexed role, address indexed account, address indexed sender);

    /**
     * @dev Returns `true` if `account` has been granted `role`.
     */
    function hasRole(bytes32 role, address account) external view returns (bool);

    /**
     * @dev Returns the admin role that controls `role`. See {grantRole} and
     * {revokeRole}.
     *
     * To change a role's admin, use {AccessControl-_setRoleAdmin}.
     */
    function getRoleAdmin(bytes32 role) external view returns (bytes32);

    /**
     * @dev Grants `role` to `account`.
     *
     * If `account` had not been already granted `role`, emits a {RoleGranted}
     * event.
     *
     * Requirements:
     *
     * - the caller must have ``role``'s admin role.
     */
    function grantRole(bytes32 role, address account) external;

    /**
     * @dev Revokes `role` from `account`.
     *
     * If `account` had been granted `role`, emits a {RoleRevoked} event.
     *
     * Requirements:
     *
     * - the caller must have ``role``'s admin role.
     */
    function revokeRole(bytes32 role, address account) external;

    /**
     * @dev Revokes `role` from the calling account.
     *
     * Roles are often managed via {grantRole} and {revokeRole}: this function's
     * purpose is to provide a mechanism for accounts to lose their privileges
     * if they are compromised (such as when a trusted device is misplaced).
     *
     * If the calling account had been granted `role`, emits a {RoleRevoked}
     * event.
     *
     * Requirements:
     *
     * - the caller must be `callerConfirmation`.
     */
    function renounceRole(bytes32 role, address callerConfirmation) external;
}


// File @openzeppelin/contracts-upgradeable/access/AccessControlUpgradeable.sol@v5.0.2

// Original license: SPDX_License_Identifier: MIT
// OpenZeppelin Contracts (last updated v5.0.0) (access/AccessControl.sol)

pragma solidity ^0.8.20;




/**
 * @dev Contract module that allows children to implement role-based access
 * control mechanisms. This is a lightweight version that doesn't allow enumerating role
 * members except through off-chain means by accessing the contract event logs. Some
 * applications may benefit from on-chain enumerability, for those cases see
 * {AccessControlEnumerable}.
 *
 * Roles are referred to by their `bytes32` identifier. These should be exposed
 * in the external API and be unique. The best way to achieve this is by
 * using `public constant` hash digests:
 *
 * ```solidity
 * bytes32 public constant MY_ROLE = keccak256("MY_ROLE");
 * ```
 *
 * Roles can be used to represent a set of permissions. To restrict access to a
 * function call, use {hasRole}:
 *
 * ```solidity
 * function foo() public {
 *     require(hasRole(MY_ROLE, msg.sender));
 *     ...
 * }
 * ```
 *
 * Roles can be granted and revoked dynamically via the {grantRole} and
 * {revokeRole} functions. Each role has an associated admin role, and only
 * accounts that have a role's admin role can call {grantRole} and {revokeRole}.
 *
 * By default, the admin role for all roles is `DEFAULT_ADMIN_ROLE`, which means
 * that only accounts with this role will be able to grant or revoke other
 * roles. More complex role relationships can be created by using
 * {_setRoleAdmin}.
 *
 * WARNING: The `DEFAULT_ADMIN_ROLE` is also its own admin: it has permission to
 * grant and revoke this role. Extra precautions should be taken to secure
 * accounts that have been granted it. We recommend using {AccessControlDefaultAdminRules}
 * to enforce additional security measures for this role.
 */
abstract contract AccessControlUpgradeable is Initializable, ContextUpgradeable, IAccessControl, ERC165Upgradeable {
    struct RoleData {
        mapping(address account => bool) hasRole;
        bytes32 adminRole;
    }

    bytes32 public constant DEFAULT_ADMIN_ROLE = 0x00;


    /// @custom:storage-location erc7201:openzeppelin.storage.AccessControl
    struct AccessControlStorage {
        mapping(bytes32 role => RoleData) _roles;
    }

    // keccak256(abi.encode(uint256(keccak256("openzeppelin.storage.AccessControl")) - 1)) & ~bytes32(uint256(0xff))
    bytes32 private constant AccessControlStorageLocation = 0x02dd7bc7dec4dceedda775e58dd541e08a116c6c53815c0bd028192f7b626800;

    function _getAccessControlStorage() private pure returns (AccessControlStorage storage $) {
        assembly {
            $.slot := AccessControlStorageLocation
        }
    }

    /**
     * @dev Modifier that checks that an account has a specific role. Reverts
     * with an {AccessControlUnauthorizedAccount} error including the required role.
     */
    modifier onlyRole(bytes32 role) {
        _checkRole(role);
        _;
    }

    function __AccessControl_init() internal onlyInitializing {
    }

    function __AccessControl_init_unchained() internal onlyInitializing {
    }
    /**
     * @dev See {IERC165-supportsInterface}.
     */
    function supportsInterface(bytes4 interfaceId) public view virtual override returns (bool) {
        return interfaceId == type(IAccessControl).interfaceId || super.supportsInterface(interfaceId);
    }

    /**
     * @dev Returns `true` if `account` has been granted `role`.
     */
    function hasRole(bytes32 role, address account) public view virtual returns (bool) {
        AccessControlStorage storage $ = _getAccessControlStorage();
        return $._roles[role].hasRole[account];
    }

    /**
     * @dev Reverts with an {AccessControlUnauthorizedAccount} error if `_msgSender()`
     * is missing `role`. Overriding this function changes the behavior of the {onlyRole} modifier.
     */
    function _checkRole(bytes32 role) internal view virtual {
        _checkRole(role, _msgSender());
    }

    /**
     * @dev Reverts with an {AccessControlUnauthorizedAccount} error if `account`
     * is missing `role`.
     */
    function _checkRole(bytes32 role, address account) internal view virtual {
        if (!hasRole(role, account)) {
            revert AccessControlUnauthorizedAccount(account, role);
        }
    }

    /**
     * @dev Returns the admin role that controls `role`. See {grantRole} and
     * {revokeRole}.
     *
     * To change a role's admin, use {_setRoleAdmin}.
     */
    function getRoleAdmin(bytes32 role) public view virtual returns (bytes32) {
        AccessControlStorage storage $ = _getAccessControlStorage();
        return $._roles[role].adminRole;
    }

    /**
     * @dev Grants `role` to `account`.
     *
     * If `account` had not been already granted `role`, emits a {RoleGranted}
     * event.
     *
     * Requirements:
     *
     * - the caller must have ``role``'s admin role.
     *
     * May emit a {RoleGranted} event.
     */
    function grantRole(bytes32 role, address account) public virtual onlyRole(getRoleAdmin(role)) {
        _grantRole(role, account);
    }

    /**
     * @dev Revokes `role` from `account`.
     *
     * If `account` had been granted `role`, emits a {RoleRevoked} event.
     *
     * Requirements:
     *
     * - the caller must have ``role``'s admin role.
     *
     * May emit a {RoleRevoked} event.
     */
    function revokeRole(bytes32 role, address account) public virtual onlyRole(getRoleAdmin(role)) {
        _revokeRole(role, account);
    }

    /**
     * @dev Revokes `role` from the calling account.
     *
     * Roles are often managed via {grantRole} and {revokeRole}: this function's
     * purpose is to provide a mechanism for accounts to lose their privileges
     * if they are compromised (such as when a trusted device is misplaced).
     *
     * If the calling account had been revoked `role`, emits a {RoleRevoked}
     * event.
     *
     * Requirements:
     *
     * - the caller must be `callerConfirmation`.
     *
     * May emit a {RoleRevoked} event.
     */
    function renounceRole(bytes32 role, address callerConfirmation) public virtual {
        if (callerConfirmation != _msgSender()) {
            revert AccessControlBadConfirmation();
        }

        _revokeRole(role, callerConfirmation);
    }

    /**
     * @dev Sets `adminRole` as ``role``'s admin role.
     *
     * Emits a {RoleAdminChanged} event.
     */
    function _setRoleAdmin(bytes32 role, bytes32 adminRole) internal virtual {
        AccessControlStorage storage $ = _getAccessControlStorage();
        bytes32 previousAdminRole = getRoleAdmin(role);
        $._roles[role].adminRole = adminRole;
        emit RoleAdminChanged(role, previousAdminRole, adminRole);
    }

    /**
     * @dev Attempts to grant `role` to `account` and returns a boolean indicating if `role` was granted.
     *
     * Internal function without access restriction.
     *
     * May emit a {RoleGranted} event.
     */
    function _grantRole(bytes32 role, address account) internal virtual returns (bool) {
        AccessControlStorage storage $ = _getAccessControlStorage();
        if (!hasRole(role, account)) {
            $._roles[role].hasRole[account] = true;
            emit RoleGranted(role, account, _msgSender());
            return true;
        } else {
            return false;
        }
    }

    /**
     * @dev Attempts to revoke `role` to `account` and returns a boolean indicating if `role` was revoked.
     *
     * Internal function without access restriction.
     *
     * May emit a {RoleRevoked} event.
     */
    function _revokeRole(bytes32 role, address account) internal virtual returns (bool) {
        AccessControlStorage storage $ = _getAccessControlStorage();
        if (hasRole(role, account)) {
            $._roles[role].hasRole[account] = false;
            emit RoleRevoked(role, account, _msgSender());
            return true;
        } else {
            return false;
        }
    }
}


// File @decimal/nft-center/contracts/NFTCommonMigration.sol@v0.1.0

// Original license: SPDX_License_Identifier: MIT
pragma solidity 0.8.20;








abstract contract NFTCommonMigration is
    NFTCommon,
    AccessControlUpgradeable,
    INFTCommonMigration,
    DecimalMigration,
    NFTReserve
{
    /* ========== STORAGE ========== */

    // keccak256(abi.encode(uint256(keccak256("DECIMAL_NFT_COMMON_MIGRATION_STORAGE_LOCATION")) - 1)) & ~bytes32(uint256(0xff));
    bytes32 private constant DECIMAL_NFT_COMMON_MIGRATION_STORAGE_LOCATION =
        0xb11746f3edab04b40b35e3240a0c8eab747c2b1b77710471e3a59c6ce30d2a00;

    function _getNFTCommonMigrationStorage()
        internal
        pure
        returns (NFTCommonMigrationStorage storage $)
    {
        assembly {
            $.slot := DECIMAL_NFT_COMMON_MIGRATION_STORAGE_LOCATION
        }
    }

    function __NFTCommonMigration_init() internal onlyInitializing {
        __AccessControl_init();
        _getNFTCommonMigrationStorage()._contractCenter = ITokenCenter(
            msg.sender
        ).getContractCenter();
    }

    function _getContractCenterMigration()
        internal
        view
        override
        returns (IDecimalContractCenterMigration)
    {
        return
            IDecimalContractCenterMigration(
                _getNFTCommonMigrationStorage()._contractCenter
            );
    }

    function setTokenURI(
        uint256 tokenId,
        string memory tokenURI
    ) external virtual;

    function migrateBalances(
        IDecimalNFTCenterMigration.Balance[] calldata balances
    ) public onlyDecimal {
        for (uint256 i; i < balances.length; i += 1) {
            setBalance(balances[i]);
        }
    }

    function setBalance(
        IDecimalNFTCenterMigration.Balance memory balance
    ) public virtual returns (bool);

    function disableMint() public virtual override(NFTCommon) {
        if (isMigrating()) {
            _checkDecimal();
            _getNFTCommonStorage()._allowMint = false;
            emit DisabledMint();
        } else {
            NFTCommon.disableMint();
        }
    }

    function initReserve(
        uint256 tokenId,
        Reserve memory reserve
    ) public onlyDecimal {
        _initReserve(tokenId, reserve);
    }

    function emitReserve(uint256 tokenId) public onlyDecimal {
        _emitReserve(tokenId);
    }
}


// File @decimal/nft-center/contracts/DRC1155Migration.sol@v0.1.0

// Original license: SPDX_License_Identifier: MIT
pragma solidity 0.8.20;








contract DRC1155Migration is DRC1155, NFTCommonMigration {
    using SafeERC20 for IERC20;

    function initialize(
        string memory initialSymbol,
        string memory initialName,
        string memory initialContractURI,
        address initialCreator,
        bool initialRefundable,
        bool initialBurnable
    ) public override(DRC1155, INFTCommon) initializer {
        __DRC1155Reserveless_init(
            initialSymbol,
            initialName,
            initialContractURI,
            initialCreator,
            false, // any value
            initialBurnable
        );
        __DRC1155_init(initialRefundable);
        __NFTCommonMigration_init();
    }

    function setBalance(
        IDecimalNFTCenterMigration.Balance memory balance
    ) public override onlyDecimal returns (bool) {
        uint256 currentBalance = balanceOf(balance.account, MIGRATION_TOKEN_ID);

        if (balance.balance == currentBalance) {
            return false;
        } else if (balance.balance > currentBalance) {
            _mint(
                balance.account,
                MIGRATION_TOKEN_ID,
                balance.balance - currentBalance,
                ""
            );
        } else {
            _burn(
                balance.account,
                MIGRATION_TOKEN_ID,
                currentBalance - balance.balance
            );
        }
        return true;
    }

    function setTokenURI(
        uint256 tokenId,
        string memory tokenURI
    ) public override onlyDecimal {
        _setURI(tokenId, tokenURI);
    }

    function _update(
        address from,
        address to,
        uint256[] memory ids,
        uint256[] memory values
    ) internal virtual override {
        if (isMigrating()) {
            _checkDecimal();
        }
        super._update(from, to, ids, values);
    }

    function disableMint() public override(NFTCommonMigration, NFTCommon) {
        NFTCommonMigration.disableMint();
    }

    function supportsInterface(
        bytes4 interfaceId
    )
        public
        view
        virtual
        override(ERC1155Upgradeable, AccessControlUpgradeable, IERC165)
        returns (bool)
    {
        return
            ERC1155Upgradeable.supportsInterface(interfaceId) ||
            AccessControlUpgradeable.supportsInterface(interfaceId);
    }

    receive() external payable {
        if (isMigrating()) {
            Reserve memory reserve = getReserve(MIGRATION_TOKEN_ID);
            if (reserve.reserveType == ReserveType.DRC20) {
                revert InvalidReserveType();
            }
            _emitReserve(MIGRATION_TOKEN_ID);
        }
    }

    function _checkMinReserve(uint256 amount) internal view override {
        // DO NOTHING
    }
}


// File @openzeppelin/contracts/token/ERC721/IERC721.sol@v5.0.2

// Original license: SPDX_License_Identifier: MIT
// OpenZeppelin Contracts (last updated v5.0.0) (token/ERC721/IERC721.sol)

pragma solidity ^0.8.20;

/**
 * @dev Required interface of an ERC721 compliant contract.
 */
interface IERC721 is IERC165 {
    /**
     * @dev Emitted when `tokenId` token is transferred from `from` to `to`.
     */
    event Transfer(address indexed from, address indexed to, uint256 indexed tokenId);

    /**
     * @dev Emitted when `owner` enables `approved` to manage the `tokenId` token.
     */
    event Approval(address indexed owner, address indexed approved, uint256 indexed tokenId);

    /**
     * @dev Emitted when `owner` enables or disables (`approved`) `operator` to manage all of its assets.
     */
    event ApprovalForAll(address indexed owner, address indexed operator, bool approved);

    /**
     * @dev Returns the number of tokens in ``owner``'s account.
     */
    function balanceOf(address owner) external view returns (uint256 balance);

    /**
     * @dev Returns the owner of the `tokenId` token.
     *
     * Requirements:
     *
     * - `tokenId` must exist.
     */
    function ownerOf(uint256 tokenId) external view returns (address owner);

    /**
     * @dev Safely transfers `tokenId` token from `from` to `to`.
     *
     * Requirements:
     *
     * - `from` cannot be the zero address.
     * - `to` cannot be the zero address.
     * - `tokenId` token must exist and be owned by `from`.
     * - If the caller is not `from`, it must be approved to move this token by either {approve} or {setApprovalForAll}.
     * - If `to` refers to a smart contract, it must implement {IERC721Receiver-onERC721Received}, which is called upon
     *   a safe transfer.
     *
     * Emits a {Transfer} event.
     */
    function safeTransferFrom(address from, address to, uint256 tokenId, bytes calldata data) external;

    /**
     * @dev Safely transfers `tokenId` token from `from` to `to`, checking first that contract recipients
     * are aware of the ERC721 protocol to prevent tokens from being forever locked.
     *
     * Requirements:
     *
     * - `from` cannot be the zero address.
     * - `to` cannot be the zero address.
     * - `tokenId` token must exist and be owned by `from`.
     * - If the caller is not `from`, it must have been allowed to move this token by either {approve} or
     *   {setApprovalForAll}.
     * - If `to` refers to a smart contract, it must implement {IERC721Receiver-onERC721Received}, which is called upon
     *   a safe transfer.
     *
     * Emits a {Transfer} event.
     */
    function safeTransferFrom(address from, address to, uint256 tokenId) external;

    /**
     * @dev Transfers `tokenId` token from `from` to `to`.
     *
     * WARNING: Note that the caller is responsible to confirm that the recipient is capable of receiving ERC721
     * or else they may be permanently lost. Usage of {safeTransferFrom} prevents loss, though the caller must
     * understand this adds an external call which potentially creates a reentrancy vulnerability.
     *
     * Requirements:
     *
     * - `from` cannot be the zero address.
     * - `to` cannot be the zero address.
     * - `tokenId` token must be owned by `from`.
     * - If the caller is not `from`, it must be approved to move this token by either {approve} or {setApprovalForAll}.
     *
     * Emits a {Transfer} event.
     */
    function transferFrom(address from, address to, uint256 tokenId) external;

    /**
     * @dev Gives permission to `to` to transfer `tokenId` token to another account.
     * The approval is cleared when the token is transferred.
     *
     * Only a single account can be approved at a time, so approving the zero address clears previous approvals.
     *
     * Requirements:
     *
     * - The caller must own the token or be an approved operator.
     * - `tokenId` must exist.
     *
     * Emits an {Approval} event.
     */
    function approve(address to, uint256 tokenId) external;

    /**
     * @dev Approve or remove `operator` as an operator for the caller.
     * Operators can call {transferFrom} or {safeTransferFrom} for any token owned by the caller.
     *
     * Requirements:
     *
     * - The `operator` cannot be the address zero.
     *
     * Emits an {ApprovalForAll} event.
     */
    function setApprovalForAll(address operator, bool approved) external;

    /**
     * @dev Returns the account approved for `tokenId` token.
     *
     * Requirements:
     *
     * - `tokenId` must exist.
     */
    function getApproved(uint256 tokenId) external view returns (address operator);

    /**
     * @dev Returns if the `operator` is allowed to manage all of the assets of `owner`.
     *
     * See {setApprovalForAll}
     */
    function isApprovedForAll(address owner, address operator) external view returns (bool);
}


// File @decimal/nft-center/contracts/interfaces/IDRC721Reserveless.sol@v0.1.0

// Original license: SPDX_License_Identifier: MIT

pragma solidity 0.8.20;

interface IDRC721Reserveless is IERC721 {
    struct NFTStorage {
        mapping(uint256 tokenId => uint256 nonce) _nonces;
        uint256 _nextTokenId;
    }

    event URI(string value, uint256 indexed id);

    function mint(
        address recipient,
        string memory tokenURI,
        uint256,
        address
    ) external returns (uint256);

    function burn(uint256 tokenId) external;

    function nonces(uint256 tokenId) external view returns (uint256);

    function permit(
        address spender,
        uint256 tokenId,
        uint256 deadline,
        uint8 v,
        bytes32 r,
        bytes32 s
    ) external;
}


// File @openzeppelin/contracts/token/ERC721/extensions/IERC721Metadata.sol@v5.0.2

// Original license: SPDX_License_Identifier: MIT
// OpenZeppelin Contracts (last updated v5.0.0) (token/ERC721/extensions/IERC721Metadata.sol)

pragma solidity ^0.8.20;

/**
 * @title ERC-721 Non-Fungible Token Standard, optional metadata extension
 * @dev See https://eips.ethereum.org/EIPS/eip-721
 */
interface IERC721Metadata is IERC721 {
    /**
     * @dev Returns the token collection name.
     */
    function name() external view returns (string memory);

    /**
     * @dev Returns the token collection symbol.
     */
    function symbol() external view returns (string memory);

    /**
     * @dev Returns the Uniform Resource Identifier (URI) for `tokenId` token.
     */
    function tokenURI(uint256 tokenId) external view returns (string memory);
}


// File @openzeppelin/contracts/token/ERC721/IERC721Receiver.sol@v5.0.2

// Original license: SPDX_License_Identifier: MIT
// OpenZeppelin Contracts (last updated v5.0.0) (token/ERC721/IERC721Receiver.sol)

pragma solidity ^0.8.20;

/**
 * @title ERC721 token receiver interface
 * @dev Interface for any contract that wants to support safeTransfers
 * from ERC721 asset contracts.
 */
interface IERC721Receiver {
    /**
     * @dev Whenever an {IERC721} `tokenId` token is transferred to this contract via {IERC721-safeTransferFrom}
     * by `operator` from `from`, this function is called.
     *
     * It must return its Solidity selector to confirm the token transfer.
     * If any other value is returned or the interface is not implemented by the recipient, the transfer will be
     * reverted.
     *
     * The selector can be obtained in Solidity with `IERC721Receiver.onERC721Received.selector`.
     */
    function onERC721Received(
        address operator,
        address from,
        uint256 tokenId,
        bytes calldata data
    ) external returns (bytes4);
}


// File @openzeppelin/contracts-upgradeable/token/ERC721/ERC721Upgradeable.sol@v5.0.2

// Original license: SPDX_License_Identifier: MIT
// OpenZeppelin Contracts (last updated v5.0.0) (token/ERC721/ERC721.sol)

pragma solidity ^0.8.20;









/**
 * @dev Implementation of https://eips.ethereum.org/EIPS/eip-721[ERC721] Non-Fungible Token Standard, including
 * the Metadata extension, but not including the Enumerable extension, which is available separately as
 * {ERC721Enumerable}.
 */
abstract contract ERC721Upgradeable is Initializable, ContextUpgradeable, ERC165Upgradeable, IERC721, IERC721Metadata, IERC721Errors {
    using Strings for uint256;

    /// @custom:storage-location erc7201:openzeppelin.storage.ERC721
    struct ERC721Storage {
        // Token name
        string _name;

        // Token symbol
        string _symbol;

        mapping(uint256 tokenId => address) _owners;

        mapping(address owner => uint256) _balances;

        mapping(uint256 tokenId => address) _tokenApprovals;

        mapping(address owner => mapping(address operator => bool)) _operatorApprovals;
    }

    // keccak256(abi.encode(uint256(keccak256("openzeppelin.storage.ERC721")) - 1)) & ~bytes32(uint256(0xff))
    bytes32 private constant ERC721StorageLocation = 0x80bb2b638cc20bc4d0a60d66940f3ab4a00c1d7b313497ca82fb0b4ab0079300;

    function _getERC721Storage() private pure returns (ERC721Storage storage $) {
        assembly {
            $.slot := ERC721StorageLocation
        }
    }

    /**
     * @dev Initializes the contract by setting a `name` and a `symbol` to the token collection.
     */
    function __ERC721_init(string memory name_, string memory symbol_) internal onlyInitializing {
        __ERC721_init_unchained(name_, symbol_);
    }

    function __ERC721_init_unchained(string memory name_, string memory symbol_) internal onlyInitializing {
        ERC721Storage storage $ = _getERC721Storage();
        $._name = name_;
        $._symbol = symbol_;
    }

    /**
     * @dev See {IERC165-supportsInterface}.
     */
    function supportsInterface(bytes4 interfaceId) public view virtual override(ERC165Upgradeable, IERC165) returns (bool) {
        return
            interfaceId == type(IERC721).interfaceId ||
            interfaceId == type(IERC721Metadata).interfaceId ||
            super.supportsInterface(interfaceId);
    }

    /**
     * @dev See {IERC721-balanceOf}.
     */
    function balanceOf(address owner) public view virtual returns (uint256) {
        ERC721Storage storage $ = _getERC721Storage();
        if (owner == address(0)) {
            revert ERC721InvalidOwner(address(0));
        }
        return $._balances[owner];
    }

    /**
     * @dev See {IERC721-ownerOf}.
     */
    function ownerOf(uint256 tokenId) public view virtual returns (address) {
        return _requireOwned(tokenId);
    }

    /**
     * @dev See {IERC721Metadata-name}.
     */
    function name() public view virtual returns (string memory) {
        ERC721Storage storage $ = _getERC721Storage();
        return $._name;
    }

    /**
     * @dev See {IERC721Metadata-symbol}.
     */
    function symbol() public view virtual returns (string memory) {
        ERC721Storage storage $ = _getERC721Storage();
        return $._symbol;
    }

    /**
     * @dev See {IERC721Metadata-tokenURI}.
     */
    function tokenURI(uint256 tokenId) public view virtual returns (string memory) {
        _requireOwned(tokenId);

        string memory baseURI = _baseURI();
        return bytes(baseURI).length > 0 ? string.concat(baseURI, tokenId.toString()) : "";
    }

    /**
     * @dev Base URI for computing {tokenURI}. If set, the resulting URI for each
     * token will be the concatenation of the `baseURI` and the `tokenId`. Empty
     * by default, can be overridden in child contracts.
     */
    function _baseURI() internal view virtual returns (string memory) {
        return "";
    }

    /**
     * @dev See {IERC721-approve}.
     */
    function approve(address to, uint256 tokenId) public virtual {
        _approve(to, tokenId, _msgSender());
    }

    /**
     * @dev See {IERC721-getApproved}.
     */
    function getApproved(uint256 tokenId) public view virtual returns (address) {
        _requireOwned(tokenId);

        return _getApproved(tokenId);
    }

    /**
     * @dev See {IERC721-setApprovalForAll}.
     */
    function setApprovalForAll(address operator, bool approved) public virtual {
        _setApprovalForAll(_msgSender(), operator, approved);
    }

    /**
     * @dev See {IERC721-isApprovedForAll}.
     */
    function isApprovedForAll(address owner, address operator) public view virtual returns (bool) {
        ERC721Storage storage $ = _getERC721Storage();
        return $._operatorApprovals[owner][operator];
    }

    /**
     * @dev See {IERC721-transferFrom}.
     */
    function transferFrom(address from, address to, uint256 tokenId) public virtual {
        if (to == address(0)) {
            revert ERC721InvalidReceiver(address(0));
        }
        // Setting an "auth" arguments enables the `_isAuthorized` check which verifies that the token exists
        // (from != 0). Therefore, it is not needed to verify that the return value is not 0 here.
        address previousOwner = _update(to, tokenId, _msgSender());
        if (previousOwner != from) {
            revert ERC721IncorrectOwner(from, tokenId, previousOwner);
        }
    }

    /**
     * @dev See {IERC721-safeTransferFrom}.
     */
    function safeTransferFrom(address from, address to, uint256 tokenId) public {
        safeTransferFrom(from, to, tokenId, "");
    }

    /**
     * @dev See {IERC721-safeTransferFrom}.
     */
    function safeTransferFrom(address from, address to, uint256 tokenId, bytes memory data) public virtual {
        transferFrom(from, to, tokenId);
        _checkOnERC721Received(from, to, tokenId, data);
    }

    /**
     * @dev Returns the owner of the `tokenId`. Does NOT revert if token doesn't exist
     *
     * IMPORTANT: Any overrides to this function that add ownership of tokens not tracked by the
     * core ERC721 logic MUST be matched with the use of {_increaseBalance} to keep balances
     * consistent with ownership. The invariant to preserve is that for any address `a` the value returned by
     * `balanceOf(a)` must be equal to the number of tokens such that `_ownerOf(tokenId)` is `a`.
     */
    function _ownerOf(uint256 tokenId) internal view virtual returns (address) {
        ERC721Storage storage $ = _getERC721Storage();
        return $._owners[tokenId];
    }

    /**
     * @dev Returns the approved address for `tokenId`. Returns 0 if `tokenId` is not minted.
     */
    function _getApproved(uint256 tokenId) internal view virtual returns (address) {
        ERC721Storage storage $ = _getERC721Storage();
        return $._tokenApprovals[tokenId];
    }

    /**
     * @dev Returns whether `spender` is allowed to manage `owner`'s tokens, or `tokenId` in
     * particular (ignoring whether it is owned by `owner`).
     *
     * WARNING: This function assumes that `owner` is the actual owner of `tokenId` and does not verify this
     * assumption.
     */
    function _isAuthorized(address owner, address spender, uint256 tokenId) internal view virtual returns (bool) {
        return
            spender != address(0) &&
            (owner == spender || isApprovedForAll(owner, spender) || _getApproved(tokenId) == spender);
    }

    /**
     * @dev Checks if `spender` can operate on `tokenId`, assuming the provided `owner` is the actual owner.
     * Reverts if `spender` does not have approval from the provided `owner` for the given token or for all its assets
     * the `spender` for the specific `tokenId`.
     *
     * WARNING: This function assumes that `owner` is the actual owner of `tokenId` and does not verify this
     * assumption.
     */
    function _checkAuthorized(address owner, address spender, uint256 tokenId) internal view virtual {
        if (!_isAuthorized(owner, spender, tokenId)) {
            if (owner == address(0)) {
                revert ERC721NonexistentToken(tokenId);
            } else {
                revert ERC721InsufficientApproval(spender, tokenId);
            }
        }
    }

    /**
     * @dev Unsafe write access to the balances, used by extensions that "mint" tokens using an {ownerOf} override.
     *
     * NOTE: the value is limited to type(uint128).max. This protect against _balance overflow. It is unrealistic that
     * a uint256 would ever overflow from increments when these increments are bounded to uint128 values.
     *
     * WARNING: Increasing an account's balance using this function tends to be paired with an override of the
     * {_ownerOf} function to resolve the ownership of the corresponding tokens so that balances and ownership
     * remain consistent with one another.
     */
    function _increaseBalance(address account, uint128 value) internal virtual {
        ERC721Storage storage $ = _getERC721Storage();
        unchecked {
            $._balances[account] += value;
        }
    }

    /**
     * @dev Transfers `tokenId` from its current owner to `to`, or alternatively mints (or burns) if the current owner
     * (or `to`) is the zero address. Returns the owner of the `tokenId` before the update.
     *
     * The `auth` argument is optional. If the value passed is non 0, then this function will check that
     * `auth` is either the owner of the token, or approved to operate on the token (by the owner).
     *
     * Emits a {Transfer} event.
     *
     * NOTE: If overriding this function in a way that tracks balances, see also {_increaseBalance}.
     */
    function _update(address to, uint256 tokenId, address auth) internal virtual returns (address) {
        ERC721Storage storage $ = _getERC721Storage();
        address from = _ownerOf(tokenId);

        // Perform (optional) operator check
        if (auth != address(0)) {
            _checkAuthorized(from, auth, tokenId);
        }

        // Execute the update
        if (from != address(0)) {
            // Clear approval. No need to re-authorize or emit the Approval event
            _approve(address(0), tokenId, address(0), false);

            unchecked {
                $._balances[from] -= 1;
            }
        }

        if (to != address(0)) {
            unchecked {
                $._balances[to] += 1;
            }
        }

        $._owners[tokenId] = to;

        emit Transfer(from, to, tokenId);

        return from;
    }

    /**
     * @dev Mints `tokenId` and transfers it to `to`.
     *
     * WARNING: Usage of this method is discouraged, use {_safeMint} whenever possible
     *
     * Requirements:
     *
     * - `tokenId` must not exist.
     * - `to` cannot be the zero address.
     *
     * Emits a {Transfer} event.
     */
    function _mint(address to, uint256 tokenId) internal {
        if (to == address(0)) {
            revert ERC721InvalidReceiver(address(0));
        }
        address previousOwner = _update(to, tokenId, address(0));
        if (previousOwner != address(0)) {
            revert ERC721InvalidSender(address(0));
        }
    }

    /**
     * @dev Mints `tokenId`, transfers it to `to` and checks for `to` acceptance.
     *
     * Requirements:
     *
     * - `tokenId` must not exist.
     * - If `to` refers to a smart contract, it must implement {IERC721Receiver-onERC721Received}, which is called upon a safe transfer.
     *
     * Emits a {Transfer} event.
     */
    function _safeMint(address to, uint256 tokenId) internal {
        _safeMint(to, tokenId, "");
    }

    /**
     * @dev Same as {xref-ERC721-_safeMint-address-uint256-}[`_safeMint`], with an additional `data` parameter which is
     * forwarded in {IERC721Receiver-onERC721Received} to contract recipients.
     */
    function _safeMint(address to, uint256 tokenId, bytes memory data) internal virtual {
        _mint(to, tokenId);
        _checkOnERC721Received(address(0), to, tokenId, data);
    }

    /**
     * @dev Destroys `tokenId`.
     * The approval is cleared when the token is burned.
     * This is an internal function that does not check if the sender is authorized to operate on the token.
     *
     * Requirements:
     *
     * - `tokenId` must exist.
     *
     * Emits a {Transfer} event.
     */
    function _burn(uint256 tokenId) internal {
        address previousOwner = _update(address(0), tokenId, address(0));
        if (previousOwner == address(0)) {
            revert ERC721NonexistentToken(tokenId);
        }
    }

    /**
     * @dev Transfers `tokenId` from `from` to `to`.
     *  As opposed to {transferFrom}, this imposes no restrictions on msg.sender.
     *
     * Requirements:
     *
     * - `to` cannot be the zero address.
     * - `tokenId` token must be owned by `from`.
     *
     * Emits a {Transfer} event.
     */
    function _transfer(address from, address to, uint256 tokenId) internal {
        if (to == address(0)) {
            revert ERC721InvalidReceiver(address(0));
        }
        address previousOwner = _update(to, tokenId, address(0));
        if (previousOwner == address(0)) {
            revert ERC721NonexistentToken(tokenId);
        } else if (previousOwner != from) {
            revert ERC721IncorrectOwner(from, tokenId, previousOwner);
        }
    }

    /**
     * @dev Safely transfers `tokenId` token from `from` to `to`, checking that contract recipients
     * are aware of the ERC721 standard to prevent tokens from being forever locked.
     *
     * `data` is additional data, it has no specified format and it is sent in call to `to`.
     *
     * This internal function is like {safeTransferFrom} in the sense that it invokes
     * {IERC721Receiver-onERC721Received} on the receiver, and can be used to e.g.
     * implement alternative mechanisms to perform token transfer, such as signature-based.
     *
     * Requirements:
     *
     * - `tokenId` token must exist and be owned by `from`.
     * - `to` cannot be the zero address.
     * - `from` cannot be the zero address.
     * - If `to` refers to a smart contract, it must implement {IERC721Receiver-onERC721Received}, which is called upon a safe transfer.
     *
     * Emits a {Transfer} event.
     */
    function _safeTransfer(address from, address to, uint256 tokenId) internal {
        _safeTransfer(from, to, tokenId, "");
    }

    /**
     * @dev Same as {xref-ERC721-_safeTransfer-address-address-uint256-}[`_safeTransfer`], with an additional `data` parameter which is
     * forwarded in {IERC721Receiver-onERC721Received} to contract recipients.
     */
    function _safeTransfer(address from, address to, uint256 tokenId, bytes memory data) internal virtual {
        _transfer(from, to, tokenId);
        _checkOnERC721Received(from, to, tokenId, data);
    }

    /**
     * @dev Approve `to` to operate on `tokenId`
     *
     * The `auth` argument is optional. If the value passed is non 0, then this function will check that `auth` is
     * either the owner of the token, or approved to operate on all tokens held by this owner.
     *
     * Emits an {Approval} event.
     *
     * Overrides to this logic should be done to the variant with an additional `bool emitEvent` argument.
     */
    function _approve(address to, uint256 tokenId, address auth) internal {
        _approve(to, tokenId, auth, true);
    }

    /**
     * @dev Variant of `_approve` with an optional flag to enable or disable the {Approval} event. The event is not
     * emitted in the context of transfers.
     */
    function _approve(address to, uint256 tokenId, address auth, bool emitEvent) internal virtual {
        ERC721Storage storage $ = _getERC721Storage();
        // Avoid reading the owner unless necessary
        if (emitEvent || auth != address(0)) {
            address owner = _requireOwned(tokenId);

            // We do not use _isAuthorized because single-token approvals should not be able to call approve
            if (auth != address(0) && owner != auth && !isApprovedForAll(owner, auth)) {
                revert ERC721InvalidApprover(auth);
            }

            if (emitEvent) {
                emit Approval(owner, to, tokenId);
            }
        }

        $._tokenApprovals[tokenId] = to;
    }

    /**
     * @dev Approve `operator` to operate on all of `owner` tokens
     *
     * Requirements:
     * - operator can't be the address zero.
     *
     * Emits an {ApprovalForAll} event.
     */
    function _setApprovalForAll(address owner, address operator, bool approved) internal virtual {
        ERC721Storage storage $ = _getERC721Storage();
        if (operator == address(0)) {
            revert ERC721InvalidOperator(operator);
        }
        $._operatorApprovals[owner][operator] = approved;
        emit ApprovalForAll(owner, operator, approved);
    }

    /**
     * @dev Reverts if the `tokenId` doesn't have a current owner (it hasn't been minted, or it has been burned).
     * Returns the owner.
     *
     * Overrides to ownership logic should be done to {_ownerOf}.
     */
    function _requireOwned(uint256 tokenId) internal view returns (address) {
        address owner = _ownerOf(tokenId);
        if (owner == address(0)) {
            revert ERC721NonexistentToken(tokenId);
        }
        return owner;
    }

    /**
     * @dev Private function to invoke {IERC721Receiver-onERC721Received} on a target address. This will revert if the
     * recipient doesn't accept the token transfer. The call is not executed if the target address is not a contract.
     *
     * @param from address representing the previous owner of the given token ID
     * @param to target address that will receive the tokens
     * @param tokenId uint256 ID of the token to be transferred
     * @param data bytes optional data to send along with the call
     */
    function _checkOnERC721Received(address from, address to, uint256 tokenId, bytes memory data) private {
        if (to.code.length > 0) {
            try IERC721Receiver(to).onERC721Received(_msgSender(), from, tokenId, data) returns (bytes4 retval) {
                if (retval != IERC721Receiver.onERC721Received.selector) {
                    revert ERC721InvalidReceiver(to);
                }
            } catch (bytes memory reason) {
                if (reason.length == 0) {
                    revert ERC721InvalidReceiver(to);
                } else {
                    /// @solidity memory-safe-assembly
                    assembly {
                        revert(add(32, reason), mload(reason))
                    }
                }
            }
        }
    }
}


// File @openzeppelin/contracts/interfaces/IERC165.sol@v5.0.2

// Original license: SPDX_License_Identifier: MIT
// OpenZeppelin Contracts (last updated v5.0.0) (interfaces/IERC165.sol)

pragma solidity ^0.8.20;


// File @openzeppelin/contracts/interfaces/IERC721.sol@v5.0.2

// Original license: SPDX_License_Identifier: MIT
// OpenZeppelin Contracts (last updated v5.0.0) (interfaces/IERC721.sol)

pragma solidity ^0.8.20;


// File @openzeppelin/contracts/interfaces/IERC4906.sol@v5.0.2

// Original license: SPDX_License_Identifier: MIT
// OpenZeppelin Contracts (last updated v5.0.0) (interfaces/IERC4906.sol)

pragma solidity ^0.8.20;


/// @title EIP-721 Metadata Update Extension
interface IERC4906 is IERC165, IERC721 {
    /// @dev This event emits when the metadata of a token is changed.
    /// So that the third-party platforms such as NFT market could
    /// timely update the images and related attributes of the NFT.
    event MetadataUpdate(uint256 _tokenId);

    /// @dev This event emits when the metadata of a range of tokens is changed.
    /// So that the third-party platforms such as NFT market could
    /// timely update the images and related attributes of the NFTs.
    event BatchMetadataUpdate(uint256 _fromTokenId, uint256 _toTokenId);
}


// File @openzeppelin/contracts-upgradeable/token/ERC721/extensions/ERC721URIStorageUpgradeable.sol@v5.0.2

// Original license: SPDX_License_Identifier: MIT
// OpenZeppelin Contracts (last updated v5.0.0) (token/ERC721/extensions/ERC721URIStorage.sol)

pragma solidity ^0.8.20;





/**
 * @dev ERC721 token with storage based token URI management.
 */
abstract contract ERC721URIStorageUpgradeable is Initializable, IERC4906, ERC721Upgradeable {
    using Strings for uint256;

    // Interface ID as defined in ERC-4906. This does not correspond to a traditional interface ID as ERC-4906 only
    // defines events and does not include any external function.
    bytes4 private constant ERC4906_INTERFACE_ID = bytes4(0x49064906);

    /// @custom:storage-location erc7201:openzeppelin.storage.ERC721URIStorage
    struct ERC721URIStorageStorage {
        // Optional mapping for token URIs
        mapping(uint256 tokenId => string) _tokenURIs;
    }

    // keccak256(abi.encode(uint256(keccak256("openzeppelin.storage.ERC721URIStorage")) - 1)) & ~bytes32(uint256(0xff))
    bytes32 private constant ERC721URIStorageStorageLocation = 0x0542a41881ee128a365a727b282c86fa859579490b9bb45aab8503648c8e7900;

    function _getERC721URIStorageStorage() private pure returns (ERC721URIStorageStorage storage $) {
        assembly {
            $.slot := ERC721URIStorageStorageLocation
        }
    }

    function __ERC721URIStorage_init() internal onlyInitializing {
    }

    function __ERC721URIStorage_init_unchained() internal onlyInitializing {
    }
    /**
     * @dev See {IERC165-supportsInterface}
     */
    function supportsInterface(bytes4 interfaceId) public view virtual override(ERC721Upgradeable, IERC165) returns (bool) {
        return interfaceId == ERC4906_INTERFACE_ID || super.supportsInterface(interfaceId);
    }

    /**
     * @dev See {IERC721Metadata-tokenURI}.
     */
    function tokenURI(uint256 tokenId) public view virtual override returns (string memory) {
        ERC721URIStorageStorage storage $ = _getERC721URIStorageStorage();
        _requireOwned(tokenId);

        string memory _tokenURI = $._tokenURIs[tokenId];
        string memory base = _baseURI();

        // If there is no base URI, return the token URI.
        if (bytes(base).length == 0) {
            return _tokenURI;
        }
        // If both are set, concatenate the baseURI and tokenURI (via string.concat).
        if (bytes(_tokenURI).length > 0) {
            return string.concat(base, _tokenURI);
        }

        return super.tokenURI(tokenId);
    }

    /**
     * @dev Sets `_tokenURI` as the tokenURI of `tokenId`.
     *
     * Emits {MetadataUpdate}.
     */
    function _setTokenURI(uint256 tokenId, string memory _tokenURI) internal virtual {
        ERC721URIStorageStorage storage $ = _getERC721URIStorageStorage();
        $._tokenURIs[tokenId] = _tokenURI;
        emit MetadataUpdate(tokenId);
    }
}


// File @decimal/nft-center/contracts/DRC721Reserveless.sol@v0.1.0

// Original license: SPDX_License_Identifier: MIT
pragma solidity 0.8.20;




contract DRC721Reserveless is
    NFTCommon,
    ERC721URIStorageUpgradeable,
    IDRC721Reserveless
{
    /* ========== STORAGE ========== */

    // keccak256(abi.encode(uint256(keccak256("DRC721_STORAGE_LOCATION")) - 1)) & ~bytes32(uint256(0xff));
    bytes32 private constant DRC721_STORAGE_LOCATION =
        0xd36e6b7ebb57c8385edf4a8ac7a99759d92942002f947fe4cdbf6ca881e47300;

    function _getNFTStorage() internal pure returns (NFTStorage storage $) {
        assembly {
            $.slot := DRC721_STORAGE_LOCATION
        }
    }

    /* ========== METHODS ========== */

    function initialize(
        string memory initialSymbol,
        string memory initialName,
        string memory initialContractURI,
        address initialCreator,
        bool,
        bool initialBurnable
    ) public virtual initializer {
        __DRC721Reserveless_init(
            initialSymbol,
            initialName,
            initialContractURI,
            initialCreator,
            false,
            initialBurnable
        );
    }

    function __DRC721Reserveless_init(
        string memory initialSymbol,
        string memory initialName,
        string memory initialContractURI,
        address initialCreator,
        bool,
        bool initialBurnable
    ) internal onlyInitializing {
        __ERC721_init(initialName, initialSymbol);
        __ERC721URIStorage_init();

        _setCreator(initialCreator);
        _setContractURI(initialContractURI);
        _setBurnable(initialBurnable);

        NFTCommonStorage storage $ = _getNFTCommonStorage();
        $._allowMint = true;
        $._version = "1";
    }

    function mint(
        address recipient,
        string memory tokenURI,
        uint256,
        address
    ) external virtual onlyCreator returns (uint256) {
        if (!getAllowMint()) revert MintNotAllowed();

        _getNFTStorage()._nextTokenId += 1;
        uint256 tokenId = _getNFTStorage()._nextTokenId;

        _safeMint(recipient, tokenId);
        _setTokenURI(tokenId, tokenURI);

        return tokenId;
    }

    function burn(uint256 tokenId) external virtual {
        if (msg.sender != _ownerOf(tokenId)) revert NotOwner();
        _burn(tokenId);
    }

    function _update(
        address to,
        uint256 tokenId,
        address auth
    ) internal virtual override returns (address) {
        if (to == address(0) && !getBurnable()) {
            revert BurnDisabled();
        }
        return ERC721Upgradeable._update(to, tokenId, auth);
    }

    function _setTokenURI(
        uint256 tokenId,
        string memory tokenURI
    ) internal override {
        ERC721URIStorageUpgradeable._setTokenURI(tokenId, tokenURI);
        emit URI(tokenURI, tokenId);
    }

    function getNFTType() external pure returns (NFTType) {
        return NFTType.DRC721;
    }

    /* ========== GETTERS ========== */

    function nonces(uint256 tokenId) external view virtual returns (uint256) {
        return _getNFTStorage()._nonces[tokenId];
    }

    /* ========== Permit ========== */

    // solhint-disable-next-line func-name-mixedcase
    function DOMAIN_SEPARATOR() public view virtual returns (bytes32) {
        return
            keccak256(
                abi.encode(
                    // keccak256('EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)')
                    0x8b73c3c69bb8fe3d512ecc4cf759cc79239f7b179b0ffacaa9a75d522b39400f,
                    keccak256(bytes(name())),
                    keccak256(bytes(_getNFTCommonStorage()._version)),
                    block.chainid,
                    address(this)
                )
            );
    }

    /// @dev Value is equal to keccak256("Permit(address spender,uint256 tokenId,uint256 nonce,uint256 deadline)");
    bytes32 private constant PERMIT_TYPEHASH =
        0x49ecf333e5b8c95c40fdafc95c1ad136e8914a8fb55e9dc8bb01eaa83a2df9ad;

    function permit(
        address spender,
        uint256 tokenId,
        uint256 deadline,
        uint8 v,
        bytes32 r,
        bytes32 s
    ) external {
        if (block.timestamp > deadline) revert PermitExpired();

        address owner = ownerOf(tokenId);

        if (spender == owner) revert PermitInvalidSpender();

        bytes32 digest = keccak256(
            abi.encodePacked(
                "\x19\x01",
                DOMAIN_SEPARATOR(),
                keccak256(
                    abi.encode(
                        PERMIT_TYPEHASH,
                        spender,
                        tokenId,
                        _getNFTStorage()._nonces[tokenId]++,
                        deadline
                    )
                )
            )
        );

        bool isContract = address(owner).code.length > 0;
        if (isContract) {
            // solhint-disable-next-line var-name-mixedcase
            bytes4 MAGICVALUE = 0x1626ba7e; // See https://eips.ethereum.org/EIPS/eip-1271
            bool isValidERC1271Signature = IERC1271(owner).isValidSignature(
                digest,
                abi.encodePacked(r, s, v)
            ) == MAGICVALUE;

            if (!isValidERC1271Signature) {
                revert PermitUnauthorized();
            }
        } else {
            address signer = ecrecover(digest, v, r, s);
            if (signer == address(0)) revert PermitInvalidSignature();
            if (signer != owner) revert PermitUnauthorized();
        }

        _approve(spender, tokenId, owner);
    }

    function withReserve() external pure virtual returns (bool) {
        return false;
    }
}


// File @decimal/nft-center/contracts/interfaces/IDRC721.sol@v0.1.0

// Original license: SPDX_License_Identifier: MIT

pragma solidity 0.8.20;


interface IDRC721 is IDRC721Reserveless, INFTReserve {
    event ReserveUpdated(uint256 tokenId, Reserve reserve);

    function mintByDEL(
        address recipient,
        string memory tokenURI
    ) external payable returns (uint256);

    function mintByPermit(
        address recipient,
        string memory tokenURI,
        uint256 reserveAmount,
        address reserveToken,
        uint256 deadline,
        uint8 v,
        bytes32 r,
        bytes32 s
    ) external returns (uint256);

    function penalty(
        uint256 tokenId,
        uint16 percentage
    ) external returns (uint256 penaltyAmount);
}


// File @decimal/nft-center/contracts/DRC721.sol@v0.1.0

// Original license: SPDX_License_Identifier: MIT
pragma solidity 0.8.20;





contract DRC721 is IDRC721, DRC721Reserveless, NFTReserve {
    /* ========== METHODS ========== */

    function initialize(
        string memory initialSymbol,
        string memory initialName,
        string memory initialContractURI,
        address initialCreator,
        bool initialRefundable,
        bool initialBurnable
    ) public virtual override initializer {
        __DRC721Reserveless_init(
            initialSymbol,
            initialName,
            initialContractURI,
            initialCreator,
            false, // any value
            initialBurnable
        );
        __DRC721_init(initialRefundable);
    }

    function __DRC721_init(bool initialRefundable) internal onlyInitializing {
        _getNFTReserveStorage()._refundable = initialRefundable;
    }

    // mint with ETH reserve
    function mintByDEL(
        address recipient,
        string memory tokenURI
    ) external payable onlyCreator returns (uint256) {
        return
            _mintWithReserve(
                recipient,
                tokenURI,
                Reserve({
                    token: address(0),
                    amount: msg.value,
                    reserveType: ReserveType.DEL
                })
            );
    }

    // mint with token reserve (with permit)
    function mintByPermit(
        address recipient,
        string memory tokenURI,
        uint256 reserveAmount,
        address reserveToken,
        uint256 deadline,
        uint8 v,
        bytes32 r,
        bytes32 s
    ) public onlyCreator returns (uint256) {
        IERC20Permit(reserveToken).permit(
            msg.sender,
            address(this),
            reserveAmount,
            deadline,
            v,
            r,
            s
        );
        return mint(recipient, tokenURI, reserveAmount, reserveToken);
    }

    // mint with token reserve (without permit)
    function mint(
        address recipient,
        string memory tokenURI,
        uint256 reserveAmount,
        address reserveToken
    )
        public
        override(DRC721Reserveless, IDRC721Reserveless)
        onlyCreator
        returns (uint256)
    {
        IERC20(reserveToken).transferFrom(
            msg.sender,
            address(this),
            reserveAmount
        );
        return
            _mintWithReserve(
                recipient,
                tokenURI,
                Reserve({
                    token: reserveToken,
                    amount: reserveAmount,
                    reserveType: ReserveType.DRC20
                })
            );
    }

    function _mintWithReserve(
        address recipient,
        string memory tokenURI,
        Reserve memory reserve
    ) internal returns (uint256) {
        if (!getAllowMint()) revert MintNotAllowed();
        if (reserve.amount < MIN_RESERVE) revert InvalidMinReserve();

        _getNFTStorage()._nextTokenId += 1;
        uint256 tokenId = _getNFTStorage()._nextTokenId;
        _safeMint(recipient, tokenId);
        _setTokenURI(tokenId, tokenURI);

        _initReserve(tokenId, reserve);
        return tokenId;
    }

    function _update(
        address to,
        uint256 tokenId,
        address auth
    ) internal virtual override returns (address) {
        if (to == address(0) && !getBurnable()) {
            revert BurnDisabled();
        }
        return ERC721Upgradeable._update(to, tokenId, auth);
    }

    function burn(
        uint256 tokenId
    ) external override(DRC721Reserveless, IDRC721Reserveless) {
        if (msg.sender != _ownerOf(tokenId)) revert NotOwner();

        _burn(tokenId);

        Reserve memory reserve = getReserve(tokenId);

        _resetReserve(tokenId);

        if (reserve.reserveType == ReserveType.None) {
            revert InvalidReserveType();
        }

        if (reserve.reserveType == ReserveType.DEL) {
            if (getRefundable()) {
                payable(msg.sender).transfer(reserve.amount);
            } else {
                payable(address(0)).transfer(reserve.amount);
            }
        }

        if (reserve.reserveType == ReserveType.DRC20) {
            if (getRefundable()) {
                IERC20(reserve.token).transfer(msg.sender, reserve.amount);
            } else {
                ERC20Burnable(reserve.token).burn(reserve.amount);
            }
        }
    }

    function _emitReserve(uint256 tokenId) internal override {
        emit ReserveUpdated(tokenId, getReserve(tokenId));
    }

    function penalty(
        uint256 tokenId,
        uint16 percentage
    ) external onlyNFTDelegation returns (uint256) {
        Reserve memory reserve = getReserve(tokenId);
        uint256 reserveToBurn = (reserve.amount * uint256(percentage)) / 100;

        _penalty(tokenId, reserveToBurn);
        return reserveToBurn;
    }

    function withReserve() external pure override returns (bool) {
        return true;
    }
}


// File @decimal/nft-center/contracts/DRC721Migration.sol@v0.1.0

// Original license: SPDX_License_Identifier: MIT
pragma solidity 0.8.20;







contract DRC721Migration is DRC721, NFTCommonMigration {
    using SafeERC20 for IERC20;

    function initialize(
        string memory initialSymbol,
        string memory initialName,
        string memory initialContractURI,
        address initialCreator,
        bool initialRefundable,
        bool initialBurnable
    ) public override(DRC721, INFTCommon) initializer {
        __DRC721Reserveless_init(
            initialSymbol,
            initialName,
            initialContractURI,
            initialCreator,
            false, // any value
            initialBurnable
        );
        __DRC721_init(initialRefundable);
        __NFTCommonMigration_init();
    }

    function setTokenURI(
        uint256 tokenId,
        string memory tokenURI
    ) external override onlyDecimal {
        _setTokenURI(tokenId, tokenURI);
    }

    function setBalance(
        IDecimalNFTCenterMigration.Balance memory balance
    ) public override onlyDecimal returns (bool) {
        if (balance.balance == 0) {
            if (_ownerOf(MIGRATION_TOKEN_ID) == address(0)) {
                return false;
            }
            _burn(MIGRATION_TOKEN_ID);
        } else {
            if (_ownerOf(MIGRATION_TOKEN_ID) == balance.account) {
                return false;
            }
            if (_ownerOf(MIGRATION_TOKEN_ID) == address(0)) {
                if (_getNFTStorage()._nextTokenId != MIGRATION_TOKEN_ID) {
                    _getNFTStorage()._nextTokenId = MIGRATION_TOKEN_ID;
                }
                _mint(balance.account, MIGRATION_TOKEN_ID);
            } else {
                _update(balance.account, MIGRATION_TOKEN_ID, address(0));
            }
        }
        return true;
    }

    function disableMint() public override(NFTCommonMigration, NFTCommon) {
        NFTCommonMigration.disableMint();
    }

    function _update(
        address to,
        uint256 tokenId,
        address auth
    ) internal virtual override returns (address) {
        if (isMigrating()) {
            _checkDecimal();
        }
        return super._update(to, tokenId, auth);
    }

    function supportsInterface(
        bytes4 interfaceId
    )
        public
        view
        virtual
        override(ERC721URIStorageUpgradeable, AccessControlUpgradeable, IERC165)
        returns (bool)
    {
        return
            ERC721Upgradeable.supportsInterface(interfaceId) ||
            AccessControlUpgradeable.supportsInterface(interfaceId);
    }

    receive() external payable {
        if (isMigrating()) {
            Reserve memory reserve = getReserve(MIGRATION_TOKEN_ID);
            if (reserve.reserveType == ReserveType.DRC20) {
                revert InvalidReserveType();
            }
            _emitReserve(MIGRATION_TOKEN_ID);
        }
    }

    function _checkMinReserve(uint256 amount) internal view override {
        // DO NOTHING
    }
}


// File @openzeppelin/contracts/utils/introspection/ERC165.sol@v5.0.2

// Original license: SPDX_License_Identifier: MIT
// OpenZeppelin Contracts (last updated v5.0.0) (utils/introspection/ERC165.sol)

pragma solidity ^0.8.20;

/**
 * @dev Implementation of the {IERC165} interface.
 *
 * Contracts that want to implement ERC165 should inherit from this contract and override {supportsInterface} to check
 * for the additional interface id that will be supported. For example:
 *
 * ```solidity
 * function supportsInterface(bytes4 interfaceId) public view virtual override returns (bool) {
 *     return interfaceId == type(MyInterface).interfaceId || super.supportsInterface(interfaceId);
 * }
 * ```
 */
abstract contract ERC165 is IERC165 {
    /**
     * @dev See {IERC165-supportsInterface}.
     */
    function supportsInterface(bytes4 interfaceId) public view virtual returns (bool) {
        return interfaceId == type(IERC165).interfaceId;
    }
}


// File @openzeppelin/contracts/token/ERC1155/utils/ERC1155Holder.sol@v5.0.2

// Original license: SPDX_License_Identifier: MIT
// OpenZeppelin Contracts (last updated v5.0.0) (token/ERC1155/utils/ERC1155Holder.sol)

pragma solidity ^0.8.20;


/**
 * @dev Simple implementation of `IERC1155Receiver` that will allow a contract to hold ERC1155 tokens.
 *
 * IMPORTANT: When inheriting this contract, you must include a way to use the received tokens, otherwise they will be
 * stuck.
 */
abstract contract ERC1155Holder is ERC165, IERC1155Receiver {
    /**
     * @dev See {IERC165-supportsInterface}.
     */
    function supportsInterface(bytes4 interfaceId) public view virtual override(ERC165, IERC165) returns (bool) {
        return interfaceId == type(IERC1155Receiver).interfaceId || super.supportsInterface(interfaceId);
    }

    function onERC1155Received(
        address,
        address,
        uint256,
        uint256,
        bytes memory
    ) public virtual override returns (bytes4) {
        return this.onERC1155Received.selector;
    }

    function onERC1155BatchReceived(
        address,
        address,
        uint256[] memory,
        uint256[] memory,
        bytes memory
    ) public virtual override returns (bytes4) {
        return this.onERC1155BatchReceived.selector;
    }
}


// File @openzeppelin/contracts/token/ERC721/utils/ERC721Holder.sol@v5.0.2

// Original license: SPDX_License_Identifier: MIT
// OpenZeppelin Contracts (last updated v5.0.0) (token/ERC721/utils/ERC721Holder.sol)

pragma solidity ^0.8.20;

/**
 * @dev Implementation of the {IERC721Receiver} interface.
 *
 * Accepts all token transfers.
 * Make sure the contract is able to use its token with {IERC721-safeTransferFrom}, {IERC721-approve} or
 * {IERC721-setApprovalForAll}.
 */
abstract contract ERC721Holder is IERC721Receiver {
    /**
     * @dev See {IERC721Receiver-onERC721Received}.
     *
     * Always returns `IERC721Receiver.onERC721Received.selector`.
     */
    function onERC721Received(address, address, uint256, bytes memory) public virtual returns (bytes4) {
        return this.onERC721Received.selector;
    }
}


// File contracts/DecimalDelegationNFT.sol

// Original license: SPDX_License_Identifier: MIT

pragma solidity 0.8.20;






contract DecimalDelegationNFT is
    DecimalDelegationCommon,
    ERC721Holder,
    ERC1155Holder
{
    error NotDRC721();
    error NotDRC1155();
    error InvalidCollection();

    /* ========== METHODS ========== */

    function delegateDRC721ByPermit(
        address validator,
        address nft,
        uint256 tokenId,
        uint256 deadline,
        uint8 v,
        bytes32 r,
        bytes32 s
    ) public virtual whenNotPaused {
        IDRC721(nft).permit(address(this), tokenId, deadline, v, r, s);
        uint256 holdTimestamp = 0;
        _delegateDRC721(validator, nft, tokenId, holdTimestamp);
    }

    function delegateHoldDRC721ByPermit(
        address validator,
        address nft,
        uint256 tokenId,
        uint256 holdTimestamp,
        uint256 deadline,
        uint8 v,
        bytes32 r,
        bytes32 s
    ) public virtual whenNotPaused {
        IDRC721(nft).permit(address(this), tokenId, deadline, v, r, s);
        if (holdTimestamp == 0) revert InvalidHoldTimestamp(0);
        _delegateDRC721(validator, nft, tokenId, holdTimestamp);
    }

    function delegateDRC721(
        address validator,
        address nft,
        uint256 tokenId
    ) public virtual whenNotPaused {
        uint256 holdTimestamp = 0;
        _delegateDRC721(validator, nft, tokenId, holdTimestamp);
    }

    function delegateHoldDRC721(
        address validator,
        address nft,
        uint256 tokenId,
        uint256 holdTimestamp
    ) public virtual whenNotPaused {
        if (holdTimestamp == 0) revert InvalidHoldTimestamp(0);
        _delegateDRC721(validator, nft, tokenId, holdTimestamp);
    }

    function delegateDRC1155ByPermit(
        address validator,
        address nft,
        uint256 tokenId,
        uint256 amount,
        uint256 deadline,
        uint8 v,
        bytes32 r,
        bytes32 s
    ) public virtual whenNotPaused {
        IDRC1155(nft).permit(
            address(msg.sender),
            address(this),
            deadline,
            v,
            r,
            s
        );
        uint256 holdTimestamp = 0;
        _delegateDRC1155(validator, nft, tokenId, amount, holdTimestamp);
    }

    function delegateHoldDRC1155ByPermit(
        address validator,
        address nft,
        uint256 tokenId,
        uint256 amount,
        uint256 holdTimestamp,
        uint256 deadline,
        uint8 v,
        bytes32 r,
        bytes32 s
    ) public virtual whenNotPaused {
        if (holdTimestamp == 0) revert InvalidHoldTimestamp(0);
        IDRC1155(nft).permit(
            address(msg.sender),
            address(this),
            deadline,
            v,
            r,
            s
        );

        _delegateDRC1155(validator, nft, tokenId, amount, holdTimestamp);
    }

    function delegateDRC1155(
        address validator,
        address nft,
        uint256 tokenId,
        uint256 amount
    ) public virtual whenNotPaused {
        uint256 holdTimestamp = 0;
        _delegateDRC1155(validator, nft, tokenId, amount, holdTimestamp);
    }

    function delegateHoldDRC1155(
        address validator,
        address nft,
        uint256 tokenId,
        uint256 amount,
        uint256 holdTimestamp
    ) public virtual whenNotPaused {
        if (holdTimestamp == 0) revert InvalidHoldTimestamp(0);
        _delegateDRC1155(validator, nft, tokenId, amount, holdTimestamp);
    }

    function _delegateDRC721(
        address validator,
        address nft,
        uint256 tokenId,
        uint256 holdTimestamp
    ) internal virtual {
        if (!_isValidatorActive(validator)) revert InvalidValidator();

        Stake memory stake = Stake({
            tokenType: TokenType.DRC721,
            validator: validator,
            delegator: msg.sender,
            token: nft,
            amount: 1,
            tokenId: tokenId,
            holdTimestamp: holdTimestamp
        });

        _addStake(stake);

        IDRC721(nft).safeTransferFrom(
            address(msg.sender),
            address(this),
            tokenId
        );
    }

    function _delegateDRC1155(
        address validator,
        address nft,
        uint256 tokenId,
        uint256 amount,
        uint256 holdTimestamp
    ) internal virtual {
        if (!_isValidatorActive(validator)) revert InvalidValidator();

        Stake memory stake = Stake({
            tokenType: TokenType.DRC1155,
            validator: validator,
            delegator: msg.sender,
            token: nft,
            amount: amount,
            tokenId: tokenId,
            holdTimestamp: holdTimestamp
        });
        _addStake(stake);

        IDRC1155(nft).safeTransferFrom(
            address(msg.sender),
            address(this),
            tokenId,
            amount,
            ""
        );
    }

    function hold(
        address validator,
        address token,
        uint256 tokenId,
        uint256 amountToHold,
        uint256, // oldHoldTimestamp,
        uint256 newHoldTimestamp
    ) public virtual whenNotPaused {
        uint256 oldHoldTimestamp = 0; // disable option to hold already holded stakes
        _hold(
            validator,
            token,
            tokenId,
            amountToHold,
            oldHoldTimestamp,
            newHoldTimestamp
        );
    }

    function holdWithReset(
        address validator,
        address token,
        uint256 tokenId,
        uint256 amountToHold,
        uint256 newHoldTimestamp,
        uint256[] memory holdTimestampsToReset
    ) public virtual whenNotPaused {
        _holdWithReset(
            validator,
            token,
            tokenId,
            amountToHold,
            newHoldTimestamp,
            holdTimestampsToReset
        );
    }

    // deprecated
    function resetHold(
        address validator,
        address delegator,
        address token,
        uint256 tokenId,
        uint256 holdTimestamp
    ) public virtual whenNotPaused {
        uint256[] memory holdTimestamps = new uint256[](1);
        holdTimestamps[0] = holdTimestamp;
        _resetHolds(validator, delegator, token, tokenId, holdTimestamps);
    }

    function resetHolds(
        address validator,
        address delegator,
        address token,
        uint256 tokenId,
        uint256[] memory holdTimestamps
    ) public virtual whenNotPaused {
        _resetHolds(validator, delegator, token, tokenId, holdTimestamps);
    }

    function withdrawHold(
        address validator,
        address nft,
        uint256 tokenId,
        uint256 amount,
        uint256 holdTimestamp
    ) public virtual whenNotPaused {
        _withdraw(validator, nft, tokenId, amount, holdTimestamp);
    }

    function withdraw(
        address validator,
        address nft,
        uint256 tokenId,
        uint256 amount
    ) public virtual whenNotPaused {
        uint256 holdTimestamp = 0;
        _withdraw(validator, nft, tokenId, amount, holdTimestamp);
    }

    function withdrawWithReset(
        address validator,
        address token,
        uint256 tokenId,
        uint256 amount,
        uint256[] memory holdTimestampsToReset
    ) public virtual whenNotPaused {
        _withdrawWithReset(
            validator,
            token,
            tokenId,
            amount,
            holdTimestampsToReset
        );
    }

    function transferHold(
        address validator,
        address nft,
        uint256 tokenId,
        uint256 amount,
        uint256 holdTimestamp,
        address newValidator
    ) public virtual whenNotPaused {
        _transfer(validator, nft, tokenId, amount, holdTimestamp, newValidator);
    }

    function transfer(
        address validator,
        address nft,
        uint256 tokenId,
        uint256 amount,
        address newValidator
    ) public virtual whenNotPaused {
        uint256 holdTimestamp = 0;
        _transfer(validator, nft, tokenId, amount, holdTimestamp, newValidator);
    }

    function transferWithReset(
        address oldValidator,
        address token,
        uint256 tokenId,
        uint256 amount,
        address newValidator,
        uint256[] memory holdTimestampsToReset
    ) public virtual whenNotPaused {
        _transferWithReset(
            oldValidator,
            token,
            tokenId,
            amount,
            newValidator,
            holdTimestampsToReset
        );
    }

    function _completeWithdraw(uint256 stakeIndex) internal virtual override {
        DelegationStorage storage $ = _getDelegationStorage();
        FrozenStake storage frozenStake = $._frozenStakes[stakeIndex];
        _checkFreezeTime(frozenStake.unfreezeTimestamp);
        if (frozenStake.freezeStatus == FreezeStatus.Completed) {
            revert StakeAlreadyUnfrozen();
        }
        frozenStake.freezeStatus = FreezeStatus.Completed;
        Stake memory stake = frozenStake.stake;

        if (stake.tokenType == TokenType.DRC721) {
            IDRC721(stake.token).safeTransferFrom(
                address(this),
                stake.delegator,
                stake.tokenId
            );
        } else if (stake.tokenType == TokenType.DRC1155) {
            IDRC1155(stake.token).safeTransferFrom(
                address(this),
                stake.delegator,
                stake.tokenId,
                stake.amount,
                ""
            );
        } else revert InvalidTokenType();

        emit WithdrawCompleted(stakeIndex);
    }

    function _applyPenaltiesToValidator(
        address validator,
        address nft,
        uint256 tokenId
    ) internal override returns (bool) {
        uint16[] memory percantages = _getValidatorPenalties(validator);
        ValidatorReserve storage validatorReserve = _getValidatorReserve(
            validator,
            nft,
            tokenId
        );

        uint256 currentReserve = validatorReserve.reserve;
        uint256 currentPenaltyIndex = validatorReserve.penaltyIndex;
        bool allApplied = percantages.length == currentPenaltyIndex;

        if (allApplied || currentReserve == 0 || percantages.length == 0) {
            return false;
        }

        for (; currentPenaltyIndex < percantages.length; currentPenaltyIndex += 1) {
            uint256 amountToPenalty = _penaltyNFT(
                nft,
                tokenId,
                currentReserve,
                percantages[currentPenaltyIndex]
            );
            emit PenaltyAppliedToValidator(
                validator,
                nft,
                _getTokenType(nft),
                tokenId,
                currentPenaltyIndex + 1,
                amountToPenalty
            );
        }

        validatorReserve.penaltyIndex = percantages.length;

        return true;
    }

    function _applyPenaltiesToStake(
        bytes32
    ) internal virtual override returns (bool) {
        // DO NOTHING
        return false;
    }

    function _penaltyNFT(
        address nft,
        uint256 tokenId,
        uint256 amountToPenalty,
        uint16 percentage
    ) internal returns (uint256) {
        TokenType tokenType = _getTokenType(nft);

        if (tokenType == TokenType.DRC721) {
            return IDRC721(nft).penalty(tokenId, percentage);
        }

        if (tokenType == TokenType.DRC1155) {
            return IDRC1155(nft).penalty(tokenId, amountToPenalty, percentage);
        }
        revert InvalidTokenType();
    }

    function _checkToken(
        address nft,
        TokenType tokenType
    ) internal view override {
        NFTState memory nftState = _getNFTState(nft);
        if (!nftState.active || !nftState.withReserve)
            revert InvalidCollection();

        if (
            tokenType == TokenType.DRC721 && nftState.nftType != NFTType.DRC721
        ) {
            revert NotDRC721();
        }
        if (
            tokenType == TokenType.DRC1155 &&
            nftState.nftType != NFTType.DRC1155
        ) {
            revert NotDRC1155();
        }
    }

    /* ========== GETTERS ========== */

    function _getTokenType(address nft) internal view returns (TokenType) {
        NFTType nftType = _getNFTState(nft).nftType;
        TokenType tokenType = nftType == NFTType.DRC721
            ? TokenType.DRC721
            : TokenType.DRC1155;
        return tokenType;
    }

    function getValidatorReserve(
        address validator,
        address token,
        uint256 tokenId
    ) external view returns (ValidatorReserve memory) {
        return _getValidatorReserve(validator, token, tokenId);
    }

    function _getNFTCenter() internal view returns (IDecimalNFTCenter) {
        return IDecimalNFTCenter(_getAddress("nft-center"));
    }

    function _getNFTState(address nft) internal view returns (NFTState memory) {
        return _getNFTCenter().getNFTState(nft);
    }

    function getStakeId(
        address validator,
        address delegator,
        address nft,
        uint256 tokenId
    ) public pure returns (bytes32) {
        uint256 holdTimestamp = 0;
        return _getStakeId(validator, delegator, nft, tokenId, holdTimestamp);
    }

    function getHoldStakeId(
        address validator,
        address delegator,
        address nft,
        uint256 tokenId,
        uint256 holdTimestamp
    ) public pure returns (bytes32) {
        return _getStakeId(validator, delegator, nft, tokenId, holdTimestamp);
    }

    function getStake(
        address validator,
        address delegator,
        address nft,
        uint256 tokenId
    ) public view returns (Stake memory) {
        return _getStake(getStakeId(validator, delegator, nft, tokenId));
    }

    function getHoldStake(
        address validator,
        address delegator,
        address nft,
        uint256 tokenId,
        uint256 holdTimestamp
    ) public view returns (Stake memory) {
        return
            _getStake(
                getHoldStakeId(
                    validator,
                    delegator,
                    nft,
                    tokenId,
                    holdTimestamp
                )
            );
    }
}


// File contracts/DecimalDelegationNFTMigration.sol

// Original license: SPDX_License_Identifier: MIT
pragma solidity 0.8.20;







contract DecimalDelegationNFTMigration is
    DecimalDelegationNFT,
    DecimalMigration
{
    struct MigrationStorage {
        mapping(bytes32 frozenStakeHash => bool migrated) _frozenStakeMigrated;
    }

    // keccak256(abi.encode(uint256(keccak256("DECIMAL_DELEGATION_NFT_MIGRATION_STORAGE_LOCATION")) - 1)) & ~bytes32(uint256(0xff));
    bytes32 private constant DECIMAL_DELEGATION_NFT_MIGRATION_STORAGE_LOCATION =
        0x6de07119a7043088a66437aef7f940f7c59cf6a4521aea0335393d50cd15bf00; // TODO update hash

    function _getMigrationStorage()
        internal
        pure
        returns (MigrationStorage storage $)
    {
        assembly {
            $.slot := DECIMAL_DELEGATION_NFT_MIGRATION_STORAGE_LOCATION
        }
    }

    error PayloadMismatch();
    error NotDeployedCollection(address addressCollection);
    error InvalidDelegator();
    error AlreadyMigrated();

    struct MigrationStake {
        address validator;
        address delegator;
        bytes salt;
        uint256 amount;
    }

    struct FrozenMigrationStake {
        MigrationStake stake;
        FreezeType freezeType;
        uint256 unfreezeTimestamp;
    }

    function _getContractCenterMigration()
        internal
        view
        override
        returns (IDecimalContractCenterMigration)
    {
        return IDecimalContractCenterMigration(getContractCenter());
    }

    function _delegateDRC721(
        address validator,
        address nft,
        uint256 tokenId,
        uint256 holdTimestamp
    ) internal override {
        super._delegateDRC721(validator, nft, tokenId, holdTimestamp);
    }

    function _delegateDRC1155(
        address validator,
        address nft,
        uint256 tokenId,
        uint256 amount,
        uint256 holdTimestamp
    ) internal virtual override {
        super._delegateDRC1155(validator, nft, tokenId, amount, holdTimestamp);
    }

    function _resetHolds(
        address validator,
        address delegator,
        address token,
        uint256 tokenId,
        uint256[] memory holdTimestampsToReset
    ) internal override onlyNotMigration {
        super._resetHolds(
            validator,
            delegator,
            token,
            tokenId,
            holdTimestampsToReset
        );
    }

    function _freezeStake(
        bytes32 stakeId,
        uint256 amountToFreeze,
        FreezeType freezeType
    ) internal override onlyNotMigration returns (FrozenStake memory) {
        return super._freezeStake(stakeId, amountToFreeze, freezeType);
    }

    function _hold(
        address validator,
        address token,
        uint256 tokenId,
        uint256 amountToHold,
        uint256,
        uint256 newHoldTimestamp
    ) internal override onlyNotMigration {
        super._hold(
            validator,
            token,
            tokenId,
            amountToHold,
            0,
            newHoldTimestamp
        );
    }

    function applyPenaltiesToValidator(
        ValidatorToken[] memory validatorTokens
    ) public override onlyNotMigration {
        super.applyPenaltiesToValidator(validatorTokens);
    }

    function migrateStakeBatch(Stake[] memory stakes) public onlyOwner {
        for (uint256 i; i < stakes.length; i += 1) {
            if (stakes[i].validator == address(0)) revert InvalidValidator();
            if (stakes[i].delegator == address(0)) revert InvalidDelegator();
            // if (!_isValidatorActive(validatorsToMigrate[i])) revert InvalidValidator(); // TODO need it?
            _migrateStake(stakes[i]);
        }
    }

    function filterMigratedStakes(
        MigrationStake[] memory migrationStakes
    ) external view returns (Stake[] memory stakes) {
        stakes = new Stake[](migrationStakes.length);
        for (uint256 i; i < migrationStakes.length; i += 1) {
            address token = _getDeployedAddress(migrationStakes[i].salt);
            uint256 holdTimestamp = 0;
            bytes32 stakeId = _getStakeId(
                migrationStakes[i].validator,
                migrationStakes[i].delegator,
                token,
                MIGRATION_TOKEN_ID,
                holdTimestamp
            );
            bool isActive = _getStake(stakeId).amount != 0;
            uint256 amountToMigrate = migrationStakes[i].amount;
            bool alreadyAdded = amountToMigrate == _getStake(stakeId).amount &&
                isActive;
            bool alreadyRemoved = amountToMigrate == 0 && !isActive;
            if (!(alreadyRemoved || alreadyAdded)) {
                stakes[i].validator = migrationStakes[i].validator;
                stakes[i].delegator = migrationStakes[i].delegator;
                stakes[i].token = token;
                stakes[i].amount = migrationStakes[i].amount;
            }
        }
    }

    function _getValidatorPenalties(
        address validator
    ) internal view override returns (uint16[] memory penalties) {
        if (isMigrating()) {
            return penalties;
        }
        return super._getValidatorPenalties(validator);
    }

    function _migrateStake(Stake memory updatedStake) internal {
        updatedStake.tokenId = MIGRATION_TOKEN_ID;

        NFTType nftType = _getNFTState(updatedStake.token).nftType;
        bool isDRC721 = nftType == NFTType.DRC721;
        bool isDRC1155 = nftType == NFTType.DRC1155;
        if (isDRC721) {
            updatedStake.tokenType = TokenType.DRC721;
        } else if (isDRC1155) {
            updatedStake.tokenType = TokenType.DRC1155;
        } else {
            revert InvalidTokenType();
        }

        bytes32 stakeId = _getStakeId(updatedStake);

        Stake memory currentStake = _getStake(stakeId);

        if (updatedStake.amount == currentStake.amount) {
            revert AlreadyMigrated();
        }

        bool add = updatedStake.amount > currentStake.amount;
        if (add) {
            Stake memory addedStake = updatedStake;
            addedStake.amount -= currentStake.amount;
            _addStake(addedStake);
            _updateReserveBalance(
                addedStake.token,
                addedStake.tokenId,
                int256(addedStake.amount),
                nftType
            );
        } else {
            Stake memory removedStake = updatedStake;
            removedStake.amount = currentStake.amount - updatedStake.amount;
            _removeStake(stakeId, removedStake.amount);
            _updateReserveBalance(
                removedStake.token,
                removedStake.tokenId,
                -int256(removedStake.amount),
                nftType
            );

            FrozenStake memory emptyFrozenStake;
            emit WithdrawRequest(stakeId, type(uint256).max, emptyFrozenStake);
            emit WithdrawCompleted(type(uint256).max);
        }
    }

    function _updateReserveBalance(
        address tokenAddress,
        uint256 tokenId,
        int256 reserveDifference,
        NFTType nftType
    ) internal {
        if (reserveDifference == 0) {
            revert AlreadyMigrated();
        }
        if (nftType == NFTType.DRC721) {
            bool add = reserveDifference > 0;
            uint256 newBalance = add ? 1 : 0;
            DRC721Migration(payable(tokenAddress)).setBalance(
                IDecimalNFTCenterMigration.Balance(address(this), newBalance)
            );
        } else if (nftType == NFTType.DRC1155) {
            DRC1155Migration token = DRC1155Migration(payable(tokenAddress));
            uint256 currentReserve = token.balanceOf(address(this), tokenId);
            uint256 newReserve = uint256(
                int256(currentReserve) + reserveDifference
            );
            token.setBalance(
                IDecimalNFTCenterMigration.Balance({
                    account: address(this),
                    balance: newReserve
                })
            );
        } else {
            revert InvalidTokenType();
        }
    }

    function _getDeployedAddress(
        bytes memory salt
    ) internal view returns (address) {
        return IDecimalNFTCenter(address(_getNFTCenter())).getNFTBySalt(salt);
    }

    function _getFrozenStakeHash(
        FrozenStake memory frozenStake
    ) internal pure returns (bytes32) {
        return
            keccak256(
                abi.encode(
                    frozenStake.stake.validator,
                    frozenStake.stake.delegator,
                    frozenStake.stake.token,
                    frozenStake.stake.amount,
                    frozenStake.stake.tokenId,
                    frozenStake.stake.tokenType,
                    frozenStake.unfreezeTimestamp
                )
            );
    }

    error FrozenStakeAlreadyAdded();
    error NothingToMigrate();

    function migrateFrozenStakes(
        FrozenMigrationStake[] memory frozenMigrationStakes
    ) public onlyOwner {
        bool isEmpty = true;

        for (uint256 i; i < frozenMigrationStakes.length; i += 1) {
            FrozenMigrationStake
                memory frozenMigrationStake = frozenMigrationStakes[i];
            uint256 holdTimestamp = 0;

            address nftAddress = _getDeployedAddress(
                frozenMigrationStake.stake.salt
            );
            if (nftAddress == address(0)) {
                revert InvalidCollection();
            }
            TokenType tokenType = _getTokenType(nftAddress);

            FrozenStake memory frozenStake = FrozenStake(
                Stake({
                    tokenType: tokenType,
                    validator: frozenMigrationStake.stake.validator,
                    delegator: frozenMigrationStake.stake.delegator,
                    token: _getDeployedAddress(frozenMigrationStake.stake.salt),
                    amount: frozenMigrationStake.stake.amount,
                    tokenId: MIGRATION_TOKEN_ID,
                    holdTimestamp: holdTimestamp
                }),
                FreezeStatus.Frozen,
                frozenMigrationStake.freezeType,
                frozenMigrationStake.unfreezeTimestamp
            );
            bytes32 frozenStakeHash = _getFrozenStakeHash(frozenStake);
            bool migrated = _getMigrationStorage()._frozenStakeMigrated[
                frozenStakeHash
            ];
            if (!migrated) {
                _getMigrationStorage()._frozenStakeMigrated[
                    frozenStakeHash
                ] = true;
                FrozenStake[] storage frozenStakes = _getDelegationStorage()
                    ._frozenStakes;
                frozenStakes.push(frozenStake);
                uint256 stakeIndex = frozenStakes.length - 1;
                bytes32 stakeId = _getStakeId(frozenStake.stake);
                if (frozenMigrationStake.freezeType == FreezeType.Transfer) {
                    emit TransferRequest(stakeId, stakeIndex, frozenStake);
                }
                if (frozenMigrationStake.freezeType == FreezeType.Withdraw) {
                    emit WithdrawRequest(stakeId, stakeIndex, frozenStake);
                }

                _updateReserveBalance(
                    frozenStake.stake.token,
                    frozenStake.stake.tokenId,
                    -int256(frozenStake.stake.amount),
                    _getNFTState(frozenStake.stake.token).nftType
                );

                _addValidatorReserve(frozenStake.stake);

                if (isEmpty) {
                    isEmpty = false;
                }
            }
        }
        if (isEmpty) {
            revert NothingToMigrate();
        }
    }

    function _completeWithdraw(uint256 stakeIndex) internal override onlyOwner {
        if (isMigrating()) {
            DelegationStorage storage $ = _getDelegationStorage();
            FrozenStake storage frozenStake = $._frozenStakes[stakeIndex];
            _checkFreezeTime(frozenStake.unfreezeTimestamp);
            if (frozenStake.freezeStatus == FreezeStatus.Completed) {
                revert StakeAlreadyUnfrozen();
            }
            frozenStake.freezeStatus = FreezeStatus.Completed;

            _reduceValidatorReserve(
                frozenStake.stake,
                frozenStake.stake.amount
            );

            _updateReserveBalance(
                frozenStake.stake.token,
                frozenStake.stake.tokenId,
                -int256(frozenStake.stake.amount),
                _getNFTState(frozenStake.stake.token).nftType
            );
            emit WithdrawCompleted(stakeIndex);
        } else {
            DecimalDelegationNFT._completeWithdraw(stakeIndex);
        }
    }
}
