package thorchain

import (
	"errors"

	"github.com/blang/semver"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"gitlab.com/thorchain/thornode/common"
	"gitlab.com/thorchain/thornode/constants"

	"gitlab.com/thorchain/thornode/x/thorchain/types"
)

var (
	notExistPoolStakerAsset, _ = common.NewAsset("BNB.NotExistPoolStakerAsset")
	notExistStakerPoolAddr     = common.Address("4252BA642F73FA402FEF18E3CB4550E5A4A6831299D5EB7E76808C8923FC1XXX")
)

type MockInMemoryPoolStorage struct {
	KVStoreDummy
	store map[string]interface{}
}

// NewMockInMemoryPoolStorage
func NewMockInMemoryPoolStorage() *MockInMemoryPoolStorage {
	return &MockInMemoryPoolStorage{store: make(map[string]interface{})}
}

func (p *MockInMemoryPoolStorage) PoolExist(ctx sdk.Context, asset common.Asset) bool {
	_, ok := p.store[asset.String()]
	return ok
}

func (p *MockInMemoryPoolStorage) GetPool(ctx sdk.Context, asset common.Asset) (Pool, error) {
	if p, ok := p.store[asset.String()]; ok {
		return p.(Pool), nil
	}
	return types.NewPool(), nil
}

func (p *MockInMemoryPoolStorage) SetPool(ctx sdk.Context, ps Pool) error {
	p.store[ps.Asset.String()] = ps
	return nil
}

func (p *MockInMemoryPoolStorage) AddToLiquidityFees(ctx sdk.Context, asset common.Asset, fs sdk.Uint) error {
	return nil
}

func (p *MockInMemoryPoolStorage) GetTotalLiquidityFees(ctx sdk.Context, height uint64) (sdk.Uint, error) {
	return sdk.ZeroUint(), nil
}

func (p *MockInMemoryPoolStorage) GetPoolLiquidityFees(ctx sdk.Context, height uint64, asset common.Asset) (sdk.Uint, error) {
	return sdk.ZeroUint(), nil
}

func (p *MockInMemoryPoolStorage) GetStakerPool(ctx sdk.Context, stakerID common.Address) (StakerPool, error) {
	if stakerID.Equals(notExistStakerPoolAddr) {
		return NewStakerPool(stakerID), errors.New("simulate error for test")
	}
	key := p.GetKey(ctx, prefixStakerPool, stakerID.String())
	if res, ok := p.store[key]; ok {
		return res.(StakerPool), nil
	}
	return NewStakerPool(stakerID), nil
}

func (p *MockInMemoryPoolStorage) SetStakerPool(ctx sdk.Context, sp StakerPool) {
	key := p.GetKey(ctx, prefixStakerPool, sp.RuneAddress.String())
	p.store[key] = sp
}

func (p *MockInMemoryPoolStorage) GetPoolStaker(ctx sdk.Context, asset common.Asset) (PoolStaker, error) {
	if notExistPoolStakerAsset.Equals(asset) {
		return NewPoolStaker(asset, sdk.ZeroUint()), errors.New("simulate error for test")
	}
	key := p.GetKey(ctx, prefixPoolStaker, asset.String())
	if res, ok := p.store[key]; ok {
		return res.(PoolStaker), nil
	}
	return NewPoolStaker(asset, sdk.ZeroUint()), nil
}

func (p *MockInMemoryPoolStorage) SetPoolStaker(ctx sdk.Context, ps PoolStaker) {
	key := p.GetKey(ctx, prefixPoolStaker, ps.Asset.String())
	p.store[key] = ps
}

func (p *MockInMemoryPoolStorage) GetLowestActiveVersion(ctx sdk.Context) semver.Version {
	return constants.SWVersion
}

func (p *MockInMemoryPoolStorage) AddIncompleteEvents(ctx sdk.Context, event Event) error { return nil }
func (p *MockInMemoryPoolStorage) SetCompletedEvent(ctx sdk.Context, event Event)         {}

func (p *MockInMemoryPoolStorage) AddFeeToReserve(ctx sdk.Context, fee sdk.Uint) error { return nil }
