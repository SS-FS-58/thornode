package thorchain

import (
	"fmt"
	"sync"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"gitlab.com/thorchain/thornode/common"
	"gitlab.com/thorchain/thornode/constants"
)

// TxOutStorageV1 is going to manage all the outgoing tx
type TxOutStorageV1 struct {
	height        int64
	keeper        Keeper
	constAccessor constants.ConstantValues
	rwMutex       *sync.Mutex // ensures we don't append to txout store at the same time and drop txout itmes
}

// NewTxOutStorage will create a new instance of TxOutStore.
func NewTxOutStorageV1(keeper Keeper) *TxOutStorageV1 {
	return &TxOutStorageV1{
		keeper:  keeper,
		rwMutex: &sync.Mutex{},
	}
}

// NewBlock create a new block
func (tos *TxOutStorageV1) NewBlock(height int64, constAccessor constants.ConstantValues) {
	tos.constAccessor = constAccessor
	tos.height = height
}

func (tos *TxOutStorageV1) GetBlockOut(ctx sdk.Context) (*TxOut, error) {
	return tos.keeper.GetTxOut(ctx, tos.height)
}

func (tos *TxOutStorageV1) GetOutboundItems(ctx sdk.Context) ([]*TxOutItem, error) {
	block, err := tos.keeper.GetTxOut(ctx, tos.height)
	return block.TxArray, err
}

func (tos *TxOutStorageV1) ClearOutboundItems(_ sdk.Context) {} // do nothing

// TryAddTxOutItem add an outbound tx to block
// return bool indicate whether the transaction had been added successful or not
// return error indicate error
func (tos *TxOutStorageV1) TryAddTxOutItem(ctx sdk.Context, toi *TxOutItem, eventManager EventManager) (bool, error) {
	success, err := tos.prepareTxOutItem(ctx, toi, eventManager)
	if err != nil {
		return success, fmt.Errorf("fail to prepare outbound tx: %w", err)
	}
	if !success {
		return false, nil
	}
	// add tx to block out
	if err := tos.addToBlockOut(ctx, toi); err != nil {
		return false, err
	}
	return true, nil
}

// UnSafeAddTxOutItem - blindly adds a tx out, skipping vault selection, transaction
// fee deduction, etc
func (tos *TxOutStorageV1) UnSafeAddTxOutItem(ctx sdk.Context, toi *TxOutItem) error {
	return tos.addToBlockOut(ctx, toi)
}

