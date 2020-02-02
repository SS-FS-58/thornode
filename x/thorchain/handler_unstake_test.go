package thorchain

import (
	"errors"

	"github.com/blang/semver"
	sdk "github.com/cosmos/cosmos-sdk/types"
	. "gopkg.in/check.v1"

	"gitlab.com/thorchain/thornode/common"
	"gitlab.com/thorchain/thornode/constants"
)

type HandlerUnstakeSuite struct{}

var _ = Suite(&HandlerUnstakeSuite{})

type MockUnstakeKeeper struct {
	KVStoreDummy
	activeNodeAccount NodeAccount
	currentPool       Pool
	failPool          bool
	suspendedPool     bool
	failPoolStaker    bool
	failAddEvents     bool
	stakerPool        StakerPool
	poolStaker        PoolStaker
}

func (mfp *MockUnstakeKeeper) PoolExist(_ sdk.Context, asset common.Asset) bool {
	return mfp.currentPool.Asset.Equals(asset)
}

// GetPool return a pool
func (mfp *MockUnstakeKeeper) GetPool(_ sdk.Context, _ common.Asset) (Pool, error) {
	if mfp.failPool {
		return Pool{}, errors.New("test error")
	}
	if mfp.suspendedPool {
		return Pool{
			BalanceRune:  sdk.ZeroUint(),
			BalanceAsset: sdk.ZeroUint(),
			Asset:        common.BNBAsset,
			PoolUnits:    sdk.ZeroUint(),
			Status:       PoolSuspended,
		}, nil
	}
	return mfp.currentPool, nil
}

func (mfp *MockUnstakeKeeper) SetPool(_ sdk.Context, pool Pool) error {
	mfp.currentPool = pool
	return nil
}

// IsActiveObserver see whether it is an active observer
func (mfp *MockUnstakeKeeper) IsActiveObserver(_ sdk.Context, addr sdk.AccAddress) bool {
	return mfp.activeNodeAccount.NodeAddress.Equals(addr)
}

func (mfp *MockUnstakeKeeper) GetPoolStaker(_ sdk.Context, _ common.Asset) (PoolStaker, error) {
	if mfp.failPoolStaker {
		return PoolStaker{}, errors.New("fail to get pool staker")
	}
	return mfp.poolStaker, nil
}

func (mfp *MockUnstakeKeeper) GetStakerPool(_ sdk.Context, _ common.Address) (StakerPool, error) {
	return mfp.stakerPool, nil
}

func (mfp *MockUnstakeKeeper) SetStakerPool(_ sdk.Context, sp StakerPool) {
	mfp.stakerPool = sp
}

func (mfp *MockUnstakeKeeper) SetPoolStaker(_ sdk.Context, ps PoolStaker) {
	mfp.poolStaker = ps
}

func (mfp *MockUnstakeKeeper) GetAdminConfigDefaultPoolStatus(_ sdk.Context, _ sdk.AccAddress) PoolStatus {
	return PoolEnabled
}
func (mfp *MockUnstakeKeeper) UpsertEvent(ctx sdk.Context, event Event) error { return nil }

func (HandlerUnstakeSuite) TestUnstakeHandler(c *C) {
	// w := getHandlerTestWrapper(c, 1, true, true)
	ctx, _ := setupKeeperForTest(c)
	activeNodeAccount := GetRandomNodeAccount(NodeActive)
	k := &MockUnstakeKeeper{
		activeNodeAccount: activeNodeAccount,
		currentPool: Pool{
			BalanceRune:  sdk.ZeroUint(),
			BalanceAsset: sdk.ZeroUint(),
			Asset:        common.BNBAsset,
			PoolUnits:    sdk.ZeroUint(),
			Status:       PoolEnabled,
		},
	}
	ver := semver.MustParse("0.1.0")
	constAccessor := constants.GetConstantValues(ver)
	// Happy path , this is a round trip , first we stake, then we unstake
	runeAddr := GetRandomBNBAddress()
	unit, err := stake(ctx,
		k,
		common.BNBAsset,
		sdk.NewUint(common.One*100),
		sdk.NewUint(common.One*100),
		runeAddr,
		runeAddr,
		GetRandomTxHash(),
		constAccessor)
	c.Assert(err, IsNil)
	c.Logf("stake unit: %d", unit)
	// let's just unstake
	unstakeHandler := NewUnstakeHandler(k, NewVersionedTxOutStoreDummy())

	msgUnstake := NewMsgSetUnStake(GetRandomTx(), runeAddr, sdk.NewUint(uint64(MaxWithdrawBasisPoints)), common.BNBAsset, activeNodeAccount.NodeAddress)
	result := unstakeHandler.Run(ctx, msgUnstake, ver, constAccessor)
	c.Assert(result.Code, Equals, sdk.CodeOK)

	// Bad version should fail
	result = unstakeHandler.Run(ctx, msgUnstake, semver.Version{}, constAccessor)
	c.Assert(result.Code, Equals, CodeBadVersion)
}

