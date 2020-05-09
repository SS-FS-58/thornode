package thorchain

import (
	"encoding/json"
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"gitlab.com/thorchain/thornode/common"
)

// EventManager define methods need to be support to manage events
type EventManager interface {
	CompleteEvents(ctx sdk.Context, keeper Keeper, height int64, txID common.TxID, txs common.Txs, eventStatus EventStatus)
	EmitPoolEvent(ctx sdk.Context, keeper Keeper, txIn common.TxID, status EventStatus, poolEvt EventPool) error
	EmitErrataEvent(ctx sdk.Context, keeper Keeper, txIn common.TxID, errataEvent EventErrata) error
	EmitGasEvent(ctx sdk.Context, keeper Keeper, gasEvent *EventGas) error
	EmitStakeEvent(ctx sdk.Context, keeper Keeper, inTx common.Tx, stakeEvent EventStake) error
	EmitRewardEvent(ctx sdk.Context, keeper Keeper, rewardEvt EventRewards) error
}

// EventMgr implement EventManager interface
type EventMgr struct {
}

// NewEventMgr create a new instance of EventMgr
func NewEventMgr() *EventMgr {
	return &EventMgr{}
}

// CompleteEvents Mark an event in the given block height to the given status
func (m *EventMgr) CompleteEvents(ctx sdk.Context, keeper Keeper, height int64, txID common.TxID, txs common.Txs, eventStatus EventStatus) {
}

// EmitPoolEvent is going to save a pool event to storage
func (m *EventMgr) EmitPoolEvent(ctx sdk.Context, keeper Keeper, txIn common.TxID, status EventStatus, poolEvt EventPool) error {
	bytes, err := json.Marshal(poolEvt)
	if err != nil {
		return fmt.Errorf("fail to marshal pool event: %w", err)
	}

	tx := common.Tx{
		ID: txIn,
	}
	evt := NewEvent(poolEvt.Type(), ctx.BlockHeight(), tx, bytes, status)
	if err := keeper.UpsertEvent(ctx, evt); err != nil {
		return fmt.Errorf("fail to save pool status change event: %w", err)
	}
	events, err := poolEvt.Events()
	if err != nil {
		return fmt.Errorf("fail to get pool events: %w", err)
	}
	ctx.EventManager().EmitEvents(events)

	return nil
}

// EmitErrataEvent generate an errata event
func (m *EventMgr) EmitErrataEvent(ctx sdk.Context, keeper Keeper, txIn common.TxID, errataEvent EventErrata) error {
	errataBuf, err := json.Marshal(errataEvent)
	if err != nil {
		ctx.Logger().Error("fail to marshal errata event to buf", "error", err)
		return fmt.Errorf("fail to marshal errata event to json: %w", err)
	}
	evt := NewEvent(
		errataEvent.Type(),
		ctx.BlockHeight(),
		common.Tx{ID: txIn},
		errataBuf,
		EventSuccess,
	)
	if err := keeper.UpsertEvent(ctx, evt); err != nil {
		ctx.Logger().Error("fail to save errata event", "error", err)
		return fmt.Errorf("fail to save errata event: %w", err)
	}
	events, err := errataEvent.Events()
	if err != nil {
		return fmt.Errorf("fail to emit standard event: %w", err)
	}
	ctx.EventManager().EmitEvents(events)
	return nil
}

func (m *EventMgr) EmitGasEvent(ctx sdk.Context, keeper Keeper, gasEvent *EventGas) error {
	if gasEvent == nil {
		return nil
	}
	buf, err := json.Marshal(gasEvent)
	if err != nil {
		ctx.Logger().Error("fail to marshal gas event", "error", err)
		return fmt.Errorf("fail to marshal gas event to json: %w", err)
	}
	evt := NewEvent(gasEvent.Type(), ctx.BlockHeight(), common.Tx{ID: common.BlankTxID}, buf, EventSuccess)
	if err := keeper.UpsertEvent(ctx, evt); err != nil {
		ctx.Logger().Error("fail to upsert event", "error", err)
		return fmt.Errorf("fail to save gas event: %w", err)
	}
	events, err := gasEvent.Events()
	if err != nil {
		return fmt.Errorf("fail to get events: %w", err)
	}
	ctx.EventManager().EmitEvents(events)

	return nil
}

// EmitStakeEvent add the stake event to block
func (m *EventMgr) EmitStakeEvent(ctx sdk.Context, keeper Keeper, inTx common.Tx, stakeEvent EventStake) error {
	stakeBytes, err := json.Marshal(stakeEvent)
	if err != nil {
		return fmt.Errorf("fail to marshal stake event to json: %w", err)
	}
	evt := NewEvent(
		stakeEvent.Type(),
		ctx.BlockHeight(),
		inTx,
		stakeBytes,
		EventSuccess,
	)
	// stake event doesn't need to have outbound
	tx := common.Tx{ID: common.BlankTxID}
	evt.OutTxs = common.Txs{tx}
	if err := keeper.UpsertEvent(ctx, evt); err != nil {
		return fmt.Errorf("fail to save stake event: %w", err)
	}
	events, err := stakeEvent.Events()
	if err != nil {
		return fmt.Errorf("fail to get events: %w", err)
	}
	ctx.EventManager().EmitEvents(events)
	return nil
}

// EmitRewardEvent save the reward event to keyvalue store and also use event manager
func (m *EventMgr) EmitRewardEvent(ctx sdk.Context, keeper Keeper, rewardEvt EventRewards) error {
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
	if err := keeper.UpsertEvent(ctx, evt); err != nil {
		return fmt.Errorf("fail to save event: %w", err)
	}
	events, err := rewardEvt.Events()
	if err != nil {
		return fmt.Errorf("fail to get events: %w", err)
	}
	ctx.EventManager().EmitEvents(events)
	return nil
}
