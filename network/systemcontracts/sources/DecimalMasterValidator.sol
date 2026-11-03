

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

pragma solidity 0.8.20;

interface IDecimalDelegation {
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
}

pragma solidity 0.8.20;

interface IDecimalMasterValidator {

    enum ValidatorStatus {
        Deleted,
        Candidate,
        Approved
    }

    struct Validator {
        ValidatorStatus status;
        bool paused;
        uint16[] penaltyPercantages;
    }

    struct ValidatorMeta {
        address validator;
        string meta;
    }

    struct Stake {
        address token;
        uint256 amount;
    }

    event ValidatorMetaUpdated(address indexed validator, string meta);
    event ValidatorUpdated(
        address indexed validator,
        ValidatorStatus status,
        bool paused
    );
    event ValidatorPenalty(address indexed validator, uint256 percentage);

    event RewardPerBlockUpdated(uint256 rewardPerBlock, bool enabled);

    error ZeroAddress();
    error AlreadyMember();
    error NotMember();
    error InvalidStatus();
    error InvalidPenalty();

    function getValidatorStatus(
        address validator
    ) external view returns (ValidatorStatus);

    function getValidatorPenalties(
        address validator
    ) external view returns (uint16[] memory);

    function isActive(address validator) external view returns (bool);

    function isMember(address validator) external view returns (bool);

    function addCandidate(
        address validator,
        string memory meta,
        Stake memory initialStake
    ) external;

    function addValidator(
        address validator,
        string memory meta,
        Stake memory initialStake
    ) external;

    function addCandidateDEL(
        address validator,
        string memory meta
    ) external payable;

    function addValidatorDEL(
        address validator,
        string memory meta
    ) external payable;

    function addValidators(ValidatorMeta[] memory metas) external;

    function removeValidator(address validator) external;

    function approveCandidates(address[] memory validator) external;

    function updateValidatorMeta(
        address validator,
        string calldata meta
    ) external;

    function pauseValidator(address validator) external;

    function unpauseValidator(address validator) external;

    function addPenalty(address validator, uint16 penaltyPercentage) external;

    function setRewardPerBlock(uint256 rewardPerBlock) external;

    function clearRewardPerBlock() external;

    function getRewardPerBlock()
        external
        view
        returns (uint256 rewardPerBlock, bool enabled);

    function upgrade(address newImpl, bytes memory data) external;
}

pragma solidity 0.8.20;

