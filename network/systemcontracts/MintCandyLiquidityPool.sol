// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.20;

interface PoolAsset {
    function balanceOf(address account) external view returns (uint256);
    function transfer(address to, uint256 amount) external returns (bool);
    function transferFrom(address from, address to, uint256 amount) external returns (bool);
}

/// @notice Single-pair, constant-product AMM. All swap fees remain in LP reserves.
contract MintCandyLiquidityPool {
    error InvalidPool();
    error InvalidAmount();
    error Expired();
    error LimitExceeded();
    error UnexpectedBalance();
    error ReentrantCall();

    uint256 private constant BPS = 10_000;
    uint256 private constant MAX_RESERVE = type(uint112).max;
    uint16 public constant feeBps = 30;
    address private immutable initializer;
    uint112 public reserve0;
    uint112 public reserve1;
    uint112 public totalShares;
    mapping(address => uint112) public sharesOf;
    bool private entered;
    PoolAsset public token0;
    PoolAsset public token1;

    event PoolInitialized(address first, address second);
    event LiquidityAdded(address indexed provider, uint112 amount0, uint112 amount1, uint112 shares);
    event LiquidityRemoved(address indexed provider, uint112 amount0, uint112 amount1, uint112 shares);
    event Swap(address indexed trader, bool zeroForOne, uint112 amountIn, uint112 amountOut);

    constructor(address authority) {
        if (authority == address(0)) revert InvalidPool();
        initializer = authority;
    }

    function initialize(PoolAsset first, PoolAsset second) external {
        if (msg.sender != initializer || address(token0) != address(0) ||
            address(first) == address(second) || address(first) == address(this) ||
            address(second) == address(this) || address(first).code.length == 0 ||
            address(second).code.length == 0) revert InvalidPool();
        token0 = first;
        token1 = second;
        emit PoolInitialized(address(first), address(second));
    }

    modifier guarded(uint256 deadline) {
        if (address(token0) == address(0)) revert InvalidPool();
        if (block.timestamp > deadline) revert Expired();
        if (entered) revert ReentrantCall();
        entered = true;
        _;
        entered = false;
    }

    function previewAddLiquidity(uint112 max0, uint112 max1) public view
        returns (uint112 amount0, uint112 amount1, uint112 shares)
    {
        if (max0 == 0 || max1 == 0) revert InvalidAmount();
        uint256 supply = totalShares;
        uint256 minted;
        if (supply == 0) {
            minted = sqrt(uint256(max0) * max1);
            amount0 = max0;
            amount1 = max1;
        } else {
            uint256 shares0 = uint256(max0) * supply / reserve0;
            uint256 shares1 = uint256(max1) * supply / reserve1;
            minted = shares0 < shares1 ? shares0 : shares1;
            amount0 = uint112((minted * reserve0 + supply - 1) / supply);
            amount1 = uint112((minted * reserve1 + supply - 1) / supply);
        }
        if (minted == 0 || supply + minted > MAX_RESERVE ||
            uint256(reserve0) + amount0 > MAX_RESERVE || uint256(reserve1) + amount1 > MAX_RESERVE) revert InvalidAmount();
        shares = uint112(minted);
    }

    function addLiquidity(uint112 max0, uint112 max1, uint112 minShares, uint256 deadline) external guarded(deadline) {
        // Opening an empty pool establishes the reserve ratio.
        if (totalShares == 0 && msg.sender != initializer) revert InvalidPool();
        (uint112 amount0, uint112 amount1, uint112 shares) = previewAddLiquidity(max0, max1);
        if (minShares == 0 || shares < minShares) revert LimitExceeded();
        pull(token0, reserve0, amount0);
        pull(token1, reserve1, amount1);
        reserve0 += amount0;
        reserve1 += amount1;
        totalShares += shares;
        sharesOf[msg.sender] += shares;
        emit LiquidityAdded(msg.sender, amount0, amount1, shares);
    }

    function previewRemoveLiquidity(uint112 shares) public view returns (uint112 amount0, uint112 amount1) {
        if (shares == 0 || shares > totalShares) revert InvalidAmount();
        amount0 = uint112(uint256(shares) * reserve0 / totalShares);
        amount1 = uint112(uint256(shares) * reserve1 / totalShares);
        if (amount0 == 0 || amount1 == 0) revert InvalidAmount();
    }

    function removeLiquidity(uint112 shares, uint112 min0, uint112 min1, uint256 deadline) external guarded(deadline) {
        if (shares > sharesOf[msg.sender]) revert InvalidAmount();
        (uint112 amount0, uint112 amount1) = previewRemoveLiquidity(shares);
        if (min0 == 0 || min1 == 0 || amount0 < min0 || amount1 < min1) revert LimitExceeded();
        push(token0, reserve0, amount0);
        push(token1, reserve1, amount1);
        reserve0 -= amount0;
        reserve1 -= amount1;
        totalShares -= shares;
        sharesOf[msg.sender] -= shares;
        emit LiquidityRemoved(msg.sender, amount0, amount1, shares);
    }

    function quoteExactInput(bool zeroForOne, uint112 amountIn) public view returns (uint112 amountOut) {
        (uint112 reserveIn, uint112 reserveOut) = zeroForOne ? (reserve0, reserve1) : (reserve1, reserve0);
        if (amountIn == 0 || reserveIn == 0 || uint256(reserveIn) + amountIn > MAX_RESERVE) revert InvalidAmount();
        uint256 net = uint256(amountIn) * (BPS - feeBps);
        amountOut = uint112(net * reserveOut / (uint256(reserveIn) * BPS + net));
        if (amountOut == 0) revert InvalidAmount();
    }

    function swapExactInput(bool zeroForOne, uint112 amountIn, uint112 minOut, uint256 deadline) external guarded(deadline) {
        uint112 amountOut = quoteExactInput(zeroForOne, amountIn);
        if (minOut == 0 || amountOut < minOut) revert LimitExceeded();
        if (zeroForOne) {
            pull(token0, reserve0, amountIn);
            push(token1, reserve1, amountOut);
            reserve0 += amountIn;
            reserve1 -= amountOut;
        } else {
            pull(token1, reserve1, amountIn);
            push(token0, reserve0, amountOut);
            reserve1 += amountIn;
            reserve0 -= amountOut;
        }
        emit Swap(msg.sender, zeroForOne, amountIn, amountOut);
    }

    // Transfers must preserve raw units. Existing surplus is excluded from the reserve ledger.
    function pull(PoolAsset asset, uint112 reserve, uint112 amount) private {
        uint256 poolBefore = asset.balanceOf(address(this));
        uint256 callerBefore = asset.balanceOf(msg.sender);
        if (poolBefore < reserve || callerBefore < amount || !asset.transferFrom(msg.sender, address(this), amount) ||
            asset.balanceOf(address(this)) != poolBefore + amount || asset.balanceOf(msg.sender) != callerBefore - amount) {
            revert UnexpectedBalance();
        }
    }

    function push(PoolAsset asset, uint112 reserve, uint112 amount) private {
        uint256 poolBefore = asset.balanceOf(address(this));
        uint256 callerBefore = asset.balanceOf(msg.sender);
        if (poolBefore < reserve || !asset.transfer(msg.sender, amount) || asset.balanceOf(address(this)) != poolBefore - amount ||
            asset.balanceOf(msg.sender) != callerBefore + amount) revert UnexpectedBalance();
    }

    function sqrt(uint256 value) private pure returns (uint256 root) {
        root = value;
        uint256 next = (root + 1) / 2;
        while (next < root) {
            root = next;
            next = (root + value / root) / 2;
        }
    }
}
