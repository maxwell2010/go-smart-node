// SPDX-License-Identifier: MIT
pragma solidity 0.8.20;

import {ERC20Upgradeable} from "./sources/DecimalTokenCenter.sol";
import {ReentrancyGuard} from "./sources/OpenZeppelinReentrancyGuard.sol";

interface IMintCandyNativeBurn {
    event NativeBurn(address indexed account, uint256 amount);
    function nativeBurn(uint256 amount) external returns (bool);
}

/// @notice NEW fresh-network wrapper, not a reproduction of historical WDEL.
/// Every issued unit is backed by native DEL held at this contract address.
contract MintCandyWrappedDEL is ERC20Upgradeable, ReentrancyGuard {
    error NativeTransferFailed();
    error NativeBurnFailed();

    IMintCandyNativeBurn private constant NATIVE_BURN =
        IMintCandyNativeBurn(0x0000000000000000000000000000000000000900);

    constructor() initializer {
        __ERC20_init("MintCandy Wrapped DEL", "WDEL");
    }

    function deposit() public payable {
        _mint(msg.sender, msg.value);
    }

    receive() external payable {
        deposit();
    }

    function withdraw(uint256 amount) external nonReentrant {
        _burn(msg.sender, amount);
        (bool success,) = payable(msg.sender).call{value: amount}("");
        if (!success) revert NativeTransferFailed();
    }

    /// @notice Irreversibly burn the caller's WDEL and equal native DEL backing.
    /// The bool return is mandatory: an inactive precompile must revert decode,
    /// never silently leave native funds locked after an ERC20-only burn.
    function burn(uint256 amount) external nonReentrant {
        _burn(msg.sender, amount);
        if (!NATIVE_BURN.nativeBurn(amount)) revert NativeBurnFailed();
    }
}
