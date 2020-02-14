package thorchain

import (
	"encoding/json"
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/pkg/errors"

	"gitlab.com/thorchain/thornode/common"
)

// validate if pools exist
func validatePools(ctx sdk.Context, keeper Keeper, assets ...common.Asset) sdk.Error {
	for _, asset := range assets {
		if !asset.IsRune() {
			if !keeper.PoolExist(ctx, asset) {
				return sdk.NewError(DefaultCodespace, CodeSwapFailPoolNotExist, "%s pool doesn't exist", asset)
			}
			pool, err := keeper.GetPool(ctx, asset)
			if err != nil {
				return sdk.ErrInternal(fmt.Errorf("fail to get %s pool : %w", asset, err).Error())
			}

			if pool.Status != PoolEnabled {
				return sdk.NewError(DefaultCodespace, CodeInvalidPoolStatus, "pool %s is in %s status, can't swap", asset.String(), pool.Status)
			}
		}
	}
	return nil
}

// validateMessage is trying to validate the legitimacy of the incoming message and decide whether THORNode can handle it
func validateMessage(tx common.Tx, target common.Asset, destination common.Address) error {
	if err := tx.IsValid(); err != nil {
		return err
	}
	if target.IsEmpty() {
		return errors.New("target is empty")
	}
	if destination.IsEmpty() {
		return errors.New("destination is empty")
	}

	return nil
}

func swap(ctx sdk.Context,
	keeper Keeper, tx common.Tx,
	target common.Asset,
	destination common.Address,
	tradeTarget sdk.Uint,
	transactionFee sdk.Uint) (sdk.Uint, []Event, sdk.Error) {
	var swapEvents []Event

	if err := validateMessage(tx, target, destination); err != nil {
		return sdk.ZeroUint(), swapEvents, sdk.NewError(DefaultCodespace, CodeValidationError, err.Error())
	}
	source := tx.Coins[0].Asset

	if err := validatePools(ctx, keeper, source, target); err != nil {
		return sdk.ZeroUint(), swapEvents, err
	}
	pools := make([]Pool, 0)
	isDoubleSwap := !source.IsRune() && !target.IsRune()
	if isDoubleSwap {
		var err error
		sourcePool, err := keeper.GetPool(ctx, source)
		if err != nil {
			return sdk.ZeroUint(), swapEvents, sdk.ErrInternal(fmt.Errorf("fail to get %s pool : %w", source, err).Error())
		}
		var swapErr sdk.Error
		var swapEvt Event
		var amt sdk.Uint
		amt, sourcePool, swapEvt, swapErr = swapOne(ctx, keeper, tx, sourcePool, common.RuneAsset(), destination, tradeTarget, transactionFee)
		if swapErr != nil {
			return sdk.ZeroUint(), swapEvents, swapErr
		}
		pools = append(pools, sourcePool)
		tx.Coins = common.Coins{common.NewCoin(common.RuneAsset(), amt)}
		tx.Gas = nil

		// pre-populate the out transaction
		swapEvt.Status = EventSuccess
		swapEvt.OutTxs = common.Txs{common.NewTx(common.BlankTxID, tx.FromAddress, tx.ToAddress, tx.Coins, tx.Gas, tx.Memo)}

		swapEvents = append(swapEvents, swapEvt)
	}

	// Set asset to our non-rune asset asset
	asset := source
	if source.IsRune() {
		asset = target
	}
	pool, err := keeper.GetPool(ctx, asset)
	if err != nil {
		return sdk.ZeroUint(), swapEvents, sdk.NewError(DefaultCodespace, CodeInvalidPoolStatus, err.Error())
	}
	assetAmount, pool, swapEvt, swapErr := swapOne(ctx, keeper, tx, pool, target, destination, tradeTarget, transactionFee)
	if swapErr != nil {
		return sdk.ZeroUint(), swapEvents, swapErr
	}
	swapEvents = append(swapEvents, swapEvt)
	pools = append(pools, pool)
	if !tradeTarget.IsZero() && assetAmount.LT(tradeTarget) {
		return sdk.ZeroUint(), swapEvents, sdk.NewError(DefaultCodespace, CodeSwapFailTradeTarget, "emit asset %s less than price limit %s", assetAmount, tradeTarget)
	}
	if target.IsRune() {
		if assetAmount.LTE(transactionFee) {
			return sdk.ZeroUint(), swapEvents, sdk.NewError(DefaultCodespace, CodeSwapFailNotEnoughFee, "output RUNE (%s) is not enough to pay transaction fee", assetAmount)
		}
	}
	// emit asset is zero
	if assetAmount.IsZero() {
		return sdk.ZeroUint(), swapEvents, sdk.NewError(DefaultCodespace, CodeSwapFailZeroEmitAsset, "zero emit asset")
	}

	// Update pools
	for _, pool := range pools {
		if err := keeper.SetPool(ctx, pool); err != nil {
			return sdk.ZeroUint(), swapEvents, sdk.NewError(DefaultCodespace, CodeSwapFail, err.Error())
		}
	}

	return assetAmount, swapEvents, nil
}

