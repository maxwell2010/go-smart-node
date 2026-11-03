// SPDX-License-Identifier: MIT
pragma solidity 0.8.20;

interface IWrappedDEL {
    function deposit() external payable;
    function withdraw(uint256) external;
}

// Test fixture only, never a fresh-network system contract or authority.
contract WrappedDELTestReceiver {
    IWrappedDEL private immutable wrapper;
    constructor(IWrappedDEL target) { wrapper = target; }
    function fund() external payable { wrapper.deposit{value: msg.value}(); }
    function redeem(uint256 amount) external { wrapper.withdraw(amount); }
    receive() external payable { revert("fixture rejects native transfer"); }
}
