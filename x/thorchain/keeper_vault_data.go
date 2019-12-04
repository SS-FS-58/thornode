package thorchain

import (
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/pkg/errors"
	"gitlab.com/thorchain/thornode/common"
)

// KeeperVaultData func to access Vault in key value store
type KeeperVaultData interface {
	GetVaultData(ctx sdk.Context) (VaultData, error)
	SetVaultData(ctx sdk.Context, data VaultData) error
	UpdateVaultData(ctx sdk.Context) error
}

// GetVaultData retrieve vault data from key value store
func (k KVStore) GetVaultData(ctx sdk.Context) (VaultData, error) {
	data := NewVaultData()
	key := k.GetKey(ctx, prefixVaultData, "")
	store := ctx.KVStore(k.storeKey)
	if !store.Has([]byte(key)) {
		return data, nil
	}
	buf := store.Get([]byte(key))
	if err := k.cdc.UnmarshalBinaryBare(buf, &data); nil != err {
		return data, dbError(ctx, "fail to unmarshal vault data", err)
	}

	return data, nil
}

// SetVaultData save the given vault data to key value store, it will overwrite existing vault
func (k KVStore) SetVaultData(ctx sdk.Context, data VaultData) error {
	key := k.GetKey(ctx, prefixVaultData, "")
	store := ctx.KVStore(k.storeKey)
	buf, err := k.cdc.MarshalBinaryBare(data)
	if nil != err {
		return fmt.Errorf("fail to marshal vault data: %w", err)
	}
	store.Set([]byte(key), buf)
	return nil
}

