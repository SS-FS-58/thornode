package types

import (
	sdk "github.com/cosmos/cosmos-sdk/types"
)

const (
	DefaultCodespace sdk.CodespaceType = ModuleName
	Majority         float64           = 0.6666665
)

func HasMajority(signers, total int) bool {
	if signers > total {
		return false // will not have majority if THORNode have more signers than trusted accounts. This shouldn't be possible
	}
	return (float64(signers) / float64(total)) >= Majority
}
