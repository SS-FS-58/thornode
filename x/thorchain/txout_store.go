package thorchain

import (
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"gitlab.com/thorchain/thornode/common"
	"gitlab.com/thorchain/thornode/constants"
)

type TxOutStore interface {
	NewBlock(height uint64, constAccessor constants.ConstantValues)
	CommitBlock(ctx sdk.Context)
	GetBlockOut() *TxOut
	GetOutboundItems() []*TxOutItem
	AddTxOutItem(ctx sdk.Context, toi *TxOutItem)
}

// TxOutStorage is going to manage all the outgoing tx
type TxOutStorage struct {
	blockOut      *TxOut
	keeper        Keeper
	constAccessor constants.ConstantValues
}

// NewTxOutStorage will create a new instance of TxOutStore.
func NewTxOutStorage(keeper Keeper) *TxOutStorage {
	return &TxOutStorage{
		keeper: keeper,
	}
}

// NewBlock create a new block
func (tos *TxOutStorage) NewBlock(height uint64, constAccessor constants.ConstantValues) {
	tos.constAccessor = constAccessor
	tos.blockOut = NewTxOut(height)
}

// CommitBlock THORNode write the block into key value store , thus THORNode could send to signer later.
func (tos *TxOutStorage) CommitBlock(ctx sdk.Context) {
	// if THORNode don't have anything in the array, THORNode don't need to save
	if len(tos.blockOut.TxArray) == 0 {
		return
	}

	// write the tos to keeper
	if err := tos.keeper.SetTxOut(ctx, tos.blockOut); nil != err {
		ctx.Logger().Error("fail to save tx out", err)
	}
}

func (tos *TxOutStorage) GetBlockOut() *TxOut {
	return tos.blockOut
}

func (tos *TxOutStorage) GetOutboundItems() []*TxOutItem {
	return tos.blockOut.TxArray
}

// AddTxOutItem add an item to internal structure
func (tos *TxOutStorage) AddTxOutItem(ctx sdk.Context, toi *TxOutItem) {
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
				ctx.Logger().Error("fail to get observed tx voter", err)
				return
			}
			tx := voter.GetTx(activeNodeAccounts)

			// collect yggdrasil pools
			yggs, err := tos.collectYggdrasilPools(ctx, tx, toi.Chain.GetGasAsset())
			if nil != err {
				ctx.Logger().Error("fail to collect yggdrasil pool", err)
				return
			}

			yggs = yggs.SortBy(toi.Coin.Asset)

			// if none of our Yggdrasil pools have enough funds to fulfil
			// the order, fallback to our Asguard pool
			if len(yggs) > 0 {
				if toi.Coin.Amount.LT(yggs[0].GetCoin(toi.Coin.Asset).Amount) {
					toi.VaultPubKey = yggs[0].PubKey
				}
			}

		}

	}

	// Apparently we couldn't find a yggdrasil vault to send from, so use asgard
	if toi.VaultPubKey.IsEmpty() {

		active, err := tos.keeper.GetAsgardVaultsByStatus(ctx, ActiveVault)
		if err != nil {
			ctx.Logger().Error("fail to get active vaults", err)
		}

		vault := active.SelectByMinCoin(toi.Coin.Asset)
		if vault.IsEmpty() {
			ctx.Logger().Error("empty vault , cannot send out fund")
			return
		}

		toi.VaultPubKey = vault.PubKey
	}

	// Ensure THORNode are not sending from and to the same address
	fromAddr, err := toi.VaultPubKey.GetAddress(toi.Chain)
	if err != nil || fromAddr.IsEmpty() || toi.ToAddress.Equals(fromAddr) {
		return
	}

	// Deduct TransactionFee from TOI and add to Reserve
	nodes, err := tos.keeper.TotalActiveNodeAccount(ctx)
	minumNodesForBFT := tos.constAccessor.GetInt64Value(constants.MinimumNodesForBFT)
	transactionFee := tos.constAccessor.GetInt64Value(constants.TransactionFee)
	if int64(nodes) >= minumNodesForBFT && err == nil {
		var runeFee sdk.Uint
		if toi.Coin.Asset.IsRune() {
			if toi.Coin.Amount.LTE(sdk.NewUint(uint64(transactionFee))) {
				runeFee = toi.Coin.Amount // Fee is the full amount
			} else {
				runeFee = sdk.NewUint(uint64(transactionFee)) // Fee is the prescribed fee
			}
			toi.Coin.Amount = common.SafeSub(toi.Coin.Amount, runeFee)
			if err := tos.keeper.AddFeeToReserve(ctx, runeFee); nil != err {
				// Add to reserve
				ctx.Logger().Error("fail to add fee to reserve", err)
			}
		} else {
			pool, err := tos.keeper.GetPool(ctx, toi.Coin.Asset) // Get pool
			if err != nil {
				// the error is already logged within kvstore
				return
			}

			assetFee := pool.RuneValueInAsset(sdk.NewUint(uint64(transactionFee))) // Get fee in Asset value
			if toi.Coin.Amount.LTE(assetFee) {
				assetFee = toi.Coin.Amount // Fee is the full amount
				runeFee = pool.AssetValueInRune(assetFee)
			} else {
				runeFee = sdk.NewUint(uint64(transactionFee))
			}

			toi.Coin.Amount = common.SafeSub(toi.Coin.Amount, assetFee)  // Deduct Asset fee
			pool.BalanceAsset = pool.BalanceAsset.Add(assetFee)          // Add Asset fee to Pool
			pool.BalanceRune = common.SafeSub(pool.BalanceRune, runeFee) // Deduct Rune from Pool
			if err := tos.keeper.SetPool(ctx, pool); err != nil {        // Set Pool
				ctx.Logger().Error("fail to save pool", err)
				return
			}
			if err := tos.keeper.AddFeeToReserve(ctx, runeFee); nil != err {
				ctx.Logger().Error("fail to add fee to reserve", err)
				return
				// Add to reserve
			}
		}
	}

	if toi.Coin.IsEmpty() {
		ctx.Logger().Info("tx out item has zero coin", toi.String())
		return
	}

	// increment out number of out tx for this in tx
	voter, err := tos.keeper.GetObservedTxVoter(ctx, toi.InHash)
	if err != nil {
		ctx.Logger().Error("fail to get observed tx voter", err)
		return
	}
	voter.Actions = append(voter.Actions, *toi)
	tos.keeper.SetObservedTxVoter(ctx, voter)

	// add tx to block out
	tos.addToBlockOut(toi)
}

func (tos *TxOutStorage) addToBlockOut(toi *TxOutItem) {
	tos.blockOut.TxArray = append(tos.blockOut.TxArray, toi)
}

func (tos *TxOutStorage) collectYggdrasilPools(ctx sdk.Context, tx ObservedTx, gasAsset common.Asset) (Vaults, error) {
	// collect yggdrasil pools
	var vaults Vaults
	iterator := tos.keeper.GetVaultIterator(ctx)
	defer iterator.Close()
	for ; iterator.Valid(); iterator.Next() {
		var vault Vault
		if err := tos.keeper.Cdc().UnmarshalBinaryBare(iterator.Value(), &vault); nil != err {
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

		// comments for future reference, this part of logic confuse me quite a few times
		// This method read the vault from key value store, and trying to find out all the ygg candidate that can be used to send out fund
		// given the fact, there might have multiple TxOutItem get created with in one block, and the fund has not been deducted from vault and save back to key values store,
		// thus every previously processed TxOut need to be deducted from the ygg vault to make sure THORNode has a correct view of the ygg funds
		for _, tx := range tos.blockOut.TxArray {
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
