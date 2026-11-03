

pragma solidity ^0.8.20;

interface IBeacon {

    function implementation() external view returns (address);
}

pragma solidity ^0.8.20;

library Address {

    error AddressInsufficientBalance(address account);

    error AddressEmptyCode(address target);

    error FailedInnerCall();

    function sendValue(address payable recipient, uint256 amount) internal {
        if (address(this).balance < amount) {
            revert AddressInsufficientBalance(address(this));
        }

        (bool success, ) = recipient.call{value: amount}("");
        if (!success) {
            revert FailedInnerCall();
        }
    }

    function functionCall(address target, bytes memory data) internal returns (bytes memory) {
        return functionCallWithValue(target, data, 0);
    }

    function functionCallWithValue(address target, bytes memory data, uint256 value) internal returns (bytes memory) {
        if (address(this).balance < value) {
            revert AddressInsufficientBalance(address(this));
        }
        (bool success, bytes memory returndata) = target.call{value: value}(data);
        return verifyCallResultFromTarget(target, success, returndata);
    }

    function functionStaticCall(address target, bytes memory data) internal view returns (bytes memory) {
        (bool success, bytes memory returndata) = target.staticcall(data);
        return verifyCallResultFromTarget(target, success, returndata);
    }

    function functionDelegateCall(address target, bytes memory data) internal returns (bytes memory) {
        (bool success, bytes memory returndata) = target.delegatecall(data);
        return verifyCallResultFromTarget(target, success, returndata);
    }

    function verifyCallResultFromTarget(
        address target,
        bool success,
        bytes memory returndata
    ) internal view returns (bytes memory) {
        if (!success) {
            _revert(returndata);
        } else {

            if (returndata.length == 0 && target.code.length == 0) {
                revert AddressEmptyCode(target);
            }
            return returndata;
        }
    }

    function verifyCallResult(bool success, bytes memory returndata) internal pure returns (bytes memory) {
        if (!success) {
            _revert(returndata);
        } else {
            return returndata;
        }
    }

    function _revert(bytes memory returndata) private pure {

        if (returndata.length > 0) {

            assembly {
                let returndata_size := mload(returndata)
                revert(add(32, returndata), returndata_size)
            }
        } else {
            revert FailedInnerCall();
        }
    }
}

pragma solidity ^0.8.20;

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

    function getAddressSlot(bytes32 slot) internal pure returns (AddressSlot storage r) {

        assembly {
            r.slot := slot
        }
    }

    function getBooleanSlot(bytes32 slot) internal pure returns (BooleanSlot storage r) {

        assembly {
            r.slot := slot
        }
    }

    function getBytes32Slot(bytes32 slot) internal pure returns (Bytes32Slot storage r) {

        assembly {
            r.slot := slot
        }
    }

    function getUint256Slot(bytes32 slot) internal pure returns (Uint256Slot storage r) {

        assembly {
            r.slot := slot
        }
    }

    function getStringSlot(bytes32 slot) internal pure returns (StringSlot storage r) {

        assembly {
            r.slot := slot
        }
    }

    function getStringSlot(string storage store) internal pure returns (StringSlot storage r) {

        assembly {
            r.slot := store.slot
        }
    }

    function getBytesSlot(bytes32 slot) internal pure returns (BytesSlot storage r) {

        assembly {
            r.slot := slot
        }
    }

    function getBytesSlot(bytes storage store) internal pure returns (BytesSlot storage r) {

        assembly {
            r.slot := store.slot
        }
    }
}

pragma solidity ^0.8.20;

library ERC1967Utils {

    event Upgraded(address indexed implementation);

    event AdminChanged(address previousAdmin, address newAdmin);

    event BeaconUpgraded(address indexed beacon);

    bytes32 internal constant IMPLEMENTATION_SLOT = 0x360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc;

    error ERC1967InvalidImplementation(address implementation);

    error ERC1967InvalidAdmin(address admin);

    error ERC1967InvalidBeacon(address beacon);

    error ERC1967NonPayable();

    function getImplementation() internal view returns (address) {
        return StorageSlot.getAddressSlot(IMPLEMENTATION_SLOT).value;
    }

    function _setImplementation(address newImplementation) private {
        if (newImplementation.code.length == 0) {
            revert ERC1967InvalidImplementation(newImplementation);
        }
        StorageSlot.getAddressSlot(IMPLEMENTATION_SLOT).value = newImplementation;
    }

    function upgradeToAndCall(address newImplementation, bytes memory data) internal {
        _setImplementation(newImplementation);
        emit Upgraded(newImplementation);

        if (data.length > 0) {
            Address.functionDelegateCall(newImplementation, data);
        } else {
            _checkNonPayable();
        }
    }

    bytes32 internal constant ADMIN_SLOT = 0xb53127684a568b3173ae13b9f8a6016e243e63b6e8ee1178d6a717850b5d6103;

    function getAdmin() internal view returns (address) {
        return StorageSlot.getAddressSlot(ADMIN_SLOT).value;
    }

    function _setAdmin(address newAdmin) private {
        if (newAdmin == address(0)) {
            revert ERC1967InvalidAdmin(address(0));
        }
        StorageSlot.getAddressSlot(ADMIN_SLOT).value = newAdmin;
    }

    function changeAdmin(address newAdmin) internal {
        emit AdminChanged(getAdmin(), newAdmin);
        _setAdmin(newAdmin);
    }

    bytes32 internal constant BEACON_SLOT = 0xa3f0ad74e5423aebfd80d3ef4346578335a9a72aeaee59ff6cb3582b35133d50;

    function getBeacon() internal view returns (address) {
        return StorageSlot.getAddressSlot(BEACON_SLOT).value;
    }

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

    function upgradeBeaconToAndCall(address newBeacon, bytes memory data) internal {
        _setBeacon(newBeacon);
        emit BeaconUpgraded(newBeacon);

        if (data.length > 0) {
            Address.functionDelegateCall(IBeacon(newBeacon).implementation(), data);
        } else {
            _checkNonPayable();
        }
    }

    function _checkNonPayable() private {
        if (msg.value > 0) {
            revert ERC1967NonPayable();
        }
    }
}

pragma solidity 0.8.20;

abstract contract DecimalImpl {
    function getImpl() external view returns (address) {
        return ERC1967Utils.getImplementation();
    }
}

pragma solidity ^0.8.20;

