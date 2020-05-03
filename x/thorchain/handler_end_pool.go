package thorchain

import (
	"fmt"

	"github.com/blang/semver"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"gitlab.com/thorchain/thornode/constants"
)

type EndPoolHandler struct {
	keeper                Keeper
	versionedTxOutStore   VersionedTxOutStore
	versionedEventManager VersionedEventManager
}

func NewEndPoolHandler(keeper Keeper, versionedTxOutStore VersionedTxOutStore, versionedEventManager VersionedEventManager) EndPoolHandler {
	return EndPoolHandler{
		keeper:                keeper,
		versionedTxOutStore:   versionedTxOutStore,
		versionedEventManager: versionedEventManager,
	}
}

func (h EndPoolHandler) Run(ctx sdk.Context, m sdk.Msg, version semver.Version, constAccessor constants.ConstantValues) sdk.Result {
	msg, ok := m.(MsgEndPool)
	if !ok {
		return errInvalidMessage.Result()
	}
	if err := h.validate(ctx, msg, version); err != nil {
		return sdk.ErrInternal(err.Error()).Result()
	}
	return h.handle(ctx, msg, version, constAccessor)
}

func (h EndPoolHandler) validate(ctx sdk.Context, msg MsgEndPool, version semver.Version) error {
	if version.GTE(semver.MustParse("0.1.0")) {
		return h.validateV1(ctx, msg)
	} else {
		ctx.Logger().Error(errInvalidVersion.Error())
		return errInvalidVersion
	}
}

func (h EndPoolHandler) validateV1(ctx sdk.Context, msg MsgEndPool) error {
	if err := msg.ValidateBasic(); err != nil {
		ctx.Logger().Error(err.Error())
		return err
	}

	if !isSignedByActiveNodeAccounts(ctx, h.keeper, msg.GetSigners()) {
		ctx.Logger().Error(notAuthorized.Error())
		return notAuthorized
	}
	return nil
}

func (h EndPoolHandler) handle(ctx sdk.Context, msg MsgEndPool, version semver.Version, constAccessor constants.ConstantValues) sdk.Result {
	ctx.Logger().Info("receive MsgEndPool", "asset", msg.Asset, "requester", msg.Tx.FromAddress, "signer", msg.Signer.String())
	if version.GTE(semver.MustParse("0.1.0")) {
		return h.handleV1(ctx, msg, version, constAccessor)
	} else {
		ctx.Logger().Error(errInvalidVersion.Error())
		return errBadVersion.Result()
	}
}

func (h EndPoolHandler) handleV1(ctx sdk.Context, msg MsgEndPool, version semver.Version, constAccessor constants.ConstantValues) sdk.Result {
	pool, err := h.keeper.GetPool(ctx, msg.Asset)
	if err != nil {
		err = fmt.Errorf("fail to set pool: %w", err)
		return sdk.ErrInternal(err.Error()).Result()
	}

	pool.Status = PoolBootstrap
	if err := h.keeper.SetPool(ctx, pool); err != nil {
		err = fmt.Errorf("fail to set pool: %w", err)
		return sdk.ErrInternal(err.Error()).Result()
	}

	iterator := h.keeper.GetStakerIterator(ctx, msg.Asset)
	defer iterator.Close()
	for ; iterator.Valid(); iterator.Next() {
		var staker Staker
		h.keeper.Cdc().MustUnmarshalBinaryBare(iterator.Value(), &staker)
		msg.Tx.FromAddress = staker.AssetAddress
		unstakeMsg := NewMsgSetUnStake(
			msg.Tx,
			staker.RuneAddress,
			sdk.NewUint(MaxUnstakeBasisPoints),
			staker.Asset,
			msg.Signer,
		)
		unstakeHandler := NewUnstakeHandler(h.keeper, h.versionedTxOutStore, h.versionedEventManager)
		result := unstakeHandler.Run(ctx, unstakeMsg, version, constAccessor)
		if !result.IsOK() {
			ctx.Logger().Error("fail to unstake", "staker", staker.RuneAddress, "error", result.Log)
			return result
		}
	}

	return sdk.Result{
		Code:      sdk.CodeOK,
		Codespace: DefaultCodespace,
	}
}
