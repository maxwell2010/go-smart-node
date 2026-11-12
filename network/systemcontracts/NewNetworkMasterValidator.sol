// SPDX-License-Identifier: MIT
pragma solidity 0.8.20;

import {DecimalMasterValidator} from "./sources/DecimalMasterValidator.sol";

/// @notice Fresh-network initialization extension using verified native accessors.
contract NewNetworkMasterValidator is DecimalMasterValidator {
    error InvalidFreshInitialization();
    error InvalidPenaltyReadRange();

    error InvalidDuplicateVotePenalty();

    event ValidatorDuplicateVotePenalty(
        address indexed validator, uint256 indexed penaltyIndex, uint256 indexed infractionHeight, uint16 percentage
    );

    struct DuplicateVoteStorage {
        mapping(address => mapping(uint256 => uint256)) heightByIndex;
        mapping(address => mapping(uint256 => bool)) recordedHeight;
    }

    bytes32 private constant DUPLICATE_VOTE_STORAGE = keccak256("mintcandy.master-validator.duplicate-vote.v1");

    function _duplicateVoteStorage() private pure returns (DuplicateVoteStorage storage state) {
        bytes32 slot = DUPLICATE_VOTE_STORAGE;
        assembly { state.slot := slot }
    }

    constructor() { _disableInitializers(); }

    // Executed by NewNetworkProxyBootstrap while its constructor-only
    // initializer is active and after explicit owner initialization.
    function initializeFresh(address registry) external onlyInitializing {
        if (address(this).code.length != 0 || registry == address(0) ||
            registry.code.length == 0 || owner() == address(0) ||
            _getValidatorStorage()._contractCenter != address(0)) {
            revert InvalidFreshInitialization();
        }
        _getValidatorStorage()._contractCenter = registry;
    }

    function contractCenter() external view returns (address) {
        return _getValidatorStorage()._contractCenter;
    }

    /// @notice Fixed-size validator header; never copies the penalty array.
    function getValidatorState(address validator) external view returns (
        ValidatorStatus status, bool paused, uint256 penaltyCount
    ) {
        Validator storage state = _getValidatorStorage()._validators[validator];
        return (state.status, state.paused, state.penaltyPercantages.length);
    }

    /// @notice Bounded readers for native reconciliation; no monetary change.
    function getPenaltyCount(address validator) external view returns (uint256) {
        return _getValidatorStorage()._validators[validator].penaltyPercantages.length;
    }

    /// @notice Records the height supplied by the native evidence coordinator.
    /// The Gateway must reconcile this typed entry against historical exposure.
    function appendDuplicateVotePenalty(
        address validator, uint256 expectedCount, uint256 infractionHeight, uint16 percentage
    ) external onlyOwner whenNotPaused {
        Validator storage state = _getValidatorStorage()._validators[validator];
        DuplicateVoteStorage storage evidence = _duplicateVoteStorage();
        if ((state.status != ValidatorStatus.Candidate && state.status != ValidatorStatus.Approved) ||
            percentage == 0 || percentage > 100 || infractionHeight == 0 || infractionHeight >= block.number ||
            state.penaltyPercantages.length != expectedCount || evidence.recordedHeight[validator][infractionHeight]) {
            revert InvalidDuplicateVotePenalty();
        }
        if (!state.paused) _setPause(validator, true);
        state.penaltyPercantages.push(percentage);
        evidence.heightByIndex[validator][expectedCount] = infractionHeight;
        evidence.recordedHeight[validator][infractionHeight] = true;
        emit ValidatorDuplicateVotePenalty(validator, expectedCount, infractionHeight, percentage);
    }

    /// @notice Zero identifies a legacy percentage entry at a known index.
    function getDuplicateVotePenaltyHeight(address validator, uint256 penaltyIndex) external view returns (uint256) {
        return _duplicateVoteStorage().heightByIndex[validator][penaltyIndex];
    }

    function getPenaltiesRange(
        address validator,
        uint256 start,
        uint256 limit
    ) external view returns (uint16[] memory result) {
        uint16[] storage penalties = _getValidatorStorage()._validators[validator].penaltyPercantages;
        if (limit > 256 || start > penalties.length) revert InvalidPenaltyReadRange();
        uint256 remaining = penalties.length - start;
        uint256 count = limit < remaining ? limit : remaining;
        result = new uint16[](count);
        for (uint256 i; i < count; ++i) {
            result[i] = penalties[start + i];
        }
    }
}
