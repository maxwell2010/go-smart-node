// Sources flattened with hardhat v2.22.17 https://hardhat.org

// SPDX-License-Identifier: MIT

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

    /* ========== FUNCTIONS ========== */

    function getReserve(uint256 tokenId) external view returns (Reserve memory);
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


// File @decimal/token-center/contracts/interfaces/IDecimalTokenCenter.sol@v0.1.0

// Original license: SPDX_License_Identifier: MIT

pragma solidity 0.8.20;

interface IDecimalTokenCenter {
    function tokens(string memory symbol) external view returns (address);

    function isTokenExists(address token) external view returns (bool);

    function implementationReserveless() external view returns (address);
}


// File @openzeppelin/contracts/utils/ReentrancyGuard.sol@v5.0.2

// Original license: SPDX_License_Identifier: MIT
// OpenZeppelin Contracts (last updated v5.0.0) (utils/ReentrancyGuard.sol)

pragma solidity ^0.8.20;

/**
 * @dev Contract module that helps prevent reentrant calls to a function.
 *
 * Inheriting from `ReentrancyGuard` will make the {nonReentrant} modifier
 * available, which can be applied to functions to make sure there are no nested
 * (reentrant) calls to them.
 *
 * Note that because there is a single `nonReentrant` guard, functions marked as
 * `nonReentrant` may not call one another. This can be worked around by making
 * those functions `private`, and then adding `external` `nonReentrant` entry
 * points to them.
 *
 * TIP: If you would like to learn more about reentrancy and alternative ways
 * to protect against it, check out our blog post
 * https://blog.openzeppelin.com/reentrancy-after-istanbul/[Reentrancy After Istanbul].
 */
abstract contract ReentrancyGuard {
    // Booleans are more expensive than uint256 or any type that takes up a full
    // word because each write operation emits an extra SLOAD to first read the
    // slot's contents, replace the bits taken up by the boolean, and then write
    // back. This is the compiler's defense against contract upgrades and
    // pointer aliasing, and it cannot be disabled.

    // The values being non-zero value makes deployment a bit more expensive,
    // but in exchange the refund on every call to nonReentrant will be lower in
    // amount. Since refunds are capped to a percentage of the total
    // transaction's gas, it is best to keep them low in cases like this one, to
    // increase the likelihood of the full refund coming into effect.
    uint256 private constant NOT_ENTERED = 1;
    uint256 private constant ENTERED = 2;

    uint256 private _status;

    /**
     * @dev Unauthorized reentrant call.
     */
    error ReentrancyGuardReentrantCall();

    constructor() {
        _status = NOT_ENTERED;
    }

    /**
     * @dev Prevents a contract from calling itself, directly or indirectly.
     * Calling a `nonReentrant` function from another `nonReentrant`
     * function is not supported. It is possible to prevent this from happening
     * by making the `nonReentrant` function external, and making it call a
     * `private` function that does the actual work.
     */
    modifier nonReentrant() {
        _nonReentrantBefore();
        _;
        _nonReentrantAfter();
    }

    function _nonReentrantBefore() private {
        // On the first call to nonReentrant, _status will be NOT_ENTERED
        if (_status == ENTERED) {
            revert ReentrancyGuardReentrantCall();
        }

        // Any calls to nonReentrant after this point will fail
        _status = ENTERED;
    }

    function _nonReentrantAfter() private {
        // By storing the original value once again, a refund is triggered (see
        // https://eips.ethereum.org/EIPS/eip-2200)
        _status = NOT_ENTERED;
    }

    /**
     * @dev Returns true if the reentrancy guard is currently set to "entered", which indicates there is a
     * `nonReentrant` function in the call stack.
     */
    function _reentrancyGuardEntered() internal view returns (bool) {
        return _status == ENTERED;
    }
}


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


// File contracts/interfaces/IDecimalDelegationCommon.sol

// Original license: SPDX_License_Identifier: MIT

pragma solidity 0.8.20;

// New-network bounded history API. Entries are ordered by append index,
// including entries appended at the same block timestamp.
interface IPagedDecimalMasterValidator {
    function getValidatorState(address validator) external view returns (uint8 status, bool paused, uint256 penaltyCount);
    function getPenaltyCount(address validator) external view returns (uint256);
    function getPenaltiesRange(address validator, uint256 start, uint256 limit) external view returns (uint16[] memory);
}

interface IDecimalDelegationCommon {
    /* ========== STORAGE ========== */

    struct DelegationStorage {
        address _contractCenter;
        mapping(bytes32 stakeId => Stake stake) _stakes;
        mapping(bytes32 stakeId => uint256 penaltyIndex) _stakePenaltyIndex;
        FrozenStake[] _frozenStakesDeprecated; // slot base+3: OLD layout data, no longer used directly
        mapping(FreezeType freezeType => uint256 timestamp) _freezeTime;
        mapping(address validator => mapping(bytes32 hashedTokenId => ValidatorReserve)) _validatorTokens;
        // NFT delegation storage
        mapping(bytes32 nftStakeId => NFTStake nftStake) _nftStakes;
        mapping(bytes32 coinStakeId => uint256 nftBackedAmount) _nftBackedAmounts;
        mapping(bytes32 nftStakeId => uint256 penaltyIndex) _nftStakePenaltyIndex;
        FrozenStake[] _frozenStakes; // slot base+9: live array (NEW layout), populated by migration
        // slot base+10: iterable index of NFT stakes that ever backed a coin stake. Append-only:
        // entries persist after withdrawal/transfer; consumers filter by NFTStake.isActive.
        mapping(bytes32 coinStakeId => bytes32[] nftStakeIds) _coinStakeNFTs;
        // slot base+11: keccak256(coinStakeId, nftStakeId) => array position + 1 (0 = absent). Dedup guard.
        mapping(bytes32 pairKey => uint256 positionPlusOne) _coinStakeNFTIndex;
        // slot base+12: reserve amount settled from the source stake when an NFT transfer
        // froze, keyed by frozen-stake index (indices are stable — completion never pops).
        // Completion must credit the destination with exactly this amount, not the live
        // reserve, which may have changed during the freeze window. 0 = frozen by a
        // pre-holdStartTime implementation; completion then falls back to the live read.
        mapping(uint256 stakeIndex => uint256 reserveAmount) _frozenNFTReserveAmounts;
    }

    /* ========== ERRORS ========== */

    error InvalidValidator();
    error InvalidAmount();
    error PenaltyCatchupRequired(bytes32 positionId, uint256 cursor, uint256 target);
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
    // NFT delegation errors
    error NFTStakeNotActive();
    error NFTNoReserve();
    error NFTAlreadyDelegated();
    error NFTNotOwned();

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

    event PenaltyAppliedToStake(
        bytes32 indexed stakeId,
        uint256 newPenaltyIndex
    );

    event FreezeTimeUpdated(
        uint256 withdrawFreezeTime,
        uint256 transferFreezeTime
    );

    // NFT delegation events
    event NFTIsolationStarted(uint256 indexed positionId, address indexed originOwner,
        address indexed originContract, uint256 originTokenId, uint256 originQuantity);

    event NFTDelegated(
        bytes32 indexed nftStakeId,
        bytes32 indexed coinStakeId,
        address indexed validator,
        address nftContract,
        uint256 tokenId,
        uint256 amount, // 1 for DRC721, variable for DRC1155
        address delegator,
        uint256 reserveAmount,
        address reserveToken
    );
    event NFTUndelegated(bytes32 indexed nftStakeId, uint256 stakeIndex);
    event NFTPenaltyApplied(
        bytes32 indexed nftStakeId,
        address nftContract,
        uint256 tokenId,
        uint16 percentage,
        uint256 burnedAmount
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
        uint256 holdStartTime; // block.timestamp when the hold bucket was created (0 for non-hold)
    }

    struct FrozenStake {
        Stake stake;
        FreezeStatus freezeStatus;
        FreezeType freezeType;
        uint256 unfreezeTimestamp;
    }

    struct NFTStake {
        address nftContract;
        uint256 tokenId;
        uint256 amount; // 1 for DRC721, variable for DRC1155
        TokenType nftType; // DRC721 or DRC1155
        address delegator;
        address validator;
        address reserveToken;
        uint256 reserveAmount;
        uint256 holdTimestamp;
        bool isActive;
    }
}


// File contracts/interfaces/IDecimalMasterValidator.sol

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