func swapOne(ctx sdk.Context,
	keeper Keeper, tx common.Tx, pool Pool,
	target common.Asset,
	destination common.Address,
	tradeTarget sdk.Uint,
	transactionFee sdk.Uint) (amt sdk.Uint, poolResult Pool, evt Event, swapErr sdk.Error) {
	source := tx.Coins[0].Asset
	amount := tx.Coins[0].Amount

	ctx.Logger().Info(fmt.Sprintf("%s Swapping %s(%s) -> %s to %s", tx.FromAddress, source, tx.Coins[0].Amount, target, destination))

	var X, x, Y, liquidityFee, emitAssets sdk.Uint
	var tradeSlip sdk.Uint

	// Set asset to our non-rune asset asset
	asset := source
	if source.IsRune() {
		asset = target
		if amount.LTE(transactionFee) {
			// stop swap , because the output will not enough to pay for transaction fee
			return sdk.ZeroUint(), Pool{}, evt, sdk.NewError(DefaultCodespace, CodeSwapFailNotEnoughFee, "output RUNE (%s) is not enough to pay transaction fee", amount)
		}
	}

	swapEvt := NewEventSwap(
		asset,
		tradeTarget,
		sdk.ZeroUint(),
		sdk.ZeroUint(),
	)

	// Check if pool exists
	if !keeper.PoolExist(ctx, asset) {
		ctx.Logger().Debug(fmt.Sprintf("pool %s doesn't exist", asset))
		return sdk.ZeroUint(), Pool{}, evt, sdk.NewError(DefaultCodespace, CodeSwapFailPoolNotExist, "pool %s doesn't exist", asset)
	}

	// Get our pool from the KVStore
	pool, poolErr := keeper.GetPool(ctx, asset)
	if poolErr != nil {
		ctx.Logger().Error(fmt.Sprintf("fail to get pool(%s)", asset), "error", poolErr)
		return sdk.ZeroUint(), Pool{}, evt, sdk.NewError(DefaultCodespace, CodeSwapFailPoolNotExist, "pool %s doesn't exist", asset)
	}
	if pool.Status != PoolEnabled {
		return sdk.ZeroUint(), pool, evt, sdk.NewError(DefaultCodespace, CodeInvalidPoolStatus, "pool %s is in %s status, can't swap", asset.String(), pool.Status)
	}

	// Get our X, x, Y values
	if source.IsRune() {
		X = pool.BalanceRune
		Y = pool.BalanceAsset
	} else {
		Y = pool.BalanceRune
		X = pool.BalanceAsset
	}
	x = amount

	// check our X,x,Y values are valid
	if x.IsZero() {
		return sdk.ZeroUint(), pool, evt, sdk.NewError(DefaultCodespace, CodeSwapFailInvalidAmount, "amount is invalid")
	}
	if X.IsZero() || Y.IsZero() {
		return sdk.ZeroUint(), pool, evt, sdk.NewError(DefaultCodespace, CodeSwapFailInvalidBalance, "invalid balance")
	}

	liquidityFee = calcLiquidityFee(X, x, Y)
	tradeSlip = calcTradeSlip(X, x)
	emitAssets = calcAssetEmission(X, x, Y)

	if source.IsRune() {
		liquidityFee = pool.AssetValueInRune(liquidityFee)
	}
	swapEvt.LiquidityFee = liquidityFee
	swapEvt.TradeSlip = tradeSlip
	errLiquidityFee := keeper.AddToLiquidityFees(ctx, pool.Asset, liquidityFee)
	if errLiquidityFee != nil {
		return sdk.ZeroUint(), pool, evt, sdk.ErrInternal(fmt.Errorf("fail to add liquidity: %w", swapErr).Error())
	}

	// do THORNode have enough balance to swap?
	if emitAssets.GT(Y) {
		return sdk.ZeroUint(), pool, evt, sdk.NewError(DefaultCodespace, CodeSwapFailNotEnoughBalance, "asset(%s) balance is %d, can't do swap", asset, Y)
	}

	ctx.Logger().Info(fmt.Sprintf("Pre-Pool: %sRune %sAsset", pool.BalanceRune, pool.BalanceAsset))

	if source.IsRune() {
		pool.BalanceRune = X.Add(x)
		pool.BalanceAsset = common.SafeSub(Y, emitAssets)
	} else {
		pool.BalanceAsset = X.Add(x)
		pool.BalanceRune = common.SafeSub(Y, emitAssets)
	}
	ctx.Logger().Info(fmt.Sprintf("Post-swap: %sRune %sAsset , user get:%s ", pool.BalanceRune, pool.BalanceAsset, emitAssets))

	swapBytes, err := json.Marshal(swapEvt)
	if err != nil {
		return sdk.ZeroUint(), pool, evt, sdk.NewError(DefaultCodespace, CodeSwapFailNotEnoughBalance, "err", err.Error())
	}
	evt = NewEvent(swapEvt.Type(), ctx.BlockHeight(), tx, swapBytes, EventPending)

	return emitAssets, pool, evt, nil
}

