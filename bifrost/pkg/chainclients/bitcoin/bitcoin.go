package bitcoin

import (
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"github.com/pkg/errors"

	"github.com/btcsuite/btcd/btcjson"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/rpcclient"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"gitlab.com/thorchain/thornode/bifrost/config"
	"gitlab.com/thorchain/thornode/bifrost/thorclient/types"
	"gitlab.com/thorchain/thornode/common"
)

// Client observes bitcoin chain and allows to sign and broadcast tx
type Client struct {
	logger zerolog.Logger
	cfg    config.ChainConfiguration
	client *rpcclient.Client
	chain  common.Chain
}

// NewClient generates a new Client
func NewClient(cfg config.ChainConfiguration) (*Client, error) {
	client, err := rpcclient.New(&rpcclient.ConnConfig{
		Host:         cfg.ChainHost,
		User:         cfg.UserName,
		Pass:         cfg.Password,
		DisableTLS:   cfg.DisableTLS,
		HTTPPostMode: cfg.HTTPostMode,
	}, nil)
	if err != nil {
		return nil, err
	}

	return &Client{
		logger: log.Logger.With().Str("module", "btcClient").Logger(),
		cfg:    cfg,
		chain:  cfg.ChainID,
		client: client,
	}, nil
}

// FetchTxs retrieves txs for a block height
func (c *Client) FetchTxs(height int64) (*types.TxIn, error) {
	block, err := c.getBlock(height)
	if err != nil {
		return &types.TxIn{}, errors.Wrap(err, "fail to get block")
	}
	txs, err := c.extractTxs(block)
	if err != nil {
		return &types.TxIn{}, errors.Wrap(err, "fail to extract txs from block")
	}
	return txs, nil
}

// getBlock retrieves block from chain for a block height
func (c *Client) getBlock(height int64) (*btcjson.GetBlockVerboseResult, error) {
	hash, err := c.client.GetBlockHash(height)
	if err != nil {
		return &btcjson.GetBlockVerboseResult{}, err
	}
	return c.client.GetBlockVerboseTx(hash)
}

// extractTxs extracts txs from a block to type TxIn
func (c *Client) extractTxs(block *btcjson.GetBlockVerboseResult) (*types.TxIn, error) {
	txIn := &types.TxIn{
		BlockHeight: strconv.FormatInt(block.Height, 10),
		Chain:       c.chain,
		Count:       strconv.Itoa(len(block.RawTx)),
	}
	var txItems []types.TxInItem
	for _, tx := range block.RawTx {
		if c.ignoreTx(&tx) {
			continue
		}
		sender, err := c.getSender(&tx)
		if err != nil {
			return &types.TxIn{}, errors.Wrap(err, "fail to get sender from tx")
		}
		memo, err := c.getMemo(&tx)
		if err != nil {
			return &types.TxIn{}, errors.Wrap(err, "fail to get memo from tx")
		}
		amount := uint64(tx.Vout[0].Value * common.One)
		txItems = append(txItems, types.TxInItem{
			Tx:     fmt.Sprintf("%s:0", tx.Txid),
			Sender: sender,
			To:     tx.Vout[0].ScriptPubKey.Addresses[0],
			Coins: common.Coins{
				common.NewCoin(common.BTCAsset, sdk.NewUint(amount)),
			},
			Memo: memo,
		})
	}
	txIn.TxArray = txItems
	return txIn, nil
}

// ignoreTx checks if we can already ignore a tx according to preset rules
//
// we expect array of "vout" for a BTC to have this format
// vout:0 is our vault
// vout:1 is any any change back to themselves
// vout:2 is OP_RETURN (first 80 bytes)
// vout:3 is OP_RETURN (next 80 bytes)
//
// Rules to ignore a tx are:
// - vout:0 doesn't have coins (value)
// - vout:0 doesn't have address
// - count vouts > 4
// - count vouts with coins (value) > 2
// - no OP_RETURN presents in tx vouts
//
func (c *Client) ignoreTx(tx *btcjson.TxRawResult) bool {
	if len(tx.Vin) == 0 || len(tx.Vout) == 0 || len(tx.Vout) > 4 {
		return true
	}
	if tx.Vout[0].Value == 0 || tx.Vin[0].Txid == "" {
		return true
	}
	// TODO check what we do if get multiple addresses
	if len(tx.Vout[0].ScriptPubKey.Addresses) != 1 {
		return true
	}
	countOPReturn := 0
	countWithCoins := 0
	for _, vout := range tx.Vout {
		if vout.Value > 0 {
			countWithCoins++
		}
		if strings.HasPrefix(vout.ScriptPubKey.Asm, "OP_RETURN") {
			countOPReturn++
		}
	}
	if countOPReturn == 0 || countOPReturn > 2 || countWithCoins > 2 {
		return true
	}
	return false
}

// getSender returns sender address for a btc tx, using vin:0
func (c *Client) getSender(tx *btcjson.TxRawResult) (string, error) {
	if len(tx.Vin) == 0 {
		return "", fmt.Errorf("no vin available in tx")
	}
	txHash, err := chainhash.NewHashFromStr(tx.Vin[0].Txid)
	if err != nil {
		return "", fmt.Errorf("fail to get tx hash from tx id string")
	}
	vinTx, err := c.client.GetRawTransactionVerbose(txHash)
	if err != nil {
		return "", fmt.Errorf("fail to query raw tx from btcd")
	}
	vout := vinTx.Vout[tx.Vin[0].Vout]
	if len(vout.ScriptPubKey.Addresses) == 0 {
		return "", fmt.Errorf("no address available in vout")
	}
	return vout.ScriptPubKey.Addresses[0], nil
}

// getMemo returns memo for a btc tx, using vout OP_RETURN
func (c *Client) getMemo(tx *btcjson.TxRawResult) (string, error) {
	var opreturns string
	for _, vout := range tx.Vout {
		if strings.HasPrefix(vout.ScriptPubKey.Asm, "OP_RETURN") {
			opreturn := strings.Split(vout.ScriptPubKey.Asm, " ")
			opreturns += opreturn[1]
		}
	}
	decoded, err := hex.DecodeString(opreturns)
	if err != nil {
		return "", fmt.Errorf("fail to decode OP_RETURN string")
	}
	return string(decoded), nil
}