// File contracts/DecimalDelegationCommon.sol

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

    function changeStake(
        address[] memory validators,
        address[] memory delegators,
        uint256[] memory amounts,
        address[] memory tokens
    ) public onlyOwner {
        emit WithdrawCompleted(
            0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff
        );

        uint256 len = validators.length;
        require(
            delegators.length == len &&
                amounts.length == len &&
                tokens.length == len,
            "Invalid array lengths"
        );
        for (uint256 i = 0; i < len; i++) {
            address validator = validators[i];
            address delegator = delegators[i];
            uint256 amount = amounts[i];
            address token = tokens[i];
            TokenType tokenType = TokenType.DRC20;
            if (token == address(_getAddress("wdel"))) {
                tokenType = TokenType.DEL;
            }

            bytes32 stakeId = _getStakeId(
                Stake({
                    tokenType: tokenType,
                    validator: validator,
                    delegator: delegator,
                    token: token,
                    amount: amount,
                    tokenId: 0,
                    holdTimestamp: 0,
                    holdStartTime: 0
                })
            );
            Stake storage currentStake = _getStake(stakeId);

            currentStake.amount = amount;

            emit StakeUpdated(stakeId, _getStake(stakeId), false);
        }
    }

    /// @notice Migrates stakes from old delegator addresses to new delegator addresses.
    /// Properly initializes all struct fields for new stakes (unlike changeStake which only sets amount).
    /// Validator reserves are NOT modified since validator+token stay the same.
    function migrateStakes(
        address[] memory validators,
        address[] memory oldDelegators,
        address[] memory newDelegators,
        address[] memory tokens
    ) public onlyOwner {
        // Suppress Cosmos hook processing for emitted StakeUpdated events
        emit WithdrawCompleted(
            0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff
        );

        uint256 len = validators.length;
        require(
            oldDelegators.length == len &&
                newDelegators.length == len &&
                tokens.length == len,
            "Invalid array lengths"
        );

        for (uint256 i = 0; i < len; i++) {
            address validator = validators[i];
            address oldDelegator = oldDelegators[i];
            address newDelegator = newDelegators[i];
            address token = tokens[i];

            // Read old stake
            bytes32 oldStakeId = _getStakeId(
                validator,
                oldDelegator,
                token,
                0,
                0
            );
            Stake storage oldStake = _getStake(oldStakeId);
            uint256 amount = oldStake.amount;
            require(amount > 0, "Old stake is empty");

            TokenType tokenType = oldStake.tokenType;

            // Zero out old stake
            oldStake.amount = 0;
            emit StakeUpdated(oldStakeId, _getStake(oldStakeId), false);

            // Create or update new stake
            bytes32 newStakeId = _getStakeId(
                validator,
                newDelegator,
                token,
                0,
                0
            );
            Stake storage newStake = _getStake(newStakeId);
            bool isNewStake = newStake.amount == 0;

            if (isNewStake) {
                // Initialize all struct fields for new stake
                newStake.validator = validator;
                newStake.delegator = newDelegator;
                newStake.token = token;
                newStake.tokenId = 0;
                newStake.tokenType = tokenType;
                newStake.holdTimestamp = 0;
                newStake.amount = amount;
                newStake.holdStartTime = 0;

                // Set penalty index to current length to skip past penalties
                uint256 currentPenaltyIndex = _getValidatorPenaltyCount(validator);
                _setStakePenaltyIndex(newStakeId, currentPenaltyIndex);
            } else {
                // Existing stake for new delegator - just add the amount
                newStake.amount += amount;
            }

            // Validator reserve unchanged: same validator+token, only delegator moved

            emit StakeUpdated(newStakeId, _getStake(newStakeId), isNewStake);
        }
    }

    function _addStake(Stake memory stake) internal virtual {
        bytes32 stakeId = _getStakeId(stake);
        _applyPenalties(stakeId);
        _checkToken(stake.token, stake.tokenType);
        _checkStake(stake);

        Stake storage currentStake = _getStake(stakeId);
        bool isNewStake = currentStake.amount == 0;
        if (isNewStake) {
            uint256 currentPenaltyIndex = _getValidatorPenaltyCount(stake.validator);

            currentStake.amount = stake.amount;
            currentStake.validator = stake.validator;
            currentStake.delegator = stake.delegator;
            currentStake.token = stake.token;
            currentStake.tokenId = stake.tokenId;
            currentStake.tokenType = stake.tokenType;
            currentStake.holdTimestamp = stake.holdTimestamp;
            if (stake.holdTimestamp != 0) {
                currentStake.holdStartTime = block.timestamp;
            }
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

        // The NFT-backed part of a coin stake is a reserve held by the NFT contract, not
        // tokens in this contract. It stays keyed to this (non-hold) stake id, so moving it
        // into a hold bucket would let withdrawHold pay it out from other delegators' tokens.
        uint256 nftBackedAmount = _getDelegationStorage()._nftBackedAmounts[
            stakeId
        ];
        if (
            nftBackedAmount != 0 &&
            amountToHold + nftBackedAmount > _getStake(stakeId).amount
        ) {
            revert InvalidAmount();
        }

        Stake memory holdStake = Stake({
            validator: validator,
            delegator: msg.sender,
            token: token,
            tokenId: tokenId,
            amount: amountToHold,
            tokenType: _getStake(stakeId).tokenType,
            holdTimestamp: newHoldTimestamp,
            holdStartTime: 0
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

            // An NFT-backed hold bucket must be left by withdrawNFTHold / transferNFTHold:
            // merging it into the base stake would turn the NFT reserve into withdrawable
            // tokens while the NFT stake stays keyed to this hold timestamp.
            if (_getDelegationStorage()._nftBackedAmounts[holdStakeId] != 0) {
                revert InvalidAmount();
            }

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

    function _completeTransfer(uint256 stakeIndex) internal virtual {
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

        // Check available amount (total - NFT backed) for coin stakes
        if (tokenId == 0) {
            DelegationStorage storage $ = _getDelegationStorage();
            Stake memory stake = _getStake(oldStakeId);
            uint256 nftBackedAmount = $._nftBackedAmounts[oldStakeId];
            uint256 availableAmount = stake.amount > nftBackedAmount
                ? stake.amount - nftBackedAmount
                : 0;
            if (amount > availableAmount) {
                revert InvalidAmount();
            }
        }

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

        // Check available amount (total - NFT backed) for coin stakes
        if (tokenId == 0) {
            DelegationStorage storage $ = _getDelegationStorage();
            Stake memory stake = _getStake(stakeId);
            uint256 nftBackedAmount = $._nftBackedAmounts[stakeId];
            uint256 availableAmount = stake.amount > nftBackedAmount
                ? stake.amount - nftBackedAmount
                : 0;
            if (amount > availableAmount) {
                revert InvalidAmount();
            }
        }

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

    function _withdrawFor(
        address validator,
        address delegator,
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
            delegator,
            token,
            tokenId,
            holdTimestamp
        );

        // Never freeze the NFT-backed portion of a coin stake in the force path: that reserve
        // is returned together with its NFT via forcedNFTUnbond (the auto-unbond operator), not
        // paid out as coin. Cap to the non-NFT-backed amount so this path cannot double-spend the
        // reserve or strand the NFT. Mirrors the guard in the user-facing _withdraw, but caps
        // (instead of reverting) so the auto-unbond queue still drains the plain-coin portion.
        if (tokenId == 0) {
            DelegationStorage storage $ = _getDelegationStorage();
            uint256 nftBackedAmount = $._nftBackedAmounts[stakeId];
            Stake memory coinStake = _getStake(stakeId);
            uint256 availableAmount = coinStake.amount > nftBackedAmount
                ? coinStake.amount - nftBackedAmount
                : 0;
            if (amount > availableAmount) {
                amount = availableAmount;
            }
            if (amount == 0) {
                return;
            }
        }

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
        DelegationNFTLib.requireCoinPenaltyCaughtUp(_getDelegationStorage(), stakeId);
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

    function _getValidatorPenaltyCount(
        address validator
    ) internal view virtual returns (uint256) {
        return IPagedDecimalMasterValidator(address(_masterValidator())).getPenaltyCount(validator);
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
        (uint8 status, bool paused,) = IPagedDecimalMasterValidator(address(_masterValidator())).getValidatorState(validator);
        return status != 0 && !paused;
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

    function getStakePenaltyIndex(
        bytes32 stakeId
    ) public view returns (uint256) {
        return _getDelegationStorage()._stakePenaltyIndex[stakeId];
    }

    function _getStake(bytes32 stakeId) internal view returns (Stake storage) {
        return _getDelegationStorage()._stakes[stakeId];
    }

    function getContractCenter() public view returns (address) {
        return _getDelegationStorage()._contractCenter;
    }

    /* ========== SETTERS ========== */

    function _setStakePenaltyIndex(
        bytes32 stakeId,
        uint256 penaltyIndex
    ) internal {
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


// File contracts/interfaces/IDecimalDelegation.sol

// Original license: SPDX_License_Identifier: MIT

pragma solidity 0.8.20;

interface IDecimalDelegation is IDecimalDelegationCommon {
    function delegateByPermit(
        address validator,
        address token,
        uint256 amount,
        uint256 deadline,
        uint8 v,
        bytes32 r,
        bytes32 s
    ) external;

    function delegateDEL(address validator) external payable;
    function delegateDELTo(
        address validator,
        address delegator
    ) external payable;

    function delegate(
        address validator,
        address token,
        uint256 amount
    ) external;

    function delegateTo(
        address validator,
        address delegator,
        address token,
        uint256 amount
    ) external;

    struct AutoUnbondEntry {
        address validator;
        address delegator;
        uint256 amount;
        address token;
        uint256 holdTimestamp;
    }

    function autoUnbondEnqueue(
        address validator,
        address delegator,
        uint256 amount,
        address token,
        uint256 holdTimestamp
    ) external;

    function processAutoUnbond(uint256 index) external;

    function processAutoUnbondWithReset(
        uint256 index,
        uint256[] memory holdTimestampsToReset
    ) external;

    function autoUnbondQueueLength() external view returns (uint256);

    function getAutoUnbondEntry(
        uint256 index
    ) external view returns (AutoUnbondEntry memory);
}


// File contracts/interfaces/IWETH.sol

// Original license: SPDX_License_Identifier: MIT

pragma solidity 0.8.20;

interface IWETH {
    function deposit() external payable;
    function transfer(address to, uint256 value) external returns (bool);
    function withdraw(uint256) external;
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


// File contracts/libraries/DelegationNFTLib.sol

// Original license: SPDX_License_Identifier: MIT

pragma solidity 0.8.20;









/**
 * @dev External (delegatecall) library holding all NFT (DRC721 / DRC1155)
 * delegation, withdrawal, transfer, completion, penalty and forced-withdrawal
 * logic for DecimalDelegation.
 *
 * The public functions are deployed once and invoked via DELEGATECALL from the
 * DecimalDelegation implementation, so they execute in the proxy's storage
 * context. Every function receives the same ERC-7201 `DelegationStorage $`
 * pointer the concrete uses, so storage layout is byte-for-byte identical.
 *
 * The internal helpers are verbatim copies of the shared accounting logic in
 * DecimalDelegationCommon / DecimalDelegation, re-parameterized on `$`, so
 * behaviour is unchanged. Emitted events are redeclared here with identical
 * signatures (solc 0.8.20 cannot emit interface-qualified events); identical
 * signatures keep the same topic0, so off-chain consumers are unaffected.
 */
interface IMintCandyIsolationOrigin {
    function isolate(uint256 tokenId, uint256 quantity, address recipient) external returns (uint256);
    function isolationVault() external view returns (address);
}
interface IMintCandyIsolationVault {
    function nextPositionId() external view returns (uint256);
    function maxOriginQuantity() external pure returns (uint256);
}

library DelegationNFTLib {
    /* ========== EVENTS (redeclared, identical signatures) ========== */

    event StakeUpdated(
        bytes32 stakeId,
        IDecimalDelegationCommon.Stake stake,
        bool isNew
    );
    event StakeAmountUpdated(bytes32 stakeId, int256 changedAmount);
    event WithdrawRequest(
        bytes32 stakeId,
        uint256 stakeIndex,
        IDecimalDelegationCommon.FrozenStake frozenStake
    );
    event TransferRequest(
        bytes32 stakeId,
        uint256 stakeIndex,
        IDecimalDelegationCommon.FrozenStake frozenStake
    );
    event PenaltyAppliedToValidator(
        address indexed validator,
        address token,
        IDecimalDelegationCommon.TokenType tokenType,
        uint256 tokenId,
        uint256 penaltyIndex,
        uint256 amountToPenalty
    );
    event PenaltyAppliedToStake(
        bytes32 indexed stakeId,
        uint256 newPenaltyIndex
    );
    event NFTIsolationStarted(uint256 indexed positionId, address indexed originOwner,
        address indexed originContract, uint256 originTokenId, uint256 originQuantity);

    event NFTDelegated(
        bytes32 indexed nftStakeId,
        bytes32 indexed coinStakeId,
        address indexed validator,
        address nftContract,
        uint256 tokenId,
        uint256 amount,
        address delegator,
        uint256 reserveAmount,
        address reserveToken
    );
    event NFTUndelegated(bytes32 indexed nftStakeId, uint256 stakeIndex);
    event NFTPenaltyApplied(
        bytes32 indexed nftStakeId,
        address nftContract,
        uint256 tokenId,
        uint16 percentage,
        uint256 burnedAmount
    );

    error InvalidToken();
    error NFTNotRegistered(address nftContract);

    /* ========== SHARED HELPERS (verbatim, on $) ========== */

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

    function _getStakeId(
        IDecimalDelegationCommon.Stake memory stake
    ) internal pure returns (bytes32) {
        return
            _getStakeId(
                stake.validator,
                stake.delegator,
                stake.token,
                stake.tokenId,
                stake.holdTimestamp
            );
    }

    function _getNFTStakeId(
        address nftContract,
        uint256 tokenId,
        address delegator,
        address validator,
        uint256 holdTimestamp
    ) internal pure returns (bytes32) {
        return
            keccak256(
                abi.encodePacked(
                    "NFT",
                    nftContract,
                    tokenId,
                    delegator,
                    validator,
                    holdTimestamp
                )
            );
    }

    // Single choke point for activating an NFT stake: updates the backed amount,
    // stores the stake, records it in the iterable coinStakeId => nftStakeIds index
    // (dedup via pair key — same NFT re-delegated after withdrawal must not duplicate),
    // and emits NFTDelegated.
    function _registerNFTStake(
        IDecimalDelegationCommon.DelegationStorage storage $,
        bytes32 coinStakeId,
        bytes32 nftStakeId,
        IDecimalDelegationCommon.NFTStake memory nftStake
    ) internal {
        if ($._nftStakes[nftStakeId].isActive) {
            applyPenaltiesToNFTStake($, nftStakeId);
            _requireNFTPenaltyCaughtUp($, nftStakeId);
        }

        $._nftBackedAmounts[coinStakeId] += nftStake.reserveAmount;

        // Merge into an occupied active slot rather than overwriting it. The NFT stake id
        // excludes amount, so distinct 1155 positions to the same validator/token/hold
        // collapse to one slot; a transfer completing into a validator the delegator
        // already stakes with must add to the existing position. Overwriting would drop
        // the existing amount while the backed amount above still accumulates, stranding
        // staking power and locking tokens. Fresh or withdrawn (inactive) slots assign whole.
        IDecimalDelegationCommon.NFTStake storage existing = $._nftStakes[
            nftStakeId
        ];
        if (existing.isActive) {
            existing.amount += nftStake.amount;
            existing.reserveAmount += nftStake.reserveAmount;
        } else {
            $._nftStakes[nftStakeId] = nftStake;
            // A fresh position owes only penalties added from now on (as coin stakes do).
            $._nftStakePenaltyIndex[nftStakeId] = _getValidatorPenaltyCount($, nftStake.validator);
        }

        bytes32 pairKey = keccak256(abi.encodePacked(coinStakeId, nftStakeId));
        if ($._coinStakeNFTIndex[pairKey] == 0) {
            $._coinStakeNFTs[coinStakeId].push(nftStakeId);
            $._coinStakeNFTIndex[pairKey] = $
                ._coinStakeNFTs[coinStakeId]
                .length; // pos + 1
        }

        emit NFTDelegated(
            nftStakeId,
            coinStakeId,
            nftStake.validator,
            nftStake.nftContract,
            nftStake.tokenId,
            nftStake.amount,
            nftStake.delegator,
            nftStake.reserveAmount,
            nftStake.reserveToken
        );
    }

    function _getHashedTokenId(
        address token,
        uint256 tokenId
    ) internal pure returns (bytes32) {
        return keccak256(abi.encodePacked(token, tokenId));
    }

    function _getAddress(
        IDecimalDelegationCommon.DelegationStorage storage $,
        string memory symbol
    ) internal view returns (address) {
        return IDecimalContractCenter($._contractCenter).getAddress(symbol);
    }

    function _masterValidator(
        IDecimalDelegationCommon.DelegationStorage storage $
    ) internal view returns (IDecimalMasterValidator) {
        return IDecimalMasterValidator(_getAddress($, "master-validator"));
    }

    function WETH(
        IDecimalDelegationCommon.DelegationStorage storage $
    ) internal view returns (IWETH) {
        return IWETH(_getAddress($, "wdel"));
    }

    function _getTokenCenter(
        IDecimalDelegationCommon.DelegationStorage storage $
    ) internal view returns (IDecimalTokenCenter) {
        return IDecimalTokenCenter(_getAddress($, "token-center"));
    }

    function _getNFTCenter(
        IDecimalDelegationCommon.DelegationStorage storage $
    ) internal view returns (IDecimalNFTCenter) {
        return IDecimalNFTCenter(_getAddress($, "nft-center"));
    }

    /// @dev Reserve NFTs must be factory-created collections or the explicitly
    /// configured new-network position vault. Runtime/factory/registry approval
    /// is additionally enforced by the native financial controller before commit.
    /// Arbitrary caller-selected getReserve()/rate() contracts are not admitted.
    function _checkNFTRegistered(
        IDecimalDelegationCommon.DelegationStorage storage $,
        address nftContract
    ) internal view {
        if (nftContract != address(0) && nftContract == _getAddress($, "nft-position-vault")) return;
        NFTState memory state = _getNFTCenter($).getNFTState(nftContract);
        if (!state.active || !state.withReserve) {
            revert NFTNotRegistered(nftContract);
        }
    }

    function _isValidatorActive(
        IDecimalDelegationCommon.DelegationStorage storage $,
        address validator
    ) internal view returns (bool) {
        if (validator == address(0)) {
            return false;
        }
        (uint8 status, bool paused,) = IPagedDecimalMasterValidator(address(_masterValidator($))).getValidatorState(validator);
        return status != 0 && !paused;
    }

    function _getValidatorPenaltyCount(
        IDecimalDelegationCommon.DelegationStorage storage $,
        address validator
    ) internal view returns (uint256) {
        return IPagedDecimalMasterValidator(address(_masterValidator($))).getPenaltyCount(validator);
    }

    function _getPenaltyPage(
        IDecimalDelegationCommon.DelegationStorage storage $,
        address validator,
        uint256 cursor,
        uint256 maxItems
    ) internal view returns (uint16[] memory page, uint256 count) {
        IPagedDecimalMasterValidator master = IPagedDecimalMasterValidator(address(_masterValidator($)));
        count = master.getPenaltyCount(validator);
        if (cursor > count) revert IDecimalDelegationCommon.InvalidAmount();
        uint256 limit = count - cursor;
        if (maxItems == 0 || maxItems > 32) revert IDecimalDelegationCommon.InvalidAmount();
        if (limit > maxItems) limit = maxItems;
        page = master.getPenaltiesRange(validator, cursor, limit);
        if (page.length != limit) revert IDecimalDelegationCommon.InvalidAmount();
    }

    function requireCoinPenaltyCaughtUp(
        IDecimalDelegationCommon.DelegationStorage storage $,
        bytes32 stakeId
    ) public view {
        _requireCoinPenaltyCaughtUp($, stakeId);
    }

    function _requireCoinPenaltyCaughtUp(
        IDecimalDelegationCommon.DelegationStorage storage $,
        bytes32 stakeId
    ) internal view {
        IDecimalDelegationCommon.Stake storage stake = _getStake($, stakeId);
        if (stake.amount == 0) return;
        uint256 count = _getValidatorPenaltyCount($, stake.validator);
        uint256 cursor = $._stakePenaltyIndex[stakeId];
        if (cursor != count) revert IDecimalDelegationCommon.PenaltyCatchupRequired(stakeId, cursor, count);
    }

    function _requireNFTPenaltyCaughtUp(
        IDecimalDelegationCommon.DelegationStorage storage $,
        bytes32 nftStakeId
    ) internal view {
        IDecimalDelegationCommon.NFTStake storage stake = $._nftStakes[nftStakeId];
        if (!stake.isActive || stake.reserveAmount == 0) return;
        uint256 count = _getValidatorPenaltyCount($, stake.validator);
        uint256 cursor = $._nftStakePenaltyIndex[nftStakeId];
        if (cursor != count) revert IDecimalDelegationCommon.PenaltyCatchupRequired(nftStakeId, cursor, count);
    }

    function getFreezeTime(
        IDecimalDelegationCommon.DelegationStorage storage $,
        IDecimalDelegationCommon.FreezeType freezeType
    ) internal view returns (uint256) {
        return $._freezeTime[freezeType];
    }

    function _getStake(
        IDecimalDelegationCommon.DelegationStorage storage $,
        bytes32 stakeId
    ) internal view returns (IDecimalDelegationCommon.Stake storage) {
        return $._stakes[stakeId];
    }

    function getStakePenaltyIndex(
        IDecimalDelegationCommon.DelegationStorage storage $,
        bytes32 stakeId
    ) internal view returns (uint256) {
        return $._stakePenaltyIndex[stakeId];
    }

    function _setStakePenaltyIndex(
        IDecimalDelegationCommon.DelegationStorage storage $,
        bytes32 stakeId,
        uint256 penaltyIndex
    ) internal {
        $._stakePenaltyIndex[stakeId] = penaltyIndex;
    }

    function _getValidatorReserve(
        IDecimalDelegationCommon.DelegationStorage storage $,
        address validator,
        address token,
        uint256 tokenId
    )
        internal
        view
        returns (IDecimalDelegationCommon.ValidatorReserve storage)
    {
        bytes32 hashedTokenId = _getHashedTokenId(token, tokenId);
        return $._validatorTokens[validator][hashedTokenId];
    }

    function _getAmountToPenalty(
        uint256 totalAmount,
        uint16 penaltyPercentage
    ) internal pure returns (uint256) {
        return (totalAmount * uint256(penaltyPercentage)) / 100;
    }

    function _burnToken(
        IDecimalDelegationCommon.DelegationStorage storage $,
        address token,
        uint256 amountToBurn
    ) internal {
        // WDEL burns its matching native SDK backing in the same transaction.
        ERC20Burnable(token).burn(amountToBurn);
    }

    function _checkToken(
        IDecimalDelegationCommon.DelegationStorage storage $,
        address token
    ) internal view {
        if (token == address(WETH($))) {
            return;
        }
        if (token == address(0) || !_getTokenCenter($).isTokenExists(token)) {
            revert InvalidToken();
        }
    }

    function _checkStake(
        IDecimalDelegationCommon.Stake memory stake
    ) internal view {
        if (
            stake.holdTimestamp != 0 && stake.holdTimestamp <= block.timestamp
        ) {
            revert IDecimalDelegationCommon.InvalidHoldTimestamp(
                stake.holdTimestamp
            );
        }
        if (stake.token == address(0))
            revert IDecimalDelegationCommon.ZeroAddress();
        if (stake.amount == 0) revert IDecimalDelegationCommon.ZeroAmount();
        if (stake.tokenType == IDecimalDelegationCommon.TokenType.None)
            revert IDecimalDelegationCommon.InvalidTokenType();
    }

    function _addValidatorReserve(
        IDecimalDelegationCommon.DelegationStorage storage $,
        IDecimalDelegationCommon.Stake memory stake
    ) internal {
        IDecimalDelegationCommon.ValidatorReserve
            storage validatorReserve = _getValidatorReserve(
                $,
                stake.validator,
                stake.token,
                stake.tokenId
            );
        validatorReserve.reserve += stake.amount;
    }

    function _reduceValidatorReserve(
        IDecimalDelegationCommon.DelegationStorage storage $,
        IDecimalDelegationCommon.Stake memory stake,
        uint256 amountToRemove
    ) internal {
        IDecimalDelegationCommon.ValidatorReserve
            storage validatorReserve = _getValidatorReserve(
                $,
                stake.validator,
                stake.token,
                stake.tokenId
            );
        validatorReserve.reserve -= amountToRemove;
    }

    // Linked delegatecall preserves the proxy storage and holder address.
    function applyPenaltiesToCoinStake(
        IDecimalDelegationCommon.DelegationStorage storage $,
        bytes32 stakeId
    ) public returns (bool) {
        return _applyPenaltiesToStake($, stakeId);
    }

    function _applyPenaltiesToStake(
        IDecimalDelegationCommon.DelegationStorage storage $,
        bytes32 stakeId
    ) internal returns (bool) {
        IDecimalDelegationCommon.Stake storage stake = _getStake($, stakeId);

        uint256 currentPenaltyIndex = getStakePenaltyIndex($, stakeId);
        (uint16[] memory percentages, uint256 count) = _getPenaltyPage($, stake.validator, currentPenaltyIndex, 32);
        if (currentPenaltyIndex == count) return false;
        if (stake.amount == 0) {
            _setStakePenaltyIndex($, stakeId, count);
            emit PenaltyAppliedToStake(stakeId, count);
            return true;
        }

        uint256 nftBackedAmount = $._nftBackedAmounts[stakeId];
        if (nftBackedAmount > stake.amount) revert IDecimalDelegationCommon.InvalidAmount();
        uint256 currentAmount = stake.amount - nftBackedAmount;
        for (uint256 i; i < percentages.length; i++) {
            currentAmount -= _getAmountToPenalty(currentAmount, percentages[i]);
        }
        currentPenaltyIndex += percentages.length;
        _setStakePenaltyIndex($, stakeId, currentPenaltyIndex);

        uint256 amountToRemove = (stake.amount - nftBackedAmount) - currentAmount;

        stake.amount = currentAmount + nftBackedAmount;
        if (amountToRemove != 0) {
            _reduceValidatorReserve($, stake, amountToRemove);
            _burnToken($, stake.token, amountToRemove);
        }

        bool isNewStake = false;
        emit StakeAmountUpdated(stakeId, -int256(amountToRemove));
        emit StakeUpdated(stakeId, _getStake($, stakeId), isNewStake);

        emit PenaltyAppliedToStake(stakeId, currentPenaltyIndex);
        return true;
    }

    function _applyPenaltiesToValidator(
        IDecimalDelegationCommon.DelegationStorage storage $,
        address validator,
        address token
    ) internal returns (bool) {
        uint256 count = _getValidatorPenaltyCount($, validator);
        IDecimalDelegationCommon.ValidatorReserve storage reserve =
            _getValidatorReserve($, validator, token, 0);
        if (reserve.penaltyIndex == count || count == 0) return false;
        reserve.penaltyIndex = count;
        // This event acknowledges the target index; losses burn on realization.
        emit PenaltyAppliedToValidator(validator, token,
            token == address(WETH($)) ? IDecimalDelegationCommon.TokenType.DEL : IDecimalDelegationCommon.TokenType.DRC20,
            0, count - 1, 0);
        return true;
    }

    function _applyPenalties(
        IDecimalDelegationCommon.DelegationStorage storage $,
        bytes32 stakeId
    ) internal returns (bool) {
        bool stakeApplied = _applyPenaltiesToStake($, stakeId);
        _requireCoinPenaltyCaughtUp($, stakeId);
        IDecimalDelegationCommon.Stake memory stake = _getStake($, stakeId);
        bool validatorApplied = _applyPenaltiesToValidator(
            $,
            stake.validator,
            stake.token
        );
        return stakeApplied || validatorApplied;
    }

    function _addStake(
        IDecimalDelegationCommon.DelegationStorage storage $,
        IDecimalDelegationCommon.Stake memory stake
    ) internal {
        bytes32 stakeId = _getStakeId(stake);
        _applyPenalties($, stakeId);
        _checkToken($, stake.token);
        _checkStake(stake);

        IDecimalDelegationCommon.Stake storage currentStake = _getStake(
            $,
            stakeId
        );
        bool isNewStake = currentStake.amount == 0;
        if (isNewStake) {
            uint256 currentPenaltyIndex = _getValidatorPenaltyCount($, stake.validator);

            currentStake.amount = stake.amount;
            currentStake.validator = stake.validator;
            currentStake.delegator = stake.delegator;
            currentStake.token = stake.token;
            currentStake.tokenId = stake.tokenId;
            currentStake.tokenType = stake.tokenType;
            currentStake.holdTimestamp = stake.holdTimestamp;
            if (stake.holdTimestamp != 0) {
                currentStake.holdStartTime = block.timestamp;
            }
            _setStakePenaltyIndex($, stakeId, currentPenaltyIndex);
        } else {
            currentStake.amount += stake.amount;
        }

        _addValidatorReserve($, stake);

        emit StakeAmountUpdated(stakeId, int256(stake.amount));
        emit StakeUpdated(stakeId, _getStake($, stakeId), isNewStake);
    }

    /* ========== DRC721 DELEGATION ========== */

    function delegateNFT(
        IDecimalDelegationCommon.DelegationStorage storage $,
        address delegator,
        address validator,
        address nftContract,
        uint256 tokenId,
        uint256 holdTimestamp
    ) public {
        _delegateReceipt($, delegator, validator, nftContract, tokenId, holdTimestamp, false);
    }

    function _delegateReceipt(
        IDecimalDelegationCommon.DelegationStorage storage $,
        address delegator, address validator, address nftContract,
        uint256 tokenId, uint256 holdTimestamp, bool received
    ) private {
        if (!_isValidatorActive($, validator))
            revert IDecimalDelegationCommon.InvalidValidator();
        _checkNFTRegistered($, nftContract);

        // Get NFT reserve
        INFTReserve.Reserve memory reserve = IDRC721(nftContract).getReserve(
            tokenId
        );
        if (
            reserve.reserveType == INFTReserve.ReserveType.None ||
            reserve.amount == 0
        ) {
            revert IDecimalDelegationCommon.NFTNoReserve();
        }

        // Generate NFT stake ID
        bytes32 nftStakeId = _getNFTStakeId(
            nftContract,
            tokenId,
            delegator,
            validator,
            holdTimestamp
        );

        // Check NFT is not already delegated
        if ($._nftStakes[nftStakeId].isActive) {
            revert IDecimalDelegationCommon.NFTAlreadyDelegated();
        }

        // New isolated receipts were minted into this Gateway in this call.
        // Ordinary wallet receipts must be transferred by the Gateway itself.
        if (received) {
            if (IDRC721(nftContract).ownerOf(tokenId) != address(this))
                revert IDecimalDelegationCommon.InvalidAmount();
        } else {
            IDRC721(nftContract).safeTransferFrom(delegator, address(this), tokenId);
        }

        // Determine reserve token and type
        address reserveToken;
        IDecimalDelegationCommon.TokenType tokenType;
        if (reserve.reserveType == INFTReserve.ReserveType.DEL) {
            reserveToken = address(WETH($));
            tokenType = IDecimalDelegationCommon.TokenType.DEL;
        } else {
            reserveToken = reserve.token;
            tokenType = IDecimalDelegationCommon.TokenType.DRC20;
        }

        // Create coin stake for reserve value (without token transfer)
        IDecimalDelegationCommon.Stake memory stake = IDecimalDelegationCommon
            .Stake({
                tokenType: tokenType,
                validator: validator,
                delegator: delegator,
                token: reserveToken,
                amount: reserve.amount,
                tokenId: 0,
                holdTimestamp: holdTimestamp,
                holdStartTime: 0
            });
        _addStake($, stake);

        bytes32 coinStakeId = _getStakeId(stake);

        _registerNFTStake(
            $,
            coinStakeId,
            nftStakeId,
            IDecimalDelegationCommon.NFTStake({
                nftContract: nftContract,
                tokenId: tokenId,
                amount: 1, // DRC721 is always 1
                nftType: IDecimalDelegationCommon.TokenType.DRC721,
                delegator: delegator,
                validator: validator,
                reserveToken: reserveToken,
                reserveAmount: reserve.amount,
                holdTimestamp: holdTimestamp,
                isActive: true
            })
        );
    }

    function delegateIsolatedNFT1155(
        IDecimalDelegationCommon.DelegationStorage storage $,
        address delegator, address validator, address originContract,
        uint256 originTokenId, uint256 quantity, uint256 holdTimestamp
    ) public {
        if (!_isValidatorActive($, validator)) revert IDecimalDelegationCommon.InvalidValidator();
        // Origins must still be approved factory-created reserve collections.
        NFTState memory state = _getNFTCenter($).getNFTState(originContract);
        if (!state.active || !state.withReserve || state.nftType != NFTType.DRC1155)
            revert NFTNotRegistered(originContract);
        address vault = _getAddress($, "nft-position-vault");
        if (vault == address(0) || vault.code.length == 0 ||
            IMintCandyIsolationOrigin(originContract).isolationVault() != vault)
            revert NFTNotRegistered(originContract);
        uint256 maximum = IMintCandyIsolationVault(vault).maxOriginQuantity();
        if (quantity == 0 || quantity > maximum) revert IDecimalDelegationCommon.InvalidAmount();
        uint256 expectedId = IMintCandyIsolationVault(vault).nextPositionId();
        if (expectedId == 0) revert IDecimalDelegationCommon.InvalidAmount();
        emit NFTIsolationStarted(expectedId, delegator, originContract, originTokenId, quantity);
        IERC1155(originContract).safeTransferFrom(delegator, address(this), originTokenId, quantity, "");
        uint256 actualId = IMintCandyIsolationOrigin(originContract).isolate(originTokenId, quantity, address(this));
        if (actualId != expectedId) revert IDecimalDelegationCommon.InvalidAmount();
        _delegateReceipt($, delegator, validator, vault, actualId, holdTimestamp, true);
    }

    /* ========== DRC1155 DELEGATION ========== */

    function delegateNFT1155(
        IDecimalDelegationCommon.DelegationStorage storage $,
        address delegator,
        address validator,
        address nftContract,
        uint256 tokenId,
        uint256 amount,
        uint256 holdTimestamp
    ) public {
        if (!_isValidatorActive($, validator))
            revert IDecimalDelegationCommon.InvalidValidator();
        if (amount == 0) revert IDecimalDelegationCommon.ZeroAmount();
        _checkNFTRegistered($, nftContract);

        // Get NFT rate (reserve per token)
        uint256 rate = IDRC1155(nftContract).rate(tokenId);
        if (rate == 0) {
            revert IDecimalDelegationCommon.NFTNoReserve();
        }

        // Calculate total reserve amount
        uint256 reserveAmount = rate * amount;

        // Get reserve type
        INFTReserve.Reserve memory reserve = IDRC1155(nftContract).getReserve(
            tokenId
        );
        if (reserve.reserveType == INFTReserve.ReserveType.None) {
            revert IDecimalDelegationCommon.NFTNoReserve();
        }

        // Generate NFT stake ID
        bytes32 nftStakeId = _getNFTStakeId(
            nftContract,
            tokenId,
            delegator,
            validator,
            holdTimestamp
        );

        // Check NFT is not already delegated with same parameters
        if ($._nftStakes[nftStakeId].isActive) {
            revert IDecimalDelegationCommon.NFTAlreadyDelegated();
        }

        // Transfer NFT to this contract
        IERC1155(nftContract).safeTransferFrom(
            delegator,
            address(this),
            tokenId,
            amount,
            ""
        );

        // Determine reserve token and type
        address reserveToken;
        IDecimalDelegationCommon.TokenType tokenType;
        if (reserve.reserveType == INFTReserve.ReserveType.DEL) {
            reserveToken = address(WETH($));
            tokenType = IDecimalDelegationCommon.TokenType.DEL;
        } else {
            reserveToken = reserve.token;
            tokenType = IDecimalDelegationCommon.TokenType.DRC20;
        }

        // Create coin stake for reserve value (without token transfer)
        IDecimalDelegationCommon.Stake memory stake = IDecimalDelegationCommon
            .Stake({
                tokenType: tokenType,
                validator: validator,
                delegator: delegator,
                token: reserveToken,
                amount: reserveAmount,
                tokenId: 0,
                holdTimestamp: holdTimestamp,
                holdStartTime: 0
            });
        _addStake($, stake);

        bytes32 coinStakeId = _getStakeId(stake);

        _registerNFTStake(
            $,
            coinStakeId,
            nftStakeId,
            IDecimalDelegationCommon.NFTStake({
                nftContract: nftContract,
                tokenId: tokenId,
                amount: amount,
                nftType: IDecimalDelegationCommon.TokenType.DRC1155,
                delegator: delegator,
                validator: validator,
                reserveToken: reserveToken,
                reserveAmount: reserveAmount,
                holdTimestamp: holdTimestamp,
                isActive: true
            })
        );
    }

    /* ========== NFT WITHDRAWAL ========== */

    function _createNFTFrozenStake(
        IDecimalDelegationCommon.DelegationStorage storage $,
        address nftContract,
        uint256 tokenId,
        address delegator,
        address validator,
        uint256 holdTimestamp
    ) internal view returns (IDecimalDelegationCommon.FrozenStake memory) {
        IDecimalDelegationCommon.Stake memory stake = IDecimalDelegationCommon
            .Stake({
                tokenType: IDecimalDelegationCommon.TokenType.DRC721,
                validator: validator,
                delegator: delegator,
                token: nftContract,
                amount: 1,
                tokenId: tokenId,
                holdTimestamp: holdTimestamp,
                holdStartTime: 0
            });

        return
            IDecimalDelegationCommon.FrozenStake({
                stake: stake,
                freezeStatus: IDecimalDelegationCommon.FreezeStatus.Frozen,
                freezeType: IDecimalDelegationCommon.FreezeType.Withdraw,
                unfreezeTimestamp: block.timestamp +
                    getFreezeTime(
                        $,
                        IDecimalDelegationCommon.FreezeType.Withdraw
                    )
            });
    }

    function _createNFT1155FrozenStake(
        IDecimalDelegationCommon.DelegationStorage storage $,
        address nftContract,
        uint256 tokenId,
        uint256 amount,
        address delegator,
        address validator,
        uint256 holdTimestamp
    ) internal view returns (IDecimalDelegationCommon.FrozenStake memory) {
        IDecimalDelegationCommon.Stake memory stake = IDecimalDelegationCommon
            .Stake({
                tokenType: IDecimalDelegationCommon.TokenType.DRC1155,
                validator: validator,
                delegator: delegator,
                token: nftContract,
                amount: amount,
                tokenId: tokenId,
                holdTimestamp: holdTimestamp,
                holdStartTime: 0
            });

        return
            IDecimalDelegationCommon.FrozenStake({
                stake: stake,
                freezeStatus: IDecimalDelegationCommon.FreezeStatus.Frozen,
                freezeType: IDecimalDelegationCommon.FreezeType.Withdraw,
                unfreezeTimestamp: block.timestamp +
                    getFreezeTime(
                        $,
                        IDecimalDelegationCommon.FreezeType.Withdraw
                    )
            });
    }

    function checkNFTReserveToken(
        IDecimalDelegationCommon.DelegationStorage storage $,
        address nftContract,
        uint256 tokenId,
        address delegator,
        address validator,
        uint256 holdTimestamp,
        address token
    ) public view {
        bytes32 nftStakeId = _getNFTStakeId(
            nftContract,
            tokenId,
            delegator,
            validator,
            holdTimestamp
        );
        require(
            $._nftStakes[nftStakeId].reserveToken == token,
            "Token mismatch"
        );
    }

    function withdrawNFT(
        IDecimalDelegationCommon.DelegationStorage storage $,
        address sender,
        address validator,
        address nftContract,
        uint256 tokenId,
        uint256 holdTimestamp
    ) public {
        // Check hold expired
        bool isHoldExpired = holdTimestamp == 0 ||
            holdTimestamp < block.timestamp;
        if (!isHoldExpired) {
            revert IDecimalDelegationCommon.HoldNotExpired(
                holdTimestamp,
                block.timestamp
            );
        }

        bytes32 nftStakeId = _getNFTStakeId(
            nftContract,
            tokenId,
            sender,
            validator,
            holdTimestamp
        );
        IDecimalDelegationCommon.NFTStake storage nftStake = $._nftStakes[
            nftStakeId
        ];

        if (!nftStake.isActive) {
            revert IDecimalDelegationCommon.NFTStakeNotActive();
        }
        if (nftStake.delegator != sender) {
            revert IDecimalDelegationCommon.NFTNotOwned();
        }

        // Realize NFT loss while it is still paired with its coin position.
        applyPenaltiesToNFTStake($, nftStakeId);
        _requireNFTPenaltyCaughtUp($, nftStakeId);

        // Settle the reserve amount recorded for this stake (kept in lockstep with
        // penalties), not the NFT's live reserve. Reading the live reserve let a
        // post-delegation reserve increase underflow the backed-amount subtraction below
        // (panic 0x11), permanently bricking the exit.
        uint256 currentReserveAmount = nftStake.reserveAmount;

        // Get coin stake ID
        bytes32 coinStakeId = _getStakeId(
            validator,
            sender,
            nftStake.reserveToken,
            0,
            holdTimestamp
        );

        // Keep NFT collateral excluded from the generic direct-coin loss.
        _applyPenalties($, coinStakeId);

        // Update NFT backed amount tracking
        $._nftBackedAmounts[coinStakeId] -= currentReserveAmount;

        // Mark NFT stake as inactive
        nftStake.isActive = false;

        // Remove from coin stake via freeze
        IDecimalDelegationCommon.FrozenStake
            memory frozenStake = _createNFTFrozenStake(
                $,
                nftContract,
                tokenId,
                sender,
                validator,
                holdTimestamp
            );

        // Reduce the coin stake amount
        IDecimalDelegationCommon.Stake storage coinStake = _getStake(
            $,
            coinStakeId
        );
        if (coinStake.amount < currentReserveAmount) {
            revert IDecimalDelegationCommon.InvalidAmount();
        }
        coinStake.amount -= currentReserveAmount;

        // Create a temporary stake to reduce validator reserve
        IDecimalDelegationCommon.Stake
            memory tempStake = IDecimalDelegationCommon.Stake({
                tokenType: coinStake.tokenType,
                validator: validator,
                delegator: sender,
                token: nftStake.reserveToken,
                amount: currentReserveAmount,
                tokenId: 0,
                holdTimestamp: holdTimestamp,
                holdStartTime: 0
            });
        _reduceValidatorReserve($, tempStake, currentReserveAmount);

        emit StakeAmountUpdated(coinStakeId, -int256(currentReserveAmount));
        emit StakeUpdated(coinStakeId, coinStake, false);

        // Add to frozen stakes
        $._frozenStakes.push(frozenStake);
        uint256 stakeIndex = $._frozenStakes.length - 1;

        emit WithdrawRequest(coinStakeId, stakeIndex, frozenStake);
        emit NFTUndelegated(nftStakeId, stakeIndex);
    }

    function withdrawNFT1155(
        IDecimalDelegationCommon.DelegationStorage storage $,
        address sender,
        address validator,
        address nftContract,
        uint256 tokenId,
        uint256 holdTimestamp
    ) public {
        // Check hold expired
        bool isHoldExpired = holdTimestamp == 0 ||
            holdTimestamp < block.timestamp;
        if (!isHoldExpired) {
            revert IDecimalDelegationCommon.HoldNotExpired(
                holdTimestamp,
                block.timestamp
            );
        }

        bytes32 nftStakeId = _getNFTStakeId(
            nftContract,
            tokenId,
            sender,
            validator,
            holdTimestamp
        );
        IDecimalDelegationCommon.NFTStake storage nftStake = $._nftStakes[
            nftStakeId
        ];

        if (!nftStake.isActive) {
            revert IDecimalDelegationCommon.NFTStakeNotActive();
        }
        if (nftStake.delegator != sender) {
            revert IDecimalDelegationCommon.NFTNotOwned();
        }

        uint256 nftAmount = nftStake.amount;

        // Realize NFT loss while it is still paired with its coin position.
        applyPenaltiesToNFTStake($, nftStakeId);
        _requireNFTPenaltyCaughtUp($, nftStakeId);

        // Settle the recorded reserve (kept in lockstep with penalties), not rate*amount.
        // rate*amount reads the NFT's live rate, so a post-delegation rate increase would
        // underflow the backed-amount subtraction below (panic 0x11) and brick the exit.
        uint256 currentReserveAmount = nftStake.reserveAmount;

        // Get coin stake ID
        bytes32 coinStakeId = _getStakeId(
            validator,
            sender,
            nftStake.reserveToken,
            0,
            holdTimestamp
        );

        // Keep NFT collateral excluded from the generic direct-coin loss.
        _applyPenalties($, coinStakeId);

        // Update NFT backed amount tracking
        $._nftBackedAmounts[coinStakeId] -= currentReserveAmount;

        // Mark NFT stake as inactive
        nftStake.isActive = false;

        // Remove from coin stake via freeze
        IDecimalDelegationCommon.FrozenStake
            memory frozenStake = _createNFT1155FrozenStake(
                $,
                nftContract,
                tokenId,
                nftAmount,
                sender,
                validator,
                holdTimestamp
            );

        // Reduce the coin stake amount
        IDecimalDelegationCommon.Stake storage coinStake = _getStake(
            $,
            coinStakeId
        );
        if (coinStake.amount < currentReserveAmount) {
            revert IDecimalDelegationCommon.InvalidAmount();
        }
        coinStake.amount -= currentReserveAmount;

        // Create a temporary stake to reduce validator reserve
        IDecimalDelegationCommon.Stake
            memory tempStake = IDecimalDelegationCommon.Stake({
                tokenType: coinStake.tokenType,
                validator: validator,
                delegator: sender,
                token: nftStake.reserveToken,
                amount: currentReserveAmount,
                tokenId: 0,
                holdTimestamp: holdTimestamp,
                holdStartTime: 0
            });
        _reduceValidatorReserve($, tempStake, currentReserveAmount);

        emit StakeAmountUpdated(coinStakeId, -int256(currentReserveAmount));
        emit StakeUpdated(coinStakeId, coinStake, false);

        // Add to frozen stakes
        $._frozenStakes.push(frozenStake);
        uint256 stakeIndex = $._frozenStakes.length - 1;

        emit WithdrawRequest(coinStakeId, stakeIndex, frozenStake);
        emit NFTUndelegated(nftStakeId, stakeIndex);
    }

    /* ========== NFT TRANSFER ========== */

    function _createNFTTransferFrozenStake(
        IDecimalDelegationCommon.DelegationStorage storage $,
        address nftContract,
        uint256 tokenId,
        address delegator,
        address newValidator,
        uint256 holdTimestamp
    ) internal view returns (IDecimalDelegationCommon.FrozenStake memory) {
        IDecimalDelegationCommon.Stake memory stake = IDecimalDelegationCommon
            .Stake({
                tokenType: IDecimalDelegationCommon.TokenType.DRC721,
                validator: newValidator,
                delegator: delegator,
                token: nftContract,
                amount: 1,
                tokenId: tokenId,
                holdTimestamp: holdTimestamp,
                holdStartTime: 0
            });

        return
            IDecimalDelegationCommon.FrozenStake({
                stake: stake,
                freezeStatus: IDecimalDelegationCommon.FreezeStatus.Frozen,
                freezeType: IDecimalDelegationCommon.FreezeType.Transfer,
                unfreezeTimestamp: block.timestamp +
                    getFreezeTime(
                        $,
                        IDecimalDelegationCommon.FreezeType.Transfer
                    )
            });
    }

    function _createNFT1155TransferFrozenStake(
        IDecimalDelegationCommon.DelegationStorage storage $,
        address nftContract,
        uint256 tokenId,
        uint256 amount,
        address delegator,
        address newValidator,
        uint256 holdTimestamp
    ) internal view returns (IDecimalDelegationCommon.FrozenStake memory) {
        IDecimalDelegationCommon.Stake memory stake = IDecimalDelegationCommon
            .Stake({
                tokenType: IDecimalDelegationCommon.TokenType.DRC1155,
                validator: newValidator,
                delegator: delegator,
                token: nftContract,
                amount: amount,
                tokenId: tokenId,
                holdTimestamp: holdTimestamp,
                holdStartTime: 0
            });

        return
            IDecimalDelegationCommon.FrozenStake({
                stake: stake,
                freezeStatus: IDecimalDelegationCommon.FreezeStatus.Frozen,
                freezeType: IDecimalDelegationCommon.FreezeType.Transfer,
                unfreezeTimestamp: block.timestamp +
                    getFreezeTime(
                        $,
                        IDecimalDelegationCommon.FreezeType.Transfer
                    )
            });
    }

    function transferNFT(
        IDecimalDelegationCommon.DelegationStorage storage $,
        address sender,
        address oldValidator,
        address nftContract,
        uint256 tokenId,
        uint256 holdTimestamp,
        address newValidator
    ) public {
        if (newValidator == oldValidator)
            revert IDecimalDelegationCommon.InvalidValidator();
        if (!_isValidatorActive($, newValidator))
            revert IDecimalDelegationCommon.InvalidValidator();

        bytes32 nftStakeId = _getNFTStakeId(
            nftContract,
            tokenId,
            sender,
            oldValidator,
            holdTimestamp
        );
        IDecimalDelegationCommon.NFTStake storage nftStake = $._nftStakes[
            nftStakeId
        ];

        if (!nftStake.isActive) {
            revert IDecimalDelegationCommon.NFTStakeNotActive();
        }
        if (nftStake.delegator != sender) {
            revert IDecimalDelegationCommon.NFTNotOwned();
        }

        // Realize NFT loss while it is still paired with its coin position.
        applyPenaltiesToNFTStake($, nftStakeId);
        _requireNFTPenaltyCaughtUp($, nftStakeId);

        // Settle the reserve amount recorded for this stake (kept in lockstep with
        // penalties), not the NFT's live reserve, so a post-delegation reserve increase
        // cannot underflow the backed-amount subtraction below (panic 0x11) and brick it.
        uint256 currentReserveAmount = nftStake.reserveAmount;

        // Get old coin stake ID
        bytes32 oldCoinStakeId = _getStakeId(
            oldValidator,
            sender,
            nftStake.reserveToken,
            0,
            holdTimestamp
        );

        // Keep NFT collateral excluded from the generic direct-coin loss.
        _applyPenalties($, oldCoinStakeId);

        // Update NFT backed amount tracking for old validator
        $._nftBackedAmounts[oldCoinStakeId] -= currentReserveAmount;

        // Mark old NFT stake as inactive
        nftStake.isActive = false;

        // Remove from old coin stake
        IDecimalDelegationCommon.Stake storage oldCoinStake = _getStake(
            $,
            oldCoinStakeId
        );
        if (oldCoinStake.amount < currentReserveAmount) {
            revert IDecimalDelegationCommon.InvalidAmount();
        }
        oldCoinStake.amount -= currentReserveAmount;

        // Reduce old validator reserve
        IDecimalDelegationCommon.Stake
            memory tempStake = IDecimalDelegationCommon.Stake({
                tokenType: oldCoinStake.tokenType,
                validator: oldValidator,
                delegator: sender,
                token: nftStake.reserveToken,
                amount: currentReserveAmount,
                tokenId: 0,
                holdTimestamp: holdTimestamp,
                holdStartTime: 0
            });
        _reduceValidatorReserve($, tempStake, currentReserveAmount);

        emit StakeAmountUpdated(oldCoinStakeId, -int256(currentReserveAmount));
        emit StakeUpdated(oldCoinStakeId, oldCoinStake, false);

        // Create frozen stake for transfer
        IDecimalDelegationCommon.FrozenStake
            memory frozenStake = _createNFTTransferFrozenStake(
                $,
                nftContract,
                tokenId,
                sender,
                newValidator,
                holdTimestamp
            );

        $._frozenStakes.push(frozenStake);
        uint256 stakeIndex = $._frozenStakes.length - 1;
        $._frozenNFTReserveAmounts[stakeIndex] = currentReserveAmount;

        emit TransferRequest(oldCoinStakeId, stakeIndex, frozenStake);
        emit NFTUndelegated(nftStakeId, stakeIndex);
    }

    function transferNFT1155(
        IDecimalDelegationCommon.DelegationStorage storage $,
        address sender,
        address oldValidator,
        address nftContract,
        uint256 tokenId,
        uint256 holdTimestamp,
        address newValidator
    ) public {
        if (newValidator == oldValidator)
            revert IDecimalDelegationCommon.InvalidValidator();
        if (!_isValidatorActive($, newValidator))
            revert IDecimalDelegationCommon.InvalidValidator();

        bytes32 nftStakeId = _getNFTStakeId(
            nftContract,
            tokenId,
            sender,
            oldValidator,
            holdTimestamp
        );
        IDecimalDelegationCommon.NFTStake storage nftStake = $._nftStakes[
            nftStakeId
        ];

        if (!nftStake.isActive) {
            revert IDecimalDelegationCommon.NFTStakeNotActive();
        }
        if (nftStake.delegator != sender) {
            revert IDecimalDelegationCommon.NFTNotOwned();
        }

        uint256 nftAmount = nftStake.amount;

        // Realize NFT loss while it is still paired with its coin position.
        applyPenaltiesToNFTStake($, nftStakeId);
        _requireNFTPenaltyCaughtUp($, nftStakeId);

        // Settle the recorded reserve (kept in lockstep with penalties), not rate*amount,
        // so a post-delegation rate increase cannot underflow the backed-amount
        // subtraction below (panic 0x11) and brick the transfer.
        uint256 currentReserveAmount = nftStake.reserveAmount;

        // Get old coin stake ID
        bytes32 oldCoinStakeId = _getStakeId(
            oldValidator,
            sender,
            nftStake.reserveToken,
            0,
            holdTimestamp
        );

        // Keep NFT collateral excluded from the generic direct-coin loss.
        _applyPenalties($, oldCoinStakeId);

        // Update NFT backed amount tracking for old validator
        $._nftBackedAmounts[oldCoinStakeId] -= currentReserveAmount;

        // Mark old NFT stake as inactive
        nftStake.isActive = false;

        // Remove from old coin stake
        IDecimalDelegationCommon.Stake storage oldCoinStake = _getStake(
            $,
            oldCoinStakeId
        );
        if (oldCoinStake.amount < currentReserveAmount) {
            revert IDecimalDelegationCommon.InvalidAmount();
        }
        oldCoinStake.amount -= currentReserveAmount;

        // Reduce old validator reserve
        IDecimalDelegationCommon.Stake
            memory tempStake = IDecimalDelegationCommon.Stake({
                tokenType: oldCoinStake.tokenType,
                validator: oldValidator,
                delegator: sender,
                token: nftStake.reserveToken,
                amount: currentReserveAmount,
                tokenId: 0,
                holdTimestamp: holdTimestamp,
                holdStartTime: 0
            });
        _reduceValidatorReserve($, tempStake, currentReserveAmount);

        emit StakeAmountUpdated(oldCoinStakeId, -int256(currentReserveAmount));
        emit StakeUpdated(oldCoinStakeId, oldCoinStake, false);

        // Create frozen stake for transfer
        IDecimalDelegationCommon.FrozenStake
            memory frozenStake = _createNFT1155TransferFrozenStake(
                $,
                nftContract,
                tokenId,
                nftAmount,
                sender,
                newValidator,
                holdTimestamp
            );

        $._frozenStakes.push(frozenStake);
        uint256 stakeIndex = $._frozenStakes.length - 1;
        $._frozenNFTReserveAmounts[stakeIndex] = currentReserveAmount;

        emit TransferRequest(oldCoinStakeId, stakeIndex, frozenStake);
        emit NFTUndelegated(nftStakeId, stakeIndex);
    }

    /* ========== NFT TRANSFER COMPLETION ========== */

    function completeNFTTransfer(
        IDecimalDelegationCommon.DelegationStorage storage $,
        uint256 stakeIndex,
        IDecimalDelegationCommon.Stake memory stake
    ) public {
        address nftContract = stake.token;
        uint256 tokenId = stake.tokenId;
        address delegator = stake.delegator;
        address newValidator = stake.validator;
        uint256 holdTimestamp = stake.holdTimestamp;

        // Settle the amount recorded when the transfer froze (what the source
        // validator was debited), not the live reserve, which may have changed
        // during the freeze window. 0 = frozen by a pre-holdStartTime impl;
        // fall back to the live read for those.
        uint256 reserveAmount = $._frozenNFTReserveAmounts[stakeIndex];
        if (reserveAmount != 0) {
            delete $._frozenNFTReserveAmounts[stakeIndex];
        }

        // The reserve record still identifies the reserve token/type.
        INFTReserve.Reserve memory reserve = IDRC721(nftContract).getReserve(
            tokenId
        );
        if (
            reserve.reserveType == INFTReserve.ReserveType.None ||
            (reserveAmount == 0 && reserve.amount == 0)
        ) {
            revert IDecimalDelegationCommon.NFTNoReserve();
        }
        if (reserveAmount == 0) {
            reserveAmount = reserve.amount;
        }

        // Determine reserve token and type
        address reserveToken;
        IDecimalDelegationCommon.TokenType tokenType;
        if (reserve.reserveType == INFTReserve.ReserveType.DEL) {
            reserveToken = address(WETH($));
            tokenType = IDecimalDelegationCommon.TokenType.DEL;
        } else {
            reserveToken = reserve.token;
            tokenType = IDecimalDelegationCommon.TokenType.DRC20;
        }

        // Create coin stake for new validator
        IDecimalDelegationCommon.Stake
            memory coinStake = IDecimalDelegationCommon.Stake({
                tokenType: tokenType,
                validator: newValidator,
                delegator: delegator,
                token: reserveToken,
                amount: reserveAmount,
                tokenId: 0,
                holdTimestamp: holdTimestamp,
                holdStartTime: 0
            });
        _addStake($, coinStake);

        bytes32 coinStakeId = _getStakeId(coinStake);
        bytes32 nftStakeId = _getNFTStakeId(
            nftContract,
            tokenId,
            delegator,
            newValidator,
            holdTimestamp
        );

        // Create new NFT stake for new validator
        _registerNFTStake(
            $,
            coinStakeId,
            nftStakeId,
            IDecimalDelegationCommon.NFTStake({
                nftContract: nftContract,
                tokenId: tokenId,
                amount: 1, // DRC721 is always 1
                nftType: IDecimalDelegationCommon.TokenType.DRC721,
                delegator: delegator,
                validator: newValidator,
                reserveToken: reserveToken,
                reserveAmount: reserveAmount,
                holdTimestamp: holdTimestamp,
                isActive: true
            })
        );
    }

    function completeNFT1155Transfer(
        IDecimalDelegationCommon.DelegationStorage storage $,
        uint256 stakeIndex,
        IDecimalDelegationCommon.Stake memory stake
    ) public {
        address nftContract = stake.token;
        uint256 tokenId = stake.tokenId;
        uint256 nftAmount = stake.amount;
        address delegator = stake.delegator;
        address newValidator = stake.validator;
        uint256 holdTimestamp = stake.holdTimestamp;

        // Settle the amount recorded when the transfer froze (what the source
        // validator was debited), not the live rate, which may have changed
        // during the freeze window. 0 = frozen by a pre-holdStartTime impl;
        // fall back to the live read for those.
        uint256 reserveAmount = $._frozenNFTReserveAmounts[stakeIndex];
        if (reserveAmount != 0) {
            delete $._frozenNFTReserveAmounts[stakeIndex];
        } else {
            uint256 rate = IDRC1155(nftContract).rate(tokenId);
            if (rate == 0) {
                revert IDecimalDelegationCommon.NFTNoReserve();
            }
            reserveAmount = rate * nftAmount;
        }

        // The reserve record still identifies the reserve token/type.
        INFTReserve.Reserve memory reserve = IDRC1155(nftContract).getReserve(
            tokenId
        );
        if (reserve.reserveType == INFTReserve.ReserveType.None) {
            revert IDecimalDelegationCommon.NFTNoReserve();
        }

        // Determine reserve token and type
        address reserveToken;
        IDecimalDelegationCommon.TokenType tokenType;
        if (reserve.reserveType == INFTReserve.ReserveType.DEL) {
            reserveToken = address(WETH($));
            tokenType = IDecimalDelegationCommon.TokenType.DEL;
        } else {
            reserveToken = reserve.token;
            tokenType = IDecimalDelegationCommon.TokenType.DRC20;
        }

        // Create coin stake for new validator
        IDecimalDelegationCommon.Stake
            memory coinStake = IDecimalDelegationCommon.Stake({
                tokenType: tokenType,
                validator: newValidator,
                delegator: delegator,
                token: reserveToken,
                amount: reserveAmount,
                tokenId: 0,
                holdTimestamp: holdTimestamp,
                holdStartTime: 0
            });
        _addStake($, coinStake);

        bytes32 coinStakeId = _getStakeId(coinStake);
        bytes32 nftStakeId = _getNFTStakeId(
            nftContract,
            tokenId,
            delegator,
            newValidator,
            holdTimestamp
        );

        // Create new NFT stake for new validator
        _registerNFTStake(
            $,
            coinStakeId,
            nftStakeId,
            IDecimalDelegationCommon.NFTStake({
                nftContract: nftContract,
                tokenId: tokenId,
                amount: nftAmount,
                nftType: IDecimalDelegationCommon.TokenType.DRC1155,
                delegator: delegator,
                validator: newValidator,
                reserveToken: reserveToken,
                reserveAmount: reserveAmount,
                holdTimestamp: holdTimestamp,
                isActive: true
            })
        );
    }

    /* ========== NFT PENALTY ========== */

    function applyPenaltiesToNFTStake(
        IDecimalDelegationCommon.DelegationStorage storage $,
        bytes32 nftStakeId
    ) public returns (bool) {
        return _applyPenaltiesToNFTStake($, nftStakeId, 16);
    }

    function applyPenaltiesToNFTStakeWithLimit(
        IDecimalDelegationCommon.DelegationStorage storage $,
        bytes32 nftStakeId,
        uint256 maxItems
    ) public returns (bool) {
        if (maxItems == 0 || maxItems > 16) revert IDecimalDelegationCommon.InvalidAmount();
        return _applyPenaltiesToNFTStake($, nftStakeId, maxItems);
    }

    function _applyPenaltiesToNFTStake(
        IDecimalDelegationCommon.DelegationStorage storage $,
        bytes32 nftStakeId,
        uint256 maxItems
    ) internal returns (bool) {
        IDecimalDelegationCommon.NFTStake storage nftStake = $._nftStakes[
            nftStakeId
        ];

        if (!nftStake.isActive) {
            return false;
        }

        uint256 currentPenaltyIndex = $._nftStakePenaltyIndex[nftStakeId];
        (uint16[] memory percentages, uint256 count) = _getPenaltyPage($, nftStake.validator, currentPenaltyIndex, maxItems);
        if (currentPenaltyIndex == count) return false;
        if (nftStake.reserveAmount == 0) {
            $._nftStakePenaltyIndex[nftStakeId] = count;
            return true;
        }

        // Get coin stake to update its amount
        bytes32 coinStakeId = _getStakeId(
            nftStake.validator,
            nftStake.delegator,
            nftStake.reserveToken,
            0,
            nftStake.holdTimestamp
        );
        IDecimalDelegationCommon.Stake storage coinStake = _getStake(
            $,
            coinStakeId
        );

        // Apply penalties
        for (uint256 pageIndex; pageIndex < percentages.length; pageIndex++) {
            // Call NFT penalty function to burn reserve
            uint256 burnedAmount;
            if (
                nftStake.nftType == IDecimalDelegationCommon.TokenType.DRC1155
            ) {
                // The inherited return value is NFT quantity, not reserve wei.
                // Preserve that API and settle the exact pool loss it realized.
                INFTReserve.Reserve memory beforeReserve = IDRC1155(nftStake.nftContract).getReserve(nftStake.tokenId);
                IDRC1155(nftStake.nftContract).penalty(
                    nftStake.tokenId,
                    nftStake.amount,
                    percentages[pageIndex]
                );
                INFTReserve.Reserve memory afterReserve = IDRC1155(nftStake.nftContract).getReserve(nftStake.tokenId);
                if (beforeReserve.token != afterReserve.token ||
                    beforeReserve.reserveType != afterReserve.reserveType ||
                    afterReserve.amount > beforeReserve.amount) {
                    revert IDecimalDelegationCommon.InvalidAmount();
                }
                burnedAmount = beforeReserve.amount - afterReserve.amount;
            } else {
                // DRC721 penalty signature
                burnedAmount = IDRC721(nftStake.nftContract).penalty(
                    nftStake.tokenId,
                    percentages[pageIndex]
                );
            }

            // All three mirrors must cover the actual NFT-contract loss.
            if (burnedAmount > nftStake.reserveAmount ||
                burnedAmount > coinStake.amount ||
                burnedAmount > $._nftBackedAmounts[coinStakeId]) {
                revert IDecimalDelegationCommon.InvalidAmount();
            }
            nftStake.reserveAmount -= burnedAmount;
            coinStake.amount -= burnedAmount;
            $._nftBackedAmounts[coinStakeId] -= burnedAmount;
            if (burnedAmount != 0) {
                _reduceValidatorReserve($, coinStake, burnedAmount);
            }

            emit NFTPenaltyApplied(
                nftStakeId,
                nftStake.nftContract,
                nftStake.tokenId,
                percentages[pageIndex],
                burnedAmount
            );

            emit StakeAmountUpdated(coinStakeId, -int256(burnedAmount));
        }

        currentPenaltyIndex += percentages.length;
        $._nftStakePenaltyIndex[nftStakeId] = currentPenaltyIndex;

        emit StakeUpdated(coinStakeId, coinStake, false);
        return true;
    }

    /* ========== FORCED NFT WITHDRAWAL ========== */

    function forcedNFTWithdrawal(
        IDecimalDelegationCommon.DelegationStorage storage $,
        address validator,
        address delegator,
        address nftContract,
        uint256 tokenId,
        uint256 holdTimestamp
    ) public {
        bytes32 nftStakeId = _getNFTStakeId(
            nftContract,
            tokenId,
            delegator,
            validator,
            holdTimestamp
        );
        IDecimalDelegationCommon.NFTStake storage nftStake = $._nftStakes[
            nftStakeId
        ];

        if (!nftStake.isActive) {
            revert IDecimalDelegationCommon.NFTStakeNotActive();
        }

        // Realize NFT loss while it is still paired with its coin position.
        applyPenaltiesToNFTStake($, nftStakeId);
        _requireNFTPenaltyCaughtUp($, nftStakeId);

        // Settle the reserve amount recorded for this stake (kept in lockstep with
        // penalties), not the NFT's live reserve, so a post-delegation reserve increase
        // cannot underflow the backed-amount subtraction below (panic 0x11) and brick even
        // the owner's forced exit.
        uint256 currentReserveAmount = nftStake.reserveAmount;

        // Get coin stake ID
        bytes32 coinStakeId = _getStakeId(
            validator,
            delegator,
            nftStake.reserveToken,
            0,
            holdTimestamp
        );

        // Keep NFT collateral excluded from the generic direct-coin loss.
        _applyPenalties($, coinStakeId);

        // Update NFT backed amount tracking
        $._nftBackedAmounts[coinStakeId] -= currentReserveAmount;

        // Mark NFT stake as inactive
        nftStake.isActive = false;

        // Reduce the coin stake amount
        IDecimalDelegationCommon.Stake storage coinStake = _getStake(
            $,
            coinStakeId
        );
        if (coinStake.amount < currentReserveAmount) {
            revert IDecimalDelegationCommon.InvalidAmount();
        }
        coinStake.amount -= currentReserveAmount;
        _reduceValidatorReserve($, coinStake, currentReserveAmount);

        // Transfer NFT back to delegator
        IDRC721(nftContract).safeTransferFrom(
            address(this),
            delegator,
            tokenId
        );

        emit StakeAmountUpdated(coinStakeId, -int256(currentReserveAmount));
        emit StakeUpdated(coinStakeId, coinStake, false);
        emit NFTUndelegated(
            nftStakeId,
            0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff
        );
    }

    function forcedNFT1155Withdrawal(
        IDecimalDelegationCommon.DelegationStorage storage $,
        address validator,
        address delegator,
        address nftContract,
        uint256 tokenId,
        uint256 holdTimestamp
    ) public {
        bytes32 nftStakeId = _getNFTStakeId(
            nftContract,
            tokenId,
            delegator,
            validator,
            holdTimestamp
        );
        IDecimalDelegationCommon.NFTStake storage nftStake = $._nftStakes[
            nftStakeId
        ];

        if (!nftStake.isActive) {
            revert IDecimalDelegationCommon.NFTStakeNotActive();
        }

        uint256 nftAmount = nftStake.amount;

        // Realize NFT loss while it is still paired with its coin position.
        applyPenaltiesToNFTStake($, nftStakeId);
        _requireNFTPenaltyCaughtUp($, nftStakeId);

        // Settle the recorded reserve (kept in lockstep with penalties), not rate*amount,
        // so a post-delegation rate increase cannot underflow the backed-amount
        // subtraction below (panic 0x11) and brick even the owner's forced exit.
        uint256 currentReserveAmount = nftStake.reserveAmount;

        // Get coin stake ID
        bytes32 coinStakeId = _getStakeId(
            validator,
            delegator,
            nftStake.reserveToken,
            0,
            holdTimestamp
        );

        // Keep NFT collateral excluded from the generic direct-coin loss.
        _applyPenalties($, coinStakeId);

        // Update NFT backed amount tracking
        $._nftBackedAmounts[coinStakeId] -= currentReserveAmount;

        // Mark NFT stake as inactive
        nftStake.isActive = false;

        // Reduce the coin stake amount
        IDecimalDelegationCommon.Stake storage coinStake = _getStake(
            $,
            coinStakeId
        );
        if (coinStake.amount < currentReserveAmount) {
            revert IDecimalDelegationCommon.InvalidAmount();
        }
        coinStake.amount -= currentReserveAmount;
        _reduceValidatorReserve($, coinStake, currentReserveAmount);

        // Transfer NFT back to delegator
        IERC1155(nftContract).safeTransferFrom(
            address(this),
            delegator,
            tokenId,
            nftAmount,
            ""
        );

        emit StakeAmountUpdated(coinStakeId, -int256(currentReserveAmount));
        emit StakeUpdated(coinStakeId, coinStake, false);
        emit NFTUndelegated(
            nftStakeId,
            0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff
        );
    }

    /* ========== FROZEN-STAKES LAYOUT MIGRATION (holdStartTime upgrade) ========== */
    // Not NFT logic — hosted here only because this external library is delegatecalled by
    // DecimalDelegation anyway and the impl has no EIP-170 headroom left for the copy loop.
    // Access control (onlyOwner + whenPaused) stays in the DecimalDelegation wrapper.

    // OLD on-chain layout of Stake (7 fields, before holdStartTime was appended).
    struct LegacyStake {
        address validator;
        address delegator;
        address token;
        uint256 amount;
        uint256 tokenId;
        IDecimalDelegationCommon.TokenType tokenType;
        uint256 holdTimestamp;
    }
    struct LegacyFrozenStake {
        LegacyStake stake;
        IDecimalDelegationCommon.FreezeStatus freezeStatus;
        IDecimalDelegationCommon.FreezeType freezeType;
        uint256 unfreezeTimestamp;
    }

    // Reads the OLD `_frozenStakes` data still sitting at DelegationStorage field index 3
    // (base slot + 3), interpreted with the OLD element stride.
    function _legacyFrozenStakes()
        private
        pure
        returns (LegacyFrozenStake[] storage arr)
    {
        assembly {
            // DECIMAL_DELEGATION_COMMON_STORAGE_LOCATION + 3
            arr.slot := add(
                0xc1dae510251b57b62087f142cc8746564a134308f8810580b66b08a29b6def00,
                3
            )
        }
    }

    // Resumable: copies up to `count` not-yet-migrated legacy frozen stakes into the new
    // `_frozenStakes` array (slot base+9, NEW layout), preserving order and indices. Old and
    // new arrays occupy disjoint storage regions, so there is no stride aliasing. Idempotent:
    // the destination length is the cursor. Call repeatedly until migrated == legacy length.
    function migrateFrozenStakesLayout(
        IDecimalDelegationCommon.DelegationStorage storage $,
        uint256 count
    ) external {
        LegacyFrozenStake[] storage legacy = _legacyFrozenStakes();
        IDecimalDelegationCommon.FrozenStake[] storage dst = $._frozenStakes;
        uint256 migrated = dst.length;
        uint256 total = legacy.length;
        uint256 end = migrated + count;
        if (end > total) end = total;
        for (uint256 i = migrated; i < end; i++) {
            LegacyFrozenStake memory s = legacy[i];
            dst.push(
                IDecimalDelegationCommon.FrozenStake({
                    stake: IDecimalDelegationCommon.Stake({
                        validator: s.stake.validator,
                        delegator: s.stake.delegator,
                        token: s.stake.token,
                        amount: s.stake.amount,
                        tokenId: s.stake.tokenId,
                        tokenType: s.stake.tokenType,
                        holdTimestamp: s.stake.holdTimestamp,
                        holdStartTime: 0
                    }),
                    freezeStatus: s.freezeStatus,
                    freezeType: s.freezeType,
                    unfreezeTimestamp: s.unfreezeTimestamp
                })
            );
        }
    }

    function legacyFrozenStakesLength() external view returns (uint256) {
        return _legacyFrozenStakes().length;
    }
}


// File contracts/DecimalDelegation.sol

// Original license: SPDX_License_Identifier: MIT

pragma solidity 0.8.20;













contract DecimalDelegation is
    IDecimalDelegation,
    DecimalDelegationCommon,
    ReentrancyGuard,
    ERC721Holder,
    ERC1155Holder
{
    using SafeERC20 for IERC20;

    error InvalidToken();

    // ============================================================
    // Auto-Unbond Queue (populated by Phase 1 via CallEVM)
    // ============================================================

    struct AutoUnbondStorage {
        AutoUnbondEntry[] queue;
    }

    // keccak256(abi.encode(uint256(keccak256("AUTO_UNBOND_STORAGE_LOCATION")) - 1)) & ~bytes32(uint256(0xff));
    bytes32 internal constant AUTO_UNBOND_STORAGE_LOCATION =
        0xd92fb98890f4a927a2850eac803b570950d9a3cb4225c0de7badfaf82a9fc800;

    function _getAutoUnbondStorage()
        internal
        pure
        returns (AutoUnbondStorage storage $)
    {
        assembly {
            $.slot := AUTO_UNBOND_STORAGE_LOCATION
        }
    }

    function autoUnbondEnqueue(
        address validator,
        address delegator,
        uint256 amount,
        address token,
        uint256 holdTimestamp
    ) external {
        require(msg.sender == address(0), "Not authorized");

        _getAutoUnbondStorage().queue.push(
            AutoUnbondEntry({
                validator: validator,
                delegator: delegator,
                amount: amount,
                token: token,
                holdTimestamp: holdTimestamp
            })
        );
    }

    function processAutoUnbond(uint256 index) external whenNotPaused {
        _processAutoUnbond(index);
    }

    // The node prunes a matured hold and enqueues its amount with holdTimestamp=0, while the
    // contract keeps it in the expired hold bucket until resetHolds. Merging those buckets
    // (the caller names them; the contract cannot enumerate a key's buckets) into the base
    // stake first lets the entry withdraw in full instead of being capped to the base stake.
    // Same checks as resetHolds: only expired, non-NFT-backed buckets of the entry's key.
    function processAutoUnbondWithReset(
        uint256 index,
        uint256[] memory holdTimestampsToReset
    ) external whenNotPaused {
        if (holdTimestampsToReset.length != 0) {
            AutoUnbondEntry memory entry = getAutoUnbondEntry(index);
            _resetHolds(
                entry.validator,
                entry.delegator,
                entry.token,
                0,
                holdTimestampsToReset
            );
        }
        _processAutoUnbond(index);
    }

    function _processAutoUnbond(uint256 index) private {
        AutoUnbondStorage storage $ = _getAutoUnbondStorage();
        uint256 len = $.queue.length;
        require(index < len, "Invalid index");

        AutoUnbondEntry memory entry = $.queue[index];

        require(
            entry.holdTimestamp == 0 || entry.holdTimestamp < block.timestamp,
            "Hold not expired"
        );

        // Swap with last element and pop (O(1) removal)
        $.queue[index] = $.queue[len - 1];
        $.queue.pop();

        _withdrawFor(
            entry.validator,
            entry.delegator,
            entry.token,
            0,
            entry.amount,
            entry.holdTimestamp
        );
    }

    function autoUnbondQueueLength() external view returns (uint256) {
        return _getAutoUnbondStorage().queue.length;
    }

    function getAutoUnbondEntry(
        uint256 index
    ) public view returns (AutoUnbondEntry memory) {
        AutoUnbondStorage storage $ = _getAutoUnbondStorage();
        require(index < $.queue.length, "Invalid index");
        return $.queue[index];
    }

    // Permissionless NFT counterpart to processAutoUnbond: return a force-unbonded NFT to its
    // delegator through the SAME freeze + NFT-typed WithdrawRequest path as the user withdrawNFT,
    // so the node hook (which already consumes WithdrawRequest) and the unbonding delay stay
    // consistent and the NFT is delivered on the later complete(). Legitimacy comes from the
    // node-only auto-unbond queue: `index` must point at a real entry (only autoUnbondEnqueue,
    // msg.sender == address(0), can create one) whose hold has expired — that is what prevents
    // force-exiting a healthy delegation. DelegationNFTLib.withdrawNFT reverts unless an ACTIVE NFT
    // stake exists for (entry.delegator, entry.validator, entry.holdTimestamp, nftContract,
    // tokenId), so a caller can only trigger returns the entry actually covers, and the NFT always
    // goes to its rightful delegator. The entry is left in place; the coin remainder (which the
    // _withdrawFor cap sizes to the non-NFT-backed portion) is drained by processAutoUnbond(index).
    // The off-chain operator supplies (nftContract, tokenId), which the node's coin shadow lacks.
    // A processable entry (exists, hold expired) that covers this NFT: an entry is for one
    // coin and must not force-exit the delegator's NFTs backed by another.
    function _autoUnbondEntryForNFT(
        uint256 index,
        address nftContract,
        uint256 tokenId
    ) private view returns (AutoUnbondEntry memory entry) {
        AutoUnbondStorage storage $ = _getAutoUnbondStorage();
        require(index < $.queue.length, "Invalid index");
        entry = $.queue[index];
        require(
            entry.holdTimestamp == 0 || entry.holdTimestamp < block.timestamp,
            "Hold not expired"
        );
        DelegationNFTLib.checkNFTReserveToken(
            _getDelegationStorage(),
            nftContract,
            tokenId,
            entry.delegator,
            entry.validator,
            entry.holdTimestamp,
            entry.token
        );
    }

    function processAutoUnbondNFT(
        uint256 index,
        address nftContract,
        uint256 tokenId
    ) external whenNotPaused {
        AutoUnbondEntry memory entry = _autoUnbondEntryForNFT(
            index,
            nftContract,
            tokenId
        );

        DelegationNFTLib.withdrawNFT(
            _getDelegationStorage(),
            entry.delegator,
            entry.validator,
            nftContract,
            tokenId,
            entry.holdTimestamp
        );
    }

    function processAutoUnbondNFT1155(
        uint256 index,
        address nftContract,
        uint256 tokenId
    ) external whenNotPaused {
        AutoUnbondEntry memory entry = _autoUnbondEntryForNFT(
            index,
            nftContract,
            tokenId
        );

        DelegationNFTLib.withdrawNFT1155(
            _getDelegationStorage(),
            entry.delegator,
            entry.validator,
            nftContract,
            tokenId,
            entry.holdTimestamp
        );
    }

    // ============================================================
    // One-shot storage-layout migration for the holdStartTime upgrade.
    // Lives here (not on the legacy DecimalDelegationMigration, which exceeds EIP-170 and is
    // no longer deployed): run it on this impl right after the upgrade, while paused.
    // The copy loop is in DelegationNFTLib (delegatecall, proxy storage context) to stay
    // under EIP-170; access control is enforced here.
    // whenPaused closes the safe window: every path that pushes to `_frozenStakes`
    // (user freezes AND the permissionless processAutoUnbond*) is whenNotPaused, so the
    // cursor/index-preservation invariant cannot be broken by a concurrent freeze.
    // ============================================================

    function migrateFrozenStakesLayout(
        uint256 count
    ) external onlyOwner whenPaused {
        DelegationNFTLib.migrateFrozenStakesLayout(
            _getDelegationStorage(),
            count
        );
    }

    function frozenStakesMigrationProgress()
        external
        view
        returns (uint256 migrated, uint256 total)
    {
        return (
            _getDelegationStorage()._frozenStakes.length,
            DelegationNFTLib.legacyFrozenStakesLength()
        );
    }

    // ============================================================

    function forcedWithdrawalBatch(
        address[] memory validators,
        address[] memory delegators,
        uint256[] memory amounts,
        address[] memory tokens
    ) public onlyOwner {
        emit WithdrawCompleted(
            0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff
        );

        uint256 len = validators.length;
        require(
            delegators.length == len &&
                amounts.length == len &&
                tokens.length == len,
            "Invalid array lengths"
        );
        for (uint256 i = 0; i < len; i++) {
            address validator = validators[i];
            address delegator = delegators[i];
            uint256 amount = amounts[i];
            address token = tokens[i];

            _applyForcedWithdrawal(validator, delegator, amount, token);
        }
    }

    function forcedWithdrawal(
        address validator,
        address delegator,
        uint256 amount,
        address token
    ) public {
        require(msg.sender == address(0), "Not authorized");

        emit WithdrawCompleted(
            0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff
        );

        _applyForcedWithdrawal(validator, delegator, amount, token);
    }

    function _applyForcedWithdrawal(
        address validator,
        address delegator,
        uint256 amount,
        address token
    ) internal {
        TokenType tokenType = TokenType.DRC20;
        if (token == address(_getAddress("wdel"))) {
            tokenType = TokenType.DEL;
        }

        bytes32 stakeId = _getStakeId(
            Stake({
                tokenType: tokenType,
                validator: validator,
                delegator: delegator,
                token: token,
                amount: amount,
                tokenId: 0,
                holdTimestamp: 0,
                holdStartTime: 0
            })
        );
        Stake storage currentStake = _getStake(stakeId);

        if (currentStake.amount < amount) {
            revert InvalidAmount();
        }

        currentStake.amount -= amount;

        if (currentStake.token == address(WETH())) {
            WETH().withdraw(amount);
            (bool success, ) = currentStake.delegator.call{value: amount}("");
            if (!success) {
                revert WithdrawFailed(currentStake.delegator);
            }
        } else {
            IERC20(currentStake.token).safeTransfer(
                currentStake.delegator,
                amount
            );
        }

        emit StakeUpdated(stakeId, _getStake(stakeId), false);
    }

    function forcedNFTWithdrawal(
        address validator,
        address delegator,
        address nftContract,
        uint256 tokenId,
        uint256 holdTimestamp
    ) public onlyOwner {
        DelegationNFTLib.forcedNFTWithdrawal(
            _getDelegationStorage(),
            validator,
            delegator,
            nftContract,
            tokenId,
            holdTimestamp
        );
    }

    function forcedNFT1155Withdrawal(
        address validator,
        address delegator,
        address nftContract,
        uint256 tokenId,
        uint256 holdTimestamp
    ) public onlyOwner {
        DelegationNFTLib.forcedNFT1155Withdrawal(
            _getDelegationStorage(),
            validator,
            delegator,
            nftContract,
            tokenId,
            holdTimestamp
        );
    }

    /* ========== METHODS ========== */

    function delegateDEL(
        address validator
    ) public payable virtual override whenNotPaused {
        _delegateDEL(msg.sender, validator, 0);
    }

    function delegateDELTo(
        address delegator,
        address validator
    ) public payable virtual whenNotPaused {
        uint256 holdTimestamp = 0;
        _delegateDEL(delegator, validator, holdTimestamp);
    }

    function delegateHoldDEL(
        address validator,
        uint256 holdTimestamp
    ) public payable virtual whenNotPaused {
        if (holdTimestamp == 0) revert InvalidHoldTimestamp(0);
        _delegateDEL(msg.sender, validator, holdTimestamp);
    }

    function _delegateDEL(
        address delegator,
        address validator,
        uint256 holdTimestamp
    ) internal {
        if (!_isValidatorActive(validator)) revert InvalidValidator();

        uint256 amount = msg.value;
        address token = address(WETH());

        Stake memory stake = Stake({
            tokenType: TokenType.DEL,
            validator: validator,
            delegator: delegator,
            token: token,
            amount: amount,
            tokenId: 0,
            holdTimestamp: holdTimestamp,
            holdStartTime: 0
        });
        _addStake(stake);

        WETH().deposit{value: amount}();
    }

    function delegate(
        address validator,
        address token,
        uint256 amount
    ) public virtual override whenNotPaused {
        uint256 holdTimestamp = 0;
        _delegate(msg.sender, validator, token, amount, holdTimestamp);
    }

    function delegateTo(
        address delegator,
        address validator,
        address token,
        uint256 amount
    ) public virtual whenNotPaused {
        uint256 holdTimestamp = 0;
        _delegate(delegator, validator, token, amount, holdTimestamp);
    }

    function delegateHold(
        address validator,
        address token,
        uint256 amount,
        uint256 holdTimestamp
    ) public virtual whenNotPaused {
        if (holdTimestamp == 0) revert InvalidHoldTimestamp(0);
        _delegate(msg.sender, validator, token, amount, holdTimestamp);
    }

    function _delegate(
        address delegator,
        address validator,
        address token,
        uint256 amount,
        uint256 holdTimestamp
    ) internal virtual {
        if (!_isValidatorActive(validator)) revert InvalidValidator();

        Stake memory stake = Stake({
            tokenType: TokenType.DRC20,
            validator: validator,
            delegator: delegator,
            token: token,
            amount: amount,
            tokenId: 0,
            holdTimestamp: holdTimestamp,
            holdStartTime: 0
        });
        _addStake(stake);
        IERC20(token).safeTransferFrom(msg.sender, address(this), amount);
    }

    function delegateByPermit(
        address validator,
        address token,
        uint256 amount,
        uint256 deadline,
        uint8 v,
        bytes32 r,
        bytes32 s
    ) public virtual whenNotPaused {
        _delegateByPermit(validator, token, amount, 0, deadline, v, r, s);
    }

    function delegateHoldByPermit(
        address validator,
        address token,
        uint256 amount,
        uint256 holdTimestamp,
        uint256 deadline,
        uint8 v,
        bytes32 r,
        bytes32 s
    ) public virtual whenNotPaused {
        if (holdTimestamp == 0) revert InvalidHoldTimestamp(0);
        _delegateByPermit(
            validator,
            token,
            amount,
            holdTimestamp,
            deadline,
            v,
            r,
            s
        );
    }

    function _delegateByPermit(
        address validator,
        address token,
        uint256 amount,
        uint256 holdTimestamp,
        uint256 deadline,
        uint8 v,
        bytes32 r,
        bytes32 s
    ) internal {
        if (!_isValidatorActive(validator)) revert InvalidValidator();

        IERC20Permit(token).permit(
            msg.sender,
            address(this),
            amount,
            deadline,
            v,
            r,
            s
        );

        _delegate(msg.sender, validator, token, amount, holdTimestamp);
    }

    /* ========== NFT DELEGATION ========== */

    function delegateNFT(
        address validator,
        address nftContract,
        uint256 tokenId
    ) public virtual whenNotPaused {
        DelegationNFTLib.delegateNFT(
            _getDelegationStorage(),
            msg.sender,
            validator,
            nftContract,
            tokenId,
            0
        );
    }

    function delegateNFTHold(
        address validator,
        address nftContract,
        uint256 tokenId,
        uint256 holdTimestamp
    ) public virtual whenNotPaused {
        if (holdTimestamp == 0) revert InvalidHoldTimestamp(0);
        DelegationNFTLib.delegateNFT(
            _getDelegationStorage(),
            msg.sender,
            validator,
            nftContract,
            tokenId,
            holdTimestamp
        );
    }

    function delegateNFTByPermit(
        address validator,
        address nftContract,
        uint256 tokenId,
        uint256 deadline,
        uint8 v,
        bytes32 r,
        bytes32 s
    ) public virtual whenNotPaused {
        IDRC721(nftContract).permit(address(this), tokenId, deadline, v, r, s);
        DelegationNFTLib.delegateNFT(
            _getDelegationStorage(),
            msg.sender,
            validator,
            nftContract,
            tokenId,
            0
        );
    }

    function _getNFTStakeId(
        address nftContract,
        uint256 tokenId,
        address delegator,
        address validator,
        uint256 holdTimestamp
    ) internal pure returns (bytes32) {
        return
            keccak256(
                abi.encodePacked(
                    "NFT",
                    nftContract,
                    tokenId,
                    delegator,
                    validator,
                    holdTimestamp
                )
            );
    }

    /* ========== DRC1155 DELEGATION ========== */

    function delegateNFT1155(
        address validator,
        address nftContract,
        uint256 tokenId,
        uint256 amount
    ) public virtual whenNotPaused {
        DelegationNFTLib.delegateNFT1155(
            _getDelegationStorage(),
            msg.sender,
            validator,
            nftContract,
            tokenId,
            amount,
            0
        );
    }

    function delegateNFT1155Hold(
        address validator,
        address nftContract,
        uint256 tokenId,
        uint256 amount,
        uint256 holdTimestamp
    ) public virtual whenNotPaused {
        if (holdTimestamp == 0) revert InvalidHoldTimestamp(0);
        DelegationNFTLib.delegateNFT1155(
            _getDelegationStorage(),
            msg.sender,
            validator,
            nftContract,
            tokenId,
            amount,
            holdTimestamp
        );
    }

    /* ========== NFT WITHDRAWAL ========== */

    function withdrawNFT(
        address validator,
        address nftContract,
        uint256 tokenId
    ) public virtual whenNotPaused {
        DelegationNFTLib.withdrawNFT(
            _getDelegationStorage(),
            msg.sender,
            validator,
            nftContract,
            tokenId,
            0
        );
    }

    function withdrawNFTHold(
        address validator,
        address nftContract,
        uint256 tokenId,
        uint256 holdTimestamp
    ) public virtual whenNotPaused {
        DelegationNFTLib.withdrawNFT(
            _getDelegationStorage(),
            msg.sender,
            validator,
            nftContract,
            tokenId,
            holdTimestamp
        );
    }

    /* ========== DRC1155 WITHDRAWAL ========== */

    function withdrawNFT1155(
        address validator,
        address nftContract,
        uint256 tokenId
    ) public virtual whenNotPaused {
        DelegationNFTLib.withdrawNFT1155(
            _getDelegationStorage(),
            msg.sender,
            validator,
            nftContract,
            tokenId,
            0
        );
    }

    function withdrawNFT1155Hold(
        address validator,
        address nftContract,
        uint256 tokenId,
        uint256 holdTimestamp
    ) public virtual whenNotPaused {
        DelegationNFTLib.withdrawNFT1155(
            _getDelegationStorage(),
            msg.sender,
            validator,
            nftContract,
            tokenId,
            holdTimestamp
        );
    }

    /* ========== NFT TRANSFER ========== */

    function transferNFT(
        address oldValidator,
        address nftContract,
        uint256 tokenId,
        address newValidator
    ) public virtual whenNotPaused {
        DelegationNFTLib.transferNFT(
            _getDelegationStorage(),
            msg.sender,
            oldValidator,
            nftContract,
            tokenId,
            0,
            newValidator
        );
    }

    function transferNFTHold(
        address oldValidator,
        address nftContract,
        uint256 tokenId,
        uint256 holdTimestamp,
        address newValidator
    ) public virtual whenNotPaused {
        DelegationNFTLib.transferNFT(
            _getDelegationStorage(),
            msg.sender,
            oldValidator,
            nftContract,
            tokenId,
            holdTimestamp,
            newValidator
        );
    }

    /* ========== DRC1155 TRANSFER ========== */

    function transferNFT1155(
        address oldValidator,
        address nftContract,
        uint256 tokenId,
        address newValidator
    ) public virtual whenNotPaused {
        DelegationNFTLib.transferNFT1155(
            _getDelegationStorage(),
            msg.sender,
            oldValidator,
            nftContract,
            tokenId,
            0,
            newValidator
        );
    }

    function transferNFT1155Hold(
        address oldValidator,
        address nftContract,
        uint256 tokenId,
        uint256 holdTimestamp,
        address newValidator
    ) public virtual whenNotPaused {
        DelegationNFTLib.transferNFT1155(
            _getDelegationStorage(),
            msg.sender,
            oldValidator,
            nftContract,
            tokenId,
            holdTimestamp,
            newValidator
        );
    }

    function hold(
        address validator,
        address token,
        uint256 amountToHold,
        uint256, //oldHoldTimestamp,
        uint256 newHoldTimestamp
    ) public virtual whenNotPaused {
        uint256 tokenId = 0;
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
        uint256 amountToHold,
        uint256 newHoldTimestamp,
        uint256[] memory holdTimestampsToReset
    ) public virtual whenNotPaused {
        uint256 tokenId = 0;
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
        uint256 holdTimestamp
    ) public virtual whenNotPaused {
        uint256 tokenId = 0;
        uint256[] memory holdTimestamps = new uint256[](1);
        holdTimestamps[0] = holdTimestamp;
        _resetHolds(validator, delegator, token, tokenId, holdTimestamps);
    }

    function resetHolds(
        address validator,
        address delegator,
        address token,
        uint256[] memory holdTimestamps
    ) public virtual whenNotPaused {
        uint256 tokenId = 0;
        _resetHolds(validator, delegator, token, tokenId, holdTimestamps);
    }

    function withdraw(
        address validator,
        address token,
        uint256 amount
    ) public virtual whenNotPaused {
        uint256 tokenId = 0;
        uint256 holdTimestamp = 0;
        _withdraw(validator, token, tokenId, amount, holdTimestamp);
    }

    function withdrawWithReset(
        address validator,
        address token,
        uint256 amount,
        uint256[] memory holdTimestampsToReset
    ) public virtual whenNotPaused {
        uint256 tokenId = 0;
        _withdrawWithReset(
            validator,
            token,
            tokenId,
            amount,
            holdTimestampsToReset
        );
    }

    function withdrawHold(
        address validator,
        address token,
        uint256 amount,
        uint256 holdTimestamp
    ) public virtual whenNotPaused {
        uint256 tokenId = 0;
        _withdraw(validator, token, tokenId, amount, holdTimestamp);
    }

    function _completeWithdraw(
        uint256 stakeIndex
    ) internal virtual override nonReentrant {
        DelegationStorage storage $ = _getDelegationStorage();
        FrozenStake storage frozenStake = $._frozenStakes[stakeIndex];
        _checkFreezeTime(frozenStake.unfreezeTimestamp);
        if (frozenStake.freezeStatus == FreezeStatus.Completed) {
            revert StakeAlreadyUnfrozen();
        }
        frozenStake.freezeStatus = FreezeStatus.Completed;
        Stake memory stake = frozenStake.stake;

        // Handle NFT withdrawal - return NFT to delegator
        if (stake.tokenType == TokenType.DRC721) {
            IDRC721(stake.token).safeTransferFrom(
                address(this),
                stake.delegator,
                stake.tokenId
            );
        } else if (stake.tokenType == TokenType.DRC1155) {
            IERC1155(stake.token).safeTransferFrom(
                address(this),
                stake.delegator,
                stake.tokenId,
                stake.amount,
                ""
            );
        } else if (stake.token == address(WETH())) {
            WETH().withdraw(stake.amount);
            (bool success, ) = stake.delegator.call{value: stake.amount}("");
            if (!success) {
                revert WithdrawFailed(stake.delegator);
            }
        } else {
            IERC20(stake.token).safeTransfer(stake.delegator, stake.amount);
        }

        emit WithdrawCompleted(stakeIndex);
    }

    function _completeTransfer(uint256 stakeIndex) internal virtual override {
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

        Stake memory stake = frozenStake.stake;

        // Handle NFT transfer - re-delegate to new validator
        if (stake.tokenType == TokenType.DRC721) {
            DelegationNFTLib.completeNFTTransfer(
                _getDelegationStorage(),
                stakeIndex,
                stake
            );
        } else if (stake.tokenType == TokenType.DRC1155) {
            DelegationNFTLib.completeNFT1155Transfer(
                _getDelegationStorage(),
                stakeIndex,
                stake
            );
        } else {
            _addStake(stake);
        }

        emit TransferCompleted(stakeIndex);
    }

    function transfer(
        address validator,
        address token,
        uint256 amount,
        address newValidator
    ) public virtual whenNotPaused {
        uint256 tokenId = 0;
        uint256 holdTimestamp = 0;
        _transfer(
            validator,
            token,
            tokenId,
            amount,
            holdTimestamp,
            newValidator
        );
    }

    function transferHold(
        address validator,
        address token,
        uint256 amount,
        uint256 holdTimestamp,
        address newValidator
    ) public virtual whenNotPaused {
        uint256 tokenId = 0;
        _transfer(
            validator,
            token,
            tokenId,
            amount,
            holdTimestamp,
            newValidator
        );
    }

    function transferWithReset(
        address oldValidator,
        address token,
        uint256 amount,
        address newValidator,
        uint256[] memory holdTimestampsToReset
    ) public virtual whenNotPaused {
        uint256 tokenId = 0;
        _transferWithReset(
            oldValidator,
            token,
            tokenId,
            amount,
            newValidator,
            holdTimestampsToReset
        );
    }

    function _applyPenaltiesToValidator(
        address validator,
        address token,
        uint256
    ) internal override returns (bool) {
        uint256 count = _getValidatorPenaltyCount(validator);
        ValidatorReserve storage reserve = _getValidatorReserve(validator, token, 0);
        if (reserve.penaltyIndex == count || count == 0) return false;
        reserve.penaltyIndex = count;
        // This event acknowledges the target index; losses burn on realization.
        emit PenaltyAppliedToValidator(validator, token,
            token == address(WETH()) ? TokenType.DEL : TokenType.DRC20,
            0, count - 1, 0);
        return true;
    }

    function applyPenaltiesToStakes(
        bytes32[] memory stakeIds
    ) public virtual whenNotPaused {
        if (stakeIds.length > 16) revert InvalidAmount();
        bool hasPenalty = false;
        for (uint256 i = 0; i < stakeIds.length; i += 1) {
            hasPenalty = _applyPenaltiesToStake(stakeIds[i]) || hasPenalty;
        }
        if (!hasPenalty) {
            revert NoPenaltyToApply();
        }
    }

    function _applyPenaltiesToStake(
        bytes32 stakeId
    ) internal override returns (bool) {
        return DelegationNFTLib.applyPenaltiesToCoinStake(_getDelegationStorage(), stakeId);
    }

    /* ========== NFT PENALTY ========== */

    function applyPenaltiesToNFTStakes(
        bytes32[] memory nftStakeIds
    ) public virtual whenNotPaused {
        if (nftStakeIds.length > 16) revert InvalidAmount();
        DelegationStorage storage $ = _getDelegationStorage();
        bool hasPenalty = false;
        uint256 remainingItems = 16;
        // The whole entrypoint, not each array element, shares the native-call
        // budget. The EVM's existing total20 native-action guard is unchanged.
        for (uint256 i; i < nftStakeIds.length && remainingItems != 0; i++) {
            bytes32 id = nftStakeIds[i];
            uint256 beforeCursor = $._nftStakePenaltyIndex[id];
            hasPenalty = DelegationNFTLib.applyPenaltiesToNFTStakeWithLimit($, id, remainingItems) || hasPenalty;
            uint256 used = $._nftStakePenaltyIndex[id] - beforeCursor;
            if (used >= remainingItems) remainingItems = 0;
            else remainingItems -= used;
        }
        if (!hasPenalty) revert NoPenaltyToApply();
    }

    function getNFTStakePenaltyIndex(
        bytes32 nftStakeId
    ) public view returns (uint256) {
        return _getDelegationStorage()._nftStakePenaltyIndex[nftStakeId];
    }

    function _burnToken(address token, uint256 amountToBurn) internal {
        // WDEL burns its matching native SDK backing in the same transaction.
        ERC20Burnable(token).burn(amountToBurn);
    }

    function _checkToken(address token, TokenType) internal view override {
        if (token == address(WETH())) {
            return;
        }
        if (token == address(0) || !_getTokenCenter().isTokenExists(token)) {
            revert InvalidToken();
        }
    }

    /* ========== GETTERS ========== */

    // solhint-disable-next-line func-name-mixedcase
    function WETH() public view returns (IWETH) {
        return IWETH(_getAddress("wdel"));
    }

    function _getTokenCenter() internal view returns (IDecimalTokenCenter) {
        return IDecimalTokenCenter(_getAddress("token-center"));
    }

    function getValidatorReserve(
        address validator,
        address token
    ) external view returns (ValidatorReserve memory) {
        return _getValidatorReserve(validator, token, 0);
    }

    function getStakeId(
        address validator,
        address delegator,
        address token
    ) public pure returns (bytes32) {
        return _getStakeId(validator, delegator, token, 0, 0);
    }

    function getHoldStakeId(
        address validator,
        address delegator,
        address token,
        uint256 holdTimestamp
    ) public pure returns (bytes32) {
        return _getStakeId(validator, delegator, token, 0, holdTimestamp);
    }

    function getStake(
        address validator,
        address delegator,
        address token
    ) public view returns (Stake memory) {
        return _getStake(getStakeId(validator, delegator, token));
    }

    // function getStakeDEL(
    //     address validator,
    //     address delegator
    // ) public view returns (Stake memory) {
    //     address token = address(WETH());
    //     return getStake(validator, delegator, token);
    // }

    function getHoldStake(
        address validator,
        address delegator,
        address token,
        uint256 holdTimestamp
    ) public view returns (Stake memory) {
        return
            _getStake(
                getHoldStakeId(validator, delegator, token, holdTimestamp)
            );
    }

    function getHoldStakeDEL(
        address validator,
        address delegator,
        uint256 holdTimestamp
    ) public view returns (Stake memory) {
        address token = address(WETH());
        return getHoldStake(validator, delegator, token, holdTimestamp);
    }

    /* ========== NFT GETTERS ========== */

    function getNFTStake(
        address nftContract,
        uint256 tokenId,
        address delegator,
        address validator,
        uint256 holdTimestamp
    ) public view returns (NFTStake memory) {
        bytes32 nftStakeId = _getNFTStakeId(
            nftContract,
            tokenId,
            delegator,
            validator,
            holdTimestamp
        );
        return _getDelegationStorage()._nftStakes[nftStakeId];
    }

    function getNFTStakeId(
        address nftContract,
        uint256 tokenId,
        address delegator,
        address validator,
        uint256 holdTimestamp
    ) public pure returns (bytes32) {
        return
            _getNFTStakeId(
                nftContract,
                tokenId,
                delegator,
                validator,
                holdTimestamp
            );
    }

    // Iterable index of NFT stakes that ever backed a coin stake; entries persist
    // after withdrawal/transfer — filter by getNFTStakeById(id).isActive.
    function getCoinStakeNFTs(
        bytes32 coinStakeId
    ) external view returns (bytes32[] memory) {
        return _getDelegationStorage()._coinStakeNFTs[coinStakeId];
    }

    function getNFTStakeById(
        bytes32 nftStakeId
    ) external view returns (NFTStake memory) {
        return _getDelegationStorage()._nftStakes[nftStakeId];
    }

    function getAvailableCoinAmount(
        address validator,
        address delegator,
        address token,
        uint256 holdTimestamp
    ) public view returns (uint256) {
        bytes32 coinStakeId = _getStakeId(
            validator,
            delegator,
            token,
            0,
            holdTimestamp
        );
        Stake memory stake = _getStake(coinStakeId);
        uint256 nftBackedAmount = _getDelegationStorage()._nftBackedAmounts[
            coinStakeId
        ];

        if (stake.amount <= nftBackedAmount) {
            return 0;
        }
        return stake.amount - nftBackedAmount;
    }

    function getNFTBackedAmount(
        address validator,
        address delegator,
        address token,
        uint256 holdTimestamp
    ) public view returns (uint256) {
        bytes32 coinStakeId = _getStakeId(
            validator,
            delegator,
            token,
            0,
            holdTimestamp
        );
        return _getDelegationStorage()._nftBackedAmounts[coinStakeId];
    }

    /**
     * @dev Fallback function to receive Ether.
     */
    receive() external payable virtual {}
}
/// @notice Fresh-network Unified Gateway: ERC1155 delegation creates an isolated ERC721 position.
/// @dev Historical shared-pool artifact remains separately named MintCandyDelegation.
contract MintCandyIsolatedDelegation is DecimalDelegation {
    function delegateNFT1155(address validator, address originContract, uint256 tokenId, uint256 quantity)
        public override whenNotPaused {
        DelegationNFTLib.delegateIsolatedNFT1155(_getDelegationStorage(), msg.sender,
            validator, originContract, tokenId, quantity, 0);
    }
    function delegateNFT1155Hold(address validator, address originContract, uint256 tokenId,
        uint256 quantity, uint256 holdTimestamp) public override whenNotPaused {
        if (holdTimestamp == 0) revert InvalidHoldTimestamp(0);
        DelegationNFTLib.delegateIsolatedNFT1155(_getDelegationStorage(), msg.sender,
            validator, originContract, tokenId, quantity, holdTimestamp);
    }
}
