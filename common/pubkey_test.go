package common

import (
	"encoding/json"
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"
	atypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	. "gopkg.in/check.v1"
)

type PubKeyTestSuite struct{}

var _ = Suite(&PubKeyTestSuite{})

// TestPubKey implementation
func (PubKeyTestSuite) TestPubKey(c *C) {
	_, pubKey, _ := atypes.KeyTestPubAddr()
	spk, err := sdk.Bech32ifyAccPub(pubKey)
	c.Assert(err, IsNil)
	pk, err := NewPubKey(spk)
	c.Assert(err, IsNil)
	hexStr := pk.String()
	c.Assert(len(hexStr) > 0, Equals, true)
	pk1, err := NewPubKey(hexStr)
	c.Assert(err, IsNil)
	c.Assert(pk.Equals(pk1), Equals, true)

	addr, err := pk.GetAddress(BNBChain)
	c.Assert(err, IsNil)
	c.Assert(addr.Equals(NoAddress), Equals, false)

	result, err := json.Marshal(pk)
	c.Assert(err, IsNil)
	c.Log(result, Equals, fmt.Sprintf(`"%s"`, hexStr))
	var pk2 PubKey
	err = json.Unmarshal(result, &pk2)
	c.Assert(err, IsNil)
	c.Assert(pk2.Equals(pk), Equals, true)
}

func (s *PubKeyTestSuite) TestPubKeySet(c *C) {
	_, pubKey, _ := atypes.KeyTestPubAddr()
	spk, err := sdk.Bech32ifyAccPub(pubKey)
	c.Assert(err, IsNil)
	pk, err := NewPubKey(spk)
	c.Assert(err, IsNil)

	c.Check(PubKeySet{}.Contains(pk), Equals, false)

	pks := PubKeySet{
		Secp256k1: pk,
	}
	c.Check(pks.Contains(pk), Equals, true)
	pks = PubKeySet{
		Ed25519: pk,
	}
	c.Check(pks.Contains(pk), Equals, true)
}

func (s *PubKeyTestSuite) TestETHPubKey(c *C) {
	pubKey := PubKey("thorpub1addwnpepqt7qug8vk9r3saw8n4r803ydj2g3dqwx0mvq5akhnze86fc536xcy2cr8a2")
	addr, err := pubKey.GetAddress(ETHChain)
	c.Assert(err, IsNil)
	c.Check(len(addr), Equals, 42)
}
