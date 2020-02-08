package thorchain

import (
	"fmt"

	"github.com/blang/semver"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"gitlab.com/thorchain/thornode/constants"
)

type ObservedTxOutHandler struct {
	keeper                Keeper
	versionedTxOutStore   VersionedTxOutStore
	validatorMgr          VersionedValidatorManager
	versionedVaultManager VersionedVaultManager
}

func NewObservedTxOutHandler(keeper Keeper, txOutStore VersionedTxOutStore, validatorMgr VersionedValidatorManager, versionedVaultManager VersionedVaultManager) ObservedTxOutHandler {
	return ObservedTxOutHandler{
		keeper:                keeper,
		versionedTxOutStore:   txOutStore,
		validatorMgr:          validatorMgr,
		versionedVaultManager: versionedVaultManager,
	}
}

func (h ObservedTxOutHandler) Run(ctx sdk.Context, m sdk.Msg, version semver.Version, _ constants.ConstantValues) sdk.Result {
	msg, ok := m.(MsgObservedTxOut)
	if !ok {
		return errInvalidMessage.Result()
	}
	if err := h.validate(ctx, msg, version); err != nil {
		return sdk.ErrInternal(err.Error()).Result()
	}
	return h.handle(ctx, msg, version)
}

func (h ObservedTxOutHandler) validate(ctx sdk.Context, msg MsgObservedTxOut, version semver.Version) error {
	if version.GTE(semver.MustParse("0.1.0")) {
		return h.validateV1(ctx, msg)
	} else {
		ctx.Logger().Error(errInvalidVersion.Error())
		return errInvalidVersion
	}
}

func (h ObservedTxOutHandler) validateV1(ctx sdk.Context, msg MsgObservedTxOut) error {
	if err := msg.ValidateBasic(); err != nil {
		ctx.Logger().Error(err.Error())
		return err
	}

	if !isSignedByActiveObserver(ctx, h.keeper, msg.GetSigners()) {
		ctx.Logger().Error(notAuthorized.Error())
		return notAuthorized
	}

	return nil
}

func (h ObservedTxOutHandler) handle(ctx sdk.Context, msg MsgObservedTxOut, version semver.Version) sdk.Result {
	ctx.Logger().Info("handleMsgObservedTxOut request", "Tx:", msg.Txs[0].String())
	if version.GTE(semver.MustParse("0.1.0")) {
		return h.handleV1(ctx, msg)
	} else {
		ctx.Logger().Error(errInvalidVersion.Error())
		return errBadVersion.Result()
	}
}

func (h ObservedTxOutHandler) preflight(ctx sdk.Context, voter ObservedTxVoter, nas NodeAccounts, tx ObservedTx, signer ObservedSigner) (ObservedTxVoter, bool) {
	voter.Add(tx, signer)
	ok := false
	if voter.HasConsensus(nas) && !voter.ProcessedOut {
		ok = true
		voter.Height = ctx.BlockHeight()
		voter.ProcessedOut = true
	}
	h.keeper.SetObservedTxVoter(ctx, voter)
	// Check to see if we have enough identical observations to process the transaction
	return voter, ok
}

// Handle a message to observe inbound tx
func (h ObservedTxOutHandler) handleV1(ctx sdk.Context, msg MsgObservedTxOut) sdk.Result {
	activeNodeAccounts, err := h.keeper.ListActiveNodeAccounts(ctx)
	if err != nil {
		err = wrapError(ctx, err, "fail to get list of active node accounts")
		return sdk.ErrInternal(err.Error()).Result()
	}

	handler := NewHandler(h.keeper, h.versionedTxOutStore, h.validatorMgr, h.versionedVaultManager)

	for _, tx := range msg.Txs {

		// check we are sending from a valid vault
		if !h.keeper.VaultExists(ctx, tx.ObservedPubKey) {
			ctx.Logger().Info("Observed Pubkey", tx.ObservedPubKey)
			return sdk.ErrInternal("Observed Tx Pubkey is not associated with a valid vault").Result()
		}

		voter, err := h.keeper.GetObservedTxVoter(ctx, tx.Tx.ID)
		if err != nil {
			return sdk.ErrInternal(err.Error()).Result()
		}

		if len(tx.Signers) == 0 {
			return sdk.ErrInternal("no signers present").Result()
		}

		// check whether the tx has consensus
		voter, ok := h.preflight(ctx, voter, activeNodeAccounts, tx, tx.Signers[0])
		if !ok {
			ctx.Logger().Info("Outbound observation preflight requirements not yet met...")
			continue
		}

		txOut := voter.GetTx(activeNodeAccounts) // get consensus tx, in case our for loop is incorrect
		m, err := processOneTxIn(ctx, h.keeper, txOut, msg.Signer)
		if err != nil || tx.Tx.Chain.IsEmpty() {
			ctx.Logger().Error("fail to process txOut",
				"error", err,
				"tx", tx.Tx.String())
			continue
		}
		// when thorchain fail to parse the out going tx memo, likely it is an unauthorised tx
		// in that case, thorchain doesn't subtract the fund from relevant vault, thus when the node/yggdrasil leave, they will either return those asset
		// or they will be slashed for that amount, also if the tx memo is unknown , thorchain also doesn't subsidise gas
		// Apply Gas fees
		if err := AddGasFees(ctx, h.keeper, tx); err != nil {
			return sdk.ErrInternal(fmt.Errorf("fail to add gas fee: %w", err).Error()).Result()
		}

		// If sending from one of our vaults, decrement coins
		vault, err := h.keeper.GetVault(ctx, tx.ObservedPubKey)
		if err != nil {
			ctx.Logger().Error("fail to get vault", "error", err)
			return sdk.ErrInternal("fail to get vault").Result()
		}
		vault.SubFunds(tx.Tx.Coins)
		if err := h.keeper.SetVault(ctx, vault); err != nil {
			ctx.Logger().Error("fail to save vault", "error", err)
			return sdk.ErrInternal("fail to save vault").Result()
		}

		// add addresses to observing addresses. This is used to detect
		// active/inactive observing node accounts
		accAddresses := make([]sdk.AccAddress, len(txOut.Signers))
		for i := range txOut.Signers {
			accAddresses[i] = txOut.Signers[i].Address
		}
		if err := h.keeper.AddObservingAddresses(ctx, accAddresses); err != nil {
			return sdk.ErrInternal(err.Error()).Result()
		}

		result := handler(ctx, m)
		if !result.IsOK() {
			ctx.Logger().Error("Handler failed:", "error", result.Log)
		}
	}

	return sdk.Result{
		Code:      sdk.CodeOK,
		Codespace: DefaultCodespace,
	}
}
