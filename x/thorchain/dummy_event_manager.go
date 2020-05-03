package thorchain

import (
	"github.com/blang/semver"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"gitlab.com/thorchain/thornode/common"
	"gitlab.com/thorchain/thornode/constants"
)

// DummyEventMgr used for test purpose , and it implement EventManager interface
type DummyEventMgr struct {
}

func NewDummyEventMgr() *DummyEventMgr {
	return &DummyEventMgr{}
}

func (m *DummyEventMgr) BeginBlock(ctx sdk.Context) {
}

func (m *DummyEventMgr) EndBlock(ctx sdk.Context, keeper Keeper) {
}

func (m *DummyEventMgr) GetBlockEvents(ctx sdk.Context, keeper Keeper, height int64) (*BlockEvents, error) {
	return nil, nil
}

func (m *DummyEventMgr) CompleteEvents(ctx sdk.Context, keeper Keeper, height int64, txID common.TxID, txs common.Txs, eventStatus EventStatus) error {
	return nil
}

func (m *DummyEventMgr) AddEvent(event Event) {
}
func (m *DummyEventMgr) FailStalePendingEvents(ctx sdk.Context, constantValues constants.ConstantValues, keeper Keeper) {
}
func (m *DummyEventMgr) UpdateEventFee(ctx sdk.Context, txID common.TxID, fee common.Fee) {}

type DummyVersionedEventMgr struct{}

func NewDummyVersionedEventMgr() *DummyVersionedEventMgr {
	return &DummyVersionedEventMgr{}
}

func (m *DummyVersionedEventMgr) GetEventManager(ctx sdk.Context, version semver.Version) (EventManager, error) {
	return NewDummyEventMgr(), nil
}
