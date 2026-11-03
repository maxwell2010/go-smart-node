// SPDX-License-Identifier: MIT
pragma solidity 0.8.20;

import {
    DRC1155, ERC1155SupplyUpgradeable, IERC20, IERC20Permit, ERC20Burnable,
    IDecimalContractCenter, INFTReserve
} from "./owned/DecimalNFTCenter.sol";
import {MAX_ISOLATED_ORIGIN_UNITS} from "./MintCandyNFTIsolationLimits.sol";

interface IMintCandyPositionIssuer {
    function maxOriginQuantity() external view returns (uint256);
    function mintFromOrigin(uint256 originId, uint256 quantity, address recipient) external payable returns (uint256 positionId);
}

/// @notice Uniform fungible origin units; every staked position is isolated.
contract MintCandyIsolatedDRC1155 is DRC1155 {
    struct UnitReserve { bool initialized; Reserve reserve; }
    struct IsolationStorage {
        bool entered;
        bool isolating;
        mapping(uint256 => UnitReserve) units;
        mapping(ReserveType => mapping(address => uint256)) totals;
    }
    bytes32 private constant ISOLATION_STORAGE = keccak256("mintcandy.storage.IsolatedDRC1155.v1");
    function _isolation() private pure returns (IsolationStorage storage state) {
        bytes32 location = ISOLATION_STORAGE;
        assembly { state.slot := location }
    }
    error NonUniformReserve();
    error IsolationRequired();
    error InvalidIsolationRecipient();
    error ReentrantIsolationOperation();
    error ReserveFundingFailed();
    event OriginIsolated(uint256 indexed originTokenId, uint256 indexed positionId,
        address indexed originHolder, address receiptOwner, uint256 quantity, uint256 reserveAmount);

    constructor() { _disableInitializers(); }
    modifier isolatedFinancial() {
        IsolationStorage storage state = _isolation();
        if (state.entered) revert ReentrantIsolationOperation();
        state.entered = true;
        _;
        state.entered = false;
    }
    function _registry() private view returns (IDecimalContractCenter) {
        return IDecimalContractCenter(_getNFTCenter().getContractCenter());
    }
    function isolationVault() public view returns (address result) {
        result = _registry().getContractAddress("nft-position-vault");
        if (result.code.length == 0) revert IsolationRequired();
    }
    function getIsolationUnitReserve(uint256 tokenId) external view returns (Reserve memory) {
        UnitReserve storage unit = _isolation().units[tokenId];
        if (!unit.initialized) revert InvalidReserve();
        return unit.reserve;
    }
    function getIsolationPolicy(uint256 tokenId) external view returns (bool initialized, bool refundable, bool burnable) {
        return (_isolation().units[tokenId].initialized, getRefundable(), getBurnable());
    }
    function declaredReserve(ReserveType kind, address token) external view returns (uint256) {
        return _isolation().totals[kind][token];
    }
    function rate(uint256 tokenId) public view override returns (uint256) {
        UnitReserve storage unit = _isolation().units[tokenId];
        if (!unit.initialized) revert InvalidReserve();
        return unit.reserve.amount;
    }

    function mintByDEL(address recipient, uint256 tokenId, uint256 quantity, string memory tokenURI)
        public payable override onlyCreator isolatedFinancial {
        _mintWithReserve(recipient, tokenId, quantity, tokenURI, Reserve(address(0), msg.value, ReserveType.DEL));
    }
    function mint(address recipient, uint256 tokenId, uint256 quantity, string memory tokenURI,
        uint256 amount, address token) public override onlyCreator isolatedFinancial {
        _fundMint(recipient, tokenId, quantity, tokenURI, amount, token);
    }
    function mintByPermit(address recipient, uint256 tokenId, uint256 quantity, string memory tokenURI,
        uint256 amount, address token, uint256 deadline, uint8 v, bytes32 r, bytes32 s)
        public override onlyCreator isolatedFinancial {
        _approvedToken(token);
        IERC20Permit(token).permit(msg.sender, address(this), amount, deadline, v, r, s);
        _fundMint(recipient, tokenId, quantity, tokenURI, amount, token);
    }
    function _approvedToken(address token) private view {
        if (token.code.length == 0) revert InvalidReserveType();
        address wrapped = _registry().getContractAddress("wdel");
        if (token != wrapped) _getNFTCenter().checkToken(token);
    }
    function _fundMint(address recipient, uint256 tokenId, uint256 quantity, string memory tokenURI,
        uint256 amount, address token) private {
        _approvedToken(token);
        uint256 beforeBalance = IERC20(token).balanceOf(address(this));
        if (!IERC20(token).transferFrom(msg.sender, address(this), amount) ||
            IERC20(token).balanceOf(address(this)) != beforeBalance + amount) revert ReserveFundingFailed();
        _mintWithReserve(recipient, tokenId, quantity, tokenURI, Reserve(token, amount, ReserveType.DRC20));
    }
    function _mintWithReserve(address recipient, uint256 tokenId, uint256 quantity,
        string memory tokenURI, Reserve memory reserve) internal override {
        if (quantity == 0) revert NonUniformReserve();
        UnitReserve storage unit = _isolation().units[tokenId];
        if (!unit.initialized) {
            if (getReserve(tokenId).reserveType != ReserveType.None || totalSupply(tokenId) != 0) revert IsolationRequired();
            if (reserve.amount % quantity != 0 || reserve.amount / quantity < MIN_RESERVE) revert NonUniformReserve();
            if ((reserve.reserveType == ReserveType.DEL && reserve.token != address(0)) ||
                (reserve.reserveType == ReserveType.DRC20 && reserve.token == address(0)) ||
                reserve.reserveType == ReserveType.None) revert InvalidReserveType();
            unit.initialized = true;
            unit.reserve = Reserve(reserve.token, reserve.amount / quantity, reserve.reserveType);
        } else {
            _checkUniform(tokenId, unit);
            if (reserve.reserveType != unit.reserve.reserveType || reserve.token != unit.reserve.token ||
                reserve.amount != unit.reserve.amount * quantity) revert NonUniformReserve();
        }
        _isolation().totals[reserve.reserveType][reserve.token] += reserve.amount;
        super._mintWithReserve(recipient, tokenId, quantity, tokenURI, reserve);
        // Base emits before minting; this final snapshot includes actual supply.
        _emitReserve(tokenId);
    }

    function addReserveByDEL(uint256 tokenId) public payable override isolatedFinancial {
        UnitReserve storage unit = _uniformTopup(tokenId, msg.value);
        if (unit.reserve.reserveType != ReserveType.DEL) revert InvalidReserveType();
        _addReserve(tokenId, msg.value);
    }
    function addReserve(uint256 tokenId, uint256 amount) public override isolatedFinancial {
        UnitReserve storage unit = _uniformTopup(tokenId, amount);
        if (unit.reserve.reserveType != ReserveType.DRC20) revert InvalidReserveType();
        uint256 beforeBalance = IERC20(unit.reserve.token).balanceOf(address(this));
        if (!IERC20(unit.reserve.token).transferFrom(msg.sender, address(this), amount) ||
            IERC20(unit.reserve.token).balanceOf(address(this)) != beforeBalance + amount) revert ReserveFundingFailed();
        _addReserve(tokenId, amount);
    }
    function _uniformTopup(uint256 tokenId, uint256 amount) private returns (UnitReserve storage unit) {
        unit = _isolation().units[tokenId];
        uint256 supply = totalSupply(tokenId);
        if (!unit.initialized || supply == 0 || amount % supply != 0) revert NonUniformReserve();
        _checkUniform(tokenId, unit);
        unit.reserve.amount += amount / supply;
        _isolation().totals[unit.reserve.reserveType][unit.reserve.token] += amount;
    }
    function _checkUniform(uint256 tokenId, UnitReserve storage unit) private view {
        Reserve memory pool = getReserve(tokenId);
        if (pool.reserveType != unit.reserve.reserveType || pool.token != unit.reserve.token ||
            pool.amount != unit.reserve.amount * totalSupply(tokenId)) revert NonUniformReserve();
    }

    function isolate(uint256 tokenId, uint256 quantity, address recipient) external isolatedFinancial returns (uint256 positionId) {
        UnitReserve storage unit = _isolation().units[tokenId];
        if (!unit.initialized || quantity == 0 || quantity > MAX_ISOLATED_ORIGIN_UNITS) revert IsolationRequired();
        _checkUniform(tokenId, unit);
        address vault = isolationVault();
        if (IMintCandyPositionIssuer(vault).maxOriginQuantity() != MAX_ISOLATED_ORIGIN_UNITS) revert IsolationRequired();
        IDecimalContractCenter registry = _registry();
        address controller = registry.getContractAddress("delegation");
        if (controller.code.length == 0 || controller != registry.getContractAddress("delegation-nft")) revert IsolationRequired();
        if (recipient == address(0) || (recipient == controller && msg.sender != controller)) revert InvalidIsolationRecipient();
        uint256 reserveAmount = unit.reserve.amount * quantity;
        _isolation().isolating = true;
        _burn(msg.sender, tokenId, quantity);
        _isolation().isolating = false;
        _removeReserve(tokenId, reserveAmount);
        _isolation().totals[unit.reserve.reserveType][unit.reserve.token] -= reserveAmount;
        if (unit.reserve.reserveType == ReserveType.DEL) {
            positionId = IMintCandyPositionIssuer(vault).mintFromOrigin{value:reserveAmount}(tokenId, quantity, recipient);
        } else {
            if (!IERC20(unit.reserve.token).approve(vault, reserveAmount)) revert ReserveFundingFailed();
            positionId = IMintCandyPositionIssuer(vault).mintFromOrigin(tokenId, quantity, recipient);
            if (!IERC20(unit.reserve.token).approve(vault, 0)) revert ReserveFundingFailed();
        }
        if (positionId == 0) revert IsolationRequired();
        emit OriginIsolated(tokenId, positionId, msg.sender, recipient, quantity, reserveAmount);
    }
    function _update(address from, address to, uint256[] memory ids, uint256[] memory amounts) internal override {
        if (_isolation().isolating) ERC1155SupplyUpgradeable._update(from, to, ids, amounts);
        else super._update(from, to, ids, amounts);
    }
    function penalty(uint256, uint256, uint16) public pure override returns (uint256) {
        revert IsolationRequired();
    }
    function burn(uint256 tokenId, uint256 quantity) external override isolatedFinancial {
        UnitReserve storage unit = _isolation().units[tokenId];
        if (!unit.initialized || quantity == 0) revert InvalidReserve();
        _checkUniform(tokenId, unit);
        uint256 amount = unit.reserve.amount * quantity;
        _burn(msg.sender, tokenId, quantity);
        _removeReserve(tokenId, amount);
        _isolation().totals[unit.reserve.reserveType][unit.reserve.token] -= amount;
        if (unit.reserve.reserveType == ReserveType.DEL) {
            if (getRefundable()) {
                (bool success,) = payable(msg.sender).call{value:amount}("");
                if (!success) revert ReserveFundingFailed();
            } else _burnNativeReserve(amount);
        } else {
            uint256 beforeBalance = IERC20(unit.reserve.token).balanceOf(address(this));
            if (getRefundable()) {
                uint256 beforeRecipient = IERC20(unit.reserve.token).balanceOf(msg.sender);
                if (!IERC20(unit.reserve.token).transfer(msg.sender, amount) ||
                    IERC20(unit.reserve.token).balanceOf(msg.sender) != beforeRecipient + amount) revert ReserveFundingFailed();
            } else ERC20Burnable(unit.reserve.token).burn(amount);
            if (IERC20(unit.reserve.token).balanceOf(address(this)) != beforeBalance - amount) revert ReserveFundingFailed();
        }
    }
}
