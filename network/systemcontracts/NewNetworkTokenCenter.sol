// SPDX-License-Identifier: MIT
pragma solidity 0.8.20;

import {DecimalTokenCenter, DRC20, DRC20Reserveless} from "./owned/DecimalTokenCenter.sol";

import {NewNetworkTokenReadableProxy} from "./NewNetworkReadableProxies.sol";

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
    function readImplementation(uint8 family) external view returns (address) {
        if (family == 0) return _getTokenCenterStorage()._tokenImpl;
        if (family == 1) return _getTokenCenterStorage()._tokenReservelessImpl;
        revert InvalidFreshInitialization();
    }

    function predict(address beaconAddress, bytes32 salt, bytes memory initializationArgs) public view override returns (address) {
        uint8 family = beaconAddress == address(this) ? 0 : 1;
        bytes memory code = abi.encodePacked(type(NewNetworkTokenReadableProxy).creationCode,
            abi.encode(beaconAddress, initializationArgs, address(this), family));
        return address(uint160(uint256(keccak256(abi.encodePacked(bytes1(0xff), address(this), salt, keccak256(code))))));
    }

    function _createToken(Token memory meta, uint256 reserve) internal override returns (address) {
        if (tokens(meta.symbol) != address(0)) revert TokenSymbolExist();
        bytes memory initializationArgs = abi.encodeWithSelector(DRC20.initialize.selector,
            meta.name, meta.symbol, meta.creator, meta.crr, meta.initialMint,
            meta.minTotalSupply, meta.maxTotalSupply, meta.identity);
        bytes32 salt = keccak256(abi.encodePacked(meta.symbol));
        address expected = predict(address(this), salt, initializationArgs);
        emit TokenDeployed(expected, meta);
        address actual = address(new NewNetworkTokenReadableProxy{value:reserve,salt:salt}(
            address(this), initializationArgs, address(this), 0));
        if (actual != expected) revert DeployTokenError();
        _getTokenCenterStorage()._tokens[meta.symbol] = actual;
        return actual;
    }

    function createTokenReserveless(string memory name, string memory symbol, bool mintable, bool burnable,
        uint256 initialMint, uint256 cap, string memory identity) external override whenNotPaused returns (address) {
        require(validateSymbol(symbol), "Invalid token symbol");
        bytes memory initializationArgs = abi.encodeWithSelector(DRC20Reserveless.initialize.selector,
            name, symbol, _msgSender(), mintable, burnable, initialMint, cap, identity);
        bytes32 salt = keccak256(abi.encodePacked(block.timestamp, name));
        address beacon = _getTokenCenterStorage()._tokenReservelessBeacon;
        address expected = predict(beacon, salt, initializationArgs);
        emit TokenReservelessDeployed(expected);
        address actual = address(new NewNetworkTokenReadableProxy{salt:salt}(beacon, initializationArgs, address(this), 1));
        if (actual != expected) revert DeployTokenError();
        return actual;
    }

}
