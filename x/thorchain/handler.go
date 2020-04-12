package thorchain

import (
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/pkg/errors"

	"gitlab.com/thorchain/thornode/common"
	"gitlab.com/thorchain/thornode/constants"
)

// THORChain error code start at 101
const (
	// CodeBadVersion error code for bad version
	CodeBadVersion            sdk.CodeType = 101
	CodeInvalidMessage        sdk.CodeType = 102
	CodeConstantsNotAvailable sdk.CodeType = 103
	CodeInvalidVault          sdk.CodeType = 104
	CodeInvalidMemo           sdk.CodeType = 105
	CodeValidationError       sdk.CodeType = 106
	CodeInvalidPoolStatus     sdk.CodeType = 107

	CodeSwapFail                 sdk.CodeType = 108
	CodeSwapFailTradeTarget      sdk.CodeType = 109
	CodeSwapFailNotEnoughFee     sdk.CodeType = 110
	CodeSwapFailZeroEmitAsset    sdk.CodeType = 111
	CodeSwapFailPoolNotExist     sdk.CodeType = 112
	CodeSwapFailInvalidAmount    sdk.CodeType = 113
	CodeSwapFailInvalidBalance   sdk.CodeType = 114
	CodeSwapFailNotEnoughBalance sdk.CodeType = 115

	CodeStakeFailValidation    sdk.CodeType = 120
	CodeFailGetPoolStaker      sdk.CodeType = 122
	CodeStakeMismatchAssetAddr sdk.CodeType = 123
	CodeStakeInvalidPoolAsset  sdk.CodeType = 124
	CodeStakeRUNEOverLimit     sdk.CodeType = 125
	CodeStakeRUNEMoreThanBond  sdk.CodeType = 126

	CodeUnstakeFailValidation sdk.CodeType = 130
	CodeFailAddOutboundTx     sdk.CodeType = 131
	CodeFailSaveEvent         sdk.CodeType = 132
	CodePoolStakerNotExist    sdk.CodeType = 133
	CodeStakerPoolNotExist    sdk.CodeType = 134
	CodeNoStakeUnitLeft       sdk.CodeType = 135
	CodeUnstakeWithin24Hours  sdk.CodeType = 136
	CodeUnstakeFail           sdk.CodeType = 137
	CodeEmptyChain            sdk.CodeType = 138
)

var (
	notAuthorized        = fmt.Errorf("not authorized")
	errInvalidVersion    = fmt.Errorf("bad version")
	errBadVersion        = sdk.NewError(DefaultCodespace, CodeBadVersion, errInvalidVersion.Error())
	errInvalidMessage    = sdk.NewError(DefaultCodespace, CodeInvalidMessage, "invalid message")
	errConstNotAvailable = sdk.NewError(DefaultCodespace, CodeConstantsNotAvailable, "constant values not available")
)

// NewHandler returns a handler for "thorchain" type messages.
func NewHandler(keeper Keeper,
	versionedTxOutStore VersionedTxOutStore,
	validatorMgr VersionedValidatorManager,
	versionedVaultManager VersionedVaultManager,
	versionedObserverManager VersionedObserverManager,
	versionedGasMgr VersionedGasManager) sdk.Handler {
	handlerMap := getHandlerMapping(keeper, versionedTxOutStore, validatorMgr, versionedVaultManager, versionedObserverManager, versionedGasMgr)

	return func(ctx sdk.Context, msg sdk.Msg) sdk.Result {
		version := keeper.GetLowestActiveVersion(ctx)
		constantValues := constants.GetConstantValues(version)
		if constantValues == nil {
			return errConstNotAvailable.Result()
		}
		h, ok := handlerMap[msg.Type()]
		if !ok {
			errMsg := fmt.Sprintf("Unrecognized thorchain Msg type: %v", msg.Type())
			return sdk.ErrUnknownRequest(errMsg).Result()
		}
		return h.Run(ctx, msg, version, constantValues)
	}
}

