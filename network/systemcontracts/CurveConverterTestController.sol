// SPDX-License-Identifier: MIT
pragma solidity 0.8.20;

import {IERC20} from "./sources/DecimalTokenCenter.sol";
interface IOrdinaryCurveConverter {
    function convert(address token, uint256 amount, uint256 minimum, uint256 deadline) external returns (uint256);
}

/// @dev Ordinary financial receiver fixture; never a production system role.
contract CurveConverterTestController {
    address private immutable OWNER;
    IOrdinaryCurveConverter private immutable CONVERTER;
    bool public rejectNative;
    constructor(address owner, address converter) {
        require(owner != address(0) && converter != address(0));
        OWNER = owner;
        CONVERTER = IOrdinaryCurveConverter(converter);
    }
    modifier onlyOwner() { require(msg.sender == OWNER, "fixture owner required"); _; }
    function setRejectNative(bool rejected) external onlyOwner { rejectNative = rejected; }
    function convert(address token, uint256 amount, uint256 minimum, uint256 deadline) external onlyOwner returns (uint256) {
        require(IERC20(token).approve(address(CONVERTER), amount));
        return CONVERTER.convert(token, amount, minimum, deadline);
    }
    function convertThenRevert(address token, uint256 amount, uint256 minimum, uint256 deadline) external onlyOwner {
        require(IERC20(token).approve(address(CONVERTER), amount));
        CONVERTER.convert(token, amount, minimum, deadline);
        revert("ordinary late financial rejection");
    }
    receive() external payable { require(!rejectNative, "ordinary native recipient rejection"); }
}
