// SPDX-License-Identifier: MIT
pragma solidity 0.8.20;

import {
    ERC721Upgradeable, OwnableUpgradeable, IERC20, ERC20Burnable,
    INFTReserve, NFTState, NFTType, IDecimalContractCenter, Math, ERC1967Utils
} from "./owned/DecimalNFTCenter.sol";
import {IMintCandyNativeBurn} from "./MintCandyWrappedDEL.sol";
import {MAX_ISOLATED_ORIGIN_UNITS} from "./MintCandyNFTIsolationLimits.sol";

interface IMintCandyOrigin1155 {
    function getIsolationUnitReserve(uint256 tokenId) external view returns (INFTReserve.Reserve memory);
    function getIsolationPolicy(uint256 tokenId) external view returns (bool initialized, bool refundable, bool burnable);
    function uri(uint256 tokenId) external view returns (string memory);
}
interface IMintCandyPositionFactory {
    function paused() external view returns (bool);
    function getContractCenter() external view returns (address);
    function getNFTState(address nft) external view returns (NFTState memory);
}
interface IMintCandyNFTCollateralConverter {
    function convert(address token, uint256 amount, uint256 minimumNativeOutput,
        uint256 deadline) external returns (uint256 nativeOutput);
}

/// @notice One ERC721 receipt represents one independently backed origin position.
/// @dev Fresh-network contract. It never remints a slashed claim into an origin ID.
contract MintCandyNFTPositionVault is ERC721Upgradeable, OwnableUpgradeable {
    enum PositionStatus { Unknown, Active, Closed }
    struct Position {
        address originContract;
        uint256 originTokenId;
        uint256 originQuantity;
        INFTReserve.Reserve reserve;
        uint256 parentPositionId;
        bool refundable;
        bool burnable;
    }
    struct PositionView {
        address originContract;
        uint256 originTokenId;
        NFTType originStandard;
        uint256 originQuantity;
        INFTReserve.Reserve reserve;
        uint256 parentPositionId;
        address physicalOwner;
        PositionStatus status;
        bool refundable;
        bool burnable;
    }
    struct VaultStorage {
        address registry;
        address factory;
        uint256 nextId;
        uint256 activeCount;
        bool entered;
        bool paused;
        mapping(uint256 => Position) positions;
        mapping(INFTReserve.ReserveType => mapping(address => uint256)) totals;
    }
    // New, explicitly declared namespace; no historical storage layout is inferred.
    bytes32 private constant VAULT_STORAGE = keccak256("mintcandy.storage.NFTPositionVault.v1");
    function _vault() private pure returns (VaultStorage storage state) {
        bytes32 location = VAULT_STORAGE;
        assembly { state.slot := location }
    }

    error InvalidFreshInitialization();
    error InvalidOrigin();
    error InvalidPosition();
    error InvalidQuantity();
    error InvalidCurrency();
    error InvalidFunding();
    error InvalidRecipient();
    error UnauthorizedPositionOwner();
    error ControllerRequired();
    error PositionBurnDisabled();
    error FinancialOperationsPaused();
    error ReentrantFinancialOperation();
    error NativeReserveBurnFailed();
    error ReserveTransferFailed();

    event PositionIsolated(uint256 indexed positionId, address indexed originContract,
        uint256 indexed originTokenId, address receiptOwner, uint256 originQuantity,
        uint8 reserveType, address reserveToken, uint256 reserveAmount, bool refundable, bool burnable);
    event PositionSplit(uint256 indexed parentPositionId, uint256 indexed childPositionId,
        address indexed receiptOwner, uint256 childQuantity, uint256 childReserve,
        uint256 parentQuantity, uint256 parentReserve);
    event PositionReserveUpdated(uint256 indexed positionId, uint8 reserveType,
        address reserveToken, uint256 reserveAmount, uint256 originQuantity);
    event PositionPenalty(uint256 indexed positionId, uint16 percentage, uint256 burnedReserve, uint256 remainingReserve);
    event PositionRedeemed(uint256 indexed positionId, address indexed recipient, uint256 reserveAmount, bool refunded);
    event Paused(address account);
    event Unpaused(address account);

    constructor() { _disableInitializers(); }
    function initializeFresh(address registry, address factory) external onlyInitializing {
        VaultStorage storage state = _vault();
        if (address(this).code.length != 0 || owner() == address(0) || registry.code.length == 0 ||
            factory.code.length == 0 || state.registry != address(0) ||
            IMintCandyPositionFactory(factory).getContractCenter() != registry) revert InvalidFreshInitialization();
        __ERC721_init("MintCandy Isolated Position", "MCNP");
        state.registry = registry;
        state.factory = factory;
        state.nextId = 1;
    }
    modifier financial() {
        VaultStorage storage state = _vault();
        if (state.entered) revert ReentrantFinancialOperation();
        if (state.paused || IMintCandyPositionFactory(state.factory).paused()) revert FinancialOperationsPaused();
        state.entered = true;
        _;
        state.entered = false;
    }
    function getContractCenter() external view returns (address) { return _vault().registry; }
    function nftCenter() external view returns (address) { return _vault().factory; }
    function nextPositionId() external view returns (uint256) { return _vault().nextId; }
    function maxOriginQuantity() external pure returns (uint256) { return MAX_ISOLATED_ORIGIN_UNITS; }
    function totalSupply() external view returns (uint256) { return _vault().activeCount; }
    function paused() external view returns (bool) { return _vault().paused; }
    function pause() external onlyOwner { _vault().paused = true; emit Paused(msg.sender); }
    function unpause() external onlyOwner { _vault().paused = false; emit Unpaused(msg.sender); }
    function upgrade(address implementation, bytes calldata initialization) external onlyOwner {
        if (initialization.length != 0) revert InvalidFreshInitialization();
        ERC1967Utils.upgradeToAndCall(implementation, initialization);
    }
    function withReserve() external pure returns (bool) { return true; }

    function _controller() private view returns (address result) {
        IDecimalContractCenter registry = IDecimalContractCenter(_vault().registry);
        result = registry.getContractAddress("delegation");
        if (result.code.length == 0 || result != registry.getContractAddress("delegation-nft")) revert ControllerRequired();
    }
    function _active(uint256 id) private view returns (Position storage position) {
        position = _vault().positions[id];
        if (id == 0 || position.originContract == address(0) || position.originQuantity == 0 || _ownerOf(id) == address(0)) revert InvalidPosition();
    }
    function getPosition(uint256 id) external view returns (PositionView memory result) {
        VaultStorage storage state = _vault();
        if (id == 0 || id >= state.nextId) revert InvalidPosition();
        Position storage position = state.positions[id];
        result = PositionView(position.originContract, position.originTokenId, NFTType.DRC1155,
            position.originQuantity, position.reserve, position.parentPositionId, _ownerOf(id),
            position.originContract == address(0) ? PositionStatus.Closed : PositionStatus.Active,
            position.refundable, position.burnable);
    }
    function getReserve(uint256 id) external view returns (INFTReserve.Reserve memory) { return _active(id).reserve; }
    function declaredReserve(INFTReserve.ReserveType kind, address token) external view returns (uint256) { return _vault().totals[kind][token]; }
    function tokenURI(uint256 id) public view override returns (string memory) {
        Position storage position = _active(id);
        return IMintCandyOrigin1155(position.originContract).uri(position.originTokenId);
    }

    function mintFromOrigin(uint256 originId, uint256 quantity, address recipient) external payable financial returns (uint256 id) {
        Position memory position = _originPosition(originId, quantity, recipient);
        _fund(position.reserve, msg.sender);
        return _mintOriginPosition(position, recipient);
    }

    function mintFromOriginConverted(uint256 originId, uint256 quantity, address recipient,
        uint256 minimumNativeOutput, uint256 deadline) external financial returns (uint256 id) {
        if (recipient != _controller() || minimumNativeOutput == 0 || deadline == 0) revert InvalidFunding();
        Position memory position = _originPosition(originId, quantity, recipient);
        if (!position.refundable || !position.burnable) revert PositionBurnDisabled();
        IDecimalContractCenter registry = IDecimalContractCenter(_vault().registry);
        address converter = registry.getContractAddress("curve-converter");
        address inputToken = position.reserve.token;
        uint256 inputAmount = position.reserve.amount;
        if (position.reserve.reserveType != INFTReserve.ReserveType.DRC20 ||
            inputToken == registry.getContractAddress("wdel") || converter.code.length == 0 || inputAmount == 0) revert InvalidCurrency();
        uint256 beforeNative = address(this).balance;
        _pull(inputToken, msg.sender, inputAmount);
        if (!IERC20(inputToken).approve(converter, inputAmount)) revert InvalidFunding();
        uint256 output = IMintCandyNFTCollateralConverter(converter).convert(inputToken, inputAmount, minimumNativeOutput, deadline);
        if (output < minimumNativeOutput || address(this).balance != beforeNative + output ||
            !IERC20(inputToken).approve(converter, 0)) revert InvalidFunding();
        position.reserve = INFTReserve.Reserve(address(0), output, INFTReserve.ReserveType.DEL);
        return _mintOriginPosition(position, recipient);
    }

    function _originPosition(uint256 originId, uint256 quantity, address recipient) private view returns (Position memory) {
        if (quantity == 0 || quantity > MAX_ISOLATED_ORIGIN_UNITS) revert InvalidQuantity();
        if (recipient == address(0) || recipient == address(this)) revert InvalidRecipient();
        NFTState memory origin = IMintCandyPositionFactory(_vault().factory).getNFTState(msg.sender);
        if (!origin.active || origin.nftType != NFTType.DRC1155 || !origin.withReserve) revert InvalidOrigin();
        (bool initialized, bool refundable, bool burnable) = IMintCandyOrigin1155(msg.sender).getIsolationPolicy(originId);
        if (!initialized) revert InvalidOrigin();
        INFTReserve.Reserve memory reserve = IMintCandyOrigin1155(msg.sender).getIsolationUnitReserve(originId);
        reserve.amount *= quantity;
        _validateCurrency(reserve);
        return Position(msg.sender, originId, quantity, reserve, 0, refundable, burnable);
    }

    function _mintOriginPosition(Position memory position, address recipient) private returns (uint256 id) {
        INFTReserve.Reserve memory reserve = position.reserve;
        _vault().totals[reserve.reserveType][reserve.token] += reserve.amount;
        id = _create(position, recipient);
        emit PositionIsolated(id, position.originContract, position.originTokenId, recipient, position.originQuantity,
            uint8(reserve.reserveType), reserve.token, reserve.amount, position.refundable, position.burnable);
        _emitReserve(id);
    }

    function _create(Position memory position, address recipient) private returns (uint256 id) {
        VaultStorage storage state = _vault();
        id = state.nextId++;
        state.positions[id] = position;
        state.activeCount++;
        _safeMint(recipient, id);
    }
    function splitPosition(uint256 id, uint256 quantity, address recipient) external financial returns (uint256 childId) {
        Position storage parent = _active(id);
        address holder = _ownerOf(id);
        if (msg.sender != holder) revert UnauthorizedPositionOwner();
        if (recipient == address(0) || recipient == address(this)) revert InvalidRecipient();
        if (recipient == _controller() && holder != recipient) revert ControllerRequired();
        if (quantity == 0 || quantity > parent.originQuantity) revert InvalidQuantity();
        uint256 reserveAmount = quantity == parent.originQuantity ? parent.reserve.amount :
            Math.mulDiv(parent.reserve.amount, quantity, parent.originQuantity);
        Position memory child = Position(parent.originContract, parent.originTokenId, quantity,
            INFTReserve.Reserve(parent.reserve.token, reserveAmount, parent.reserve.reserveType), id, parent.refundable, parent.burnable);
        parent.originQuantity -= quantity;
        parent.reserve.amount -= reserveAmount;
        uint256 remainingQuantity = parent.originQuantity;
        uint256 remainingReserve = parent.reserve.amount;
        if (remainingQuantity == 0) _close(id);
        childId = _create(child, recipient);
        emit PositionSplit(id, childId, recipient, quantity, reserveAmount, remainingQuantity, remainingReserve);
        if (remainingQuantity != 0) _emitReserve(id);
        _emitReserve(childId);
    }

    function penalty(uint256 id, uint16 percentage) external financial returns (uint256 burnedAmount) {
        if (percentage > 100 || msg.sender != _controller() || _ownerOf(id) != msg.sender) revert ControllerRequired();
        Position storage position = _active(id);
        burnedAmount = Math.mulDiv(position.reserve.amount, uint256(percentage), 100);
        position.reserve.amount -= burnedAmount;
        _vault().totals[position.reserve.reserveType][position.reserve.token] -= burnedAmount;
        _burnReserve(position.reserve.reserveType, position.reserve.token, burnedAmount);
        emit PositionPenalty(id, percentage, burnedAmount, position.reserve.amount);
        _emitReserve(id);
    }
    function addReserveByDEL(uint256 id) external payable financial {
        Position storage position = _active(id);
        if (position.reserve.reserveType != INFTReserve.ReserveType.DEL) revert InvalidCurrency();
        _checkLockedMutation(id);
        position.reserve.amount += msg.value;
        _vault().totals[position.reserve.reserveType][position.reserve.token] += msg.value;
        _emitReserve(id);
    }
    function addReserve(uint256 id, uint256 amount) external financial {
        Position storage position = _active(id);
        if (position.reserve.reserveType != INFTReserve.ReserveType.DRC20) revert InvalidCurrency();
        _checkLockedMutation(id);
        _pull(position.reserve.token, msg.sender, amount);
        position.reserve.amount += amount;
        _vault().totals[position.reserve.reserveType][position.reserve.token] += amount;
        _emitReserve(id);
    }
    function _checkLockedMutation(uint256 id) private view {
        address controller = _controller();
        if (_ownerOf(id) == controller && msg.sender != controller) revert ControllerRequired();
    }
    function redeemPosition(uint256 id, address recipient) external financial {
        Position memory position = _active(id);
        if (msg.sender != _ownerOf(id) || msg.sender == _controller()) revert UnauthorizedPositionOwner();
        if (!position.burnable) revert PositionBurnDisabled();
        if (recipient == address(0) || recipient == address(this)) revert InvalidRecipient();
        _vault().totals[position.reserve.reserveType][position.reserve.token] -= position.reserve.amount;
        _close(id);
        if (position.refundable) _transferReserve(position.reserve, recipient);
        else _burnReserve(position.reserve.reserveType, position.reserve.token, position.reserve.amount);
        emit PositionRedeemed(id, recipient, position.reserve.amount, position.refundable);
    }
    function _close(uint256 id) private {
        _burn(id);
        delete _vault().positions[id];
        _vault().activeCount--;
    }
    function _emitReserve(uint256 id) private {
        Position storage position = _active(id);
        emit PositionReserveUpdated(id, uint8(position.reserve.reserveType), position.reserve.token,
            position.reserve.amount, position.originQuantity);
    }
    function _validateCurrency(INFTReserve.Reserve memory reserve) private pure {
        if (reserve.reserveType == INFTReserve.ReserveType.DEL) {
            if (reserve.token != address(0)) revert InvalidCurrency();
        } else if (reserve.reserveType == INFTReserve.ReserveType.DRC20) {
            if (reserve.token == address(0)) revert InvalidCurrency();
        } else revert InvalidCurrency();
    }
    function _fund(INFTReserve.Reserve memory reserve, address sender) private {
        if (reserve.reserveType == INFTReserve.ReserveType.DEL) {
            if (msg.value != reserve.amount) revert InvalidFunding();
        } else {
            if (msg.value != 0) revert InvalidFunding();
            _pull(reserve.token, sender, reserve.amount);
        }
    }
    function _pull(address token, address sender, uint256 amount) private {
        uint256 beforeBalance = IERC20(token).balanceOf(address(this));
        if (!IERC20(token).transferFrom(sender, address(this), amount) || IERC20(token).balanceOf(address(this)) != beforeBalance + amount) revert InvalidFunding();
    }
    function _transferReserve(INFTReserve.Reserve memory reserve, address recipient) private {
        if (reserve.amount == 0) return;
        if (reserve.reserveType == INFTReserve.ReserveType.DEL) {
            (bool success,) = payable(recipient).call{value:reserve.amount}("");
            if (!success) revert ReserveTransferFailed();
        } else {
            uint256 beforeSender = IERC20(reserve.token).balanceOf(address(this));
            uint256 beforeRecipient = IERC20(reserve.token).balanceOf(recipient);
            if (!IERC20(reserve.token).transfer(recipient, reserve.amount) ||
                IERC20(reserve.token).balanceOf(address(this)) != beforeSender - reserve.amount ||
                IERC20(reserve.token).balanceOf(recipient) != beforeRecipient + reserve.amount) revert ReserveTransferFailed();
        }
    }
    function _burnReserve(INFTReserve.ReserveType kind, address token, uint256 amount) private {
        if (amount == 0) return;
        if (kind == INFTReserve.ReserveType.DEL) {
            (bool success,bytes memory result) = address(0x0900).call(abi.encodeWithSelector(IMintCandyNativeBurn.nativeBurn.selector,amount));
            if (!success || result.length != 32 || !abi.decode(result,(bool))) revert NativeReserveBurnFailed();
        } else {
            uint256 beforeBalance = IERC20(token).balanceOf(address(this));
            uint256 beforeSupply = IERC20(token).totalSupply();
            ERC20Burnable(token).burn(amount);
            if (IERC20(token).balanceOf(address(this)) != beforeBalance - amount || IERC20(token).totalSupply() != beforeSupply - amount) revert ReserveTransferFailed();
        }
    }
    function _update(address to, uint256 id, address auth) internal override returns (address) {
        if (_vault().paused || IMintCandyPositionFactory(_vault().factory).paused()) revert FinancialOperationsPaused();
        address from = _ownerOf(id);
        if (to == _controller() && from != address(0) && msg.sender != to) revert ControllerRequired();
        return super._update(to,id,auth);
    }
    function approve(address to, uint256 id) public override {
        if (_vault().paused || IMintCandyPositionFactory(_vault().factory).paused()) revert FinancialOperationsPaused();
        super.approve(to,id);
    }
    function setApprovalForAll(address operator, bool approved) public override {
        if (_vault().paused || IMintCandyPositionFactory(_vault().factory).paused()) revert FinancialOperationsPaused();
        super.setApprovalForAll(operator,approved);
    }
    receive() external payable { /* Unsolicited native balance is not declared backing. */ }
}