func getHandlerMapping(keeper Keeper,
	versionedTxOutStore VersionedTxOutStore,
	validatorMgr VersionedValidatorManager,
	versionedVaultManager VersionedVaultManager,
	versionedObserverManager VersionedObserverManager,
	versionedGasMgr VersionedGasManager) map[string]MsgHandler {
	// New arch handlers
	m := make(map[string]MsgHandler)
	m[MsgOutboundTx{}.Type()] = NewOutboundTxHandler(keeper)
	m[MsgTssPool{}.Type()] = NewTssHandler(keeper, versionedVaultManager)
	m[MsgYggdrasil{}.Type()] = NewYggdrasilHandler(keeper, versionedTxOutStore, validatorMgr)
	m[MsgEndPool{}.Type()] = NewEndPoolHandler(keeper, versionedTxOutStore)
	m[MsgSetNodeKeys{}.Type()] = NewSetNodeKeysHandler(keeper)
	m[MsgSwap{}.Type()] = NewSwapHandler(keeper, versionedTxOutStore)
	m[MsgReserveContributor{}.Type()] = NewReserveContributorHandler(keeper)
	m[MsgSetVersion{}.Type()] = NewVersionHandler(keeper)
	m[MsgBond{}.Type()] = NewBondHandler(keeper)
	m[MsgObservedTxIn{}.Type()] = NewObservedTxInHandler(keeper, versionedObserverManager, versionedTxOutStore, validatorMgr, versionedVaultManager, versionedGasMgr)
	m[MsgObservedTxOut{}.Type()] = NewObservedTxOutHandler(keeper, versionedObserverManager, versionedTxOutStore, validatorMgr, versionedVaultManager, versionedGasMgr)
	m[MsgLeave{}.Type()] = NewLeaveHandler(keeper, validatorMgr, versionedTxOutStore)
	m[MsgAdd{}.Type()] = NewAddHandler(keeper)
	m[MsgSetUnStake{}.Type()] = NewUnstakeHandler(keeper, versionedTxOutStore)
	m[MsgSetStakeData{}.Type()] = NewStakeHandler(keeper)
	m[MsgRefundTx{}.Type()] = NewRefundHandler(keeper)
	m[MsgTssKeysignFail{}.Type()] = NewTssKeysignHandler(keeper)
	m[MsgMigrate{}.Type()] = NewMigrateHandler(keeper)
	m[MsgRagnarok{}.Type()] = NewRagnarokHandler(keeper)
	return m
}

func processOneTxIn(ctx sdk.Context, keeper Keeper, tx ObservedTx, signer sdk.AccAddress) (sdk.Msg, sdk.Error) {
	if len(tx.Tx.Coins) == 0 {
		return nil, sdk.ErrUnknownRequest("no coin found")
	}
	memo, err := ParseMemo(tx.Tx.Memo)
	if err != nil {
		ctx.Logger().Error("fail to parse memo", "error", err)
		return nil, sdk.NewError(DefaultCodespace, CodeInvalidMemo, err.Error())
	}
	// THORNode should not have one tx across chain, if it is cross chain it should be separate tx
	var newMsg sdk.Msg
	// interpret the memo and initialize a corresponding msg event
	switch m := memo.(type) {
	case StakeMemo:
		newMsg, err = getMsgStakeFromMemo(ctx, m, tx, signer)
		if err != nil {
			return nil, sdk.NewError(DefaultCodespace, CodeInvalidMemo, "invalid stake memo:%s", err.Error())
		}

	case UnstakeMemo:
		newMsg, err = getMsgUnstakeFromMemo(m, tx, signer)
		if err != nil {
			return nil, sdk.NewError(DefaultCodespace, CodeInvalidMemo, "invalid withdraw memo:%s", err.Error())
		}
	case SwapMemo:
		newMsg, err = getMsgSwapFromMemo(m, tx, signer)
		if err != nil {
			return nil, sdk.NewError(DefaultCodespace, CodeInvalidMemo, "invalid swap memo:%s", err.Error())
		}
	case AddMemo:
		newMsg, err = getMsgAddFromMemo(m, tx, signer)
		if err != nil {
			return nil, sdk.NewError(DefaultCodespace, CodeInvalidMemo, "invalid add memo:%s", err.Error())
		}
	case GasMemo:
		newMsg, err = getMsgNoOpFromMemo(tx, signer)
		if err != nil {
			return nil, sdk.NewError(DefaultCodespace, CodeInvalidMemo, "invalid noop memo:%s", err.Error())
		}
	case RefundMemo:
		newMsg, err = getMsgRefundFromMemo(m, tx, signer)
		if err != nil {
			return nil, sdk.NewError(DefaultCodespace, CodeInvalidMemo, "invalid refund memo:%s", err.Error())
		}
	case OutboundMemo:
		newMsg, err = getMsgOutboundFromMemo(m, tx, signer)
		if err != nil {
			return nil, sdk.NewError(DefaultCodespace, CodeInvalidMemo, "invalid outbound memo:%s", err.Error())
		}
	case MigrateMemo:
		newMsg, err = getMsgMigrateFromMemo(m, tx, signer)
		if err != nil {
			return nil, sdk.NewError(DefaultCodespace, CodeInvalidMemo, "invalid migrate memo: %s", err.Error())
		}
	case BondMemo:
		newMsg, err = getMsgBondFromMemo(m, tx, signer)
		if err != nil {
			return nil, sdk.NewError(DefaultCodespace, CodeInvalidMemo, "invalid bond memo:%s", err.Error())
		}
	case RagnarokMemo:
		newMsg, err = getMsgRagnarokFromMemo(m, tx, signer)
		if err != nil {
			return nil, sdk.NewError(DefaultCodespace, CodeInvalidMemo, "invalid ragnarok memo: %s", err.Error())
		}
	case LeaveMemo:
		newMsg = NewMsgLeave(tx.Tx, signer)
	case YggdrasilFundMemo:
		newMsg = NewMsgYggdrasil(tx.Tx, tx.ObservedPubKey, m.GetBlockHeight(), true, tx.Tx.Coins, signer)
	case YggdrasilReturnMemo:
		newMsg = NewMsgYggdrasil(tx.Tx, tx.ObservedPubKey, m.GetBlockHeight(), false, tx.Tx.Coins, signer)
	case ReserveMemo:
		res := NewReserveContributor(tx.Tx.FromAddress, tx.Tx.Coins[0].Amount)
		newMsg = NewMsgReserveContributor(tx.Tx, res, signer)

	default:
		return nil, sdk.NewError(DefaultCodespace, CodeInvalidMemo, "invalid memo")
	}

	if err := newMsg.ValidateBasic(); err != nil {
		return nil, sdk.NewError(DefaultCodespace, CodeInvalidMemo, "invalid message:%s", err.Error())
	}
	return newMsg, nil
}

