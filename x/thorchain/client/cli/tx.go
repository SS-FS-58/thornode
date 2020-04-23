package cli

import (
	"fmt"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/context"
	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/auth"
	"github.com/cosmos/cosmos-sdk/x/auth/client/utils"
	"github.com/spf13/cobra"

	"gitlab.com/thorchain/thornode/common"
	"gitlab.com/thorchain/thornode/constants"

	"gitlab.com/thorchain/thornode/x/thorchain/types"
)

func GetTxCmd(storeKey string, cdc *codec.Codec) *cobra.Command {
	thorchainTxCmd := &cobra.Command{
		Use:                        types.ModuleName,
		Short:                      "thorchain transaction subcommands",
		DisableFlagParsing:         true,
		SuggestionsMinimumDistance: 2,
		RunE:                       client.ValidateCmd,
	}

	thorchainTxCmd.AddCommand(client.PostCommands(
		GetCmdSetNodeKeys(cdc),
		GetCmdEndPool(cdc),
		GetCmdSetVersion(cdc),
		GetCmdSetIPAddress(cdc),
		GetCmdBan(cdc),
	)...)

	return thorchainTxCmd
}

// GetCmdBan command to ban a node accounts
func GetCmdBan(cdc *codec.Codec) *cobra.Command {
	return &cobra.Command{
		Use:   "ban [node address]",
		Short: "votes to ban a node address (caution: costs 0.1%% of minimum bond)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cliCtx := context.NewCLIContext().WithCodec(cdc)
			txBldr := auth.NewTxBuilderFromCLI().WithTxEncoder(utils.GetTxEncoder(cdc))

			addr, err := sdk.AccAddressFromBech32(args[0])
			if err != nil {
				return fmt.Errorf("invalid node address: %w", err)
			}

			msg := types.NewMsgBan(addr, cliCtx.GetFromAddress())
			if err := msg.ValidateBasic(); err != nil {
				return err
			}
			return utils.GenerateOrBroadcastMsgs(cliCtx, txBldr, []sdk.Msg{msg})
		},
	}
}

// GetCmdSetIPAddress command to set a node accounts IP Address
func GetCmdSetIPAddress(cdc *codec.Codec) *cobra.Command {
	return &cobra.Command{
		Use:   "set-ip-address [ip address]",
		Short: "update registered ip address",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cliCtx := context.NewCLIContext().WithCodec(cdc)
			txBldr := auth.NewTxBuilderFromCLI().WithTxEncoder(utils.GetTxEncoder(cdc))

			msg := types.NewMsgSetIPAddress(args[0], cliCtx.GetFromAddress())
			if err := msg.ValidateBasic(); err != nil {
				return err
			}
			return utils.GenerateOrBroadcastMsgs(cliCtx, txBldr, []sdk.Msg{msg})
		},
	}
}

// GetCmdSetVersion command to set an admin config
func GetCmdSetVersion(cdc *codec.Codec) *cobra.Command {
	return &cobra.Command{
		Use:   "set-version",
		Short: "update registered version",
		Args:  cobra.ExactArgs(0),
		RunE: func(cmd *cobra.Command, args []string) error {
			cliCtx := context.NewCLIContext().WithCodec(cdc)
			txBldr := auth.NewTxBuilderFromCLI().WithTxEncoder(utils.GetTxEncoder(cdc))

			msg := types.NewMsgSetVersion(constants.SWVersion, cliCtx.GetFromAddress())
			if err := msg.ValidateBasic(); err != nil {
				return err
			}
			return utils.GenerateOrBroadcastMsgs(cliCtx, txBldr, []sdk.Msg{msg})
		},
	}
}

// GetCmdSetNodeKeys command to add a node keys
func GetCmdSetNodeKeys(cdc *codec.Codec) *cobra.Command {
	return &cobra.Command{
		Use:   "set-node-keys  [secp256k1] [ed25519] [validator_consensus_pub_key]",
		Short: "set node keys, the account use to sign this tx has to be whitelist first",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			cliCtx := context.NewCLIContext().WithCodec(cdc)
			txBldr := auth.NewTxBuilderFromCLI().WithTxEncoder(utils.GetTxEncoder(cdc))
			txBldr = txBldr.WithGas(600000) // set gas

			secp256k1Key, err := common.NewPubKey(args[0])
			if err != nil {
				return fmt.Errorf("fail to parse secp256k1 pub key ,err:%w", err)
			}
			ed25519Key, err := common.NewPubKey(args[1])
			if err != nil {
				return fmt.Errorf("fail to parse ed25519 pub key ,err:%w", err)
			}
			pk := common.NewPubKeySet(secp256k1Key, ed25519Key)
			validatorConsPubKey, err := sdk.GetConsPubKeyBech32(args[2])
			if err != nil {
				return fmt.Errorf("fail to parse validator consensus public key: %w", err)
			}
			validatorConsPubKeyStr, err := sdk.Bech32ifyConsPub(validatorConsPubKey)
			if err != nil {
				return fmt.Errorf("fail to convert public key to string: %w", err)
			}
			msg := types.NewMsgSetNodeKeys(pk, validatorConsPubKeyStr, cliCtx.GetFromAddress())
			err = msg.ValidateBasic()
			if err != nil {
				return err
			}
			return utils.GenerateOrBroadcastMsgs(cliCtx, txBldr, []sdk.Msg{msg})
		},
	}
}

// GetCmdEndPool
func GetCmdEndPool(cdc *codec.Codec) *cobra.Command {
	return &cobra.Command{
		Use:   "set-end-pool [asset] [requester_bnb_address] [request_txhash]",
		Short: "set end pool",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			cliCtx := context.NewCLIContext().WithCodec(cdc)
			txBldr := auth.NewTxBuilderFromCLI().WithTxEncoder(utils.GetTxEncoder(cdc))
			txBldr = txBldr.WithGas(600000) // set gas

			asset, err := common.NewAsset(args[0])
			if err != nil {
				return fmt.Errorf("invalid asset: %w", err)
			}
			requester, err := common.NewAddress(args[1])
			if err != nil {
				return fmt.Errorf("invalid requster bnb address: %w", err)
			}
			txID, err := common.NewTxID(args[2])
			if err != nil {
				return fmt.Errorf("invalid tx hash: %w", err)
			}

			tx := common.Tx{
				ID:          txID,
				FromAddress: requester,
				ToAddress:   requester,
				Chain:       asset.Chain,
				Coins: common.Coins{
					common.Coin{
						Asset:  asset,
						Amount: sdk.NewUint(1),
					},
				},
			}

			msg := types.NewMsgEndPool(asset, tx, cliCtx.GetFromAddress())
			if err := msg.ValidateBasic(); err != nil {
				return err
			}
			return utils.GenerateOrBroadcastMsgs(cliCtx, txBldr, []sdk.Msg{msg})
		},
	}
}
