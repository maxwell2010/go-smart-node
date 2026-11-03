// SPDX-License-Identifier: MIT
pragma solidity 0.8.20;

interface INativeBurnWrapperFixture {
    function deposit() external payable;
    function burn(uint256 amount) external;
}

/// @dev Ordinary financial rollback fixture; never a network system contract.
contract NativeBurnTestTarget {
    INativeBurnWrapperFixture private immutable wrapper;
    constructor(INativeBurnWrapperFixture target) { wrapper = target; }
    function fund() external payable { wrapper.deposit{value: msg.value}(); }
    function fundNative() external payable {}
    function burnWrapped(uint256 amount) external { wrapper.burn(amount); }
    function fundAndBurn(uint256 amount) external payable {
        wrapper.deposit{value: msg.value}();
        wrapper.burn(amount);
    }
    function burnThenRevert(uint256 amount) external {
        wrapper.burn(amount);
        revert("fixture rejects after successful burn");
    }
    receive() external payable {}
}
