package thorchain

import (
	"github.com/blang/semver"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"gitlab.com/thorchain/thornode/constants"
)

type ReserveContributorHandler struct {
	keeper Keeper
}

func NewReserveContributorHandler(keeper Keeper) ReserveContributorHandler {
	return ReserveContributorHandler{
		keeper: keeper,
	}
}

func (h ReserveContributorHandler) Run(ctx sdk.Context, m sdk.Msg, version semver.Version, _ constants.ConstantValues) sdk.Result {
	msg, ok := m.(MsgReserveContributor)
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

func (h ReserveContributorHandler) Validate(ctx sdk.Context, msg MsgReserveContributor, version semver.Version) error {
	if version.GTE(semver.MustParse("0.1.0")) {
		return h.ValidateV1(ctx, msg)
	} else {
		ctx.Logger().Error(errInvalidVersion.Error())
		return errInvalidVersion
	}
}

func (h ReserveContributorHandler) ValidateV1(ctx sdk.Context, msg MsgReserveContributor) error {
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

func (h ReserveContributorHandler) Handle(ctx sdk.Context, msg MsgReserveContributor, version semver.Version) error {
	ctx.Logger().Info("handleMsgReserveContributor request")
	if version.GTE(semver.MustParse("0.1.0")) {
		return h.HandleV1(ctx, msg)
	} else {
		ctx.Logger().Error(errInvalidVersion.Error())
		return errInvalidVersion
	}
}

// Handle a message to set pooldata
func (h ReserveContributorHandler) HandleV1(ctx sdk.Context, msg MsgReserveContributor) error {
	reses, err := h.keeper.GetReservesContributors(ctx)
	if err != nil {
		ctx.Logger().Error("fail to get reserve contributors", "error", err)
		return err
	}

	reses = reses.Add(msg.Contributor)
	if err := h.keeper.SetReserveContributors(ctx, reses); err != nil {
		ctx.Logger().Error("fail to save reserve contributors", "error", err)
		return err
	}

	vault, err := h.keeper.GetVaultData(ctx)
	if err != nil {
		ctx.Logger().Error("fail to get vault data", "error", err)
		return err
	}

	vault.TotalReserve = vault.TotalReserve.Add(msg.Contributor.Amount)
	if err := h.keeper.SetVaultData(ctx, vault); err != nil {
		ctx.Logger().Error("fail to save vault data", "error", err)
		return err
	}

	return nil
}