// UpdateVaultData Update the vault data to reflect changing in this block
func (k KVStore) UpdateVaultData(ctx sdk.Context) error {
	vault, err := k.GetVaultData(ctx)
	if nil != err {
		return fmt.Errorf("fail to get existing vault data: %w", err)
	}
	currentHeight := uint64(ctx.BlockHeight())

	// First get active pools and total staked Rune
	totalRune := sdk.ZeroUint()
	var pools Pools
	iterator := k.GetPoolIterator(ctx)
	defer iterator.Close()
	for ; iterator.Valid(); iterator.Next() {
		var pool Pool
		k.Cdc().MustUnmarshalBinaryBare(iterator.Value(), &pool)
		if pool.IsEnabled() && !pool.BalanceRune.IsZero() {
			totalRune = totalRune.Add(pool.BalanceRune)
			pools = append(pools, pool)
		}
	}

	// First subsidise the gas that was consumed from reserves, any
	// reserves we take, minus from the gas we owe.
	vault.TotalReserve, vault.Gas, err = subtractGas(ctx, k, vault.TotalReserve, vault.Gas)
	if err != nil {
		return err
	}

	// Then get fees and rewards
	totalLiquidityFees, err := k.GetTotalLiquidityFees(ctx, currentHeight)
	if nil != err {
		return fmt.Errorf("fail to get total liquidity fee: %w", err)
	}
	var totalFees sdk.Uint
	// If we have any remaining gas to pay, take from total liquidity fees
	totalFees, vault.Gas, err = subtractGas(ctx, k, totalFees, vault.Gas)
	if err != nil {
		return err
	}

	// if we continue to have remaining gas to pay off, take from the pools 😖
	for i, gas := range vault.Gas {
		if !gas.Amount.IsZero() {
			pool, err := k.GetPool(ctx, gas.Asset)
			if err != nil {
				return err
			}

			vault.Gas[i].Amount = common.SafeSub(vault.Gas[i].Amount, pool.BalanceAsset)
			pool.BalanceAsset = common.SafeSub(pool.BalanceAsset, gas.Amount)
			if err := k.SetPool(ctx, pool); err != nil {
				err = errors.Wrap(err, "fail to set pool")
				ctx.Logger().Error(err.Error())
				return err
			}
		}
		pool := k.GetPool(ctx, gas.Asset)
		vault.Gas[i].Amount = common.SafeSub(vault.Gas[i].Amount, pool.BalanceAsset)
		pool.BalanceAsset = common.SafeSub(pool.BalanceAsset, gas.Amount)
		k.SetPool(ctx, pool)
	}

	// If no Rune is staked, then don't give out block rewards.
	if totalRune.IsZero() {
		return nil // If no Rune is staked, then don't give out block rewards.
	}

	bondReward, totalPoolRewards, stakerDeficit := calcBlockRewards(vault.TotalReserve, totalFees)

	if !vault.TotalReserve.IsZero() {
		// Move Rune from the Reserve to the Bond and Pool Rewards
		if vault.TotalReserve.LT(totalPoolRewards) {
			vault.TotalReserve = sdk.ZeroUint()
		} else {
			vault.TotalReserve = common.SafeSub(
				common.SafeSub(vault.TotalReserve, bondReward),
				totalPoolRewards) // Subtract Bond and Pool rewards
		}
		vault.BondRewardRune = vault.BondRewardRune.Add(bondReward) // Add here for individual Node collection later
	}

	if !totalPoolRewards.IsZero() { // If Pool Rewards to hand out
		// First subsidise the gas that was consumed
		for _, coin := range vault.Gas {
			pool, err := k.GetPool(ctx, coin.Asset)
			if err != nil {
				return err
			}
			runeGas := pool.AssetValueInRune(coin.Amount)
			pool.BalanceRune = pool.BalanceRune.Add(runeGas)
			if err := k.SetPool(ctx, pool); err != nil {
				err = errors.Wrap(err, "fail to set pool")
				ctx.Logger().Error(err.Error())
				return err
			}
			totalPoolRewards = common.SafeSub(totalPoolRewards, runeGas)
		}

		// Then add pool rewards
		poolRewards := calcPoolRewards(totalPoolRewards, totalRune, pools)
		for i, reward := range poolRewards {
			pools[i].BalanceRune = pools[i].BalanceRune.Add(reward)
			if err := k.SetPool(ctx, pools[i]); err != nil {
				err = errors.Wrap(err, "fail to set pool")
				ctx.Logger().Error(err.Error())
				return err
			}
		}
	} else { // Else deduct pool deficit

		for _, pool := range pools {
			poolFees, err := k.GetPoolLiquidityFees(ctx, currentHeight, pool.Asset)
			if nil != err {
				return fmt.Errorf("fail to get liquidity fees for pool(%s): %w", pool.Asset, err)
			}
			if !pool.BalanceRune.IsZero() || !poolFees.IsZero() { // Safety checks
				continue
			}
			poolDeficit := calcPoolDeficit(stakerDeficit, totalLiquidityFees, poolFees)
			pool.BalanceRune = common.SafeSub(pool.BalanceRune, poolDeficit)
			if err := k.SetPool(ctx, pool); err != nil {
				err = errors.Wrap(err, "fail to set pool")
				ctx.Logger().Error(err.Error())
				return err
			}
		}
	}

	i, err := k.TotalActiveNodeAccount(ctx)
	if nil != err {
		return fmt.Errorf("fail to get total active node account: %w", err)
	}
	vault.TotalBondUnits = vault.TotalBondUnits.Add(sdk.NewUint(uint64(i))) // Add 1 unit for each active Node

	k.SetVaultData(ctx, vault)

	return nil
}

// remove gas
func subtractGas(ctx sdk.Context, keeper Keeper, val sdk.Uint, gases common.Gas) (sdk.Uint, common.Gas, error) {
	for i, gas := range gases {
		if !gas.Amount.IsZero() {
			pool, err := keeper.GetPool(ctx, gas.Asset)
			if err != nil {
				return sdk.ZeroUint(), nil, err
			}
			runeGas := pool.AssetValueInRune(gas.Amount)
			gases[i].Amount = common.SafeSub(gases[i].Amount, gas.Amount)
			val = common.SafeSub(val, runeGas)
		}
		pool := keeper.GetPool(ctx, coin.Asset)
		runeGas := pool.AssetValueInRune(coin.Amount)
		gas[i].Amount = common.SafeSub(gas[i].Amount, coin.Amount)
		val = common.SafeSub(val, runeGas)

	}
	return val, gases, nil
}
