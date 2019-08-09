package swapservice

import (
	"github.com/jpthor/cosmos-swap/x/swapservice/types"
)

const (
	ModuleName       = types.ModuleName
	RouterKey        = types.RouterKey
	StoreKey         = types.StoreKey
	DefaultCodespace = types.DefaultCodespace
	PoolActive       = types.Active
	PoolSuspended    = types.Suspended
	RuneTicker       = types.RuneTicker
)

var (
	NewPoolStruct      = types.NewPoolStruct
	NewMsgSetTxHash    = types.NewMsgSetTxHash
	NewMsgSetPoolData  = types.NewMsgSetPoolData
	NewMsgSetStakeData = types.NewMsgSetStakeData
	NewMsgSetUnStake   = types.NewMsgSetUnStake
	NewMsgSwap         = types.NewMsgSwap
	NewTxOut           = types.NewTxOut
	ModuleCdc          = types.ModuleCdc
	RegisterCodec      = types.RegisterCodec
)

type (
	MsgSetUnStake       = types.MsgSetUnStake
	MsgUnStakeComplete  = types.MsgUnStakeComplete
	MsgSwapComplete     = types.MsgSwapComplete
	MsgSetPoolData      = types.MsgSetPoolData
	MsgSetStakeData     = types.MsgSetStakeData
	MsgSetTxHash        = types.MsgSetTxHash
	MsgSwap             = types.MsgSwap
	QueryResPoolStructs = types.QueryResPoolStructs
	TxHash              = types.TxHash
	PoolStruct          = types.PoolStruct
	PoolStaker          = types.PoolStaker
	StakerPool          = types.StakerPool
	StakerUnit          = types.StakerUnit
	TxOutItem           = types.TxOutItem

	TxOut = types.TxOut
	Coin  = types.Coin
)
