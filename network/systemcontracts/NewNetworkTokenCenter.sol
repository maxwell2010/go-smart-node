// SPDX-License-Identifier: MIT
pragma solidity 0.8.20;

import {DecimalTokenCenter} from "./sources/DecimalTokenCenter.sol";

contract NewNetworkReservelessTokenBeacon {
    DecimalTokenCenter public immutable TOKEN_CENTER;
    constructor(address center) {
        require(center != address(0), "Zero token center");
        TOKEN_CENTER = DecimalTokenCenter(payable(center));
    }
    function implementation() external view returns (address) {
        return TOKEN_CENTER.implementationReserveless();
    }
}

/// @notice Initializes the missing fresh reserveless beacon through verified accessors.
contract NewNetworkTokenCenter is DecimalTokenCenter {
    error InvalidFreshInitialization();
    constructor() { _disableInitializers(); }
    function initializeFresh(address registry) external onlyInitializing {
        TokenCenterStorage storage state = _getTokenCenterStorage();
        if (address(this).code.length != 0 || registry == address(0) ||
            registry.code.length == 0 || owner() == address(0) ||
            state._contractCenter != address(0) || state._tokenReservelessBeacon != address(0)) {
            revert InvalidFreshInitialization();
        }
        state._contractCenter = registry;
        state._tokenReservelessBeacon = address(new NewNetworkReservelessTokenBeacon(address(this)));
    }
    function reservelessBeacon() external view returns (address) {
        return _getTokenCenterStorage()._tokenReservelessBeacon;
    }
}
