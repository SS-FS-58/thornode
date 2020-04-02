package thorchain

import (
	"sort"

	"github.com/blang/semver"
	sdk "github.com/cosmos/cosmos-sdk/types"
	tssCommon "gitlab.com/thorchain/tss/go-tss/common"
	. "gopkg.in/check.v1"

	"gitlab.com/thorchain/thornode/common"
	"gitlab.com/thorchain/thornode/constants"
)

type HandlerTssSuite struct{}

var _ = Suite(&HandlerTssSuite{})

type tssHandlerTestHelper struct {
	ctx           sdk.Context
	version       semver.Version
	keeper        *tssKeeperHelper
	constAccessor constants.ConstantValues
	nodeAccount   NodeAccount
	vaultManager  VersionedVaultManager
	members       common.PubKeys
}

type tssKeeperHelper struct {
	Keeper
	errListActiveAccounts bool
	errGetTssVoter        bool
	errFailSaveVault      bool
	errFailGetNodeAccount bool
	errFailGetVaultData   bool
	errFailSetVaultData   bool
	errFailSetNodeAccount bool
}

func (k *tssKeeperHelper) GetNodeAccountByPubKey(ctx sdk.Context, pk common.PubKey) (NodeAccount, error) {
	if k.errFailGetNodeAccount {
		return NodeAccount{}, kaboom
	}
	return k.Keeper.GetNodeAccountByPubKey(ctx, pk)
}

func (k *tssKeeperHelper) SetVault(ctx sdk.Context, vault Vault) error {
	if k.errFailSaveVault {
		return kaboom
	}
	return k.Keeper.SetVault(ctx, vault)
}

func (k *tssKeeperHelper) GetTssVoter(ctx sdk.Context, id string) (TssVoter, error) {
	if k.errGetTssVoter {
		return TssVoter{}, kaboom
	}
	return k.Keeper.GetTssVoter(ctx, id)
}

func (k *tssKeeperHelper) ListActiveNodeAccounts(ctx sdk.Context) (NodeAccounts, error) {
	if k.errListActiveAccounts {
		return NodeAccounts{}, kaboom
	}
	return k.Keeper.ListActiveNodeAccounts(ctx)
}

func (k *tssKeeperHelper) GetVaultData(ctx sdk.Context) (VaultData, error) {
	if k.errFailGetVaultData {
		return VaultData{}, kaboom
	}
	return k.Keeper.GetVaultData(ctx)
}

func (k *tssKeeperHelper) SetVaultData(ctx sdk.Context, data VaultData) error {
	if k.errFailSetVaultData {
		return kaboom
	}
	return k.Keeper.SetVaultData(ctx, data)
}

func (k *tssKeeperHelper) SetNodeAccount(ctx sdk.Context, na NodeAccount) error {
	if k.errFailSetNodeAccount {
		return kaboom
	}
	return k.Keeper.SetNodeAccount(ctx, na)
}

func newTssKeeperHelper(keeper Keeper) *tssKeeperHelper {
	return &tssKeeperHelper{
		Keeper: keeper,
	}
}

func newTssHandlerTestHelper(c *C) tssHandlerTestHelper {
	ctx, k := setupKeeperForTest(c)
	ctx = ctx.WithBlockHeight(1023)
	version := semver.MustParse("0.1.0")
	keeper := newTssKeeperHelper(k)
	// active account
	nodeAccount := GetRandomNodeAccount(NodeActive)
	nodeAccount.Bond = sdk.NewUint(100 * common.One)
	c.Assert(keeper.SetNodeAccount(ctx, nodeAccount), IsNil)

	constAccessor := constants.GetConstantValues(version)
	versionedTxOutStore := NewVersionedTxOutStore()
	vaultMgr := NewVersionedVaultMgr(versionedTxOutStore)
	var members common.PubKeys
	for i := 0; i < 8; i++ {
		na := GetRandomNodeAccount(NodeStandby)
		members = append(members, na.PubKeySet.Secp256k1)
		_ = keeper.SetNodeAccount(ctx, na)
	}

	asgardVault := NewVault(ctx.BlockHeight(), ActiveVault, AsgardVault, GetRandomPubKey())
	c.Assert(keeper.SetVault(ctx, asgardVault), IsNil)
	return tssHandlerTestHelper{
		ctx:           ctx,
		version:       version,
		keeper:        keeper,
		constAccessor: constAccessor,
		nodeAccount:   nodeAccount,
		vaultManager:  vaultMgr,
		members:       members,
	}
}

