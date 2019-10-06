package types

import "gitlab.com/thorchain/bepswap/common"

type PoolAddresses struct {
	Previous common.BnbAddress `json:"previous"`
	Current  common.BnbAddress `json:"current"`
	Next     common.BnbAddress `json:"next"`
	RotateAt int64             `json:"rotate_at"`
}

// IsEmpty check whether PoolAddress is empty
func (pa PoolAddresses) IsEmpty() bool {
	// when current pool address is empty then we think it is empty , even the others are not, that will not matter
	return pa.Current.IsEmpty()
}

// EmptyPoolAddress is just empty
var EmptyPoolAddress PoolAddresses
