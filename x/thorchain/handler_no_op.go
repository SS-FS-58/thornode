package thorchain

import (
	"fmt"

	"github.com/blang/semver"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"gitlab.com/thorchain/thornode/common"
	"gitlab.com/thorchain/thornode/constants"
)

type NoOpHandler struct {
	keeper Keeper
}

func NewNoOpHandler(keeper Keeper) NoOpHandler {
	return NoOpHandler{
		keeper: keeper,
	}
}

func (h NoOpHandler) Run(ctx sdk.Context, m sdk.Msg, version semver.Version, _ constants.ConstantValues) sdk.Result {
	msg, ok := m.(MsgNoOp)
	if !ok {
		return errInvalidMessage.Result()
	}
	if err := h.Validate(ctx, msg, version); err != nil {
		return sdk.ErrInternal(err.Error()).Result()
	}
	if err := h.Handle(ctx, msg, version); err != nil {
		return sdk.ErrInternal(err.Error()).Result()
	}
	return sdk.Result{
		Code:      sdk.CodeOK,
		Codespace: DefaultCodespace,
	}
}

func (h NoOpHandler) Validate(ctx sdk.Context, msg MsgNoOp, version semver.Version) error {
	if version.GTE(semver.MustParse("0.1.0")) {
		return h.ValidateV1(ctx, msg)
	} else {
		ctx.Logger().Error(errInvalidVersion.Error())
		return errInvalidVersion
	}
}

func (h NoOpHandler) ValidateV1(ctx sdk.Context, msg MsgNoOp) error {
	if err := msg.ValidateBasic(); err != nil {
		ctx.Logger().Error(err.Error())
		return err
	}
	return nil
}

func (h NoOpHandler) Handle(ctx sdk.Context, msg MsgNoOp, version semver.Version) error {
	ctx.Logger().Info("handleMsgNoOp request")
	if version.GTE(semver.MustParse("0.1.0")) {
		return h.HandleV1(ctx, msg)
	} else {
		ctx.Logger().Error(errInvalidVersion.Error())
		return errInvalidVersion
	}
}

// Handle doesn't do anything, its a no op
func (h NoOpHandler) HandleV1(ctx sdk.Context, msg MsgNoOp) error {
	ctx.Logger().Info("receive no op msg")
	gasCoin := common.Gas{}
	for _, c := range msg.ObservedTx.Tx.Coins {
		gasCoin = append(gasCoin, c)
	}
	blockGas, err := h.keeper.GetBlockGas(ctx)
	if err != nil {
		return fmt.Errorf("fail to get block gas: %w", err)
	}
	blockGas.AddGas(gasCoin, GasTypeTopup)
	return h.keeper.SaveBlockGas(ctx, blockGas)
}