// PrepareTxOutItem will do some data validation which include the following
// 1. Make sure it has a legitimate memo
// 2. choose an appropriate pool,Yggdrasil or Asgard
// 3. deduct transaction fee, keep in mind, only take transaction fee when active nodes are  more then minimumBFT
// return bool indicated whether the given TxOutItem should be added into block or not
func (tos *TxOutStorageV1) prepareTxOutItem(ctx sdk.Context, toi *TxOutItem, eventMgr EventManager) (bool, error) {
	// Default the memo to the standard outbound memo
	if toi.Memo == "" {
		toi.Memo = NewOutboundMemo(toi.InHash).String()
	}

	// If THORNode don't have a pool already selected to send from, discover one.
	if toi.VaultPubKey.IsEmpty() {
		// When deciding which Yggdrasil pool will send out our tx out, we
		// should consider which ones observed the inbound request tx, as
		// yggdrasil pools can go offline. Here THORNode get the voter record and
		// only consider Yggdrasils where their observed saw the "correct"
		// tx.

		activeNodeAccounts, err := tos.keeper.ListActiveNodeAccounts(ctx)
		if len(activeNodeAccounts) > 0 && err == nil {
			voter, err := tos.keeper.GetObservedTxVoter(ctx, toi.InHash)
			if err != nil {
				return false, fmt.Errorf("fail to get observed tx voter: %w", err)
			}
			tx := voter.GetTx(activeNodeAccounts)

			// collect yggdrasil pools
			yggs, err := tos.collectYggdrasilPools(ctx, tx, toi.Chain.GetGasAsset())
			if err != nil {
				return false, fmt.Errorf("fail to collect yggdrasil pool: %w", err)
			}

			vault := yggs.SelectByMaxCoin(toi.Coin.Asset)
			// if none of the ygg vaults have enough funds, don't select one
			// and we'll select an asgard vault a few lines down
			if toi.Coin.Amount.LT(vault.GetCoin(toi.Coin.Asset).Amount) {
				toi.VaultPubKey = vault.PubKey
			}
		}
	}

	// Apparently we couldn't find a yggdrasil vault to send from, so use asgard
	if toi.VaultPubKey.IsEmpty() {

		active, err := tos.keeper.GetAsgardVaultsByStatus(ctx, ActiveVault)
		if err != nil {
			ctx.Logger().Error("fail to get active vaults", "error", err)
		}

		vault := active.SelectByMaxCoin(toi.Coin.Asset)
		if vault.IsEmpty() {
			return false, fmt.Errorf("empty vault, cannot send out fund: %w", err)
		}

		// check that this vault has enough funds to satisfy the request
		if toi.Coin.Amount.GT(vault.GetCoin(toi.Coin.Asset).Amount) {
			// not enough funds
			return false, fmt.Errorf("vault %s, does not have enough funds. Has %s, but requires %s", vault.PubKey, vault.GetCoin(toi.Coin.Asset), toi.Coin)
		}

		toi.VaultPubKey = vault.PubKey
	}

	// Ensure the InHash is set
	if toi.InHash == "" {
		toi.InHash = common.BlankTxID
	}

	// Ensure THORNode are not sending from and to the same address
	fromAddr, err := toi.VaultPubKey.GetAddress(toi.Chain)
	if err != nil || fromAddr.IsEmpty() || toi.ToAddress.Equals(fromAddr) {
		return false, nil
	}

	// Deduct TransactionFee from TOI and add to Reserve
	transactionFee := tos.constAccessor.GetInt64Value(constants.TransactionFee)
	memo, err := ParseMemo(toi.Memo) // ignore err
	if err == nil && !memo.IsType(TxYggdrasilFund) && !memo.IsType(TxYggdrasilReturn) && !memo.IsType(TxMigrate) && !memo.IsType(TxRagnarok) {
		var runeFee sdk.Uint
		if toi.Coin.Asset.IsRune() {
			if toi.Coin.Amount.LTE(sdk.NewUint(uint64(transactionFee))) {
				runeFee = toi.Coin.Amount // Fee is the full amount
			} else {
				runeFee = sdk.NewUint(uint64(transactionFee)) // Fee is the prescribed fee
			}
			toi.Coin.Amount = common.SafeSub(toi.Coin.Amount, runeFee)
			fee := common.NewFee(common.Coins{common.NewCoin(toi.Coin.Asset, runeFee)}, sdk.ZeroUint())

			eventMgr.UpdateEventFee(ctx, toi.InHash, fee)
			if err := tos.keeper.AddFeeToReserve(ctx, runeFee); err != nil {
				// Add to reserve
				ctx.Logger().Error("fail to add fee to reserve", "error", err)
			}

		} else {
			pool, err := tos.keeper.GetPool(ctx, toi.Coin.Asset) // Get pool
			if err != nil {
				// the error is already logged within kvstore
				return false, fmt.Errorf("fail to get pool: %w", err)
			}

			assetFee := pool.RuneValueInAsset(sdk.NewUint(uint64(transactionFee))) // Get fee in Asset value
			if toi.Coin.Amount.LTE(assetFee) {
				assetFee = toi.Coin.Amount // Fee is the full amount
				runeFee = pool.AssetValueInRune(assetFee)
			} else {
				runeFee = sdk.NewUint(uint64(transactionFee))
			}

			toi.Coin.Amount = common.SafeSub(toi.Coin.Amount, assetFee) // Deduct Asset fee
			pool.BalanceAsset = pool.BalanceAsset.Add(assetFee)         // Add Asset fee to Pool
			var poolDeduct sdk.Uint
			if runeFee.GT(pool.BalanceRune) {
				poolDeduct = pool.BalanceRune
			} else {
				poolDeduct = runeFee
			}
			pool.BalanceRune = common.SafeSub(pool.BalanceRune, runeFee) // Deduct Rune from Pool
			fee := common.NewFee(common.Coins{common.NewCoin(toi.Coin.Asset, assetFee)}, poolDeduct)
			eventMgr.UpdateEventFee(ctx, toi.InHash, fee)

			if err := tos.keeper.SetPool(ctx, pool); err != nil { // Set Pool
				return false, fmt.Errorf("fail to save pool: %w", err)
			}
			if err := tos.keeper.AddFeeToReserve(ctx, runeFee); err != nil {
				return false, fmt.Errorf("fail to add fee to reserve: %w", err)
			}
		}
	}

	// When we request Yggdrasil pool to return the fund, the coin field is actually empty
	// Signer when it sees an tx out item with memo "yggdrasil-" it will query the account on relevant chain
	// and coin field will be filled there, thus we have to let this one go
	if toi.Coin.IsEmpty() && !memo.IsType(TxYggdrasilReturn) {
		ctx.Logger().Info("tx out item has zero coin", toi.String())
		return false, nil
	}

	// increment out number of out tx for this in tx
	voter, err := tos.keeper.GetObservedTxVoter(ctx, toi.InHash)
	if err != nil {
		return false, fmt.Errorf("fail to get observed tx voter: %w", err)
	}
	voter.Actions = append(voter.Actions, *toi)
	tos.keeper.SetObservedTxVoter(ctx, voter)

	return true, nil
}

