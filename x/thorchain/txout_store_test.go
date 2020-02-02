package thorchain

import (
	"github.com/blang/semver"
	sdk "github.com/cosmos/cosmos-sdk/types"
	. "gopkg.in/check.v1"

	"gitlab.com/thorchain/thornode/common"
	"gitlab.com/thorchain/thornode/constants"
)

type TxOutStoreSuite struct{}

var _ = Suite(&TxOutStoreSuite{})

func (s TxOutStoreSuite) TestAddGasFees(c *C) {
	ctx, k := setupKeeperForTest(c)

	tx := GetRandomObservedTx()

	err := AddGasFees(ctx, k, tx)
	c.Assert(err, IsNil)
	vault, err := k.GetVaultData(ctx)
	c.Assert(err, IsNil)
	c.Assert(vault.Gas, HasLen, 1)
	c.Check(vault.Gas[0].Asset.Equals(common.BNBAsset), Equals, true)
	c.Check(vault.Gas[0].Amount.Equal(sdk.NewUint(37500)), Equals, true)
}

func (s TxOutStoreSuite) TestAddOutTxItem(c *C) {
	w := getHandlerTestWrapper(c, 1, true, true)
	vault := GetRandomVault()
	w.keeper.SetVault(w.ctx, vault)

	acc1 := GetRandomNodeAccount(NodeActive)
	acc2 := GetRandomNodeAccount(NodeActive)
	acc3 := GetRandomNodeAccount(NodeActive)
	c.Assert(w.keeper.SetNodeAccount(w.ctx, acc1), IsNil)
	c.Assert(w.keeper.SetNodeAccount(w.ctx, acc2), IsNil)
	c.Assert(w.keeper.SetNodeAccount(w.ctx, acc3), IsNil)

	ygg := NewVault(w.ctx.BlockHeight(), ActiveVault, YggdrasilVault, acc1.PubKeySet.Secp256k1)
	ygg.AddFunds(
		common.Coins{
			common.NewCoin(common.BNBAsset, sdk.NewUint(40*common.One)),
		},
	)
	c.Assert(w.keeper.SetVault(w.ctx, ygg), IsNil)

	ygg = NewVault(w.ctx.BlockHeight(), ActiveVault, YggdrasilVault, acc2.PubKeySet.Secp256k1)
	ygg.AddFunds(
		common.Coins{
			common.NewCoin(common.BNBAsset, sdk.NewUint(50*common.One)),
		},
	)
	c.Assert(w.keeper.SetVault(w.ctx, ygg), IsNil)

	ygg = NewVault(w.ctx.BlockHeight(), ActiveVault, YggdrasilVault, acc3.PubKeySet.Secp256k1)
	ygg.AddFunds(
		common.Coins{
			common.NewCoin(common.BNBAsset, sdk.NewUint(100*common.One)),
		},
	)
	c.Assert(w.keeper.SetVault(w.ctx, ygg), IsNil)

	// Create voter
	inTxID := GetRandomTxHash()
	voter := NewObservedTxVoter(inTxID, ObservedTxs{
		ObservedTx{
			Signers: []sdk.AccAddress{w.activeNodeAccount.NodeAddress, acc1.NodeAddress, acc2.NodeAddress},
		},
	})
	w.keeper.SetObservedTxVoter(w.ctx, voter)

	// Should get acc2. Acc3 hasn't signed and acc2 is the highest value
	item := &TxOutItem{
		Chain:     common.BNBChain,
		ToAddress: GetRandomBNBAddress(),
		InHash:    inTxID,
		Coin:      common.NewCoin(common.BNBAsset, sdk.NewUint(20*common.One)),
	}
	txOutStore := NewTxOutStorageV1(w.keeper)
	constantValues := constants.GetConstantValues(semver.MustParse("0.1.0"))
	txOutStore.NewBlock(w.ctx.BlockHeight(), constantValues)
	txOutStore.TryAddTxOutItem(w.ctx, item)
	msgs := txOutStore.GetOutboundItems()
	c.Assert(msgs, HasLen, 1)
	c.Assert(msgs[0].VaultPubKey.String(), Equals, acc2.PubKeySet.Secp256k1.String())
	c.Assert(msgs[0].Coin.Amount.Equal(sdk.NewUint(19*common.One)), Equals, true)

	// Should get acc1. Acc3 hasn't signed and acc1 now has the highest amount
	// of coin.
	item = &TxOutItem{
		Chain:     common.BNBChain,
		ToAddress: GetRandomBNBAddress(),
		InHash:    inTxID,
		Coin:      common.NewCoin(common.BNBAsset, sdk.NewUint(20*common.One)),
	}
	success, err := txOutStore.TryAddTxOutItem(w.ctx, item)
	c.Assert(success, Equals, true)
	c.Assert(err, IsNil)
	msgs = txOutStore.GetOutboundItems()
	c.Assert(msgs, HasLen, 2)
	c.Assert(msgs[1].VaultPubKey.String(), Equals, acc1.PubKeySet.Secp256k1.String())

	item = &TxOutItem{
		Chain:     common.BNBChain,
		ToAddress: GetRandomBNBAddress(),
		InHash:    inTxID,
		Coin:      common.NewCoin(common.BNBAsset, sdk.NewUint(1000*common.One)),
	}
	success, err = txOutStore.TryAddTxOutItem(w.ctx, item)
	c.Assert(success, Equals, true)
	c.Assert(err, IsNil)
	msgs = txOutStore.GetOutboundItems()
	c.Assert(msgs, HasLen, 3)
	c.Assert(msgs[2].VaultPubKey.String(), Equals, vault.PubKey.String())
}

func (s TxOutStoreSuite) TestAddOutTxItemWithoutBFT(c *C) {
	w := getHandlerTestWrapper(c, 1, true, true)
	w.keeper.SetVault(w.ctx, GetRandomVault())

	inTxID := GetRandomTxHash()
	item := &TxOutItem{
		Chain:     common.BNBChain,
		ToAddress: GetRandomBNBAddress(),
		InHash:    inTxID,
		Coin:      common.NewCoin(common.RuneAsset(), sdk.NewUint(20*common.One)),
	}
	txOutStore := NewTxOutStoreDummy()
	success, err := txOutStore.TryAddTxOutItem(w.ctx, item)
	c.Assert(success, Equals, true)
	c.Assert(err, IsNil)
	msgs := txOutStore.GetOutboundItems()
	c.Assert(msgs, HasLen, 1)
	c.Assert(msgs[0].Coin.Amount.Equal(sdk.NewUint(20*common.One)), Equals, true)
}