// calculate the number of assets sent to the address (includes liquidity fee)
func calcAssetEmission(X, x, Y sdk.Uint) sdk.Uint {
	// ( x * X * Y ) / ( x + X )^2
	numerator := x.Mul(X).Mul(Y)
	denominator := x.Add(X).Mul(x.Add(X))
	return numerator.Quo(denominator)
}

// calculateFee the fee of the swap
func calcLiquidityFee(X, x, Y sdk.Uint) sdk.Uint {
	// ( x^2 *  Y ) / ( x + X )^2
	numerator := x.Mul(x).Mul(Y)
	denominator := x.Add(X).Mul(x.Add(X))
	return numerator.Quo(denominator)
}

// calcTradeSlip - calculate the trade slip, expressed in basis points (10000)
func calcTradeSlip(Xi, xi sdk.Uint) sdk.Uint {
	// Cast to DECs
	xD := sdk.NewDec(int64(xi.Uint64()))
	XD := sdk.NewDec(int64(Xi.Uint64()))
	dec2 := sdk.NewDec(int64(sdk.NewUint(2).Uint64()))
	dec10k := sdk.NewDec(int64(sdk.NewUint(10000).Uint64()))

	// x * (2*X + x) / (X * X)
	numD := xD.Mul((dec2.Mul(XD)).Add(xD))
	denD := XD.Mul(XD)
	tradeSlipD := numD.Quo(denD) // Division with DECs

	tradeSlip := tradeSlipD.Mul(dec10k)                          // Adds 5 0's
	tradeSlipUint := sdk.NewUint(uint64(tradeSlip.RoundInt64())) // Casts back to Uint as Basis Points
	return tradeSlipUint
}
