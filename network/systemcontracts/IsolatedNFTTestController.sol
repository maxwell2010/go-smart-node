// SPDX-License-Identifier: MIT
pragma solidity 0.8.20;

import {IERC721Receiver, IERC1155Receiver, IERC165} from "./owned/DecimalNFTCenter.sol";

interface IIsolatedOriginFixture {
    function isolate(uint256 id, uint256 quantity, address recipient) external returns (uint256);
}
interface IPositionFixture {
    function penalty(uint256 id, uint16 percentage) external returns (uint256);
    function splitPosition(uint256 id, uint256 quantity, address recipient) external returns (uint256);
    function addReserveByDEL(uint256 id) external payable;
    function safeTransferFrom(address from, address to, uint256 id) external;
}

/// @dev Ordinary financial fixture only; never a production system controller.
contract IsolatedNFTTestController is IERC721Receiver, IERC1155Receiver {
    address private immutable OWNER;
    IPositionFixture private immutable VAULT;
    constructor(address owner, address vault) { require(owner != address(0) && vault != address(0)); OWNER = owner; VAULT = IPositionFixture(vault); }
    modifier onlyOwner() { require(msg.sender == OWNER, "fixture owner required"); _; }
    function isolate(address origin, uint256 id, uint256 quantity) external onlyOwner returns (uint256) {
        return IIsolatedOriginFixture(origin).isolate(id, quantity, address(this));
    }
    function isolateThenRevert(address origin, uint256 id, uint256 quantity) external onlyOwner {
        IIsolatedOriginFixture(origin).isolate(id, quantity, address(this));
        revert("ordinary fixture rejects after completed isolation");
    }
    function penalty(uint256 id, uint16 percentage) external onlyOwner returns (uint256) { return VAULT.penalty(id, percentage); }
    function split(uint256 id, uint256 quantity, address recipient) external onlyOwner returns (uint256) { return VAULT.splitPosition(id, quantity, recipient); }
    function release(uint256 id, address recipient) external onlyOwner { VAULT.safeTransferFrom(address(this), recipient, id); }
    function topup(uint256 id) external payable onlyOwner { VAULT.addReserveByDEL{value:msg.value}(id); }
    function onERC721Received(address, address, uint256, bytes calldata) external pure returns (bytes4) { return IERC721Receiver.onERC721Received.selector; }
    function onERC1155Received(address, address, uint256, uint256, bytes calldata) external pure returns (bytes4) { return IERC1155Receiver.onERC1155Received.selector; }
    function onERC1155BatchReceived(address, address, uint256[] calldata, uint256[] calldata, bytes calldata) external pure returns (bytes4) { return IERC1155Receiver.onERC1155BatchReceived.selector; }
    function supportsInterface(bytes4 interfaceId) external pure returns (bool) { return interfaceId == type(IERC165).interfaceId || interfaceId == type(IERC1155Receiver).interfaceId || interfaceId == type(IERC721Receiver).interfaceId; }
}