func getMsgNoOpFromMemo(tx ObservedTx, signer sdk.AccAddress) (sdk.Msg, error) {
	for _, coin := range tx.Tx.Coins {
		if !coin.Asset.IsBNB() {
			return nil, errors.New("Only accepts BNB coins")
		}
	}
	return NewMsgNoOp(tx, signer), nil
}

func getMsgSwapFromMemo(memo SwapMemo, tx ObservedTx, signer sdk.AccAddress) (sdk.Msg, error) {
	if len(tx.Tx.Coins) > 1 {
		return nil, errors.New("not expecting multiple coins in a swap")
	}
	if memo.Destination.IsEmpty() {
		memo.Destination = tx.Tx.FromAddress
	}

	coin := tx.Tx.Coins[0]
	if memo.Asset.Equals(coin.Asset) {
		return nil, errors.Errorf("swap from %s to %s is noop, refund", memo.Asset.String(), coin.Asset.String())
	}

	// Looks like at the moment THORNode can only process ont ty
	return NewMsgSwap(tx.Tx, memo.GetAsset(), memo.Destination, memo.SlipLimit, signer), nil
}

func getMsgUnstakeFromMemo(memo UnstakeMemo, tx ObservedTx, signer sdk.AccAddress) (sdk.Msg, error) {
	withdrawAmount := sdk.NewUint(MaxUnstakeBasisPoints)
	if len(memo.GetAmount()) > 0 {
		withdrawAmount = sdk.NewUintFromString(memo.GetAmount())
	}
	return NewMsgSetUnStake(tx.Tx, tx.Tx.FromAddress, withdrawAmount, memo.GetAsset(), signer), nil
}