func (s *HandlerTssSuite) TestTssHandler(c *C) {
	testCases := []struct {
		name           string
		messageCreator func(helper tssHandlerTestHelper) sdk.Msg
		runner         func(handler TssHandler, msg sdk.Msg, helper tssHandlerTestHelper) sdk.Result
		validator      func(helper tssHandlerTestHelper, msg sdk.Msg, result sdk.Result, c *C)
		expectedResult sdk.CodeType
	}{
		{
			name: "invalid message should return an error",
			messageCreator: func(helper tssHandlerTestHelper) sdk.Msg {
				return NewMsgNoOp(GetRandomObservedTx(), helper.nodeAccount.NodeAddress)
			},
			runner: func(handler TssHandler, msg sdk.Msg, helper tssHandlerTestHelper) sdk.Result {
				return handler.Run(helper.ctx, msg, helper.version, helper.constAccessor)
			},
			expectedResult: CodeInvalidMessage,
		},
		{
			name: "bad version should return an error",
			messageCreator: func(helper tssHandlerTestHelper) sdk.Msg {
				return NewMsgTssPool(helper.members, GetRandomPubKey(), AsgardKeygen, helper.ctx.BlockHeight(), tssCommon.NoBlame, helper.nodeAccount.NodeAddress)
			},
			runner: func(handler TssHandler, msg sdk.Msg, helper tssHandlerTestHelper) sdk.Result {
				return handler.Run(helper.ctx, msg, semver.MustParse("0.0.1"), helper.constAccessor)
			},
			expectedResult: CodeBadVersion,
		},
		{
			name: "Not signed by an active account should return an error",
			messageCreator: func(helper tssHandlerTestHelper) sdk.Msg {
				return NewMsgTssPool(helper.members, GetRandomPubKey(), AsgardKeygen, helper.ctx.BlockHeight(), tssCommon.NoBlame, GetRandomNodeAccount(NodeActive).NodeAddress)
			},
			runner: func(handler TssHandler, msg sdk.Msg, helper tssHandlerTestHelper) sdk.Result {
				return handler.Run(helper.ctx, msg, semver.MustParse("0.1.0"), helper.constAccessor)
			},
			expectedResult: sdk.CodeUnauthorized,
		},
		{
			name: "empty signer should return an error",
			messageCreator: func(helper tssHandlerTestHelper) sdk.Msg {
				return NewMsgTssPool(helper.members, GetRandomPubKey(), AsgardKeygen, helper.ctx.BlockHeight(), tssCommon.NoBlame, sdk.AccAddress{})
			},
			runner: func(handler TssHandler, msg sdk.Msg, helper tssHandlerTestHelper) sdk.Result {
				return handler.Run(helper.ctx, msg, semver.MustParse("0.1.0"), helper.constAccessor)
			},
			expectedResult: sdk.CodeInvalidAddress,
		},
		{
			name: "empty id should return an error",
			messageCreator: func(helper tssHandlerTestHelper) sdk.Msg {
				tssMsg := NewMsgTssPool(helper.members, GetRandomPubKey(), AsgardKeygen, helper.ctx.BlockHeight(), tssCommon.NoBlame, helper.nodeAccount.NodeAddress)
				tssMsg.ID = ""
				return tssMsg
			},
			runner: func(handler TssHandler, msg sdk.Msg, helper tssHandlerTestHelper) sdk.Result {
				return handler.Run(helper.ctx, msg, semver.MustParse("0.1.0"), helper.constAccessor)
			},
			expectedResult: sdk.CodeUnknownRequest,
		},
		{
			name: "empty member pubkeys should return an error",
			messageCreator: func(helper tssHandlerTestHelper) sdk.Msg {
				tssMsg := NewMsgTssPool(common.PubKeys{}, GetRandomPubKey(), AsgardKeygen, helper.ctx.BlockHeight(), tssCommon.NoBlame, helper.nodeAccount.NodeAddress)
				return tssMsg
			},
			runner: func(handler TssHandler, msg sdk.Msg, helper tssHandlerTestHelper) sdk.Result {
				return handler.Run(helper.ctx, msg, semver.MustParse("0.1.0"), helper.constAccessor)
			},
			expectedResult: sdk.CodeUnknownRequest,
		},
		{
			name: "less than two member pubkeys should return an error",
			messageCreator: func(helper tssHandlerTestHelper) sdk.Msg {
				tssMsg := NewMsgTssPool(common.PubKeys{GetRandomPubKey()}, GetRandomPubKey(), AsgardKeygen, helper.ctx.BlockHeight(), tssCommon.NoBlame, helper.nodeAccount.NodeAddress)
				return tssMsg
			},
			runner: func(handler TssHandler, msg sdk.Msg, helper tssHandlerTestHelper) sdk.Result {
				return handler.Run(helper.ctx, msg, semver.MustParse("0.1.0"), helper.constAccessor)
			},
			expectedResult: sdk.CodeUnknownRequest,
		},
		{
			name: "there are empty pubkeys in member pubkey should return an error",
			messageCreator: func(helper tssHandlerTestHelper) sdk.Msg {
				tssMsg := NewMsgTssPool(common.PubKeys{GetRandomPubKey(), GetRandomPubKey(), common.EmptyPubKey}, GetRandomPubKey(), AsgardKeygen, helper.ctx.BlockHeight(), tssCommon.NoBlame, helper.nodeAccount.NodeAddress)
				return tssMsg
			},
			runner: func(handler TssHandler, msg sdk.Msg, helper tssHandlerTestHelper) sdk.Result {
				return handler.Run(helper.ctx, msg, semver.MustParse("0.1.0"), helper.constAccessor)
			},
			expectedResult: sdk.CodeUnknownRequest,
		},
		{
			name: "success while pool pub key is empty should return error",
			messageCreator: func(helper tssHandlerTestHelper) sdk.Msg {
				tssMsg := NewMsgTssPool(helper.members, common.EmptyPubKey, AsgardKeygen, helper.ctx.BlockHeight(), tssCommon.NoBlame, helper.nodeAccount.NodeAddress)
				return tssMsg
			},
			runner: func(handler TssHandler, msg sdk.Msg, helper tssHandlerTestHelper) sdk.Result {
				return handler.Run(helper.ctx, msg, semver.MustParse("0.1.0"), helper.constAccessor)
			},
			expectedResult: sdk.CodeUnknownRequest,
		},
		{
			name: "invalid pool pub key should return error",
			messageCreator: func(helper tssHandlerTestHelper) sdk.Msg {
				tssMsg := NewMsgTssPool(helper.members, "whatever", AsgardKeygen, helper.ctx.BlockHeight(), tssCommon.NoBlame, helper.nodeAccount.NodeAddress)
				return tssMsg
			},
			runner: func(handler TssHandler, msg sdk.Msg, helper tssHandlerTestHelper) sdk.Result {
				return handler.Run(helper.ctx, msg, semver.MustParse("0.1.0"), helper.constAccessor)
			},
			expectedResult: sdk.CodeUnknownRequest,
		},
		{
			name: "fail to list active node accounts should return an error",
			messageCreator: func(helper tssHandlerTestHelper) sdk.Msg {
				tssMsg := NewMsgTssPool(helper.members, GetRandomPubKey(), AsgardKeygen, helper.ctx.BlockHeight(), tssCommon.NoBlame, helper.nodeAccount.NodeAddress)
				return tssMsg
			},
			runner: func(handler TssHandler, msg sdk.Msg, helper tssHandlerTestHelper) sdk.Result {
				helper.keeper.errListActiveAccounts = true
				return handler.Run(helper.ctx, msg, semver.MustParse("0.1.0"), helper.constAccessor)
			},
			expectedResult: sdk.CodeInternal,
		},
		{
			name: "fail to get tss voter should return an error",
			messageCreator: func(helper tssHandlerTestHelper) sdk.Msg {
				tssMsg := NewMsgTssPool(helper.members, GetRandomPubKey(), AsgardKeygen, helper.ctx.BlockHeight(), tssCommon.NoBlame, helper.nodeAccount.NodeAddress)
				return tssMsg
			},
			runner: func(handler TssHandler, msg sdk.Msg, helper tssHandlerTestHelper) sdk.Result {
				helper.keeper.errGetTssVoter = true
				return handler.Run(helper.ctx, msg, semver.MustParse("0.1.0"), helper.constAccessor)
			},
			expectedResult: sdk.CodeInternal,
		},
		{
			name: "fail to save vault should return an error",
			messageCreator: func(helper tssHandlerTestHelper) sdk.Msg {
				tssMsg := NewMsgTssPool(helper.members, GetRandomPubKey(), AsgardKeygen, helper.ctx.BlockHeight(), tssCommon.NoBlame, helper.nodeAccount.NodeAddress)
				return tssMsg
			},
			runner: func(handler TssHandler, msg sdk.Msg, helper tssHandlerTestHelper) sdk.Result {
				helper.keeper.errFailSaveVault = true
				return handler.Run(helper.ctx, msg, semver.MustParse("0.1.0"), helper.constAccessor)
			},
			expectedResult: sdk.CodeInternal,
		},
		{
			name: "not having consensus should not perform any actions",
			messageCreator: func(helper tssHandlerTestHelper) sdk.Msg {
				tssMsg := NewMsgTssPool(helper.members, GetRandomPubKey(), AsgardKeygen, helper.ctx.BlockHeight(), tssCommon.NoBlame, helper.nodeAccount.NodeAddress)
				return tssMsg
			},
			runner: func(handler TssHandler, msg sdk.Msg, helper tssHandlerTestHelper) sdk.Result {
				for i := 0; i < 8; i++ {
					na := GetRandomNodeAccount(NodeActive)
					_ = helper.keeper.SetNodeAccount(helper.ctx, na)
				}
				return handler.Run(helper.ctx, msg, semver.MustParse("0.1.0"), helper.constAccessor)
			},
			expectedResult: sdk.CodeOK,
		},
		{
			name: "normal success",
			messageCreator: func(helper tssHandlerTestHelper) sdk.Msg {
				tssMsg := NewMsgTssPool(helper.members, GetRandomPubKey(), AsgardKeygen, helper.ctx.BlockHeight(), tssCommon.NoBlame, helper.nodeAccount.NodeAddress)
				return tssMsg
			},
			runner: func(handler TssHandler, msg sdk.Msg, helper tssHandlerTestHelper) sdk.Result {
				return handler.Run(helper.ctx, msg, semver.MustParse("0.1.0"), helper.constAccessor)
			},
			expectedResult: sdk.CodeOK,
		},
		{
			name: "fail to keygen with invalid blame node account address should return an error",
			messageCreator: func(helper tssHandlerTestHelper) sdk.Msg {
				sort.SliceStable(helper.members, func(i, j int) bool {
					return helper.members[i].String() < helper.members[j].String()
				})
				b := tssCommon.Blame{
					FailReason: "who knows",
					BlameNodes: []string{
						"whatever",
					},
				}
				return NewMsgTssPool(helper.members, GetRandomPubKey(), AsgardKeygen, helper.ctx.BlockHeight(), b, helper.nodeAccount.NodeAddress)
			},
			runner: func(handler TssHandler, msg sdk.Msg, helper tssHandlerTestHelper) sdk.Result {
				ctx := helper.ctx.WithBlockHeight(60000)
				return handler.Run(ctx, msg, semver.MustParse("0.1.0"), helper.constAccessor)
			},
			expectedResult: sdk.CodeInternal,
		},
		{
			name: "fail to keygen retry should be slashed",
			messageCreator: func(helper tssHandlerTestHelper) sdk.Msg {
				sort.SliceStable(helper.members, func(i, j int) bool {
					return helper.members[i].String() < helper.members[j].String()
				})
				thorAddr, _ := helper.members[3].GetThorAddress()
				na, _ := helper.keeper.GetNodeAccount(helper.ctx, thorAddr)
				na.UpdateStatus(NodeActive, helper.ctx.BlockHeight())
				_ = helper.keeper.SetNodeAccount(helper.ctx, na)
				b := tssCommon.Blame{
					FailReason: "who knows",
					BlameNodes: []string{
						helper.members[3].String(),
					},
				}
				return NewMsgTssPool(helper.members, GetRandomPubKey(), AsgardKeygen, helper.ctx.BlockHeight(), b, helper.nodeAccount.NodeAddress)
			},
			runner: func(handler TssHandler, msg sdk.Msg, helper tssHandlerTestHelper) sdk.Result {
				ctx := helper.ctx.WithBlockHeight(60000)
				return handler.Run(ctx, msg, semver.MustParse("0.1.0"), helper.constAccessor)
			},
			validator: func(helper tssHandlerTestHelper, msg sdk.Msg, result sdk.Result, c *C) {
				// make sure node get slashed
				pubKey := helper.members[3]
				na, err := helper.keeper.GetNodeAccountByPubKey(helper.ctx, pubKey)
				c.Assert(err, IsNil)
				c.Assert(na.SlashPoints > 0, Equals, true)
			},
			expectedResult: sdk.CodeOK,
		},
		{
			name: "fail to keygen but can't get vault data should return an error",
			messageCreator: func(helper tssHandlerTestHelper) sdk.Msg {
				sort.SliceStable(helper.members, func(i, j int) bool {
					return helper.members[i].String() < helper.members[j].String()
				})
				b := tssCommon.Blame{
					FailReason: "who knows",
					BlameNodes: []string{
						helper.members[3].String(),
					},
				}
				return NewMsgTssPool(helper.members, GetRandomPubKey(), AsgardKeygen, helper.ctx.BlockHeight(), b, helper.nodeAccount.NodeAddress)
			},
			runner: func(handler TssHandler, msg sdk.Msg, helper tssHandlerTestHelper) sdk.Result {
				ctx := helper.ctx.WithBlockHeight(60000)
				helper.keeper.errFailGetVaultData = true
				return handler.Run(ctx, msg, semver.MustParse("0.1.0"), helper.constAccessor)
			},
			expectedResult: sdk.CodeInternal,
		},
		{
			name: "fail to keygen retry and none active account should be slashed with bond",
			messageCreator: func(helper tssHandlerTestHelper) sdk.Msg {
				sort.SliceStable(helper.members, func(i, j int) bool {
					return helper.members[i].String() < helper.members[j].String()
				})
				b := tssCommon.Blame{
					FailReason: "who knows",
					BlameNodes: []string{
						helper.members[3].String(),
					},
				}
				return NewMsgTssPool(helper.members, GetRandomPubKey(), AsgardKeygen, helper.ctx.BlockHeight(), b, helper.nodeAccount.NodeAddress)
			},
			runner: func(handler TssHandler, msg sdk.Msg, helper tssHandlerTestHelper) sdk.Result {
				ctx := helper.ctx.WithBlockHeight(60000)
				vd := VaultData{
					BondRewardRune: sdk.NewUint(5000 * common.One),
					TotalBondUnits: sdk.NewUint(10000),
				}
				_ = helper.keeper.SetVaultData(helper.ctx, vd)
				return handler.Run(ctx, msg, semver.MustParse("0.1.0"), helper.constAccessor)
			},
			validator: func(helper tssHandlerTestHelper, msg sdk.Msg, result sdk.Result, c *C) {
				// make sure node get slashed
				pubKey := helper.members[3]
				na, err := helper.keeper.GetNodeAccountByPubKey(helper.ctx, pubKey)
				c.Assert(err, IsNil)
				c.Assert(na.Bond.Equal(sdk.ZeroUint()), Equals, true)
			},
			expectedResult: sdk.CodeOK,
		},
		{
			name: "fail to keygen and none active account, fail to set vault data should return an error",
			messageCreator: func(helper tssHandlerTestHelper) sdk.Msg {
				sort.SliceStable(helper.members, func(i, j int) bool {
					return helper.members[i].String() < helper.members[j].String()
				})
				b := tssCommon.Blame{
					FailReason: "who knows",
					BlameNodes: []string{
						helper.members[3].String(),
					},
				}
				return NewMsgTssPool(helper.members, GetRandomPubKey(), AsgardKeygen, helper.ctx.BlockHeight(), b, helper.nodeAccount.NodeAddress)
			},
			runner: func(handler TssHandler, msg sdk.Msg, helper tssHandlerTestHelper) sdk.Result {
				ctx := helper.ctx.WithBlockHeight(60000)
				vd := VaultData{
					BondRewardRune: sdk.NewUint(5000 * common.One),
					TotalBondUnits: sdk.NewUint(10000),
				}
				_ = helper.keeper.SetVaultData(helper.ctx, vd)
				helper.keeper.errFailSetVaultData = true
				return handler.Run(ctx, msg, semver.MustParse("0.1.0"), helper.constAccessor)
			},
			expectedResult: sdk.CodeInternal,
		},
		{
			name: "fail to keygen and fail to get node account should return an error",
			messageCreator: func(helper tssHandlerTestHelper) sdk.Msg {
				b := tssCommon.Blame{
					FailReason: "who knows",
					BlameNodes: []string{
						helper.members[3].String(),
					},
				}
				return NewMsgTssPool(helper.members, GetRandomPubKey(), AsgardKeygen, helper.ctx.BlockHeight(), b, helper.nodeAccount.NodeAddress)
			},
			runner: func(handler TssHandler, msg sdk.Msg, helper tssHandlerTestHelper) sdk.Result {
				helper.keeper.errFailGetNodeAccount = true
				ctx := helper.ctx.WithBlockHeight(60000)
				return handler.Run(ctx, msg, semver.MustParse("0.1.0"), helper.constAccessor)
			},
			expectedResult: sdk.CodeInternal,
		},
		{
			name: "fail to keygen and fail to set node account should return an error",
			messageCreator: func(helper tssHandlerTestHelper) sdk.Msg {
				b := tssCommon.Blame{
					FailReason: "who knows",
					BlameNodes: []string{
						helper.members[3].String(),
					},
				}
				return NewMsgTssPool(helper.members, GetRandomPubKey(), AsgardKeygen, helper.ctx.BlockHeight(), b, helper.nodeAccount.NodeAddress)
			},
			runner: func(handler TssHandler, msg sdk.Msg, helper tssHandlerTestHelper) sdk.Result {
				helper.keeper.errFailSetNodeAccount = true
				ctx := helper.ctx.WithBlockHeight(60000)
				return handler.Run(ctx, msg, semver.MustParse("0.1.0"), helper.constAccessor)
			},
			expectedResult: sdk.CodeInternal,
		},
	}
	for _, tc := range testCases {
		c.Log(tc.name)
		helper := newTssHandlerTestHelper(c)
		handler := NewTssHandler(helper.keeper, helper.vaultManager)
		msg := tc.messageCreator(helper)
		result := tc.runner(handler, msg, helper)
		c.Assert(result.Code, Equals, tc.expectedResult, Commentf("name:%s", tc.name))
		if tc.validator != nil {
			tc.validator(helper, msg, result, c)
		}
	}
}
