// SPDX-License-Identifier: MIT
pragma solidity 0.8.20;

import {IERC20, IERC20Metadata} from "./sources/DecimalTokenCenter.sol";
import {ReentrancyGuard} from "./sources/OpenZeppelinReentrancyGuard.sol";

interface IMintCandyConversionRegistry {
    function getContractAddress(string calldata role) external view returns (address);
}
interface IMintCandyConversionCenter {
    function paused() external view returns (bool);
    function getContractCenter() external view returns (address);
    function isTokenExists(address token) external view returns (bool);
    function tokens(string calldata symbol) external view returns (address);
}
interface IMintCandyConversionTokenProxy {
    function FACTORY() external view returns (address);
    function FAMILY() external view returns (uint8);
}
interface IMintCandyReserveCurve {
    function minTotalSupply() external view returns (uint256);
    function reserve() external view returns (uint256);
    function MIN_RESERVE() external view returns (uint256);
    function calculateSellOutput(uint256 amount) external view returns (uint256);
    function sellExactTokensForDEL(uint256 amount, uint256 minimum, address recipient) external;
}
interface IMintCandyUnwrapDEL {
    function withdraw(uint256 amount) external;
}

/// @notice Explicit entry conversion only. Every caller must fund its own input.
/// @dev Raw receive is deliberately stipend-safe for the pinned curve's transfer.
contract MintCandyCurveConverter is ReentrancyGuard {
    address public immutable REGISTRY;

    error ConversionNotConfigured();
    error UnauthorizedController();
    error InvalidConversionLimits();
    error ConversionDeadlineExpired();
    error UnapprovedReserveCurve();
    error ConversionLiquidityUnavailable();
    error UnbackedWrappedCurrency();
    error ConversionTokenTransferFailed();
    error ConversionNativeTransferFailed();
    error UnexpectedConversionDelta();

    event CollateralConverted(address indexed caller, address indexed inputToken,
        address indexed tokenCenter, uint256 inputAmount, uint256 nativeOutput,
        uint256 minimumNativeOutput, uint256 deadline, bool wrappedInput);

    constructor(address registry) {
        if (registry.code.length == 0) revert ConversionNotConfigured();
        REGISTRY = registry;
    }

    function _registry() private view returns (IMintCandyConversionRegistry) {
        if (IMintCandyConversionRegistry(REGISTRY).getContractAddress("curve-converter") != address(this)) revert ConversionNotConfigured();
        return IMintCandyConversionRegistry(REGISTRY);
    }
    function _controller() private view {
        IMintCandyConversionRegistry registry = _registry();
        address gateway = registry.getContractAddress("delegation");
        address vault = registry.getContractAddress("nft-position-vault");
        if (gateway.code.length == 0 || vault.code.length == 0 ||
            gateway != registry.getContractAddress("delegation-nft") ||
            (msg.sender != gateway && msg.sender != vault)) revert UnauthorizedController();
    }

    function preview(address token, uint256 amount, uint256 minimumNativeOutput,
        uint256 deadline) external view returns (uint256 nativeQuote) {
        (nativeQuote,,) = _preview(token, amount, minimumNativeOutput, deadline);
    }
    function _preview(address token, uint256 amount, uint256 minimumNativeOutput,
        uint256 deadline) private view returns (uint256 nativeQuote, address center, bool wrapped) {
        if (token.code.length == 0 || amount == 0 || minimumNativeOutput == 0) revert InvalidConversionLimits();
        if (deadline < block.timestamp) revert ConversionDeadlineExpired();
        IMintCandyConversionRegistry registry = _registry();
        wrapped = token == registry.getContractAddress("wdel");
        uint256 supply = IERC20(token).totalSupply();
        if (wrapped) {
            if (supply < amount || token.balance < supply) revert UnbackedWrappedCurrency();
            nativeQuote = amount;
        } else {
            center = registry.getContractAddress("token-center");
            if (center.code.length == 0 || IMintCandyConversionCenter(center).paused() ||
                IMintCandyConversionCenter(center).getContractCenter() != REGISTRY ||
                !IMintCandyConversionCenter(center).isTokenExists(token) ||
                IMintCandyConversionCenter(center).tokens(IERC20Metadata(token).symbol()) != token ||
                IMintCandyConversionTokenProxy(token).FACTORY() != center ||
                IMintCandyConversionTokenProxy(token).FAMILY() != 0) revert UnapprovedReserveCurve();
            IMintCandyReserveCurve curve = IMintCandyReserveCurve(token);
            uint256 minimumSupply = curve.minTotalSupply();
            if (minimumSupply == 0 || supply < minimumSupply || amount > supply - minimumSupply) revert ConversionLiquidityUnavailable();
            uint256 nativeReserve = curve.reserve();
            if (nativeReserve != token.balance) revert ConversionLiquidityUnavailable();
            nativeQuote = curve.calculateSellOutput(amount);
            uint256 minimumReserve = curve.MIN_RESERVE();
            if (minimumReserve == 0 || nativeQuote > nativeReserve || nativeReserve - nativeQuote < minimumReserve) revert ConversionLiquidityUnavailable();
        }
        if (nativeQuote == 0 || nativeQuote < minimumNativeOutput) revert ConversionLiquidityUnavailable();
    }

    struct ConversionSnapshot {
        uint256 quote;
        address center;
        bool wrapped;
        uint256 inputBalance;
        uint256 callerBalance;
        uint256 supply;
        uint256 reserve;
        uint256 nativeBalance;
    }

    function _pullExact(address token, uint256 amount, ConversionSnapshot memory beforeState) private {
        if (beforeState.callerBalance < amount || !IERC20(token).transferFrom(msg.sender, address(this), amount) ||
            IERC20(token).balanceOf(address(this)) != beforeState.inputBalance + amount ||
            IERC20(token).balanceOf(msg.sender) != beforeState.callerBalance - amount) revert ConversionTokenTransferFailed();
    }

    function _validateResult(address token, uint256 amount, uint256 minimumNativeOutput,
        ConversionSnapshot memory beforeState) private view returns (uint256 nativeOutput) {
        uint256 afterNative = address(this).balance;
        if (afterNative < beforeState.nativeBalance) revert UnexpectedConversionDelta();
        nativeOutput = afterNative - beforeState.nativeBalance;
        if (nativeOutput == 0 || nativeOutput != beforeState.quote || nativeOutput < minimumNativeOutput ||
            IERC20(token).balanceOf(address(this)) != beforeState.inputBalance ||
            IERC20(token).totalSupply() != beforeState.supply - amount ||
            token.balance != beforeState.reserve - nativeOutput) revert UnexpectedConversionDelta();
    }

    function convert(address token, uint256 amount, uint256 minimumNativeOutput,
        uint256 deadline) external nonReentrant returns (uint256 nativeOutput) {
        _controller();
        ConversionSnapshot memory beforeState;
        (beforeState.quote, beforeState.center, beforeState.wrapped) = _preview(token, amount, minimumNativeOutput, deadline);
        beforeState.inputBalance = IERC20(token).balanceOf(address(this));
        beforeState.callerBalance = IERC20(token).balanceOf(msg.sender);
        beforeState.supply = IERC20(token).totalSupply();
        beforeState.reserve = token.balance;
        beforeState.nativeBalance = address(this).balance;
        _pullExact(token, amount, beforeState);
        if (beforeState.wrapped) IMintCandyUnwrapDEL(token).withdraw(amount);
        else IMintCandyReserveCurve(token).sellExactTokensForDEL(amount, minimumNativeOutput, address(this));
        nativeOutput = _validateResult(token, amount, minimumNativeOutput, beforeState);
        (bool sent,) = payable(msg.sender).call{value:nativeOutput}("");
        if (!sent || address(this).balance != beforeState.nativeBalance) revert ConversionNativeTransferFailed();
        emit CollateralConverted(msg.sender, token, beforeState.center, amount, nativeOutput,
            minimumNativeOutput, deadline, beforeState.wrapped);
    }
    receive() external payable {}
}