abstract contract Initializable {

    struct InitializableStorage {

        uint64 _initialized;

        bool _initializing;
    }

    bytes32 private constant INITIALIZABLE_STORAGE = 0xf0c57e16840df040f15088dc2f81fe391c3923bec73e23a9662efc9c229c6a00;

    error InvalidInitialization();

    error NotInitializing();

    event Initialized(uint64 version);

    modifier initializer() {

        InitializableStorage storage $ = _getInitializableStorage();

        bool isTopLevelCall = !$._initializing;
        uint64 initialized = $._initialized;

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

    modifier reinitializer(uint64 version) {

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

    modifier onlyInitializing() {
        _checkInitializing();
        _;
    }

    function _checkInitializing() internal view virtual {
        if (!_isInitializing()) {
            revert NotInitializing();
        }
    }

    function _disableInitializers() internal virtual {

        InitializableStorage storage $ = _getInitializableStorage();

        if ($._initializing) {
            revert InvalidInitialization();
        }
        if ($._initialized != type(uint64).max) {
            $._initialized = type(uint64).max;
            emit Initialized(type(uint64).max);
        }
    }

    function _getInitializedVersion() internal view returns (uint64) {
        return _getInitializableStorage()._initialized;
    }

    function _isInitializing() internal view returns (bool) {
        return _getInitializableStorage()._initializing;
    }

    function _getInitializableStorage() private pure returns (InitializableStorage storage $) {
        assembly {
            $.slot := INITIALIZABLE_STORAGE
        }
    }
}

pragma solidity ^0.8.20;

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

pragma solidity ^0.8.20;

abstract contract OwnableUpgradeable is Initializable, ContextUpgradeable {

    struct OwnableStorage {
        address _owner;
    }

    bytes32 private constant OwnableStorageLocation = 0x9016d09d72d40fdae2fd8ceac6b6234c7706214fd39c1cd1e609a0528c199300;

    function _getOwnableStorage() private pure returns (OwnableStorage storage $) {
        assembly {
            $.slot := OwnableStorageLocation
        }
    }

    error OwnableUnauthorizedAccount(address account);

    error OwnableInvalidOwner(address owner);

    event OwnershipTransferred(address indexed previousOwner, address indexed newOwner);

    function __Ownable_init(address initialOwner) internal onlyInitializing {
        __Ownable_init_unchained(initialOwner);
    }

    function __Ownable_init_unchained(address initialOwner) internal onlyInitializing {
        if (initialOwner == address(0)) {
            revert OwnableInvalidOwner(address(0));
        }
        _transferOwnership(initialOwner);
    }

    modifier onlyOwner() {
        _checkOwner();
        _;
    }

    function owner() public view virtual returns (address) {
        OwnableStorage storage $ = _getOwnableStorage();
        return $._owner;
    }

    function _checkOwner() internal view virtual {
        if (owner() != _msgSender()) {
            revert OwnableUnauthorizedAccount(_msgSender());
        }
    }

    function renounceOwnership() public virtual onlyOwner {
        _transferOwnership(address(0));
    }

    function transferOwnership(address newOwner) public virtual onlyOwner {
        if (newOwner == address(0)) {
            revert OwnableInvalidOwner(address(0));
        }
        _transferOwnership(newOwner);
    }

    function _transferOwnership(address newOwner) internal virtual {
        OwnableStorage storage $ = _getOwnableStorage();
        address oldOwner = $._owner;
        $._owner = newOwner;
        emit OwnershipTransferred(oldOwner, newOwner);
    }
}

pragma solidity ^0.8.20;

interface IERC20Errors {

    error ERC20InsufficientBalance(address sender, uint256 balance, uint256 needed);

    error ERC20InvalidSender(address sender);

    error ERC20InvalidReceiver(address receiver);

    error ERC20InsufficientAllowance(address spender, uint256 allowance, uint256 needed);

    error ERC20InvalidApprover(address approver);

    error ERC20InvalidSpender(address spender);
}

interface IERC721Errors {

    error ERC721InvalidOwner(address owner);

    error ERC721NonexistentToken(uint256 tokenId);

    error ERC721IncorrectOwner(address sender, uint256 tokenId, address owner);

    error ERC721InvalidSender(address sender);

    error ERC721InvalidReceiver(address receiver);

    error ERC721InsufficientApproval(address operator, uint256 tokenId);

    error ERC721InvalidApprover(address approver);

    error ERC721InvalidOperator(address operator);
}

interface IERC1155Errors {

    error ERC1155InsufficientBalance(address sender, uint256 balance, uint256 needed, uint256 tokenId);

    error ERC1155InvalidSender(address sender);

    error ERC1155InvalidReceiver(address receiver);

    error ERC1155MissingApprovalForAll(address operator, address owner);

    error ERC1155InvalidApprover(address approver);

    error ERC1155InvalidOperator(address operator);

    error ERC1155InvalidArrayLength(uint256 idsLength, uint256 valuesLength);
}

pragma solidity ^0.8.20;

interface IERC20 {

    event Transfer(address indexed from, address indexed to, uint256 value);

    event Approval(address indexed owner, address indexed spender, uint256 value);

    function totalSupply() external view returns (uint256);

    function balanceOf(address account) external view returns (uint256);

    function transfer(address to, uint256 value) external returns (bool);

    function allowance(address owner, address spender) external view returns (uint256);

    function approve(address spender, uint256 value) external returns (bool);

    function transferFrom(address from, address to, uint256 value) external returns (bool);
}

pragma solidity ^0.8.20;

interface IERC20Metadata is IERC20 {

    function name() external view returns (string memory);

    function symbol() external view returns (string memory);

    function decimals() external view returns (uint8);
}

pragma solidity ^0.8.20;

abstract contract ERC20Upgradeable is Initializable, ContextUpgradeable, IERC20, IERC20Metadata, IERC20Errors {

    struct ERC20Storage {
        mapping(address account => uint256) _balances;

        mapping(address account => mapping(address spender => uint256)) _allowances;

        uint256 _totalSupply;

        string _name;
        string _symbol;
    }

    bytes32 private constant ERC20StorageLocation = 0x52c63247e1f47db19d5ce0460030c497f067ca4cebf71ba98eeadabe20bace00;

    function _getERC20Storage() private pure returns (ERC20Storage storage $) {
        assembly {
            $.slot := ERC20StorageLocation
        }
    }

    function __ERC20_init(string memory name_, string memory symbol_) internal onlyInitializing {
        __ERC20_init_unchained(name_, symbol_);
    }

    function __ERC20_init_unchained(string memory name_, string memory symbol_) internal onlyInitializing {
        ERC20Storage storage $ = _getERC20Storage();
        $._name = name_;
        $._symbol = symbol_;
    }

    function name() public view virtual returns (string memory) {
        ERC20Storage storage $ = _getERC20Storage();
        return $._name;
    }

    function symbol() public view virtual returns (string memory) {
        ERC20Storage storage $ = _getERC20Storage();
        return $._symbol;
    }

    function decimals() public view virtual returns (uint8) {
        return 18;
    }

    function totalSupply() public view virtual returns (uint256) {
        ERC20Storage storage $ = _getERC20Storage();
        return $._totalSupply;
    }

    function balanceOf(address account) public view virtual returns (uint256) {
        ERC20Storage storage $ = _getERC20Storage();
        return $._balances[account];
    }

    function transfer(address to, uint256 value) public virtual returns (bool) {
        address owner = _msgSender();
        _transfer(owner, to, value);
        return true;
    }

    function allowance(address owner, address spender) public view virtual returns (uint256) {
        ERC20Storage storage $ = _getERC20Storage();
        return $._allowances[owner][spender];
    }

    function approve(address spender, uint256 value) public virtual returns (bool) {
        address owner = _msgSender();
        _approve(owner, spender, value);
        return true;
    }

    function transferFrom(address from, address to, uint256 value) public virtual returns (bool) {
        address spender = _msgSender();
        _spendAllowance(from, spender, value);
        _transfer(from, to, value);
        return true;
    }

    function _transfer(address from, address to, uint256 value) internal {
        if (from == address(0)) {
            revert ERC20InvalidSender(address(0));
        }
        if (to == address(0)) {
            revert ERC20InvalidReceiver(address(0));
        }
        _update(from, to, value);
    }

    function _update(address from, address to, uint256 value) internal virtual {
        ERC20Storage storage $ = _getERC20Storage();
        if (from == address(0)) {

            $._totalSupply += value;
        } else {
            uint256 fromBalance = $._balances[from];
            if (fromBalance < value) {
                revert ERC20InsufficientBalance(from, fromBalance, value);
            }
            unchecked {

                $._balances[from] = fromBalance - value;
            }
        }

        if (to == address(0)) {
            unchecked {

                $._totalSupply -= value;
            }
        } else {
            unchecked {

                $._balances[to] += value;
            }
        }

        emit Transfer(from, to, value);
    }

    function _mint(address account, uint256 value) internal {
        if (account == address(0)) {
            revert ERC20InvalidReceiver(address(0));
        }
        _update(address(0), account, value);
    }

    function _burn(address account, uint256 value) internal {
        if (account == address(0)) {
            revert ERC20InvalidSender(address(0));
        }
        _update(account, address(0), value);
    }

    function _approve(address owner, address spender, uint256 value) internal {
        _approve(owner, spender, value, true);
    }

    function _approve(address owner, address spender, uint256 value, bool emitEvent) internal virtual {
        ERC20Storage storage $ = _getERC20Storage();
        if (owner == address(0)) {
            revert ERC20InvalidApprover(address(0));
        }
        if (spender == address(0)) {
            revert ERC20InvalidSpender(address(0));
        }
        $._allowances[owner][spender] = value;
        if (emitEvent) {
            emit Approval(owner, spender, value);
        }
    }

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

pragma solidity ^0.8.20;

abstract contract ERC20BurnableUpgradeable is Initializable, ContextUpgradeable, ERC20Upgradeable {
    function __ERC20Burnable_init() internal onlyInitializing {
    }

    function __ERC20Burnable_init_unchained() internal onlyInitializing {
    }

    function burn(uint256 value) public virtual {
        _burn(_msgSender(), value);
    }

    function burnFrom(address account, uint256 value) public virtual {
        _spendAllowance(account, _msgSender(), value);
        _burn(account, value);
    }
}

pragma solidity ^0.8.20;

abstract contract ERC20CappedUpgradeable is Initializable, ERC20Upgradeable {

    struct ERC20CappedStorage {
        uint256 _cap;
    }

    bytes32 private constant ERC20CappedStorageLocation = 0x0f070392f17d5f958cc1ac31867dabecfc5c9758b4a419a200803226d7155d00;

    function _getERC20CappedStorage() private pure returns (ERC20CappedStorage storage $) {
        assembly {
            $.slot := ERC20CappedStorageLocation
        }
    }

    error ERC20ExceededCap(uint256 increasedSupply, uint256 cap);

    error ERC20InvalidCap(uint256 cap);

    function __ERC20Capped_init(uint256 cap_) internal onlyInitializing {
        __ERC20Capped_init_unchained(cap_);
    }

    function __ERC20Capped_init_unchained(uint256 cap_) internal onlyInitializing {
        ERC20CappedStorage storage $ = _getERC20CappedStorage();
        if (cap_ == 0) {
            revert ERC20InvalidCap(0);
        }
        $._cap = cap_;
    }

    function cap() public view virtual returns (uint256) {
        ERC20CappedStorage storage $ = _getERC20CappedStorage();
        return $._cap;
    }

    function _update(address from, address to, uint256 value) internal virtual override {
        super._update(from, to, value);

        if (from == address(0)) {
            uint256 maxSupply = cap();
            uint256 supply = totalSupply();
            if (supply > maxSupply) {
                revert ERC20ExceededCap(supply, maxSupply);
            }
        }
    }
}

pragma solidity ^0.8.20;

interface IERC5267 {

    event EIP712DomainChanged();

    function eip712Domain()
        external
        view
        returns (
            bytes1 fields,
            string memory name,
            string memory version,
            uint256 chainId,
            address verifyingContract,
            bytes32 salt,
            uint256[] memory extensions
        );
}

pragma solidity ^0.8.20;

library Math {

    error MathOverflowedMulDiv();

    enum Rounding {
        Floor,
        Ceil,
        Trunc,
        Expand
    }

    function tryAdd(uint256 a, uint256 b) internal pure returns (bool, uint256) {
        unchecked {
            uint256 c = a + b;
            if (c < a) return (false, 0);
            return (true, c);
        }
    }

    function trySub(uint256 a, uint256 b) internal pure returns (bool, uint256) {
        unchecked {
            if (b > a) return (false, 0);
            return (true, a - b);
        }
    }

    function tryMul(uint256 a, uint256 b) internal pure returns (bool, uint256) {
        unchecked {

            if (a == 0) return (true, 0);
            uint256 c = a * b;
            if (c / a != b) return (false, 0);
            return (true, c);
        }
    }

    function tryDiv(uint256 a, uint256 b) internal pure returns (bool, uint256) {
        unchecked {
            if (b == 0) return (false, 0);
            return (true, a / b);
        }
    }

    function tryMod(uint256 a, uint256 b) internal pure returns (bool, uint256) {
        unchecked {
            if (b == 0) return (false, 0);
            return (true, a % b);
        }
    }

    function max(uint256 a, uint256 b) internal pure returns (uint256) {
        return a > b ? a : b;
    }

    function min(uint256 a, uint256 b) internal pure returns (uint256) {
        return a < b ? a : b;
    }

    function average(uint256 a, uint256 b) internal pure returns (uint256) {

        return (a & b) + (a ^ b) / 2;
    }

    function ceilDiv(uint256 a, uint256 b) internal pure returns (uint256) {
        if (b == 0) {

            return a / b;
        }

        return a == 0 ? 0 : (a - 1) / b + 1;
    }

    function mulDiv(uint256 x, uint256 y, uint256 denominator) internal pure returns (uint256 result) {
        unchecked {

            uint256 prod0 = x * y;
            uint256 prod1;
            assembly {
                let mm := mulmod(x, y, not(0))
                prod1 := sub(sub(mm, prod0), lt(mm, prod0))
            }

            if (prod1 == 0) {

                return prod0 / denominator;
            }

            if (denominator <= prod1) {
                revert MathOverflowedMulDiv();
            }

            uint256 remainder;
            assembly {

                remainder := mulmod(x, y, denominator)

                prod1 := sub(prod1, gt(remainder, prod0))
                prod0 := sub(prod0, remainder)
            }

            uint256 twos = denominator & (0 - denominator);
            assembly {

                denominator := div(denominator, twos)

                prod0 := div(prod0, twos)

                twos := add(div(sub(0, twos), twos), 1)
            }

            prod0 |= prod1 * twos;

            uint256 inverse = (3 * denominator) ^ 2;

            inverse *= 2 - denominator * inverse;
            inverse *= 2 - denominator * inverse;
            inverse *= 2 - denominator * inverse;
            inverse *= 2 - denominator * inverse;
            inverse *= 2 - denominator * inverse;
            inverse *= 2 - denominator * inverse;

            result = prod0 * inverse;
            return result;
        }
    }

    function mulDiv(uint256 x, uint256 y, uint256 denominator, Rounding rounding) internal pure returns (uint256) {
        uint256 result = mulDiv(x, y, denominator);
        if (unsignedRoundsUp(rounding) && mulmod(x, y, denominator) > 0) {
            result += 1;
        }
        return result;
    }

    function sqrt(uint256 a) internal pure returns (uint256) {
        if (a == 0) {
            return 0;
        }

        uint256 result = 1 << (log2(a) >> 1);

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

    function sqrt(uint256 a, Rounding rounding) internal pure returns (uint256) {
        unchecked {
            uint256 result = sqrt(a);
            return result + (unsignedRoundsUp(rounding) && result * result < a ? 1 : 0);
        }
    }

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

    function log2(uint256 value, Rounding rounding) internal pure returns (uint256) {
        unchecked {
            uint256 result = log2(value);
            return result + (unsignedRoundsUp(rounding) && 1 << result < value ? 1 : 0);
        }
    }

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

    function log10(uint256 value, Rounding rounding) internal pure returns (uint256) {
        unchecked {
            uint256 result = log10(value);
            return result + (unsignedRoundsUp(rounding) && 10 ** result < value ? 1 : 0);
        }
    }

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

    function log256(uint256 value, Rounding rounding) internal pure returns (uint256) {
        unchecked {
            uint256 result = log256(value);
            return result + (unsignedRoundsUp(rounding) && 1 << (result << 3) < value ? 1 : 0);
        }
    }

    function unsignedRoundsUp(Rounding rounding) internal pure returns (bool) {
        return uint8(rounding) % 2 == 1;
    }
}

pragma solidity ^0.8.20;

library SignedMath {

    function max(int256 a, int256 b) internal pure returns (int256) {
        return a > b ? a : b;
    }

    function min(int256 a, int256 b) internal pure returns (int256) {
        return a < b ? a : b;
    }

    function average(int256 a, int256 b) internal pure returns (int256) {

        int256 x = (a & b) + ((a ^ b) >> 1);
        return x + (int256(uint256(x) >> 255) & (a ^ b));
    }

    function abs(int256 n) internal pure returns (uint256) {
        unchecked {

            return uint256(n >= 0 ? n : -n);
        }
    }
}

pragma solidity ^0.8.20;

library Strings {
    bytes16 private constant HEX_DIGITS = "0123456789abcdef";
    uint8 private constant ADDRESS_LENGTH = 20;

    error StringsInsufficientHexLength(uint256 value, uint256 length);

    function toString(uint256 value) internal pure returns (string memory) {
        unchecked {
            uint256 length = Math.log10(value) + 1;
            string memory buffer = new string(length);
            uint256 ptr;

            assembly {
                ptr := add(buffer, add(32, length))
            }
            while (true) {
                ptr--;

                assembly {
                    mstore8(ptr, byte(mod(value, 10), HEX_DIGITS))
                }
                value /= 10;
                if (value == 0) break;
            }
            return buffer;
        }
    }

    function toStringSigned(int256 value) internal pure returns (string memory) {
        return string.concat(value < 0 ? "-" : "", toString(SignedMath.abs(value)));
    }

    function toHexString(uint256 value) internal pure returns (string memory) {
        unchecked {
            return toHexString(value, Math.log256(value) + 1);
        }
    }

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

    function toHexString(address addr) internal pure returns (string memory) {
        return toHexString(uint256(uint160(addr)), ADDRESS_LENGTH);
    }

    function equal(string memory a, string memory b) internal pure returns (bool) {
        return bytes(a).length == bytes(b).length && keccak256(bytes(a)) == keccak256(bytes(b));
    }
}

pragma solidity ^0.8.20;

library MessageHashUtils {

    function toEthSignedMessageHash(bytes32 messageHash) internal pure returns (bytes32 digest) {

        assembly {
            mstore(0x00, "\x19Ethereum Signed Message:\n32")
            mstore(0x1c, messageHash)
            digest := keccak256(0x00, 0x3c)
        }
    }

    function toEthSignedMessageHash(bytes memory message) internal pure returns (bytes32) {
        return
            keccak256(bytes.concat("\x19Ethereum Signed Message:\n", bytes(Strings.toString(message.length)), message));
    }

    function toDataWithIntendedValidatorHash(address validator, bytes memory data) internal pure returns (bytes32) {
        return keccak256(abi.encodePacked(hex"19_00", validator, data));
    }

    function toTypedDataHash(bytes32 domainSeparator, bytes32 structHash) internal pure returns (bytes32 digest) {

        assembly {
            let ptr := mload(0x40)
            mstore(ptr, hex"19_01")
            mstore(add(ptr, 0x02), domainSeparator)
            mstore(add(ptr, 0x22), structHash)
            digest := keccak256(ptr, 0x42)
        }
    }
}

pragma solidity ^0.8.20;

abstract contract EIP712Upgradeable is Initializable, IERC5267 {
    bytes32 private constant TYPE_HASH =
        keccak256("EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)");

    struct EIP712Storage {

        bytes32 _hashedName;

        bytes32 _hashedVersion;

        string _name;
        string _version;
    }

    bytes32 private constant EIP712StorageLocation = 0xa16a46d94261c7517cc8ff89f61c0ce93598e3c849801011dee649a6a557d100;

    function _getEIP712Storage() private pure returns (EIP712Storage storage $) {
        assembly {
            $.slot := EIP712StorageLocation
        }
    }

    function __EIP712_init(string memory name, string memory version) internal onlyInitializing {
        __EIP712_init_unchained(name, version);
    }

    function __EIP712_init_unchained(string memory name, string memory version) internal onlyInitializing {
        EIP712Storage storage $ = _getEIP712Storage();
        $._name = name;
        $._version = version;

        $._hashedName = 0;
        $._hashedVersion = 0;
    }

    function _domainSeparatorV4() internal view returns (bytes32) {
        return _buildDomainSeparator();
    }

    function _buildDomainSeparator() private view returns (bytes32) {
        return keccak256(abi.encode(TYPE_HASH, _EIP712NameHash(), _EIP712VersionHash(), block.chainid, address(this)));
    }

    function _hashTypedDataV4(bytes32 structHash) internal view virtual returns (bytes32) {
        return MessageHashUtils.toTypedDataHash(_domainSeparatorV4(), structHash);
    }

    function eip712Domain()
        public
        view
        virtual
        returns (
            bytes1 fields,
            string memory name,
            string memory version,
            uint256 chainId,
            address verifyingContract,
            bytes32 salt,
            uint256[] memory extensions
        )
    {
        EIP712Storage storage $ = _getEIP712Storage();

        require($._hashedName == 0 && $._hashedVersion == 0, "EIP712: Uninitialized");

        return (
            hex"0f",
            _EIP712Name(),
            _EIP712Version(),
            block.chainid,
            address(this),
            bytes32(0),
            new uint256[](0)
        );
    }

    function _EIP712Name() internal view virtual returns (string memory) {
        EIP712Storage storage $ = _getEIP712Storage();
        return $._name;
    }

    function _EIP712Version() internal view virtual returns (string memory) {
        EIP712Storage storage $ = _getEIP712Storage();
        return $._version;
    }

    function _EIP712NameHash() internal view returns (bytes32) {
        EIP712Storage storage $ = _getEIP712Storage();
        string memory name = _EIP712Name();
        if (bytes(name).length > 0) {
            return keccak256(bytes(name));
        } else {

            bytes32 hashedName = $._hashedName;
            if (hashedName != 0) {
                return hashedName;
            } else {
                return keccak256("");
            }
        }
    }

    function _EIP712VersionHash() internal view returns (bytes32) {
        EIP712Storage storage $ = _getEIP712Storage();
        string memory version = _EIP712Version();
        if (bytes(version).length > 0) {
            return keccak256(bytes(version));
        } else {

            bytes32 hashedVersion = $._hashedVersion;
            if (hashedVersion != 0) {
                return hashedVersion;
            } else {
                return keccak256("");
            }
        }
    }
}

pragma solidity ^0.8.20;

abstract contract NoncesUpgradeable is Initializable {

    error InvalidAccountNonce(address account, uint256 currentNonce);

    struct NoncesStorage {
        mapping(address account => uint256) _nonces;
    }

    bytes32 private constant NoncesStorageLocation = 0x5ab42ced628888259c08ac98db1eb0cf702fc1501344311d8b100cd1bfe4bb00;

    function _getNoncesStorage() private pure returns (NoncesStorage storage $) {
        assembly {
            $.slot := NoncesStorageLocation
        }
    }

    function __Nonces_init() internal onlyInitializing {
    }

    function __Nonces_init_unchained() internal onlyInitializing {
    }

    function nonces(address owner) public view virtual returns (uint256) {
        NoncesStorage storage $ = _getNoncesStorage();
        return $._nonces[owner];
    }

    function _useNonce(address owner) internal virtual returns (uint256) {
        NoncesStorage storage $ = _getNoncesStorage();

        unchecked {

            return $._nonces[owner]++;
        }
    }

    function _useCheckedNonce(address owner, uint256 nonce) internal virtual {
        uint256 current = _useNonce(owner);
        if (nonce != current) {
            revert InvalidAccountNonce(owner, current);
        }
    }
}

pragma solidity ^0.8.20;

interface IERC20Permit {

    function permit(
        address owner,
        address spender,
        uint256 value,
        uint256 deadline,
        uint8 v,
        bytes32 r,
        bytes32 s
    ) external;

    function nonces(address owner) external view returns (uint256);

    function DOMAIN_SEPARATOR() external view returns (bytes32);
}

pragma solidity ^0.8.20;

library ECDSA {
    enum RecoverError {
        NoError,
        InvalidSignature,
        InvalidSignatureLength,
        InvalidSignatureS
    }

    error ECDSAInvalidSignature();

    error ECDSAInvalidSignatureLength(uint256 length);

    error ECDSAInvalidSignatureS(bytes32 s);

    function tryRecover(bytes32 hash, bytes memory signature) internal pure returns (address, RecoverError, bytes32) {
        if (signature.length == 65) {
            bytes32 r;
            bytes32 s;
            uint8 v;

            assembly {
                r := mload(add(signature, 0x20))
                s := mload(add(signature, 0x40))
                v := byte(0, mload(add(signature, 0x60)))
            }
            return tryRecover(hash, v, r, s);
        } else {
            return (address(0), RecoverError.InvalidSignatureLength, bytes32(signature.length));
        }
    }

    function recover(bytes32 hash, bytes memory signature) internal pure returns (address) {
        (address recovered, RecoverError error, bytes32 errorArg) = tryRecover(hash, signature);
        _throwError(error, errorArg);
        return recovered;
    }

    function tryRecover(bytes32 hash, bytes32 r, bytes32 vs) internal pure returns (address, RecoverError, bytes32) {
        unchecked {
            bytes32 s = vs & bytes32(0x7fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff);

            uint8 v = uint8((uint256(vs) >> 255) + 27);
            return tryRecover(hash, v, r, s);
        }
    }

    function recover(bytes32 hash, bytes32 r, bytes32 vs) internal pure returns (address) {
        (address recovered, RecoverError error, bytes32 errorArg) = tryRecover(hash, r, vs);
        _throwError(error, errorArg);
        return recovered;
    }

    function tryRecover(
        bytes32 hash,
        uint8 v,
        bytes32 r,
        bytes32 s
    ) internal pure returns (address, RecoverError, bytes32) {

        if (uint256(s) > 0x7FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF5D576E7357A4501DDFE92F46681B20A0) {
            return (address(0), RecoverError.InvalidSignatureS, s);
        }

        address signer = ecrecover(hash, v, r, s);
        if (signer == address(0)) {
            return (address(0), RecoverError.InvalidSignature, bytes32(0));
        }

        return (signer, RecoverError.NoError, bytes32(0));
    }

    function recover(bytes32 hash, uint8 v, bytes32 r, bytes32 s) internal pure returns (address) {
        (address recovered, RecoverError error, bytes32 errorArg) = tryRecover(hash, v, r, s);
        _throwError(error, errorArg);
        return recovered;
    }

    function _throwError(RecoverError error, bytes32 errorArg) private pure {
        if (error == RecoverError.NoError) {
            return;
        } else if (error == RecoverError.InvalidSignature) {
            revert ECDSAInvalidSignature();
        } else if (error == RecoverError.InvalidSignatureLength) {
            revert ECDSAInvalidSignatureLength(uint256(errorArg));
        } else if (error == RecoverError.InvalidSignatureS) {
            revert ECDSAInvalidSignatureS(errorArg);
        }
    }
}

pragma solidity ^0.8.20;

abstract contract ERC20PermitUpgradeable is Initializable, ERC20Upgradeable, IERC20Permit, EIP712Upgradeable, NoncesUpgradeable {
    bytes32 private constant PERMIT_TYPEHASH =
        keccak256("Permit(address owner,address spender,uint256 value,uint256 nonce,uint256 deadline)");

    error ERC2612ExpiredSignature(uint256 deadline);

    error ERC2612InvalidSigner(address signer, address owner);

    function __ERC20Permit_init(string memory name) internal onlyInitializing {
        __EIP712_init_unchained(name, "1");
    }

    function __ERC20Permit_init_unchained(string memory) internal onlyInitializing {}

    function permit(
        address owner,
        address spender,
        uint256 value,
        uint256 deadline,
        uint8 v,
        bytes32 r,
        bytes32 s
    ) public virtual {
        if (block.timestamp > deadline) {
            revert ERC2612ExpiredSignature(deadline);
        }

        bytes32 structHash = keccak256(abi.encode(PERMIT_TYPEHASH, owner, spender, value, _useNonce(owner), deadline));

        bytes32 hash = _hashTypedDataV4(structHash);

        address signer = ECDSA.recover(hash, v, r, s);
        if (signer != owner) {
            revert ERC2612InvalidSigner(signer, owner);
        }

        _approve(owner, spender, value);
    }

    function nonces(address owner) public view virtual override(IERC20Permit, NoncesUpgradeable) returns (uint256) {
        return super.nonces(owner);
    }

    function DOMAIN_SEPARATOR() external view virtual returns (bytes32) {
        return _domainSeparatorV4();
    }
}

pragma solidity ^0.8.20;

abstract contract PausableUpgradeable is Initializable, ContextUpgradeable {

    struct PausableStorage {
        bool _paused;
    }

    bytes32 private constant PausableStorageLocation = 0xcd5ed15c6e187e77e9aee88184c21f4f2182ab5827cb3b7e07fbedcd63f03300;

    function _getPausableStorage() private pure returns (PausableStorage storage $) {
        assembly {
            $.slot := PausableStorageLocation
        }
    }

    event Paused(address account);

    event Unpaused(address account);

    error EnforcedPause();

    error ExpectedPause();

    function __Pausable_init() internal onlyInitializing {
        __Pausable_init_unchained();
    }

    function __Pausable_init_unchained() internal onlyInitializing {
        PausableStorage storage $ = _getPausableStorage();
        $._paused = false;
    }

    modifier whenNotPaused() {
        _requireNotPaused();
        _;
    }

    modifier whenPaused() {
        _requirePaused();
        _;
    }

    function paused() public view virtual returns (bool) {
        PausableStorage storage $ = _getPausableStorage();
        return $._paused;
    }

    function _requireNotPaused() internal view virtual {
        if (paused()) {
            revert EnforcedPause();
        }
    }

    function _requirePaused() internal view virtual {
        if (!paused()) {
            revert ExpectedPause();
        }
    }

    function _pause() internal virtual whenNotPaused {
        PausableStorage storage $ = _getPausableStorage();
        $._paused = true;
        emit Paused(_msgSender());
    }

    function _unpause() internal virtual whenPaused {
        PausableStorage storage $ = _getPausableStorage();
        $._paused = false;
        emit Unpaused(_msgSender());
    }
}

pragma solidity ^0.8.20;

abstract contract Proxy {

    function _delegate(address implementation) internal virtual {
        assembly {

            calldatacopy(0, 0, calldatasize())

            let result := delegatecall(gas(), implementation, 0, calldatasize(), 0, 0)

            returndatacopy(0, 0, returndatasize())

            switch result

            case 0 {
                revert(0, returndatasize())
            }
            default {
                return(0, returndatasize())
            }
        }
    }

    function _implementation() internal view virtual returns (address);

    function _fallback() internal virtual {
        _delegate(_implementation());
    }

    fallback() external payable virtual {
        _fallback();
    }
}

pragma solidity ^0.8.20;

contract BeaconProxy is Proxy {

    address private immutable _beacon;

    constructor(address beacon, bytes memory data) payable {
        ERC1967Utils.upgradeBeaconToAndCall(beacon, data);
        _beacon = beacon;
    }

    function _implementation() internal view virtual override returns (address) {
        return IBeacon(_getBeacon()).implementation();
    }

    function _getBeacon() internal view virtual returns (address) {
        return _beacon;
    }
}

pragma solidity 0.8.20;

abstract contract DecimalTokenCenterCore {

    struct TokenCenterStorage {
        address _tokenImpl;
        address _tokenReservelessImpl;
        address _tokenReservelessBeacon;
        mapping(string => address) _tokens;
        address _contractCenter;
    }

    bytes32 private constant DECIMAL_TOKEN_CENTER_STORAGE_LOCATION =
        0xc505efeb1b151d8e7a9297d5f8aae79af810c94de3fc8fc2e27ad1b30c968200;

    function _getTokenCenterStorage()
        internal
        pure
        returns (TokenCenterStorage storage $)
    {
        assembly {
            $.slot := DECIMAL_TOKEN_CENTER_STORAGE_LOCATION
        }
    }
}

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

    event MaxTotalSupplyUpdated(uint256 newMaxTotalSupply);
    event MinTotalSupplyUpdated(uint256 newMinTotalSupply);

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

    function updateIdentity(string memory newIdentity) external;

    function updateMaxTotalSupply(uint256 newMaxTotalSupply) external;

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

pragma solidity >=0.8.19;

error PRBMath_MulDiv_Overflow(uint256 x, uint256 y, uint256 denominator);

error PRBMath_MulDiv18_Overflow(uint256 x, uint256 y);

error PRBMath_MulDivSigned_InputTooSmall();

error PRBMath_MulDivSigned_Overflow(int256 x, int256 y);

uint128 constant MAX_UINT128 = type(uint128).max;

uint40 constant MAX_UINT40 = type(uint40).max;

uint256 constant UNIT_ = 1e18;

uint256 constant UNIT_INVERSE = 78156646155174841979727994598816262306175212592076161876661_508869554232690281;

uint256 constant UNIT_LPOTD = 262144;

function exp2_(uint256 x) pure returns (uint256 result) {
    unchecked {

        result = 0x800000000000000000000000000000000000000000000000;

        if (x & 0xFF00000000000000 > 0) {
            if (x & 0x8000000000000000 > 0) {
                result = (result * 0x16A09E667F3BCC909) >> 64;
            }
            if (x & 0x4000000000000000 > 0) {
                result = (result * 0x1306FE0A31B7152DF) >> 64;
            }
            if (x & 0x2000000000000000 > 0) {
                result = (result * 0x1172B83C7D517ADCE) >> 64;
            }
            if (x & 0x1000000000000000 > 0) {
                result = (result * 0x10B5586CF9890F62A) >> 64;
            }
            if (x & 0x800000000000000 > 0) {
                result = (result * 0x1059B0D31585743AE) >> 64;
            }
            if (x & 0x400000000000000 > 0) {
                result = (result * 0x102C9A3E778060EE7) >> 64;
            }
            if (x & 0x200000000000000 > 0) {
                result = (result * 0x10163DA9FB33356D8) >> 64;
            }
            if (x & 0x100000000000000 > 0) {
                result = (result * 0x100B1AFA5ABCBED61) >> 64;
            }
        }

        if (x & 0xFF000000000000 > 0) {
            if (x & 0x80000000000000 > 0) {
                result = (result * 0x10058C86DA1C09EA2) >> 64;
            }
            if (x & 0x40000000000000 > 0) {
                result = (result * 0x1002C605E2E8CEC50) >> 64;
            }
            if (x & 0x20000000000000 > 0) {
                result = (result * 0x100162F3904051FA1) >> 64;
            }
            if (x & 0x10000000000000 > 0) {
                result = (result * 0x1000B175EFFDC76BA) >> 64;
            }
            if (x & 0x8000000000000 > 0) {
                result = (result * 0x100058BA01FB9F96D) >> 64;
            }
            if (x & 0x4000000000000 > 0) {
                result = (result * 0x10002C5CC37DA9492) >> 64;
            }
            if (x & 0x2000000000000 > 0) {
                result = (result * 0x1000162E525EE0547) >> 64;
            }
            if (x & 0x1000000000000 > 0) {
                result = (result * 0x10000B17255775C04) >> 64;
            }
        }

        if (x & 0xFF0000000000 > 0) {
            if (x & 0x800000000000 > 0) {
                result = (result * 0x1000058B91B5BC9AE) >> 64;
            }
            if (x & 0x400000000000 > 0) {
                result = (result * 0x100002C5C89D5EC6D) >> 64;
            }
            if (x & 0x200000000000 > 0) {
                result = (result * 0x10000162E43F4F831) >> 64;
            }
            if (x & 0x100000000000 > 0) {
                result = (result * 0x100000B1721BCFC9A) >> 64;
            }
            if (x & 0x80000000000 > 0) {
                result = (result * 0x10000058B90CF1E6E) >> 64;
            }
            if (x & 0x40000000000 > 0) {
                result = (result * 0x1000002C5C863B73F) >> 64;
            }
            if (x & 0x20000000000 > 0) {
                result = (result * 0x100000162E430E5A2) >> 64;
            }
            if (x & 0x10000000000 > 0) {
                result = (result * 0x1000000B172183551) >> 64;
            }
        }

        if (x & 0xFF00000000 > 0) {
            if (x & 0x8000000000 > 0) {
                result = (result * 0x100000058B90C0B49) >> 64;
            }
            if (x & 0x4000000000 > 0) {
                result = (result * 0x10000002C5C8601CC) >> 64;
            }
            if (x & 0x2000000000 > 0) {
                result = (result * 0x1000000162E42FFF0) >> 64;
            }
            if (x & 0x1000000000 > 0) {
                result = (result * 0x10000000B17217FBB) >> 64;
            }
            if (x & 0x800000000 > 0) {
                result = (result * 0x1000000058B90BFCE) >> 64;
            }
            if (x & 0x400000000 > 0) {
                result = (result * 0x100000002C5C85FE3) >> 64;
            }
            if (x & 0x200000000 > 0) {
                result = (result * 0x10000000162E42FF1) >> 64;
            }
            if (x & 0x100000000 > 0) {
                result = (result * 0x100000000B17217F8) >> 64;
            }
        }

        if (x & 0xFF000000 > 0) {
            if (x & 0x80000000 > 0) {
                result = (result * 0x10000000058B90BFC) >> 64;
            }
            if (x & 0x40000000 > 0) {
                result = (result * 0x1000000002C5C85FE) >> 64;
            }
            if (x & 0x20000000 > 0) {
                result = (result * 0x100000000162E42FF) >> 64;
            }
            if (x & 0x10000000 > 0) {
                result = (result * 0x1000000000B17217F) >> 64;
            }
            if (x & 0x8000000 > 0) {
                result = (result * 0x100000000058B90C0) >> 64;
            }
            if (x & 0x4000000 > 0) {
                result = (result * 0x10000000002C5C860) >> 64;
            }
            if (x & 0x2000000 > 0) {
                result = (result * 0x1000000000162E430) >> 64;
            }
            if (x & 0x1000000 > 0) {
                result = (result * 0x10000000000B17218) >> 64;
            }
        }

        if (x & 0xFF0000 > 0) {
            if (x & 0x800000 > 0) {
                result = (result * 0x1000000000058B90C) >> 64;
            }
            if (x & 0x400000 > 0) {
                result = (result * 0x100000000002C5C86) >> 64;
            }
            if (x & 0x200000 > 0) {
                result = (result * 0x10000000000162E43) >> 64;
            }
            if (x & 0x100000 > 0) {
                result = (result * 0x100000000000B1721) >> 64;
            }
            if (x & 0x80000 > 0) {
                result = (result * 0x10000000000058B91) >> 64;
            }
            if (x & 0x40000 > 0) {
                result = (result * 0x1000000000002C5C8) >> 64;
            }
            if (x & 0x20000 > 0) {
                result = (result * 0x100000000000162E4) >> 64;
            }
            if (x & 0x10000 > 0) {
                result = (result * 0x1000000000000B172) >> 64;
            }
        }

        if (x & 0xFF00 > 0) {
            if (x & 0x8000 > 0) {
                result = (result * 0x100000000000058B9) >> 64;
            }
            if (x & 0x4000 > 0) {
                result = (result * 0x10000000000002C5D) >> 64;
            }
            if (x & 0x2000 > 0) {
                result = (result * 0x1000000000000162E) >> 64;
            }
            if (x & 0x1000 > 0) {
                result = (result * 0x10000000000000B17) >> 64;
            }
            if (x & 0x800 > 0) {
                result = (result * 0x1000000000000058C) >> 64;
            }
            if (x & 0x400 > 0) {
                result = (result * 0x100000000000002C6) >> 64;
            }
            if (x & 0x200 > 0) {
                result = (result * 0x10000000000000163) >> 64;
            }
            if (x & 0x100 > 0) {
                result = (result * 0x100000000000000B1) >> 64;
            }
        }

        if (x & 0xFF > 0) {
            if (x & 0x80 > 0) {
                result = (result * 0x10000000000000059) >> 64;
            }
            if (x & 0x40 > 0) {
                result = (result * 0x1000000000000002C) >> 64;
            }
            if (x & 0x20 > 0) {
                result = (result * 0x10000000000000016) >> 64;
            }
            if (x & 0x10 > 0) {
                result = (result * 0x1000000000000000B) >> 64;
            }
            if (x & 0x8 > 0) {
                result = (result * 0x10000000000000006) >> 64;
            }
            if (x & 0x4 > 0) {
                result = (result * 0x10000000000000003) >> 64;
            }
            if (x & 0x2 > 0) {
                result = (result * 0x10000000000000001) >> 64;
            }
            if (x & 0x1 > 0) {
                result = (result * 0x10000000000000001) >> 64;
            }
        }

        result *= UNIT_;
        result >>= (191 - (x >> 64));
    }
}

function msb(uint256 x) pure returns (uint256 result) {

    assembly ("memory-safe") {
        let factor := shl(7, gt(x, 0xFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF))
        x := shr(factor, x)
        result := or(result, factor)
    }

    assembly ("memory-safe") {
        let factor := shl(6, gt(x, 0xFFFFFFFFFFFFFFFF))
        x := shr(factor, x)
        result := or(result, factor)
    }

    assembly ("memory-safe") {
        let factor := shl(5, gt(x, 0xFFFFFFFF))
        x := shr(factor, x)
        result := or(result, factor)
    }

    assembly ("memory-safe") {
        let factor := shl(4, gt(x, 0xFFFF))
        x := shr(factor, x)
        result := or(result, factor)
    }

    assembly ("memory-safe") {
        let factor := shl(3, gt(x, 0xFF))
        x := shr(factor, x)
        result := or(result, factor)
    }

    assembly ("memory-safe") {
        let factor := shl(2, gt(x, 0xF))
        x := shr(factor, x)
        result := or(result, factor)
    }

    assembly ("memory-safe") {
        let factor := shl(1, gt(x, 0x3))
        x := shr(factor, x)
        result := or(result, factor)
    }

    assembly ("memory-safe") {
        let factor := gt(x, 0x1)
        result := or(result, factor)
    }
}

function mulDiv(
    uint256 x,
    uint256 y,
    uint256 denominator
) pure returns (uint256 result) {

    uint256 prod0;
    uint256 prod1;
    assembly ("memory-safe") {
        let mm := mulmod(x, y, not(0))
        prod0 := mul(x, y)
        prod1 := sub(sub(mm, prod0), lt(mm, prod0))
    }

    if (prod1 == 0) {
        unchecked {
            return prod0 / denominator;
        }
    }

    if (prod1 >= denominator) {
        revert PRBMath_MulDiv_Overflow(x, y, denominator);
    }

    uint256 remainder;
    assembly ("memory-safe") {

        remainder := mulmod(x, y, denominator)

        prod1 := sub(prod1, gt(remainder, prod0))
        prod0 := sub(prod0, remainder)
    }

    unchecked {

        uint256 lpotdod = denominator & (~denominator + 1);
        uint256 flippedLpotdod;

        assembly ("memory-safe") {

            denominator := div(denominator, lpotdod)

            prod0 := div(prod0, lpotdod)

            flippedLpotdod := add(div(sub(0, lpotdod), lpotdod), 1)
        }

        prod0 |= prod1 * flippedLpotdod;

        uint256 inverse = (3 * denominator) ^ 2;

        inverse *= 2 - denominator * inverse;
        inverse *= 2 - denominator * inverse;
        inverse *= 2 - denominator * inverse;
        inverse *= 2 - denominator * inverse;
        inverse *= 2 - denominator * inverse;
        inverse *= 2 - denominator * inverse;

        result = prod0 * inverse;
    }
}

function mulDiv18(uint256 x, uint256 y) pure returns (uint256 result) {
    uint256 prod0;
    uint256 prod1;
    assembly ("memory-safe") {
        let mm := mulmod(x, y, not(0))
        prod0 := mul(x, y)
        prod1 := sub(sub(mm, prod0), lt(mm, prod0))
    }

    if (prod1 == 0) {
        unchecked {
            return prod0 / UNIT_;
        }
    }

    if (prod1 >= UNIT_) {
        revert PRBMath_MulDiv18_Overflow(x, y);
    }

    uint256 remainder;
    assembly ("memory-safe") {
        remainder := mulmod(x, y, UNIT_)
        result := mul(
            or(
                div(sub(prod0, remainder), UNIT_LPOTD),
                mul(
                    sub(prod1, gt(remainder, prod0)),
                    add(div(sub(0, UNIT_LPOTD), UNIT_LPOTD), 1)
                )
            ),
            UNIT_INVERSE
        )
    }
}

function mulDivSigned(
    int256 x,
    int256 y,
    int256 denominator
) pure returns (int256 result) {
    if (
        x == type(int256).min ||
        y == type(int256).min ||
        denominator == type(int256).min
    ) {
        revert PRBMath_MulDivSigned_InputTooSmall();
    }

    uint256 xAbs;
    uint256 yAbs;
    uint256 dAbs;
    unchecked {
        xAbs = x < 0 ? uint256(-x) : uint256(x);
        yAbs = y < 0 ? uint256(-y) : uint256(y);
        dAbs = denominator < 0 ? uint256(-denominator) : uint256(denominator);
    }

    uint256 resultAbs = mulDiv(xAbs, yAbs, dAbs);
    if (resultAbs > uint256(type(int256).max)) {
        revert PRBMath_MulDivSigned_Overflow(x, y);
    }

    uint256 sx;
    uint256 sy;
    uint256 sd;
    assembly ("memory-safe") {

        sx := sgt(x, sub(0, 1))
        sy := sgt(y, sub(0, 1))
        sd := sgt(denominator, sub(0, 1))
    }

    unchecked {
        result = sx ^ sy ^ sd == 0 ? -int256(resultAbs) : int256(resultAbs);
    }
}

function sqrt_(uint256 x) pure returns (uint256 result) {
    if (x == 0) {
        return 0;
    }

    uint256 xAux = uint256(x);
    result = 1;
    if (xAux >= 2 ** 128) {
        xAux >>= 128;
        result <<= 64;
    }
    if (xAux >= 2 ** 64) {
        xAux >>= 64;
        result <<= 32;
    }
    if (xAux >= 2 ** 32) {
        xAux >>= 32;
        result <<= 16;
    }
    if (xAux >= 2 ** 16) {
        xAux >>= 16;
        result <<= 8;
    }
    if (xAux >= 2 ** 8) {
        xAux >>= 8;
        result <<= 4;
    }
    if (xAux >= 2 ** 4) {
        xAux >>= 4;
        result <<= 2;
    }
    if (xAux >= 2 ** 2) {
        result <<= 1;
    }

    unchecked {
        result = (result + x / result) >> 1;
        result = (result + x / result) >> 1;
        result = (result + x / result) >> 1;
        result = (result + x / result) >> 1;
        result = (result + x / result) >> 1;
        result = (result + x / result) >> 1;
        result = (result + x / result) >> 1;

        uint256 roundedResult = x / result;
        if (result >= roundedResult) {
            result = roundedResult;
        }
    }
}

pragma solidity >=0.8.19;

type UD60x18 is uint256;

using {

    intoUint128,
    intoUint256,
    intoUint40,
    unwrap
} for UD60x18 global;

using {
    avg,
    ceil,
    div,
    exp,
    exp2,
    floor,
    frac,
    gm,
    inv,
    ln,
    log10,
    log2,
    mul,
    pow,
    powu,
    sqrt
} for UD60x18 global;

using {
    add,
    and,
    eq,
    gt,
    gte,
    isZero,
    lshift,
    lt,
    lte,
    mod,
    neq,
    not,
    or,
    rshift,
    sub,
    uncheckedAdd,
    uncheckedSub,
    xor
} for UD60x18 global;

using {
    add as +,
    and2 as &,
    div as /,
    eq as ==,
    gt as >,
    gte as >=,
    lt as <,
    lte as <=,
    or as |,
    mod as %,
    mul as *,
    neq as !=,
    not as ~,
    sub as -,
    xor as ^
} for UD60x18 global;

function intoUint256(UD60x18 x) pure returns (uint256 result) {
    result = UD60x18.unwrap(x);
}

function intoUint128(UD60x18 x) pure returns (uint128 result) {
    uint256 xUint = UD60x18.unwrap(x);
    if (xUint > MAX_UINT128) {
        revert PRBMath_UD60x18_IntoUint128_Overflow(x);
    }
    result = uint128(xUint);
}

function intoUint40(UD60x18 x) pure returns (uint40 result) {
    uint256 xUint = UD60x18.unwrap(x);
    if (xUint > MAX_UINT40) {
        revert PRBMath_UD60x18_IntoUint40_Overflow(x);
    }
    result = uint40(xUint);
}

function ud(uint256 x) pure returns (UD60x18 result) {
    result = UD60x18.wrap(x);
}

function ud60x18(uint256 x) pure returns (UD60x18 result) {
    result = UD60x18.wrap(x);
}

function unwrap(UD60x18 x) pure returns (uint256 result) {
    result = UD60x18.unwrap(x);
}

function wrap(uint256 x) pure returns (UD60x18 result) {
    result = UD60x18.wrap(x);
}

function add(UD60x18 x, UD60x18 y) pure returns (UD60x18 result) {
    result = wrap(x.unwrap() + y.unwrap());
}

function and(UD60x18 x, uint256 bits) pure returns (UD60x18 result) {
    result = wrap(x.unwrap() & bits);
}

function and2(UD60x18 x, UD60x18 y) pure returns (UD60x18 result) {
    result = wrap(x.unwrap() & y.unwrap());
}

function eq(UD60x18 x, UD60x18 y) pure returns (bool result) {
    result = x.unwrap() == y.unwrap();
}

function gt(UD60x18 x, UD60x18 y) pure returns (bool result) {
    result = x.unwrap() > y.unwrap();
}

function gte(UD60x18 x, UD60x18 y) pure returns (bool result) {
    result = x.unwrap() >= y.unwrap();
}

function isZero(UD60x18 x) pure returns (bool result) {

    result = x.unwrap() == 0;
}

function lshift(UD60x18 x, uint256 bits) pure returns (UD60x18 result) {
    result = wrap(x.unwrap() << bits);
}

function lt(UD60x18 x, UD60x18 y) pure returns (bool result) {
    result = x.unwrap() < y.unwrap();
}

function lte(UD60x18 x, UD60x18 y) pure returns (bool result) {
    result = x.unwrap() <= y.unwrap();
}

function mod(UD60x18 x, UD60x18 y) pure returns (UD60x18 result) {
    result = wrap(x.unwrap() % y.unwrap());
}

function neq(UD60x18 x, UD60x18 y) pure returns (bool result) {
    result = x.unwrap() != y.unwrap();
}

function not(UD60x18 x) pure returns (UD60x18 result) {
    result = wrap(~x.unwrap());
}

function or(UD60x18 x, UD60x18 y) pure returns (UD60x18 result) {
    result = wrap(x.unwrap() | y.unwrap());
}

function rshift(UD60x18 x, uint256 bits) pure returns (UD60x18 result) {
    result = wrap(x.unwrap() >> bits);
}

function sub(UD60x18 x, UD60x18 y) pure returns (UD60x18 result) {
    result = wrap(x.unwrap() - y.unwrap());
}

function uncheckedAdd(UD60x18 x, UD60x18 y) pure returns (UD60x18 result) {
    unchecked {
        result = wrap(x.unwrap() + y.unwrap());
    }
}

function uncheckedSub(UD60x18 x, UD60x18 y) pure returns (UD60x18 result) {
    unchecked {
        result = wrap(x.unwrap() - y.unwrap());
    }
}

function xor(UD60x18 x, UD60x18 y) pure returns (UD60x18 result) {
    result = wrap(x.unwrap() ^ y.unwrap());
}

UD60x18 constant E = UD60x18.wrap(2_718281828459045235);

uint256 constant uEXP_MAX_INPUT = 133_084258667509499440;
UD60x18 constant EXP_MAX_INPUT = UD60x18.wrap(uEXP_MAX_INPUT);

uint256 constant uEXP2_MAX_INPUT = 192e18 - 1;
UD60x18 constant EXP2_MAX_INPUT = UD60x18.wrap(uEXP2_MAX_INPUT);

uint256 constant uHALF_UNIT = 0.5e18;
UD60x18 constant HALF_UNIT = UD60x18.wrap(uHALF_UNIT);

uint256 constant uLOG2_10 = 3_321928094887362347;
UD60x18 constant LOG2_10 = UD60x18.wrap(uLOG2_10);

uint256 constant uLOG2_E = 1_442695040888963407;
UD60x18 constant LOG2_E = UD60x18.wrap(uLOG2_E);

uint256 constant uMAX_UD60x18 = 115792089237316195423570985008687907853269984665640564039457_584007913129639935;
UD60x18 constant MAX_UD60x18 = UD60x18.wrap(uMAX_UD60x18);

uint256 constant uMAX_WHOLE_UD60x18 = 115792089237316195423570985008687907853269984665640564039457_000000000000000000;
UD60x18 constant MAX_WHOLE_UD60x18 = UD60x18.wrap(uMAX_WHOLE_UD60x18);

UD60x18 constant PI = UD60x18.wrap(3_141592653589793238);

uint256 constant uUNIT = 1e18;
UD60x18 constant UNIT = UD60x18.wrap(uUNIT);

uint256 constant uUNIT_SQUARED = 1e36;
UD60x18 constant UNIT_SQUARED = UD60x18.wrap(uUNIT_SQUARED);

UD60x18 constant ZERO = UD60x18.wrap(0);

function avg(UD60x18 x, UD60x18 y) pure returns (UD60x18 result) {
    uint256 xUint = x.unwrap();
    uint256 yUint = y.unwrap();
    unchecked {
        result = wrap((xUint & yUint) + ((xUint ^ yUint) >> 1));
    }
}

function ceil(UD60x18 x) pure returns (UD60x18 result) {
    uint256 xUint = x.unwrap();
    if (xUint > uMAX_WHOLE_UD60x18) {
        revert PRBMath_UD60x18_Ceil_Overflow(x);
    }

    assembly ("memory-safe") {

        let remainder := mod(x, uUNIT)

        let delta := sub(uUNIT, remainder)

        result := add(x, mul(delta, gt(remainder, 0)))
    }
}

function div(UD60x18 x, UD60x18 y) pure returns (UD60x18 result) {
    result = wrap(mulDiv(x.unwrap(), uUNIT, y.unwrap()));
}

function exp(UD60x18 x) pure returns (UD60x18 result) {
    uint256 xUint = x.unwrap();

    if (xUint > uEXP_MAX_INPUT) {
        revert PRBMath_UD60x18_Exp_InputTooBig(x);
    }

    unchecked {

        uint256 doubleUnitProduct = xUint * uLOG2_E;
        result = exp2(wrap(doubleUnitProduct / uUNIT));
    }
}

function exp2(UD60x18 x) pure returns (UD60x18 result) {
    uint256 xUint = x.unwrap();

    if (xUint > uEXP2_MAX_INPUT) {
        revert PRBMath_UD60x18_Exp2_InputTooBig(x);
    }

    uint256 x_192x64 = (xUint << 64) / uUNIT;

    result = wrap(exp2_(x_192x64));
}

function floor(UD60x18 x) pure returns (UD60x18 result) {
    assembly ("memory-safe") {

        let remainder := mod(x, uUNIT)

        result := sub(x, mul(remainder, gt(remainder, 0)))
    }
}

function frac(UD60x18 x) pure returns (UD60x18 result) {
    assembly ("memory-safe") {
        result := mod(x, uUNIT)
    }
}

function gm(UD60x18 x, UD60x18 y) pure returns (UD60x18 result) {
    uint256 xUint = x.unwrap();
    uint256 yUint = y.unwrap();
    if (xUint == 0 || yUint == 0) {
        return ZERO;
    }

    unchecked {

        uint256 xyUint = xUint * yUint;
        if (xyUint / xUint != yUint) {
            revert PRBMath_UD60x18_Gm_Overflow(x, y);
        }

        result = wrap(sqrt_(xyUint));
    }
}

function inv(UD60x18 x) pure returns (UD60x18 result) {
    unchecked {
        result = wrap(uUNIT_SQUARED / x.unwrap());
    }
}

function ln(UD60x18 x) pure returns (UD60x18 result) {
    unchecked {

        result = wrap(log2(x).unwrap() * uUNIT / uLOG2_E);
    }
}

function log10(UD60x18 x) pure returns (UD60x18 result) {
    uint256 xUint = x.unwrap();
    if (xUint < uUNIT) {
        revert PRBMath_UD60x18_Log_InputTooSmall(x);
    }

    assembly ("memory-safe") {
        switch x
        case 1 { result := mul(uUNIT, sub(0, 18)) }
        case 10 { result := mul(uUNIT, sub(1, 18)) }
        case 100 { result := mul(uUNIT, sub(2, 18)) }
        case 1000 { result := mul(uUNIT, sub(3, 18)) }
        case 10000 { result := mul(uUNIT, sub(4, 18)) }
        case 100000 { result := mul(uUNIT, sub(5, 18)) }
        case 1000000 { result := mul(uUNIT, sub(6, 18)) }
        case 10000000 { result := mul(uUNIT, sub(7, 18)) }
        case 100000000 { result := mul(uUNIT, sub(8, 18)) }
        case 1000000000 { result := mul(uUNIT, sub(9, 18)) }
        case 10000000000 { result := mul(uUNIT, sub(10, 18)) }
        case 100000000000 { result := mul(uUNIT, sub(11, 18)) }
        case 1000000000000 { result := mul(uUNIT, sub(12, 18)) }
        case 10000000000000 { result := mul(uUNIT, sub(13, 18)) }
        case 100000000000000 { result := mul(uUNIT, sub(14, 18)) }
        case 1000000000000000 { result := mul(uUNIT, sub(15, 18)) }
        case 10000000000000000 { result := mul(uUNIT, sub(16, 18)) }
        case 100000000000000000 { result := mul(uUNIT, sub(17, 18)) }
        case 1000000000000000000 { result := 0 }
        case 10000000000000000000 { result := uUNIT }
        case 100000000000000000000 { result := mul(uUNIT, 2) }
        case 1000000000000000000000 { result := mul(uUNIT, 3) }
        case 10000000000000000000000 { result := mul(uUNIT, 4) }
        case 100000000000000000000000 { result := mul(uUNIT, 5) }
        case 1000000000000000000000000 { result := mul(uUNIT, 6) }
        case 10000000000000000000000000 { result := mul(uUNIT, 7) }
        case 100000000000000000000000000 { result := mul(uUNIT, 8) }
        case 1000000000000000000000000000 { result := mul(uUNIT, 9) }
        case 10000000000000000000000000000 { result := mul(uUNIT, 10) }
        case 100000000000000000000000000000 { result := mul(uUNIT, 11) }
        case 1000000000000000000000000000000 { result := mul(uUNIT, 12) }
        case 10000000000000000000000000000000 { result := mul(uUNIT, 13) }
        case 100000000000000000000000000000000 { result := mul(uUNIT, 14) }
        case 1000000000000000000000000000000000 { result := mul(uUNIT, 15) }
        case 10000000000000000000000000000000000 { result := mul(uUNIT, 16) }
        case 100000000000000000000000000000000000 { result := mul(uUNIT, 17) }
        case 1000000000000000000000000000000000000 { result := mul(uUNIT, 18) }
        case 10000000000000000000000000000000000000 { result := mul(uUNIT, 19) }
        case 100000000000000000000000000000000000000 { result := mul(uUNIT, 20) }
        case 1000000000000000000000000000000000000000 { result := mul(uUNIT, 21) }
        case 10000000000000000000000000000000000000000 { result := mul(uUNIT, 22) }
        case 100000000000000000000000000000000000000000 { result := mul(uUNIT, 23) }
        case 1000000000000000000000000000000000000000000 { result := mul(uUNIT, 24) }
        case 10000000000000000000000000000000000000000000 { result := mul(uUNIT, 25) }
        case 100000000000000000000000000000000000000000000 { result := mul(uUNIT, 26) }
        case 1000000000000000000000000000000000000000000000 { result := mul(uUNIT, 27) }
        case 10000000000000000000000000000000000000000000000 { result := mul(uUNIT, 28) }
        case 100000000000000000000000000000000000000000000000 { result := mul(uUNIT, 29) }
        case 1000000000000000000000000000000000000000000000000 { result := mul(uUNIT, 30) }
        case 10000000000000000000000000000000000000000000000000 { result := mul(uUNIT, 31) }
        case 100000000000000000000000000000000000000000000000000 { result := mul(uUNIT, 32) }
        case 1000000000000000000000000000000000000000000000000000 { result := mul(uUNIT, 33) }
        case 10000000000000000000000000000000000000000000000000000 { result := mul(uUNIT, 34) }
        case 100000000000000000000000000000000000000000000000000000 { result := mul(uUNIT, 35) }
        case 1000000000000000000000000000000000000000000000000000000 { result := mul(uUNIT, 36) }
        case 10000000000000000000000000000000000000000000000000000000 { result := mul(uUNIT, 37) }
        case 100000000000000000000000000000000000000000000000000000000 { result := mul(uUNIT, 38) }
        case 1000000000000000000000000000000000000000000000000000000000 { result := mul(uUNIT, 39) }
        case 10000000000000000000000000000000000000000000000000000000000 { result := mul(uUNIT, 40) }
        case 100000000000000000000000000000000000000000000000000000000000 { result := mul(uUNIT, 41) }
        case 1000000000000000000000000000000000000000000000000000000000000 { result := mul(uUNIT, 42) }
        case 10000000000000000000000000000000000000000000000000000000000000 { result := mul(uUNIT, 43) }
        case 100000000000000000000000000000000000000000000000000000000000000 { result := mul(uUNIT, 44) }
        case 1000000000000000000000000000000000000000000000000000000000000000 { result := mul(uUNIT, 45) }
        case 10000000000000000000000000000000000000000000000000000000000000000 { result := mul(uUNIT, 46) }
        case 100000000000000000000000000000000000000000000000000000000000000000 { result := mul(uUNIT, 47) }
        case 1000000000000000000000000000000000000000000000000000000000000000000 { result := mul(uUNIT, 48) }
        case 10000000000000000000000000000000000000000000000000000000000000000000 { result := mul(uUNIT, 49) }
        case 100000000000000000000000000000000000000000000000000000000000000000000 { result := mul(uUNIT, 50) }
        case 1000000000000000000000000000000000000000000000000000000000000000000000 { result := mul(uUNIT, 51) }
        case 10000000000000000000000000000000000000000000000000000000000000000000000 { result := mul(uUNIT, 52) }
        case 100000000000000000000000000000000000000000000000000000000000000000000000 { result := mul(uUNIT, 53) }
        case 1000000000000000000000000000000000000000000000000000000000000000000000000 { result := mul(uUNIT, 54) }
        case 10000000000000000000000000000000000000000000000000000000000000000000000000 { result := mul(uUNIT, 55) }
        case 100000000000000000000000000000000000000000000000000000000000000000000000000 { result := mul(uUNIT, 56) }
        case 1000000000000000000000000000000000000000000000000000000000000000000000000000 { result := mul(uUNIT, 57) }
        case 10000000000000000000000000000000000000000000000000000000000000000000000000000 { result := mul(uUNIT, 58) }
        case 100000000000000000000000000000000000000000000000000000000000000000000000000000 { result := mul(uUNIT, 59) }
        default { result := uMAX_UD60x18 }
    }

    if (result.unwrap() == uMAX_UD60x18) {
        unchecked {

            result = wrap(log2(x).unwrap() * uUNIT / uLOG2_10);
        }
    }
}

function log2(UD60x18 x) pure returns (UD60x18 result) {
    uint256 xUint = x.unwrap();

    if (xUint < uUNIT) {
        revert PRBMath_UD60x18_Log_InputTooSmall(x);
    }

    unchecked {

        uint256 n = msb(xUint / uUNIT);

        uint256 resultUint = n * uUNIT;

        uint256 y = xUint >> n;

        if (y == uUNIT) {
            return wrap(resultUint);
        }

        uint256 DOUBLE_UNIT = 2e18;
        for (uint256 delta = uHALF_UNIT; delta > 0; delta >>= 1) {
            y = (y * y) / uUNIT;

            if (y >= DOUBLE_UNIT) {

                resultUint += delta;

                y >>= 1;
            }
        }
        result = wrap(resultUint);
    }
}

function mul(UD60x18 x, UD60x18 y) pure returns (UD60x18 result) {
    result = wrap(mulDiv18(x.unwrap(), y.unwrap()));
}

function pow(UD60x18 x, UD60x18 y) pure returns (UD60x18 result) {
    uint256 xUint = x.unwrap();
    uint256 yUint = y.unwrap();

    if (xUint == 0) {
        return yUint == 0 ? UNIT : ZERO;
    }

    else if (xUint == uUNIT) {
        return UNIT;
    }

    if (yUint == 0) {
        return UNIT;
    }

    else if (yUint == uUNIT) {
        return x;
    }

    if (xUint > uUNIT) {
        result = exp2(mul(log2(x), y));
    }

    else {
        UD60x18 i = wrap(uUNIT_SQUARED / xUint);
        UD60x18 w = exp2(mul(log2(i), y));
        result = wrap(uUNIT_SQUARED / w.unwrap());
    }
}

function powu(UD60x18 x, uint256 y) pure returns (UD60x18 result) {

    uint256 xUint = x.unwrap();
    uint256 resultUint = y & 1 > 0 ? xUint : uUNIT;

    for (y >>= 1; y > 0; y >>= 1) {
        xUint = mulDiv18(xUint, xUint);

        if (y & 1 > 0) {
            resultUint = mulDiv18(resultUint, xUint);
        }
    }
    result = wrap(resultUint);
}

function sqrt(UD60x18 x) pure returns (UD60x18 result) {
    uint256 xUint = x.unwrap();

    unchecked {
        if (xUint > uMAX_UD60x18 / uUNIT) {
            revert PRBMath_UD60x18_Sqrt_Overflow(x);
        }

        result = wrap(sqrt_(xUint * uUNIT));
    }
}

error PRBMath_UD60x18_Ceil_Overflow(UD60x18 x);

error PRBMath_UD60x18_Convert_Overflow(uint256 x);

error PRBMath_UD60x18_Exp_InputTooBig(UD60x18 x);

error PRBMath_UD60x18_Exp2_InputTooBig(UD60x18 x);

error PRBMath_UD60x18_Gm_Overflow(UD60x18 x, UD60x18 y);

error PRBMath_UD60x18_IntoSD1x18_Overflow(UD60x18 x);

error PRBMath_UD60x18_IntoSD59x18_Overflow(UD60x18 x);

error PRBMath_UD60x18_IntoUD2x18_Overflow(UD60x18 x);

error PRBMath_UD60x18_IntoUint128_Overflow(UD60x18 x);

error PRBMath_UD60x18_IntoUint40_Overflow(UD60x18 x);

error PRBMath_UD60x18_Log_InputTooSmall(UD60x18 x);

error PRBMath_UD60x18_Sqrt_Overflow(UD60x18 x);

pragma solidity >=0.8.19;

pragma solidity 0.8.20;

function _calculateSellOutput(
    uint256 supply,
    uint256 reserve,
    uint256 crr,
    uint256 amountIn
) pure returns (uint256) {

    if (amountIn == supply) {
        return reserve;
    }

    if (crr == 100) {
        return UD60x18.unwrap(ud((reserve * amountIn) / supply));
    }

    UD60x18 wrappedSupply = ud(supply);
    UD60x18 wrappedReserve = ud(reserve);
    UD60x18 wrappedCrr = ud((1e18 * 100) / crr);
    UD60x18 wrappedAmountIn = ud(amountIn);

    UD60x18 res = wrappedAmountIn.div(wrappedSupply);
    res = ud(1e18).sub(res);
    res = res.pow(wrappedCrr);
    res = ud(1e18).sub(res);
    res = wrappedReserve.mul(res);
    return UD60x18.unwrap(res);
}

function _calculateSellInput(
    uint256 supply,
    uint256 reserve,
    uint256 crr,
    uint256 amountOut
) pure returns (uint256) {
    if (crr == 100) {
        return UD60x18.unwrap(ud((amountOut * supply) / reserve));
    }
    UD60x18 wrappedSupply = ud(supply);
    UD60x18 wrappedReserve = ud(reserve);
    UD60x18 wrappedCrr = ud(crr * 1e16);
    UD60x18 wrappedAmountOut = ud(amountOut);

    UD60x18 res = wrappedReserve.sub(wrappedAmountOut);
    res = (res.div(wrappedReserve)).pow(wrappedCrr);
    UD60x18 one = ud(1e18).sub(res);
    res = wrappedSupply.mul(one);
    return UD60x18.unwrap(res);
}

function _calculateBuyOutput(
    uint256 supply,
    uint256 reserve,
    uint256 crr,
    uint256 amountIn
) pure returns (uint256) {
    if (crr == 100) {
        return UD60x18.unwrap(ud((supply * amountIn) / reserve));
    }
    UD60x18 wrappedSupply = ud(supply);
    UD60x18 wrappedReserve = ud(reserve);
    UD60x18 wrappedCrr = ud(crr * 1e16);
    UD60x18 wrappedAmountIn = ud(amountIn);

    UD60x18 res = wrappedAmountIn.div(wrappedReserve);
    res = res.add(ud(1e18));
    res = res.pow(wrappedCrr);
    UD60x18 one = res.sub(ud(1e18));
    res = wrappedSupply.mul(one);
    return UD60x18.unwrap(res);
}

function _calculateBuyInput(
    uint256 supply,
    uint256 reserve,
    uint256 crr,
    uint256 amountOut
) pure returns (uint256) {
    if (crr == 100) {
        return UD60x18.unwrap(ud((amountOut * reserve) / supply));
    }

    UD60x18 wrappedSupply = ud(supply);
    UD60x18 wrappedReserve = ud(reserve);
    UD60x18 wrappedCrr = ud((1e18 * 100) / crr);
    UD60x18 wrappedAmountOut = ud(amountOut);

    UD60x18 res = wrappedAmountOut.add(wrappedSupply);
    res = (res.div(wrappedSupply)).pow(wrappedCrr);

    UD60x18 one = res.sub(ud(1e18));
    res = wrappedReserve.mul(one);
    return UD60x18.unwrap(res);
}

pragma solidity 0.8.20;

contract DRC20 is IDRC20, ERC20PermitUpgradeable {

    constructor() {
        _disableInitializers();
    }

    bytes32 internal constant DRC20_STORAGE_LOCATION =
        0xb76f77bacf50c2c0746ca0d07482543304b44684f1a10a9d69fece929ef6b900;

    uint256 public constant MIN_RESERVE = 1 ether;
    uint256 public constant MIN_INITIAL_MINT = 1 ether;
    uint256 public constant MIN_CRR = 10;
    uint256 public constant MAX_CRR = 100;
    uint256 public constant MIN_TOTAL_SUPPLY = 1 ether;
    uint256 public constant MAX_TOTAL_SUPPLY = 1_000_000_000_000_000 ether;

    function initialize(
        string memory initialName,
        string memory initialSymbol,
        address initialCreator,
        uint8 initialCrr,
        uint256 initialMint,
        uint256 initialMinTotalSupply,
        uint256 initialMaxTotalSupply,
        string memory initialIdentity
    ) public payable virtual initializer {
        __DRC20_init(
            initialName,
            initialSymbol,
            initialCreator,
            initialCrr,
            initialMint,
            initialMinTotalSupply,
            initialMaxTotalSupply,
            initialIdentity
        );
    }

    function __DRC20_init(
        string memory initialName,
        string memory initialSymbol,
        address initialCreator,
        uint8 initialCrr,
        uint256 initialMint,
        uint256 initialMinTotalSupply,
        uint256 initialMaxTotalSupply,
        string memory initialIdentity
    ) internal onlyInitializing checkReserveAndSupply {
        _checkInitialData(
            initialMint,
            initialMinTotalSupply,
            initialMaxTotalSupply
        );

        __ERC20_init(initialName, initialSymbol);
        __ERC20Permit_init(initialName);

        _setCreator(initialCreator);

        TokenStorage storage $ = _getTokenStorage();
        $._crr = initialCrr;
        $._minTotalSupply = initialMinTotalSupply;
        $._maxTotalSupply = initialMaxTotalSupply;
        $._identity = initialIdentity;

        if (initialMint > 0) _mint(initialCreator, initialMint);

        if (totalSupply() > maxTotalSupply()) revert NewSupplyTooLarge();
    }

    function _setCreator(address newCreator) internal {
        TokenStorage storage $ = _getTokenStorage();
        $._creator = newCreator;
    }

    function _checkInitialData(
        uint256 initialMint,
        uint256 minTotalSupplyToCheck,
        uint256 maxTotalSupplyToCheck
    ) internal view virtual {
        if (
            initialMint < MIN_INITIAL_MINT ||
            initialMint > maxTotalSupplyToCheck
        ) revert InvalidInitialMint();

        if (minTotalSupplyToCheck < MIN_TOTAL_SUPPLY) {
            revert InvalidMinTotalSupply();
        }
        if (maxTotalSupplyToCheck > MAX_TOTAL_SUPPLY) {
            revert InvalidMaxTotalSupply();
        }
    }

    function _getTokenStorage() internal pure returns (TokenStorage storage $) {
        assembly {
            $.slot := DRC20_STORAGE_LOCATION
        }
    }

    function _emitNewPrice() internal {
        emit ReserveUpdated(reserve(), totalSupply(), getCurrentPrice());
    }

    function _update(
        address from,
        address to,
        uint256 value
    ) internal virtual override {
        super._update(from, to, value);
        bool isMint = from == address(0);
        bool isBurn = to == address(0);
        if (isMint || isBurn) {
            _emitNewPrice();
        }
    }

    function _checkReserve() internal view virtual {
        if (reserve() < MIN_RESERVE) revert NewReserveTooSmall();
    }

    function _checkSupply() internal view virtual {
        if (totalSupply() < minTotalSupply()) revert NewSupplyTooSmall();
        if (totalSupply() > maxTotalSupply()) revert NewSupplyTooLarge();
    }

    function _checkReserveAndSupply() internal view virtual {
        _checkReserve();
        _checkSupply();
    }

    function updateIdentity(
        string memory newIdentity
    ) public virtual onlyCreator {
        _updateIdentity(newIdentity);
    }

    function updateMaxTotalSupply(
        uint256 newMaxTotalSupply
    ) public virtual onlyCreator {
        _updateMaxTotalSupply(newMaxTotalSupply);
    }

    function updateMinTotalSupply(
        uint256 newMinTotalSupply
    ) public virtual onlyCreator {
        _updateMinTotalSupply(newMinTotalSupply);
    }

    function _updateIdentity(string memory newIdentity) internal {
        TokenStorage storage $ = _getTokenStorage();
        $._identity = newIdentity;
    }

    function _updateMaxTotalSupply(uint256 newMaxTotalSupply) internal {
        if (newMaxTotalSupply > MAX_TOTAL_SUPPLY) {
            revert InvalidMaxTotalSupply();
        }

        TokenStorage storage $ = _getTokenStorage();
        $._maxTotalSupply = newMaxTotalSupply;
        emit MaxTotalSupplyUpdated(newMaxTotalSupply);
    }

    function _updateMinTotalSupply(uint256 newMinTotalSupply) internal {
        if (newMinTotalSupply < MIN_TOTAL_SUPPLY) {
            revert InvalidMinTotalSupply();
        }

        TokenStorage storage $ = _getTokenStorage();
        $._minTotalSupply = newMinTotalSupply;
        emit MinTotalSupplyUpdated(newMinTotalSupply);
    }

    function getCurrentPrice() public view returns (uint256) {
        if (totalSupply() == 0 || reserve() == 0) {
            return 0;
        }
        UD60x18 wrappedSupply = ud(totalSupply());
        UD60x18 wrappedReserve = ud(reserve());
        UD60x18 wrappedCrr = ud(crr() * uint256(1e18));

        UD60x18 res = wrappedReserve.div(wrappedSupply);
        res = res.mul(ud(100e18));
        res = res.div(wrappedCrr);
        return UD60x18.unwrap(res);
    }

    function crr() public view returns (uint256) {
        return _getTokenStorage()._crr;
    }

    function maxTotalSupply() public view returns (uint256) {
        return _getTokenStorage()._maxTotalSupply;
    }

    function identity() public view returns (string memory) {
        return _getTokenStorage()._identity;
    }

    function reserve() public view returns (uint256) {
        return address(this).balance;
    }

    function minTotalSupply() public view returns (uint256) {
        return _getTokenStorage()._minTotalSupply;
    }

    function sell(
        uint256 amountIn,
        uint256 amountOutMin,
        address recipient
    ) external {
        sellExactTokensForDEL(amountIn, amountOutMin, recipient);
    }

    function buy(uint256 amountOutMin, address recipient) external payable {
        buyTokenForExactDEL(amountOutMin, recipient);
    }

    function sellExactTokensForDEL(
        uint256 amountIn,
        uint256 amountOutMin,
        address recipient
    ) public checkReserveAndSupply {
        if (amountIn == 0) revert InvalidAmount();
        uint256 amountBase = calculateSellOutput(amountIn);
        if (amountBase < amountOutMin) revert InsufficientAmountOutput();

        _burn(_msgSender(), amountIn);
        payable(recipient).transfer(amountBase);
    }

    function sellTokensForExactDEL(
        uint256 amountOut,
        uint256 amountInMax,
        address recipient
    ) external checkReserveAndSupply {
        if (amountOut == 0 || amountInMax == 0) revert InvalidAmount();

        uint256 amountToken = calculateSellInput(amountOut);
        if (amountToken > amountInMax) revert InsufficientAmountInput();

        _burn(_msgSender(), amountToken);
        payable(recipient).transfer(amountOut);
    }

    function buyTokenForExactDEL(
        uint256 amountOutMin,
        address recipient
    ) public payable checkReserveAndSupply {
        if (msg.value == 0) revert InvalidValue();
        uint256 amountToken = calculateBuyOutputBefore(msg.value);

        if (amountToken < amountOutMin) revert InsufficientAmountOutput();

        _mint(recipient, amountToken);
    }

    function buyExactTokenForDEL(
        uint256 amountOut,
        address recipient
    ) external payable checkReserveAndSupply {
        if (msg.value == 0) revert InvalidValue();

        uint256 amountBase = calculateBuyInputBefore(amountOut, msg.value);

        if (msg.value < amountBase) revert InsufficientAmountInput();

        _mint(recipient, amountOut);

        if (msg.value > amountBase) {
            payable(_msgSender()).transfer(msg.value - amountBase);
        }
    }

    function calculateSellOutput(uint256 amount) public view returns (uint256) {
        return _calculateSellOutput(totalSupply(), reserve(), crr(), amount);
    }

    function calculateSellInput(uint256 amount) public view returns (uint256) {
        return _calculateSellInput(totalSupply(), reserve(), crr(), amount);
    }

    function calculateBuyOutput(uint256 amount) public view returns (uint256) {
        return _calculateBuyOutput(totalSupply(), reserve(), crr(), amount);
    }

    function calculateBuyInput(uint256 amount) public view returns (uint256) {
        return _calculateBuyInput(totalSupply(), reserve(), crr(), amount);
    }

    function burn(uint256 value) public virtual checkReserveAndSupply {
        _burn(_msgSender(), value);
    }

    function calculateBuyOutputBefore(
        uint256 amount
    ) public view returns (uint256) {
        return _calculateBuyOutput(totalSupply(), reserve() - amount, crr(), amount);
    }

    function calculateBuyInputBefore(
        uint256 amount,
        uint256 value
    ) public view returns (uint256) {
        return _calculateBuyInput(totalSupply(), reserve() - value, crr(), amount);
    }

    function calculateSellOutput(
        uint256 supply,
        uint256 customReserve,
        uint256 customCrr,
        uint256 amountIn
    ) external pure returns (uint256) {
        return _calculateSellOutput(supply, customReserve, customCrr, amountIn);
    }

    function calculateSellInput(
        uint256 supply,
        uint256 customReserve,
        uint256 customCrr,
        uint256 amountOut
    ) external pure returns (uint256) {
        return _calculateSellInput(supply, customReserve, customCrr, amountOut);
    }

    function calculateBuyOutput(
        uint256 supply,
        uint256 customReserve,
        uint256 customCrr,
        uint256 amountIn
    ) external pure returns (uint256) {
        return _calculateBuyOutput(supply, customReserve, customCrr, amountIn);
    }

    function calculateBuyInput(
        uint256 supply,
        uint256 customReserve,
        uint256 customCrr,
        uint256 amountOut
    ) external pure returns (uint256) {
        return _calculateBuyInput(supply, customReserve, customCrr, amountOut);
    }

    bytes32 public constant PERMIT_TYPEHASH =
        keccak256(
            "Permit(address owner,address spender,uint256 value,uint256 nonce,uint256 deadline)"
        );

    function permitHash(
        address owner,
        address spender,
        uint256 value,
        uint256 nonce,
        uint256 deadline
    ) public view returns (bytes32) {
        bytes32 structHash = keccak256(
            abi.encode(PERMIT_TYPEHASH, owner, spender, value, nonce, deadline)
        );
        bytes32 hash = _hashTypedDataV4(structHash);
        return hash;
    }

    function creator() public view returns (address) {
        return _getTokenStorage()._creator;
    }

    modifier onlyCreator() {
        if (_msgSender() != creator()) {
            revert OnlyCreator();
        }
        _;
    }

    modifier checkReserveAndSupply() {
        uint256 supplyBefore = totalSupply();
        _;
        uint256 supplyAfter = totalSupply();

        if (supplyBefore < supplyAfter) {

            if (totalSupply() > maxTotalSupply()) revert NewSupplyTooLarge();
        } else if (supplyBefore > supplyAfter) {

            if (totalSupply() < minTotalSupply()) revert NewSupplyTooSmall();
        }

        _checkReserve();
    }

    receive() external payable {
        _emitNewPrice();
    }
}

pragma solidity 0.8.20;

contract DRC20Reserveless is
    ERC20PermitUpgradeable,
    ERC20BurnableUpgradeable,
    ERC20CappedUpgradeable,
    OwnableUpgradeable
{
    error NotMintable();
    error NotBurnable();
    error CapAvailableOnlyForMintable();

    struct TokenStorage {
        bool _mintable;
        bool _burnable;
        string _identity;
    }

    bytes32 internal constant DRC20_RESERVELESS_STORAGE_LOCATION =
        0xed216e73b55985dc9aa89ab575f538d915b9d4d1e349fd366c9650df2b66a100;

    function _getTokenStorage() private pure returns (TokenStorage storage $) {
        assembly {
            $.slot := DRC20_RESERVELESS_STORAGE_LOCATION
        }
    }

    constructor() {
        _disableInitializers();
    }

    function initialize(
        string memory initialName,
        string memory initialSymbol,
        address initialCreator,
        bool initialMintable,
        bool initialBurnable,
        uint256 initialMint,
        uint256 initialCap,
        string memory initialIdentity
    ) public payable virtual initializer {
        if (initialCap != 0 && !initialMintable) {
            revert CapAvailableOnlyForMintable();
        }
        __ERC20_init(initialName, initialSymbol);
        __ERC20Permit_init(initialName);
        __Ownable_init(initialCreator);

        bool noCapProvided = initialCap == 0;
        if (noCapProvided) {
            __ERC20Capped_init(type(uint256).max);
        } else {
            __ERC20Capped_init(initialCap);
        }

        TokenStorage storage $ = _getTokenStorage();
        $._mintable = initialMintable;
        $._burnable = initialBurnable;
        $._identity = initialIdentity;

        if (initialMint != 0) {
            _mint(initialCreator, initialMint);
        }
    }

    function withCap() public view returns (bool) {
        return cap() != type(uint256).max;
    }

    function identity() public view returns (string memory) {
        return _getTokenStorage()._identity;
    }

    function _update(
        address from,
        address to,
        uint256 value
    ) internal virtual override(ERC20Upgradeable, ERC20CappedUpgradeable) {
        if (to == address(0)) {
            if (!burnable()) {
                revert NotBurnable();
            }
        }
        ERC20CappedUpgradeable._update(from, to, value);
    }

    function mint(address to, uint256 amount) public onlyOwner {
        if (!mintable()) {
            revert NotMintable();
        }
        _mint(to, amount);
    }

    function mintable() public view returns (bool) {
        return _getTokenStorage()._mintable;
    }

    function burnable() public view returns (bool) {
        return _getTokenStorage()._burnable;
    }

    bytes32 public constant PERMIT_TYPEHASH =
        keccak256(
            "Permit(address owner,address spender,uint256 value,uint256 nonce,uint256 deadline)"
        );

    function permitHash(
        address owner,
        address spender,
        uint256 value,
        uint256 nonce,
        uint256 deadline
    ) public view returns (bytes32) {
        bytes32 structHash = keccak256(
            abi.encode(PERMIT_TYPEHASH, owner, spender, value, nonce, deadline)
        );
        bytes32 hash = _hashTypedDataV4(structHash);
        return hash;
    }
}

pragma solidity 0.8.20;

interface IDecimalTokenCenter {
    function tokens(string memory symbol) external view returns (address);

    function isTokenExists(address token) external view returns (bool);

    function implementationReserveless() external view returns (address);
}

pragma solidity 0.8.20;

contract DecimalTokenCenter is
    IDecimalTokenCenter,
    DecimalTokenCenterCore,
    OwnableUpgradeable,
    PausableUpgradeable,
    DecimalImpl
{

    struct Token {
        uint256 initialMint;
        uint256 minTotalSupply;
        uint256 maxTotalSupply;
        address creator;
        uint8 crr;
        string identity;
        string symbol;
        string name;
    }

    event TokenDeployed(address tokenAddress, Token meta);
    event TokenReservelessDeployed(address tokenAddress);
    event TokenUpgraded(
        address indexed oldContract,
        address indexed newContract
    );
    event TokenReservelessUpgraded(
        address indexed oldContract,
        address indexed newContract
    );

    error InvalidMinReserve();
    error InvalidComission(uint256 expectedCommission);
    error TokenSymbolExist();
    error InvalidAddress();
    error DeployTokenError();

    function initialize() public virtual initializer {

    }

    function convert(
        address payable tokenIn,
        address payable tokenOut,
        uint256 amountIn,
        uint256 amountOutMin,
        address recipient,
        uint256 deadline,
        uint8 v,
        bytes32 r,
        bytes32 s
    ) external whenNotPaused {
        DRC20(tokenIn).permit(
            msg.sender,
            address(this),
            amountIn,
            deadline,
            v,
            r,
            s
        );
        convert(tokenIn, tokenOut, amountIn, amountOutMin, recipient);
    }

    function convert(
        address payable tokenIn,
        address payable tokenOut,
        uint256 amountIn,
        uint256 amountOutMin,
        address recipient
    ) public whenNotPaused {
        DRC20(tokenIn).transferFrom(msg.sender, address(this), amountIn);

        uint256 ethAmount = DRC20(tokenIn).calculateSellOutput(amountIn);

        DRC20(tokenIn).sell(amountIn, 0, address(this));

        DRC20(tokenOut).buy{value: ethAmount}(amountOutMin, recipient);
    }

    function createToken(
        Token memory meta
    ) public payable virtual whenNotPaused {
        require(validateSymbol(meta.symbol), "Invalid token symbol");

        uint256 commission = getCommissionSymbol(meta.symbol);

        if (msg.value < commission) {
            revert InvalidComission(commission);
        }
        uint256 reserve = msg.value - commission;

        payable(address(0)).transfer(commission);
        _createToken(meta, reserve);
    }

    function validateSymbol(string memory symbol) internal pure returns (bool) {
        bytes memory symbolBytes = bytes(symbol);
        if (symbolBytes.length < 3 || symbolBytes.length > 10) {
            return false;
        }
        for (uint256 i = 0; i < symbolBytes.length; i++) {
            if (
                !(symbolBytes[i] >= 0x30 && symbolBytes[i] <= 0x39) &&
                !(symbolBytes[i] >= 0x61 && symbolBytes[i] <= 0x7A)
            ) {
                return false;
            }
        }
        return true;
    }

    function _createToken(
        Token memory meta,
        uint256 reserve
    ) internal virtual returns (address) {
        if (tokens(meta.symbol) != address(0)) revert TokenSymbolExist();

        bytes memory initialisationArgs = abi.encodeWithSelector(
            DRC20.initialize.selector,
            meta.name,
            meta.symbol,
            meta.creator,
            meta.crr,
            meta.initialMint,
            meta.minTotalSupply,
            meta.maxTotalSupply,
            meta.identity
        );

        bytes32 salt = keccak256(abi.encodePacked(meta.symbol));

        address beaconAddress = address(this);
        address predictedAddress = predict(
            beaconAddress,
            salt,
            initialisationArgs
        );

        emit TokenDeployed(predictedAddress, meta);

        address deployedAddress = address(
            new BeaconProxy{value: reserve, salt: salt}(
                beaconAddress,
                initialisationArgs
            )
        );

        if (deployedAddress != predictedAddress) revert DeployTokenError();

        _getTokenCenterStorage()._tokens[meta.symbol] = deployedAddress;

        return deployedAddress;
    }

    function createTokenReserveless(
        string memory name,
        string memory symbol,
        bool mintable,
        bool burnable,
        uint256 initialMint,
        uint256 cap,
        string memory identity
    ) external virtual whenNotPaused returns (address) {
        require(validateSymbol(symbol), "Invalid token symbol");

        bytes memory initialisationArgs = abi.encodeWithSelector(
            DRC20Reserveless.initialize.selector,
            name,
            symbol,
            _msgSender(),
            mintable,
            burnable,
            initialMint,
            cap,
            identity
        );

        bytes32 salt = keccak256(abi.encodePacked(block.timestamp, name));
        address beaconAddress = _getTokenCenterStorage()
            ._tokenReservelessBeacon;
        address predictedAddress = predict(
            beaconAddress,
            salt,
            initialisationArgs
        );

        emit TokenReservelessDeployed(predictedAddress);

        address deployedAddress = address(
            new BeaconProxy{salt: salt}(beaconAddress, initialisationArgs)
        );

        if (deployedAddress != predictedAddress) revert DeployTokenError();
        return deployedAddress;
    }

    function calculateSellOutput(
        uint256 supply,
        uint256 customReserve,
        uint256 customCrr,
        uint256 amountIn
    ) external pure returns (uint256) {
        return _calculateSellOutput(supply, customReserve, customCrr, amountIn);
    }

    function calculateSellInput(
        uint256 supply,
        uint256 customReserve,
        uint256 customCrr,
        uint256 amountOut
    ) external pure returns (uint256) {
        return _calculateSellInput(supply, customReserve, customCrr, amountOut);
    }

    function calculateBuyOutput(
        uint256 supply,
        uint256 customReserve,
        uint256 customCrr,
        uint256 amountIn
    ) external pure returns (uint256) {
        return
            _calculateBuyOutput(supply, customReserve, customCrr, amountIn);
    }

    function calculateBuyInput(
        uint256 supply,
        uint256 customReserve,
        uint256 customCrr,
        uint256 amountOut
    ) external pure returns (uint256) {
        return _calculateBuyInput(supply, customReserve, customCrr, amountOut);
    }

    function getCommissionSymbol(
        string memory symbol
    ) public pure returns (uint256) {
        uint256 symbolLen = strlen(symbol);
        if (symbolLen >= 7) return 0.25 ether;
        if (symbolLen >= 6) return 2.5 ether;
        if (symbolLen >= 5) return 25 ether;
        if (symbolLen >= 4) return 250 ether;
        if (symbolLen >= 3) return 2500 ether;
        return 2500 ether;
    }

    function strlen(string memory s) internal pure returns (uint256) {
        uint256 len;
        uint256 i = 0;
        uint256 bytelength = bytes(s).length;
        for (len = 0; i < bytelength; len++) {
            bytes1 b = bytes(s)[i];
            if (b < 0x80) {
                i += 1;
            } else if (b < 0xE0) {
                i += 2;
            } else if (b < 0xF0) {
                i += 3;
            } else if (b < 0xF8) {
                i += 4;
            } else if (b < 0xFC) {
                i += 5;
            } else {
                i += 6;
            }
        }
        return len;
    }

    function predict(
        address beaconAddress,
        bytes32 salt,
        bytes memory initialisationArgs
    ) public view virtual returns (address) {
        bytes memory bytecode = type(BeaconProxy).creationCode;
        bytes memory creationBytecode = abi.encodePacked(
            bytecode,
            abi.encode(beaconAddress, initialisationArgs)
        );
        bytes32 hash = keccak256(
            abi.encodePacked(
                bytes1(0xff),
                address(this),
                uint256(salt),
                keccak256(creationBytecode)
            )
        );
        return address(uint160(uint256(hash)));
    }

    receive() external payable {}

    function implementation() public view returns (address) {
        if (paused()) {
            return address(0);
        }
        return _getTokenCenterStorage()._tokenImpl;
    }

    function implementationReserveless() public view returns (address) {
        if (paused()) {
            return address(0);
        }
        return _getTokenCenterStorage()._tokenReservelessImpl;
    }

    function tokens(string memory symbol) public view returns (address) {
        return _getTokenCenterStorage()._tokens[symbol];
    }

    function isTokenExists(address token) public view returns (bool) {
        (bool success, bytes memory res) = token.staticcall(
            abi.encodeWithSelector(IERC20Metadata.symbol.selector)
        );
        if (!success || res.length == 0) {
            return false;
        }
        string memory symbol = abi.decode(res, (string));
        return tokens(symbol) != address(0);
    }

    function getContractCenter() public view returns (address) {
        return _getTokenCenterStorage()._contractCenter;
    }

    function setContractCenter(address addressContractCenter) public onlyOwner {
        _getTokenCenterStorage()._contractCenter = addressContractCenter;
    }

    function upgrade(address newImpl, bytes memory data) public onlyOwner {
        address currentImpl = ERC1967Utils.getImplementation();

        if (newImpl == address(0) || newImpl == currentImpl)
            revert InvalidAddress();

        ERC1967Utils.upgradeToAndCall(newImpl, data);
    }

    function upgradeToken(address newTokenImpl) public onlyOwner {
        address currentTokenImpl = implementation();

        if (newTokenImpl == address(0)) revert InvalidAddress();
        _getTokenCenterStorage()._tokenImpl = newTokenImpl;

        emit TokenUpgraded(currentTokenImpl, newTokenImpl);
    }

    function upgradeTokenReserveless(address newTokenImpl) public onlyOwner {
        address currentTokenImpl = implementationReserveless();

        if (newTokenImpl == address(0)) revert InvalidAddress();
        _getTokenCenterStorage()._tokenReservelessImpl = newTokenImpl;

        emit TokenReservelessUpgraded(currentTokenImpl, newTokenImpl);
    }

    function pause() public onlyOwner {
        _pause();
    }

    function unpause() public onlyOwner {
        _unpause();
    }
}
