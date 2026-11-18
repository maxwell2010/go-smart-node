// SPDX-License-Identifier: MIT
pragma solidity 0.8.20;

/// @notice CI-only deposit prototype. NOT a production bridge: there is no
/// release, refund, administrator, verifier bypass or upgrade mechanism.
/// Depositing real funds would lock them with no implemented exit. Do not deploy
/// this prototype for production or send real funds to it.
contract DecimalNativeDELEscrow {
    // These exact persistent slots are part of the versioned proof protocol.
    mapping(bytes32 => bytes32) public commitments; // slot 0
    mapping(address => uint256) public senderNonces; // slot 1

    uint64 public immutable destinationEVMChainID;
    bytes32 public immutable destinationBridgeID;
    bytes32 public constant DEPOSIT_DOMAIN = keccak256("MINTCANDY_DECIMAL_BRIDGE_V1_DEPOSIT");

    event Deposit(bytes32 indexed operationID, address indexed sender, address indexed recipient, uint256 amount, uint256 nonce);

    constructor(uint64 destinationChain, bytes32 destinationBridge) {
        require(block.chainid > 0 && block.chainid <= type(uint64).max, "source domain");
        require(destinationChain > 0 && destinationChain != block.chainid, "destination domain");
        require(destinationBridge != bytes32(0), "destination bridge");
        destinationEVMChainID = destinationChain;
        destinationBridgeID = destinationBridge;
    }

    function deposit(address recipient) external payable returns (bytes32 operationID) {
        require(msg.sender != address(0) && recipient != address(0), "sender or recipient");
        require(msg.value > 0, "zero deposit");
        require(block.chainid > 0 && block.chainid <= type(uint64).max && block.chainid != destinationEVMChainID, "source domain");
        uint256 nonce = senderNonces[msg.sender];
        operationID = keccak256(abi.encode(DEPOSIT_DOMAIN, block.chainid, uint256(destinationEVMChainID),
            address(this), destinationBridgeID, msg.sender, recipient, msg.value, nonce));
        require(commitments[operationID] == bytes32(0), "duplicate commitment");
        senderNonces[msg.sender] = nonce + 1;
        commitments[operationID] = operationID;
        emit Deposit(operationID, msg.sender, recipient, msg.value, nonce);
    }
}
