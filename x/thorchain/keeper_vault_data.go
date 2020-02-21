package thorchain

import (
	"encoding/json"
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/pkg/errors"

	"gitlab.com/thorchain/thornode/common"
	"gitlab.com/thorchain/thornode/constants"
)

// KeeperVaultData func to access Vault in key value store
type KeeperVaultData interface {
	GetVaultData(ctx sdk.Context) (VaultData, error)
	SetVaultData(ctx sdk.Context, data VaultData) error
	UpdateVaultData(ctx sdk.Context, constAccessor constants.ConstantValues) error
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
	if err := k.cdc.UnmarshalBinaryBare(buf, &data); err != nil {
		return data, dbError(ctx, "fail to unmarshal vault data", err)
	}

	return data, nil
}

// SetVaultData save the given vault data to key value store, it will overwrite existing vault
func (k KVStore) SetVaultData(ctx sdk.Context, data VaultData) error {
	key := k.GetKey(ctx, prefixVaultData, "")
	store := ctx.KVStore(k.storeKey)
	buf, err := k.cdc.MarshalBinaryBare(data)
	if err != nil {
		return fmt.Errorf("fail to marshal vault data: %w", err)
	}
	store.Set([]byte(key), buf)
	return nil
}

func (k KVStore) getEnabledPoolsAndTotalStakedRune(ctx sdk.Context) (Pools, sdk.Uint, error) {
	// First get active pools and total staked Rune
	totalStaked := sdk.ZeroUint()
	var pools Pools
	iterator := k.GetPoolIterator(ctx)
	defer iterator.Close()
	for ; iterator.Valid(); iterator.Next() {
		var pool Pool
		if err := k.Cdc().UnmarshalBinaryBare(iterator.Value(), &pool); err != nil {
			return nil, sdk.ZeroUint(), fmt.Errorf("fail to unmarhsl pool: %w", err)
		}
		if pool.IsEnabled() && !pool.BalanceRune.IsZero() {
			totalStaked = totalStaked.Add(pool.BalanceRune)
			pools = append(pools, pool)
		}
	}
	return pools, totalStaked, nil
}

func (k KVStore) getTotalActiveBond(ctx sdk.Context) (sdk.Uint, error) {
	totalBonded := sdk.ZeroUint()
	nodes, err := k.ListActiveNodeAccounts(ctx)
	if err != nil {
		return sdk.ZeroUint(), fmt.Errorf("fail to get all active accounts: %w", err)
	}
	for _, node := range nodes {
		totalBonded = totalBonded.Add(node.Bond)
	}
	return totalBonded, nil
}