func (HandlerUnstakeSuite) TestUnstakeHandler_Validation(c *C) {
	ctx, k := setupKeeperForTest(c)
	testCases := []struct {
		name           string
		msg            MsgSetUnStake
		expectedResult sdk.CodeType
	}{
		{
			name:           "not signed by active observer should fail",
			msg:            NewMsgSetUnStake(GetRandomTx(), GetRandomBNBAddress(), sdk.NewUint(uint64(MaxWithdrawBasisPoints)), common.BNBAsset, GetRandomNodeAccount(NodeActive).NodeAddress),
			expectedResult: sdk.CodeUnauthorized,
		},
		{
			name:           "empty signer should fail",
			msg:            NewMsgSetUnStake(GetRandomTx(), GetRandomBNBAddress(), sdk.NewUint(uint64(MaxWithdrawBasisPoints)), common.BNBAsset, sdk.AccAddress{}),
			expectedResult: CodeUnstakeFailValidation,
		},
		{
			name:           "empty asset should fail",
			msg:            NewMsgSetUnStake(GetRandomTx(), GetRandomBNBAddress(), sdk.NewUint(uint64(MaxWithdrawBasisPoints)), common.Asset{}, GetRandomNodeAccount(NodeActive).NodeAddress),
			expectedResult: CodeUnstakeFailValidation,
		},
		{
			name:           "empty RUNE address should fail",
			msg:            NewMsgSetUnStake(GetRandomTx(), common.NoAddress, sdk.NewUint(uint64(MaxWithdrawBasisPoints)), common.BNBAsset, GetRandomNodeAccount(NodeActive).NodeAddress),
			expectedResult: CodeUnstakeFailValidation,
		},
		{
			name:           "withdraw basis point is 0 should fail",
			msg:            NewMsgSetUnStake(GetRandomTx(), GetRandomBNBAddress(), sdk.ZeroUint(), common.BNBAsset, GetRandomNodeAccount(NodeActive).NodeAddress),
			expectedResult: CodeUnstakeFailValidation,
		},
		{
			name:           "withdraw basis point is larger than 10000 should fail",
			msg:            NewMsgSetUnStake(GetRandomTx(), GetRandomBNBAddress(), sdk.NewUint(uint64(MaxWithdrawBasisPoints+100)), common.BNBAsset, GetRandomNodeAccount(NodeActive).NodeAddress),
			expectedResult: CodeUnstakeFailValidation,
		},
	}
	ver := semver.MustParse("0.1.0")
	constAccessor := constants.GetConstantValues(ver)
	for _, tc := range testCases {
		unstakeHandler := NewUnstakeHandler(k, NewVersionedTxOutStoreDummy())
		c.Assert(unstakeHandler.Run(ctx, tc.msg, ver, constAccessor).Code, Equals, tc.expectedResult, Commentf(tc.name))
	}
}

func (HandlerUnstakeSuite) TestUnstakeHandler_mockFailScenarios(c *C) {
	activeNodeAccount := GetRandomNodeAccount(NodeActive)
	currentPool := Pool{
		BalanceRune:  sdk.ZeroUint(),
		BalanceAsset: sdk.ZeroUint(),
		Asset:        common.BNBAsset,
		PoolUnits:    sdk.ZeroUint(),
		Status:       PoolEnabled,
	}
	testCases := []struct {
		name           string
		k              Keeper
		expectedResult sdk.CodeType
	}{
		{
			name: "fail to get pool unstake should fail",
			k: &MockUnstakeKeeper{
				activeNodeAccount: activeNodeAccount,
				failPool:          true,
			},
			expectedResult: sdk.CodeInternal,
		},
		{
			name: "suspended pool unstake should fail",
			k: &MockUnstakeKeeper{
				activeNodeAccount: activeNodeAccount,
				suspendedPool:     true,
			},
			expectedResult: CodeInvalidPoolStatus,
		},
		{
			name: "fail to get pool staker unstake should fail",
			k: &MockUnstakeKeeper{
				activeNodeAccount: activeNodeAccount,
				failPoolStaker:    true,
			},
			expectedResult: CodeFailGetPoolStaker,
		},
		{
			name: "fail to add incomplete event unstake should fail",
			k: &MockUnstakeKeeper{
				activeNodeAccount: activeNodeAccount,
				currentPool:       currentPool,
				failAddEvents:     true,
			},
			expectedResult: sdk.CodeInternal,
		},
	}
	ver := semver.MustParse("0.1.0")
	constAccessor := constants.GetConstantValues(ver)

	for _, tc := range testCases {
		ctx, _ := setupKeeperForTest(c)
		unstakeHandler := NewUnstakeHandler(tc.k, NewVersionedTxOutStoreDummy())
		msgUnstake := NewMsgSetUnStake(GetRandomTx(), GetRandomBNBAddress(), sdk.NewUint(uint64(MaxWithdrawBasisPoints)), common.BNBAsset, activeNodeAccount.NodeAddress)
		c.Assert(unstakeHandler.Run(ctx, msgUnstake, ver, constAccessor).Code, Equals, tc.expectedResult, Commentf(tc.name))
	}
}
