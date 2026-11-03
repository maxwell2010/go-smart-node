// SPDX-License-Identifier: MIT
pragma solidity 0.8.20;

// Generated from pinned compiler ABIs; build.py verifies the exact selector policy.
library NewNetworkPausedViews {
    function allowed(bytes4 selector, uint8 family) internal pure returns (bool) {
        if (family == 0) return
            selector == bytes4(keccak256("DOMAIN_SEPARATOR()")) ||
            selector == bytes4(keccak256("MAX_CRR()")) ||
            selector == bytes4(keccak256("MAX_TOTAL_SUPPLY()")) ||
            selector == bytes4(keccak256("MIN_CRR()")) ||
            selector == bytes4(keccak256("MIN_INITIAL_MINT()")) ||
            selector == bytes4(keccak256("MIN_RESERVE()")) ||
            selector == bytes4(keccak256("MIN_TOTAL_SUPPLY()")) ||
            selector == bytes4(keccak256("PERMIT_TYPEHASH()")) ||
            selector == bytes4(keccak256("allowance(address,address)")) ||
            selector == bytes4(keccak256("balanceOf(address)")) ||
            selector == bytes4(keccak256("calculateBuyInput(uint256)")) ||
            selector == bytes4(keccak256("calculateBuyInput(uint256,uint256,uint256,uint256)")) ||
            selector == bytes4(keccak256("calculateBuyInputBefore(uint256,uint256)")) ||
            selector == bytes4(keccak256("calculateBuyOutput(uint256)")) ||
            selector == bytes4(keccak256("calculateBuyOutput(uint256,uint256,uint256,uint256)")) ||
            selector == bytes4(keccak256("calculateBuyOutputBefore(uint256)")) ||
            selector == bytes4(keccak256("calculateSellInput(uint256)")) ||
            selector == bytes4(keccak256("calculateSellInput(uint256,uint256,uint256,uint256)")) ||
            selector == bytes4(keccak256("calculateSellOutput(uint256)")) ||
            selector == bytes4(keccak256("calculateSellOutput(uint256,uint256,uint256,uint256)")) ||
            selector == bytes4(keccak256("creator()")) ||
            selector == bytes4(keccak256("crr()")) ||
            selector == bytes4(keccak256("decimals()")) ||
            selector == bytes4(keccak256("eip712Domain()")) ||
            selector == bytes4(keccak256("getCurrentPrice()")) ||
            selector == bytes4(keccak256("identity()")) ||
            selector == bytes4(keccak256("maxTotalSupply()")) ||
            selector == bytes4(keccak256("minTotalSupply()")) ||
            selector == bytes4(keccak256("name()")) ||
            selector == bytes4(keccak256("nonces(address)")) ||
            selector == bytes4(keccak256("permitHash(address,address,uint256,uint256,uint256)")) ||
            selector == bytes4(keccak256("reserve()")) ||
            selector == bytes4(keccak256("symbol()")) ||
            selector == bytes4(keccak256("totalSupply()"));
        if (family == 1) return
            selector == bytes4(keccak256("DOMAIN_SEPARATOR()")) ||
            selector == bytes4(keccak256("PERMIT_TYPEHASH()")) ||
            selector == bytes4(keccak256("allowance(address,address)")) ||
            selector == bytes4(keccak256("balanceOf(address)")) ||
            selector == bytes4(keccak256("burnable()")) ||
            selector == bytes4(keccak256("cap()")) ||
            selector == bytes4(keccak256("decimals()")) ||
            selector == bytes4(keccak256("eip712Domain()")) ||
            selector == bytes4(keccak256("identity()")) ||
            selector == bytes4(keccak256("mintable()")) ||
            selector == bytes4(keccak256("name()")) ||
            selector == bytes4(keccak256("nonces(address)")) ||
            selector == bytes4(keccak256("owner()")) ||
            selector == bytes4(keccak256("permitHash(address,address,uint256,uint256,uint256)")) ||
            selector == bytes4(keccak256("symbol()")) ||
            selector == bytes4(keccak256("totalSupply()")) ||
            selector == bytes4(keccak256("withCap()"));
        if (family == 2) return
            selector == bytes4(keccak256("DOMAIN_SEPARATOR()")) ||
            selector == bytes4(keccak256("MIN_RESERVE()")) ||
            selector == bytes4(keccak256("balanceOf(address)")) ||
            selector == bytes4(keccak256("contractURI()")) ||
            selector == bytes4(keccak256("creator()")) ||
            selector == bytes4(keccak256("getAllowMint()")) ||
            selector == bytes4(keccak256("getApproved(uint256)")) ||
            selector == bytes4(keccak256("getBurnable()")) ||
            selector == bytes4(keccak256("getNFTType()")) ||
            selector == bytes4(keccak256("getRefundable()")) ||
            selector == bytes4(keccak256("getReserve(uint256)")) ||
            selector == bytes4(keccak256("isApprovedForAll(address,address)")) ||
            selector == bytes4(keccak256("name()")) ||
            selector == bytes4(keccak256("nonces(uint256)")) ||
            selector == bytes4(keccak256("ownerOf(uint256)")) ||
            selector == bytes4(keccak256("supportsInterface(bytes4)")) ||
            selector == bytes4(keccak256("symbol()")) ||
            selector == bytes4(keccak256("tokenURI(uint256)")) ||
            selector == bytes4(keccak256("withReserve()"));
        if (family == 3) return
            selector == bytes4(keccak256("DOMAIN_SEPARATOR()")) ||
            selector == bytes4(keccak256("MIN_RESERVE()")) ||
            selector == bytes4(keccak256("balanceOf(address,uint256)")) ||
            selector == bytes4(keccak256("balanceOfBatch(address[],uint256[])")) ||
            selector == bytes4(keccak256("contractURI()")) ||
            selector == bytes4(keccak256("creator()")) ||
            selector == bytes4(keccak256("exists(uint256)")) ||
            selector == bytes4(keccak256("getAllowMint()")) ||
            selector == bytes4(keccak256("getBurnable()")) ||
            selector == bytes4(keccak256("getNFTType()")) ||
            selector == bytes4(keccak256("getRefundable()")) ||
            selector == bytes4(keccak256("getReserve(uint256)")) ||
            selector == bytes4(keccak256("isApprovedForAll(address,address)")) ||
            selector == bytes4(keccak256("name()")) ||
            selector == bytes4(keccak256("nonces(address)")) ||
            selector == bytes4(keccak256("rate(uint256)")) ||
            selector == bytes4(keccak256("supportsInterface(bytes4)")) ||
            selector == bytes4(keccak256("symbol()")) ||
            selector == bytes4(keccak256("totalSupply()")) ||
            selector == bytes4(keccak256("totalSupply(uint256)")) ||
            selector == bytes4(keccak256("uri(uint256)")) ||
            selector == bytes4(keccak256("withReserve()"));
        if (family == 4) return
            selector == bytes4(keccak256("DOMAIN_SEPARATOR()")) ||
            selector == bytes4(keccak256("balanceOf(address)")) ||
            selector == bytes4(keccak256("contractURI()")) ||
            selector == bytes4(keccak256("creator()")) ||
            selector == bytes4(keccak256("getAllowMint()")) ||
            selector == bytes4(keccak256("getApproved(uint256)")) ||
            selector == bytes4(keccak256("getBurnable()")) ||
            selector == bytes4(keccak256("getNFTType()")) ||
            selector == bytes4(keccak256("isApprovedForAll(address,address)")) ||
            selector == bytes4(keccak256("name()")) ||
            selector == bytes4(keccak256("nonces(uint256)")) ||
            selector == bytes4(keccak256("ownerOf(uint256)")) ||
            selector == bytes4(keccak256("supportsInterface(bytes4)")) ||
            selector == bytes4(keccak256("symbol()")) ||
            selector == bytes4(keccak256("tokenURI(uint256)")) ||
            selector == bytes4(keccak256("withReserve()"));
        if (family == 5) return
            selector == bytes4(keccak256("DOMAIN_SEPARATOR()")) ||
            selector == bytes4(keccak256("balanceOf(address,uint256)")) ||
            selector == bytes4(keccak256("balanceOfBatch(address[],uint256[])")) ||
            selector == bytes4(keccak256("contractURI()")) ||
            selector == bytes4(keccak256("creator()")) ||
            selector == bytes4(keccak256("exists(uint256)")) ||
            selector == bytes4(keccak256("getAllowMint()")) ||
            selector == bytes4(keccak256("getBurnable()")) ||
            selector == bytes4(keccak256("getNFTType()")) ||
            selector == bytes4(keccak256("isApprovedForAll(address,address)")) ||
            selector == bytes4(keccak256("name()")) ||
            selector == bytes4(keccak256("nonces(address)")) ||
            selector == bytes4(keccak256("supportsInterface(bytes4)")) ||
            selector == bytes4(keccak256("symbol()")) ||
            selector == bytes4(keccak256("totalSupply()")) ||
            selector == bytes4(keccak256("totalSupply(uint256)")) ||
            selector == bytes4(keccak256("uri(uint256)")) ||
            selector == bytes4(keccak256("withReserve()"));
        return false;
    }
}