// UpdateVaultData Update the vault data to reflect changing in this block
func (k KVStore) UpdateVaultData(ctx sdk.Context, constAccessor constants.ConstantValues) error {
	vault, err := k.GetVaultData(ctx)
	if err != nil {
		return fmt.Errorf("fail to get existing vault data: %w", err)
	}
	// when total reserve is zero , can't pay reward
	if vault.TotalReserve.IsZero() {
		return nil
	}
	currentHeight := uint64(ctx.BlockHeight())
	pools, totalStaked, err := k.getEnabledPoolsAndTotalStakedRune(ctx)
	if err != nil {
		return fmt.Errorf("fail to get enabled pools and total staked rune: %w", err)
	}

	// If no Rune is staked, then don't give out block rewards.
	if totalStaked.IsZero() {
		return nil // If no Rune is staked, then don't give out block rewards.
	}

	// First subsidise the gas that was consumed from reserves, any
	// reserves we take, minus from the gas we owe.
	vault.TotalReserve, vault.Gas, err = subtractGas(ctx, k, vault.TotalReserve, vault.Gas)
	if err != nil {
		return fmt.Errorf("fail to subtract gas from reserve: %w", err)
	}

	// get total liquidity fees
	totalLiquidityFees, err := k.GetTotalLiquidityFees(ctx, currentHeight)
	if err != nil {
		return fmt.Errorf("fail to get total liquidity fee: %w", err)
	}

	// if we have no swaps, no block rewards for this block
	if totalLiquidityFees.IsZero() {
		return k.SetVaultData(ctx, vault)
	}

	// get total Fees (which is total liquidity fees, minus any gas we have left to pay)
	var totalFees sdk.Uint
	totalFees, vault.Gas, err = subtractGas(ctx, k, totalLiquidityFees, vault.Gas)
	if err != nil {
		return fmt.Errorf("fail to subtract gas from liquidity fees: %w", err)
	}

	// NOTE: if we continue to have remaining gas to pay off (which is
	// extremely unlikely), ignore it for now (attempt to recover in the next
	// block). This should be OK as the asset amount in the pool has already
	// been deducted so the balances are correct. Just operating at a deficit.
	totalBonded, err := k.getTotalActiveBond(ctx)
	if err != nil {
		return fmt.Errorf("fail to get total active bond: %w", err)
	}
	emissionCurve := constAccessor.GetInt64Value(constants.EmissionCurve)
	blocksOerYear := constAccessor.GetInt64Value(constants.BlocksPerYear)
	bondReward, totalPoolRewards, stakerDeficit := calcBlockRewards(totalStaked, totalBonded, vault.TotalReserve, totalFees, emissionCurve, blocksOerYear)

	// if we don't have enough reserve to pay out various rewards, save the
	// vault changes and return
	if vault.TotalReserve.LT(totalPoolRewards.Add(bondReward)) {
		return k.SetVaultData(ctx, vault)
	}

	// Move Rune from the Reserve to the Bond and Pool Rewards
	vault.TotalReserve = common.SafeSub(vault.TotalReserve, bondReward.Add(totalPoolRewards))
	vault.BondRewardRune = vault.BondRewardRune.Add(bondReward) // Add here for individual Node collection later

	var evtPools []PoolAmt

	if !totalPoolRewards.IsZero() { // If Pool Rewards to hand out
		// First subsidise the gas that was consumed (if any remain)
		for _, coin := range vault.Gas {
			if coin.Amount.IsZero() {
				continue
			}
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

		var rewardAmts []sdk.Uint

		if !totalLiquidityFees.IsZero() {
			// Pool Rewards are based on Fee Share
			for _, pool := range pools {
				fees, err := k.GetPoolLiquidityFees(ctx, currentHeight, pool.Asset)
				if err != nil {
					err = errors.Wrap(err, "fail to get fees")
					ctx.Logger().Error(err.Error())
					return err
				}
				amt := common.GetShare(fees, totalLiquidityFees, totalPoolRewards)
				rewardAmts = append(rewardAmts, amt)
				evtPools = append(evtPools, PoolAmt{Asset: pool.Asset, Amount: int64(amt.Uint64())})
			}
		} else {
			// Pool Rewards are based on Depth Share
			rewardAmts = calcPoolRewards(totalPoolRewards, totalStaked, pools)
		}
		// Pay out
		if err := payPoolRewards(ctx, k, rewardAmts, pools); err != nil {
			return err
		}

	} else { // Else deduct pool deficit

		for _, pool := range pools {
			poolFees, err := k.GetPoolLiquidityFees(ctx, currentHeight, pool.Asset)
			if err != nil {
				return fmt.Errorf("fail to get liquidity fees for pool(%s): %w", pool.Asset, err)
			}
			if pool.BalanceRune.IsZero() || poolFees.IsZero() { // Safety checks
				continue
			}
			poolDeficit := calcPoolDeficit(stakerDeficit, totalLiquidityFees, poolFees)
			pool.BalanceRune = common.SafeSub(pool.BalanceRune, poolDeficit)
			vault.BondRewardRune = vault.BondRewardRune.Add(poolDeficit)
			if err := k.SetPool(ctx, pool); err != nil {
				err = errors.Wrap(err, "fail to set pool")
				ctx.Logger().Error(err.Error())
				return err
			}
			evtPools = append(evtPools, PoolAmt{
				Asset:  pool.Asset,
				Amount: 0 - int64(poolDeficit.Uint64()),
			})
		}
	}

	rewardEvt := NewEventRewards(bondReward, evtPools)
	evtBytes, err := json.Marshal(rewardEvt)
	if err != nil {
		return fmt.Errorf("fail to marshal reward event to json: %w", err)
	}
	evt := NewEvent(
		rewardEvt.Type(),
		ctx.BlockHeight(),
		common.Tx{ID: common.BlankTxID},
		evtBytes,
		EventSuccess,
	)
	if err := k.UpsertEvent(ctx, evt); err != nil {
		return fmt.Errorf("fail to save event: %w", err)
	}

	i, err := getTotalActiveNodeWithBond(ctx, k)
	if err != nil {
		return fmt.Errorf("fail to get total active node account: %w", err)
	}
	vault.TotalBondUnits = vault.TotalBondUnits.Add(sdk.NewUint(uint64(i))) // Add 1 unit for each active Node

	return k.SetVaultData(ctx, vault)
}

func getTotalActiveNodeWithBond(ctx sdk.Context, k Keeper) (int64, error) {
	nas, err := k.ListActiveNodeAccounts(ctx)
	if err != nil {
		return 0, fmt.Errorf("fail to get active node accounts: %w", err)
	}
	var total int64
	for _, item := range nas {
		if !item.Bond.IsZero() {
			total++
		}
	}
	return total, nil
}

// remove gas
func subtractGas(ctx sdk.Context, keeper Keeper, val sdk.Uint, gas common.Gas) (sdk.Uint, common.Gas, error) {
	for i, coin := range gas {
		// if the coin is zero amount, don't need to do anything
		if coin.Amount.IsZero() {
			continue
		}
		pool, err := keeper.GetPool(ctx, coin.Asset)
		if err != nil {
			return sdk.ZeroUint(), nil, fmt.Errorf("fail to get pool(%s): %w", coin.Asset, err)
		}

		runeGas := pool.AssetValueInRune(coin.Amount)
		if runeGas.GT(val) {
			runeGas = val
		}
		gas[i].Amount = common.SafeSub(gas[i].Amount, pool.RuneValueInAsset(runeGas))
		val = common.SafeSub(val, runeGas)
		pool.BalanceRune = pool.BalanceRune.Add(runeGas)
		if err := keeper.SetPool(ctx, pool); err != nil {
			return sdk.ZeroUint(), nil, fmt.Errorf("fail to set pool(%s): %w", coin.Asset, err)
		}
	}
	return val, gas, nil
}

// Pays out Rewards
func payPoolRewards(ctx sdk.Context, k Keeper, poolRewards []sdk.Uint, pools Pools) error {
	for i, reward := range poolRewards {
		pools[i].BalanceRune = pools[i].BalanceRune.Add(reward)
		if err := k.SetPool(ctx, pools[i]); err != nil {
			err = errors.Wrap(err, "fail to set pool")
			ctx.Logger().Error(err.Error())
			return err
		}
	}
	return nil
}