func getMsgStakeFromMemo(ctx sdk.Context, memo StakeMemo, tx ObservedTx, signer sdk.AccAddress) (sdk.Msg, error) {
	// when staker stake to a pool ,usually it will be two coins, RUNE and the asset of the pool.
	// if it is multi-chain , like NOT Binance chain , it is using two asymmetric staking
	if len(tx.Tx.Coins) > 2 {
		return nil, errors.New("not expecting more than two coins in a stake")
	}
	runeAmount := sdk.ZeroUint()
	assetAmount := sdk.ZeroUint()
	asset := memo.GetAsset()
	if asset.IsEmpty() {
		return nil, errors.New("Unable to determine the intended pool for this stake")
	}
	// There is no dedicate pool for RUNE ,because every pool will have RUNE , that's by design
	if asset.IsRune() {
		return nil, errors.New("invalid pool asset")
	}
	// Extract the Rune amount and the asset amount from the transaction. At least one of them must be
	// nonzero. If we saw two types of coins, one of them must be the asset coin.
	for _, coin := range tx.Tx.Coins {
		ctx.Logger().Info("coin", "asset", coin.Asset.String(), "amount", coin.Amount.String())
		if coin.Asset.IsRune() {
			runeAmount = coin.Amount
		}
		if asset.Equals(coin.Asset) {
			assetAmount = coin.Amount
		}
	}

	if runeAmount.IsZero() && assetAmount.IsZero() {
		return nil, errors.New("did not find any valid coins for stake")
	}

	// when THORNode receive two coins, but THORNode didn't find the coin specify by asset, then user might send in the wrong coin
	if assetAmount.IsZero() && len(tx.Tx.Coins) == 2 {
		return nil, fmt.Errorf("did not find %s ", asset)
	}

	runeAddr := tx.Tx.FromAddress
	assetAddr := memo.GetDestination()
	// this is to cover multi-chain scenario, for example BTC , staker who would like to stake in BTC pool,  will have to complete
	// the stake operation by sending in two asymmetric stake tx, one tx on BTC chain with memo stake:BTC:<RUNE address> ,
	// and another one on Binance chain with stake:BTC , with only RUNE as the coin
	// Thorchain will use the <RUNE address> to match these two together , and consider it as one stake.
	if !runeAddr.IsChain(common.BNBChain) {
		runeAddr = memo.GetDestination()
		assetAddr = tx.Tx.FromAddress
	} else {
		// if it is on BNB chain , while the asset addr is empty, then the asset addr is runeAddr
		if assetAddr.IsEmpty() {
			assetAddr = runeAddr
		}
	}

	return NewMsgSetStakeData(
		tx.Tx,
		asset,
		runeAmount,
		assetAmount,
		runeAddr,
		assetAddr,
		signer,
	), nil
}

func getMsgAddFromMemo(memo AddMemo, tx ObservedTx, signer sdk.AccAddress) (sdk.Msg, error) {
	runeAmount := sdk.ZeroUint()
	assetAmount := sdk.ZeroUint()
	for _, coin := range tx.Tx.Coins {
		if coin.Asset.IsRune() {
			runeAmount = coin.Amount
		} else if memo.GetAsset().Equals(coin.Asset) {
			assetAmount = coin.Amount
		}
	}
	return NewMsgAdd(
		tx.Tx,
		memo.GetAsset(),
		runeAmount,
		assetAmount,
		signer,
	), nil
}

func getMsgRefundFromMemo(memo RefundMemo, tx ObservedTx, signer sdk.AccAddress) (sdk.Msg, error) {
	return NewMsgRefundTx(
		tx,
		memo.GetTxID(),
		signer,
	), nil
}

func getMsgOutboundFromMemo(memo OutboundMemo, tx ObservedTx, signer sdk.AccAddress) (sdk.Msg, error) {
	return NewMsgOutboundTx(
		tx,
		memo.GetTxID(),
		signer,
	), nil
}

func getMsgMigrateFromMemo(memo MigrateMemo, tx ObservedTx, signer sdk.AccAddress) (sdk.Msg, error) {
	return NewMsgMigrate(tx, memo.GetBlockHeight(), signer), nil
}

func getMsgRagnarokFromMemo(memo RagnarokMemo, tx ObservedTx, signer sdk.AccAddress) (sdk.Msg, error) {
	return NewMsgRagnarok(tx, memo.GetBlockHeight(), signer), nil
}

func getMsgBondFromMemo(memo BondMemo, tx ObservedTx, signer sdk.AccAddress) (sdk.Msg, error) {
	runeAmount := sdk.ZeroUint()
	for _, coin := range tx.Tx.Coins {
		if coin.Asset.IsRune() {
			runeAmount = coin.Amount
		}
	}
	if runeAmount.IsZero() {
		return nil, errors.New("RUNE amount is 0")
	}
	return NewMsgBond(tx.Tx, memo.GetNodeAddress(), runeAmount, tx.Tx.FromAddress, signer), nil
}
