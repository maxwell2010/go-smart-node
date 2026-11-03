// SPDX-License-Identifier: MIT
pragma solidity 0.8.20;

import {ERC20Upgradeable} from "./sources/DecimalTokenCenter.sol";
import {ReentrancyGuard} from "./sources/OpenZeppelinReentrancyGuard.sol";

/// @notice NEW fresh-network wrapper, not a reproduction of historical WDEL.
/// Every issued unit is backed by native DEL held at this contract address.
contract MintCandyWrappedDEL is ERC20Upgradeable, ReentrancyGuard {
    error NativeTransferFailed();

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
}
