package common

import (
	sdk "github.com/cosmos/cosmos-sdk/types"
	atypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	. "gopkg.in/check.v1"
)

type PoolPubKeySuite struct{}

var _ = Suite(&PoolPubKeySuite{})

func (PoolPubKeySuite) TestNewPoolPubKey(c *C) {
	pKey := GetPubKeyForTest()
	ppk := NewPoolPubKey(BNBChain, 1024, pKey)
	c.Assert(ppk, NotNil)
	c.Assert(ppk.IsEmpty(), Equals, false)
	ppk1 := NewPoolPubKey(BNBChain, 1024, pKey)
	c.Assert(ppk1, NotNil)
	c.Assert(ppk1.IsEmpty(), Equals, false)
	c.Assert(ppk1.Equals(ppk), Equals, true)
	c.Log(ppk.String())
	addr, err := ppk.GetAddress()
	c.Assert(err, IsNil)
	addr1, err := ppk.PubKey.GetAddress(BNBChain)
	c.Assert(err, IsNil)
	c.Assert(addr.Equals(addr1), Equals, true)
}

func (PoolPubKeySuite) TestGetSeqNo(c *C) {
	pKey := GetPubKeyForTest()
	ppk := NewPoolPubKey(BNBChain, 0, pKey)
	c.Assert(ppk, NotNil)
	c.Assert(ppk.IsEmpty(), Equals, false)
	c.Assert(ppk.GetSeqNo(), Equals, uint64(0))
	c.Assert(ppk.SeqNo, Equals, uint64(1))
	for i := 0; i < 100; i++ {
		c.Assert(ppk.GetSeqNo(), Equals, uint64(i+1))
	}
}
func GetPubKeyForTest() PubKey {
	_, pubKey, _ := atypes.KeyTestPubAddr()
	spk, _ := sdk.Bech32ifyAccPub(pubKey)
	pk, _ := NewPubKey(spk)
	return pk
}

func (PoolPubKeySuite) TestPoolPubKeys(c *C) {
	c.Assert(EmptyPoolPubKeys.IsEmpty(), Equals, true)
	current := PoolPubKeys{
		NewPoolPubKey(BNBChain, 0, GetPubKeyForTest()),
		NewPoolPubKey(BTCChain, 0, GetPubKeyForTest()),
		NewPoolPubKey(ETHChain, 0, GetPubKeyForTest()),
	}
	c.Assert(current.IsEmpty(), Equals, false)
	c.Assert(current.GetByChain(BNBChain), NotNil)
	// Try add nil should be safe
	result := current.TryAddKey(nil)
	c.Assert(result, NotNil)
	c.Assert(len(result), Equals, 3)
	ppk := NewPoolPubKey(BNBChain, 1024, GetPubKeyForTest())
	current = current.TryAddKey(ppk)
	c.Assert(current.IsEmpty(), Equals, false)
	c.Assert(len(current), Equals, 3)
	ppk1 := NewPoolPubKey(Chain("TestChain"), 1, GetPubKeyForTest())
	c.Assert(ppk1, NotNil)
	current = current.TryAddKey(ppk1)
	c.Assert(len(current), Equals, 4)
	current = current.TryRemoveKey(ppk1)
	c.Assert(len(current), Equals, 3)
	bnbPK := current[0]
	current = current.TryRemoveKey(bnbPK)
	c.Assert(len(current), Equals, 2)
	newPK := current.GetByChain(BNBChain)
	c.Assert(newPK, IsNil)
	current = current.TryAddKey(bnbPK)
	c.Assert(len(current), Equals, 3)
}