contract DecimalMasterValidator is
    IDecimalMasterValidator,
    OwnableUpgradeable,
    PausableUpgradeable,
    DecimalImpl
{

    struct ValidatorStorage {
        address _contractCenter;
        mapping(address => Validator) _validators;

        uint256 _rewardPerBlock;
        bool _rewardPerBlockEnabled;
    }

    bytes32 private constant DECIMAL_VALIDATOR_STORAGE_LOCATION =
        0xa438eed44269bc879291c2546911ec266d7579942af2843c7b788fe771ec9a00;

    function _getValidatorStorage()
        internal
        pure
        returns (ValidatorStorage storage $)
    {
        assembly {
            $.slot := DECIMAL_VALIDATOR_STORAGE_LOCATION
        }
    }

    function _initValidator(
        address validatorAddress,
        ValidatorStatus status
    ) internal {
        if (validatorAddress == address(0)) revert ZeroAddress();
        if (isMember(validatorAddress)) revert AlreadyMember();
        if (
            status != ValidatorStatus.Candidate &&
            status != ValidatorStatus.Approved
        ) {
            revert InvalidStatus();
        }
        Validator memory validator;
        validator.status = status;
        validator.paused = false;
        _getValidatorStorage()._validators[validatorAddress] = validator;
        _emitValidatorUpdated(validatorAddress);
    }

    function _approveCandidate(address candidateAddress) internal {
        if (!isMember(candidateAddress)) revert NotMember();
        if (getValidatorStatus(candidateAddress) != ValidatorStatus.Candidate) {
            revert InvalidStatus();
        }
        _getValidatorStorage()
            ._validators[candidateAddress]
            .status = ValidatorStatus.Approved;
        _emitValidatorUpdated(candidateAddress);
    }

    function _removeValidator(address validatorAddress) internal {
        if (!isMember(validatorAddress)) revert NotMember();
        _getValidatorStorage()
            ._validators[validatorAddress]
            .status = ValidatorStatus.Deleted;
        _emitValidatorUpdated(validatorAddress);
    }

    function _setPause(address validatorAddress, bool validatorPause) internal {
        if (!isMember(validatorAddress)) revert NotMember();
        ValidatorStorage storage $ = _getValidatorStorage();
        if ($._validators[validatorAddress].paused == validatorPause) {
            revert InvalidStatus();
        }
        $._validators[validatorAddress].paused = validatorPause;
        _emitValidatorUpdated(validatorAddress);
    }

    function getValidatorStatus(
        address validator
    ) public view returns (ValidatorStatus) {
        return _getValidatorStorage()._validators[validator].status;
    }

    function getValidator(
        address validator
    ) public view returns (Validator memory) {
        return _getValidatorStorage()._validators[validator];
    }

    function getValidatorPenalties(
        address validator
    ) public view returns (uint16[] memory) {
        return getValidator(validator).penaltyPercantages;
    }

    function isActive(address validator) public view returns (bool) {
        return isMember(validator) && !getValidator(validator).paused;
    }

    function isMember(address validator) public view returns (bool) {
        return getValidatorStatus(validator) != ValidatorStatus.Deleted;
    }

    function _getContractCenter()
        internal
        view
        returns (IDecimalContractCenter)
    {
        return IDecimalContractCenter(_getValidatorStorage()._contractCenter);
    }

    function _delegation() internal view returns (IDecimalDelegation) {
        return
            IDecimalDelegation(
                _getContractCenter().getContractAddress("delegation")
            );
    }

    function _delegate(address validator, Stake memory stake) internal {
        IERC20(stake.token).transferFrom(
            _msgSender(),
            address(this),
            stake.amount
        );
        IERC20(stake.token).approve(address(_delegation()), stake.amount);
        _delegation().delegateTo(
            validator,
            validator,
            stake.token,
            stake.amount
        );
    }

    function _delegateDEL(address validator, uint256 amount) internal {
        _delegation().delegateDELTo{value: amount}(validator, validator);
    }

    function _addValidator(
        address validator,
        ValidatorStatus status,
        string calldata meta
    ) internal {
        if (status == ValidatorStatus.Deleted) revert InvalidStatus();
        if (validator == address(0)) revert ZeroAddress();
        if (isMember(validator)) revert AlreadyMember();
        _initValidator(validator, status);
        _emitMetaUpdated(validator, meta);
    }

    function addValidator(
        address validator,
        string calldata meta,
        Stake memory initialStake
    ) public onlyOwner whenNotPaused {
        _addValidator(validator, ValidatorStatus.Approved, meta);
        _delegate(validator, initialStake);
    }

    function addValidatorDEL(
        address validator,
        string calldata meta
    ) public payable onlyOwner whenNotPaused {
        _addValidator(validator, ValidatorStatus.Approved, meta);
        _delegateDEL(validator, msg.value);
    }

    function addCandidate(
        address validator,
        string calldata meta,
        Stake memory initialStake
    ) public whenNotPaused {
        _addValidator(validator, ValidatorStatus.Candidate, meta);
        _delegate(validator, initialStake);
    }

    function addCandidateDEL(
        address validator,
        string calldata meta
    ) public payable whenNotPaused {
        _addValidator(validator, ValidatorStatus.Candidate, meta);
        _delegateDEL(validator, msg.value);
    }

    function updateValidatorMeta(
        address validator,
        string calldata meta
    ) external whenNotPaused onlyOwnerOrValidator(validator) {
        if (!isMember(validator)) revert NotMember();
        _emitMetaUpdated(validator, meta);
    }

    function approveCandidates(
        address[] memory candidates
    ) external whenNotPaused onlyOwner {
        for (uint256 i; i < candidates.length; i += 1) {
            address candidate = candidates[i];
            if (!isMember(candidate)) revert NotMember();
            _approveCandidate(candidate);
        }
    }

    function addValidators(
        ValidatorMeta[] calldata metas
    ) external whenNotPaused onlyOwner {
        bool isEmpty = true;
        for (uint256 i; i < metas.length; i += 1) {
            if (!isMember(metas[i].validator)) {
                _addValidator(
                    metas[i].validator,
                    ValidatorStatus.Approved,
                    metas[i].meta
                );
                isEmpty = false;
            }
        }
        if (isEmpty) {
            revert AlreadyMember();
        }
    }

    function removeValidator(
        address validator
    ) external whenNotPaused onlyOwnerOrValidator(validator) {
        _removeValidator(validator);
    }

    function pauseSelf() external whenNotPaused {
        pauseValidator(_msgSender());
    }

    function unpauseSelf() external whenNotPaused {
        unpauseValidator(_msgSender());
    }

    function pauseValidator(
        address validator
    ) public whenNotPaused onlyOwnerOrValidator(validator) {
        _setPause(validator, true);
    }

    function unpauseValidator(
        address validator
    ) public whenNotPaused onlyOwnerOrValidator(validator) {
        _setPause(validator, false);
    }

    modifier onlyOwnerOrValidator(address validator) {
        if (_msgSender() != owner()) {
            if (!isMember(_msgSender())) {
                revert NotMember();
            }
            if (_msgSender() != validator) {
                revert OwnableUnauthorizedAccount(_msgSender());
            }
        }
        _;
    }

    function _emitValidatorUpdated(address validatorAddress) internal {
        Validator memory validator = _getValidatorStorage()._validators[
            validatorAddress
        ];
        emit ValidatorUpdated(
            validatorAddress,
            validator.status,
            validator.paused
        );
    }

    function _emitMetaUpdated(
        address validatorAddress,
        string memory meta
    ) internal {
        emit ValidatorMetaUpdated(validatorAddress, meta);
    }

    function addPenalty(
        address validator,
        uint16 penaltyPercentage
    ) public onlyOwner whenNotPaused {
        pauseValidator(validator);
        if (penaltyPercentage == 0 || penaltyPercentage > 100) {
            revert InvalidPenalty();
        }
        _getValidatorStorage()._validators[validator].penaltyPercantages.push(
            penaltyPercentage
        );
        emit ValidatorPenalty(validator, penaltyPercentage);
    }

    function setRewardPerBlock(uint256 rewardPerBlock) public onlyOwner {
        ValidatorStorage storage $ = _getValidatorStorage();
        $._rewardPerBlock = rewardPerBlock;
        $._rewardPerBlockEnabled = true;
        emit RewardPerBlockUpdated(rewardPerBlock, true);
    }

    function clearRewardPerBlock() public onlyOwner {
        ValidatorStorage storage $ = _getValidatorStorage();
        $._rewardPerBlock = 0;
        $._rewardPerBlockEnabled = false;
        emit RewardPerBlockUpdated(0, false);
    }

    function getRewardPerBlock()
        public
        view
        returns (uint256 rewardPerBlock, bool enabled)
    {
        ValidatorStorage storage $ = _getValidatorStorage();
        return ($._rewardPerBlock, $._rewardPerBlockEnabled);
    }

    function upgrade(address newImpl, bytes memory data) public onlyOwner {
        ERC1967Utils.upgradeToAndCall(newImpl, data);
    }

    function pause() public onlyOwner {
        _pause();
    }

    function unpause() public onlyOwner {
        _unpause();
    }
}