func (tos *TxOutStorageV1) addToBlockOut(ctx sdk.Context, toi *TxOutItem) error {
	tos.rwMutex.Lock()
	defer tos.rwMutex.Unlock()
	return tos.keeper.AppendTxOut(ctx, tos.height, toi)
}

func (tos *TxOutStorageV1) collectYggdrasilPools(ctx sdk.Context, tx ObservedTx, gasAsset common.Asset) (Vaults, error) {
	// collect yggdrasil pools
	var vaults Vaults
	iterator := tos.keeper.GetVaultIterator(ctx)
	defer iterator.Close()
	for ; iterator.Valid(); iterator.Next() {
		var vault Vault
		if err := tos.keeper.Cdc().UnmarshalBinaryBare(iterator.Value(), &vault); err != nil {
			return nil, fmt.Errorf("fail to unmarshal vault: %w", err)
		}
		if !vault.IsYggdrasil() {
			continue
		}
		// When trying to choose a ygg pool candidate to send out fund , let's make sure the ygg pool has gasAsset , for example, if it is
		// on Binance chain , make sure ygg pool has BNB asset in it , otherwise it won't be able to pay the transaction fee
		if !vault.HasAsset(gasAsset) {
			continue
		}

		// if THORNode are already sending assets from this ygg pool, deduct them.
		addr, err := vault.PubKey.GetThorAddress()
		if err != nil {
			return nil, fmt.Errorf("fail to get thor address from pub key(%s):%w", vault.PubKey, err)
		}

		// if the ygg pool didn't observe the TxIn, and didn't sign the TxIn, THORNode is not going to choose them to send out fund , because they might offline
		if !tx.HasSigned(addr) {
			continue
		}

		block, err := tos.GetBlockOut(ctx)
		if err != nil {
			return nil, fmt.Errorf("fail to get block:%w", err)
		}

		// comments for future reference, this part of logic confuse me quite a few times
		// This method read the vault from key value store, and trying to find out all the ygg candidate that can be used to send out fund
		// given the fact, there might have multiple TxOutItem get created with in one block, and the fund has not been deducted from vault and save back to key values store,
		// thus every previously processed TxOut need to be deducted from the ygg vault to make sure THORNode has a correct view of the ygg funds

		for _, tx := range block.TxArray {
			if !tx.VaultPubKey.Equals(vault.PubKey) {
				continue
			}
			for i, yggCoin := range vault.Coins {
				if !yggCoin.Asset.Equals(tx.Coin.Asset) {
					continue
				}
				vault.Coins[i].Amount = common.SafeSub(vault.Coins[i].Amount, tx.Coin.Amount)
			}
		}

		vaults = append(vaults, vault)
	}

	return vaults, nil
}
