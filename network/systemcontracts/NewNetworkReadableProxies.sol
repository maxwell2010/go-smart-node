// SPDX-License-Identifier: MIT
pragma solidity 0.8.20;

import {BeaconProxy as TokenBeaconProxy} from "./owned/DecimalTokenCenter.sol";
import {BeaconProxy as NFTBeaconProxy} from "./owned/DecimalNFTCenter.sol";
import {NewNetworkPausedViews} from "./NewNetworkPausedViews.sol";

interface IFreshReadableCenter {
    function paused() external view returns (bool);
    function readImplementation(uint8 family) external view returns (address);
}

library NewNetworkPauseResolver {
    error FactoryPaused();
    error InvalidReadableImplementation();
    function resolve(address implementation, address factory, uint8 family, bytes4 selector) internal view returns (address) {
        if (!IFreshReadableCenter(factory).paused()) {
            if (implementation.code.length == 0) revert InvalidReadableImplementation();
            return implementation;
        }
        if (!NewNetworkPausedViews.allowed(selector, family)) {
            revert FactoryPaused();
        }
        address readable = IFreshReadableCenter(factory).readImplementation(family);
        if (readable.code.length == 0) revert InvalidReadableImplementation();
        return readable;
    }
}

contract NewNetworkTokenReadableProxy is TokenBeaconProxy {
    address public immutable FACTORY;
    uint8 public immutable FAMILY;
    constructor(address beacon, bytes memory data, address factory, uint8 family) payable TokenBeaconProxy(beacon, data) {
        require(factory != address(0) && family < 2, "Invalid token proxy profile");
        FACTORY = factory; FAMILY = family;
    }
    function _implementation() internal view override returns (address) {
        return NewNetworkPauseResolver.resolve(super._implementation(), FACTORY, FAMILY, msg.sig);
    }
    function getBeacon() external view returns (address) { return _getBeacon(); }
}

contract NewNetworkNFTReadableProxy is NFTBeaconProxy {
    address public immutable FACTORY;
    uint8 public immutable FAMILY;
    constructor(address beacon, bytes memory data, address factory, uint8 family) payable NFTBeaconProxy(beacon, data) {
        require(factory != address(0) && family >= 2 && family < 6, "Invalid NFT proxy profile");
        FACTORY = factory; FAMILY = family;
    }
    function _implementation() internal view override returns (address) {
        return NewNetworkPauseResolver.resolve(super._implementation(), FACTORY, FAMILY, msg.sig);
    }
    function getBeacon() external view returns (address) { return _getBeacon(); }
    receive() external payable {
        if (IFreshReadableCenter(FACTORY).paused()) revert NewNetworkPauseResolver.FactoryPaused();
    }
}
