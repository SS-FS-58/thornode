package binance

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/binance-chain/go-sdk/common/types"
	bmsg "github.com/binance-chain/go-sdk/types/msg"
	"github.com/binance-chain/go-sdk/types/tx"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/tendermint/go-amino"

	"gitlab.com/thorchain/thornode/common"

	"gitlab.com/thorchain/thornode/bifrost/blockscanner"
	"gitlab.com/thorchain/thornode/bifrost/config"
	"gitlab.com/thorchain/thornode/bifrost/metrics"
	pubkeymanager "gitlab.com/thorchain/thornode/bifrost/pubkeymanager"
	stypes "gitlab.com/thorchain/thornode/bifrost/thorclient/types"
)

// BinanceBlockScanner is to scan the blocks
type BinanceBlockScanner struct {
	cfg            config.BlockScannerConfiguration
	logger         zerolog.Logger
	wg             *sync.WaitGroup
	stopChan       chan struct{}
	db             blockscanner.ScannerStorage
	m              *metrics.Metrics
	errCounter     *prometheus.CounterVec
	pubkeyMgr      pubkeymanager.PubKeyValidator
	globalTxsQueue chan stypes.TxIn
	http           *http.Client
	singleFee      uint64
	multiFee       uint64
	rpcHost        string
}

type QueryResult struct {
	Result struct {
		Response struct {
			Value string `json:"value"`
		} `json:"response"`
	} `json:"result"`
}

type itemData struct {
	Txs []string `json:"txs"`
}

type itemHeader struct {
	Height string `json:"height"`
}

type itemBlock struct {
	Header itemHeader `json:"header"`
	Data   itemData   `json:"data"`
}

type itemResult struct {
	Block itemBlock `json:"block"`
}

type item struct {
	Jsonrpc string     `json:"jsonrpc"`
	ID      string     `json:"id"`
	Result  itemResult `json:"result"`
}

// NewBinanceBlockScanner create a new instance of BlockScan
func NewBinanceBlockScanner(cfg config.BlockScannerConfiguration, startBlockHeight int64, scanStorage blockscanner.ScannerStorage, isTestNet bool, pkmgr pubkeymanager.PubKeyValidator, m *metrics.Metrics) (*BinanceBlockScanner, error) {
	if len(cfg.RPCHost) == 0 {
		return nil, errors.New("rpc host is empty")
	}

	rpcHost := cfg.RPCHost
	if !strings.HasPrefix(rpcHost, "http") {
		rpcHost = fmt.Sprintf("http://%s", rpcHost)
	}

	if scanStorage == nil {
		return nil, errors.New("scanStorage is nil")
	}
	if pkmgr == nil {
		return nil, errors.New("pubkey validator is nil")
	}
	if m == nil {
		return nil, errors.New("metrics is nil")
	}
	if isTestNet {
		types.Network = types.TestNetwork
	} else {
		types.Network = types.ProdNetwork
	}

	netClient := &http.Client{
		Timeout: time.Second * 10,
	}

	return &BinanceBlockScanner{
		cfg:        cfg,
		pubkeyMgr:  pkmgr,
		logger:     log.Logger.With().Str("module", "blockscanner").Str("chain", "binance").Logger(),
		wg:         &sync.WaitGroup{},
		stopChan:   make(chan struct{}),
		db:         scanStorage,
		errCounter: m.GetCounterVec(metrics.BlockScanError(common.BNBChain)),
		http:       netClient,
		rpcHost:    rpcHost,
	}, nil
}

// getTxHash return hex formatted value of tx hash
// raw tx base 64 encoded -> base64 decode -> sha256sum = tx hash
func (b *BinanceBlockScanner) getTxHash(encodedTx string) (string, error) {
	decodedTx, err := base64.StdEncoding.DecodeString(encodedTx)
	if err != nil {
		b.errCounter.WithLabelValues("fail_decode_tx", encodedTx).Inc()
		return "", errors.Wrap(err, "fail to decode tx")
	}
	return fmt.Sprintf("%X", sha256.Sum256(decodedTx)), nil
}

func (b *BinanceBlockScanner) updateFees(height int64) error {
	url := fmt.Sprintf("%s/abci_query?path=\"/param/fees\"&height=%d", b.rpcHost, height)
	resp, err := b.http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("failed to get current gas fees: non 200 error (%d)", resp.StatusCode)
	}

	bz, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	var result QueryResult
	if err := json.Unmarshal(bz, &result); err != nil {
		return err
	}

	val, err := base64.StdEncoding.DecodeString(result.Result.Response.Value)
	if err != nil {
		return err
	}

	var fees []types.FeeParam
	cdc := amino.NewCodec()
	types.RegisterWire(cdc)
	err = cdc.UnmarshalBinaryLengthPrefixed(val, &fees)
	if err != nil {
		return err
	}

	for _, fee := range fees {
		if fee.GetParamType() == types.TransferFeeType {
			if err := fee.Check(); err != nil {
				return err
			}

			transferFee := fee.(*types.TransferFeeParam)
			if transferFee.FixedFeeParams.Fee > 0 {
				b.singleFee = uint64(transferFee.FixedFeeParams.Fee)
			}
			if transferFee.MultiTransferFee > 0 {
				b.multiFee = uint64(transferFee.MultiTransferFee)
			}
		}
	}

	return nil
}

func (b *BinanceBlockScanner) processBlock(block blockscanner.Block) (stypes.TxIn, error) {
	var txIn stypes.TxIn
	strBlock := strconv.FormatInt(block.Height, 10)
	if err := b.db.SetBlockScanStatus(block, blockscanner.Processing); err != nil {
		b.errCounter.WithLabelValues("fail_set_block_status", strBlock).Inc()
		return txIn, errors.Wrapf(err, "fail to set block scan status for block %d", block.Height)
	}

	b.logger.Debug().Int64("block", block.Height).Int("txs", len(block.Txs)).Msg("txs")
	if len(block.Txs) == 0 {
		b.m.GetCounter(metrics.BlockWithoutTx("BNB")).Inc()
		b.logger.Debug().Int64("block", block.Height).Msg("there are no txs in this block")
		return txIn, nil
	}

	// update our gas fees from binance RPC node
	if err := b.updateFees(block.Height); err != nil {
		b.logger.Error().Err(err).Msg("fail to update Binance gas fees")
	}

	// TODO implement pagination appropriately
	for _, txn := range block.Txs {
		hash, err := b.getTxHash(txn)
		if err != nil {
			b.errCounter.WithLabelValues("fail_get_tx_hash", strBlock).Inc()
			b.logger.Error().Err(err).Str("tx", txn).Msg("fail to get tx hash from raw data")
			return txIn, errors.Wrap(err, "fail to get tx hash from tx raw data")
		}

		txItemIns, err := b.fromTxToTxIn(hash, txn)
		if err != nil {
			b.errCounter.WithLabelValues("fail_get_tx", strBlock).Inc()
			b.logger.Error().Err(err).Str("hash", hash).Msg("fail to get one tx from server")
			// if THORNode fail to get one tx hash from server, then THORNode should bail, because THORNode might miss tx
			// if THORNode bail here, then THORNode should retry later
			return txIn, errors.Wrap(err, "fail to get one tx from server")
		}
		if len(txItemIns) > 0 {
			txIn.TxArray = append(txIn.TxArray, txItemIns...)
			b.m.GetCounter(metrics.BlockWithTxIn("BNB")).Inc()
			b.logger.Info().Str("hash", hash).Msg("THORNode got one tx")
		}
	}
	if len(txIn.TxArray) == 0 {
		b.m.GetCounter(metrics.BlockNoTxIn("BNB")).Inc()
		b.logger.Debug().Int64("block", block.Height).Msg("no tx need to be processed in this block")
		return txIn, nil
	}

	txIn.BlockHeight = strconv.FormatInt(block.Height, 10)
	txIn.Count = strconv.Itoa(len(txIn.TxArray))
	txIn.Chain = common.BNBChain
	return txIn, nil
}

func (b *BinanceBlockScanner) getFromHttp(url string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		b.errCounter.WithLabelValues("fail_create_http_request", url).Inc()
		return nil, errors.Wrap(err, "fail to create http request")
	}
	resp, err := b.http.Do(req)
	if err != nil {
		b.errCounter.WithLabelValues("fail_send_http_request", url).Inc()
		return nil, errors.Wrapf(err, "fail to get from %s ", url)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			b.logger.Error().Err(err).Msg("fail to close http response body.")
		}
	}()

	if resp.StatusCode != http.StatusOK {
		b.errCounter.WithLabelValues("unexpected_status_code", resp.Status).Inc()
		return nil, errors.Errorf("unexpected status code:%d from %s", resp.StatusCode, url)
	}
	buf, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	// test if our response body is an error block json format
	errorBlock := struct {
		Error struct {
			Code    int64  `json:"code"`
			Message string `json:"message"`
			Data    string `json:"data"`
		} `json:"error"`
	}{}

	_ = json.Unmarshal(buf, &errorBlock) // ignore error
	if errorBlock.Error.Code != 0 {
		return nil, fmt.Errorf(
			"%s (%d): %s",
			errorBlock.Error.Message,
			errorBlock.Error.Code,
			errorBlock.Error.Data,
		)
	}

	return buf, nil
}

func (b *BinanceBlockScanner) getRPCBlock(height int64) (int64, []string, error) {
	start := time.Now()
	defer func() {
		if err := recover(); err != nil {
			b.logger.Error().Msgf("fail to get RPCBlock:%s", err)
		}
		duration := time.Since(start)
		b.m.GetHistograms(metrics.BlockDiscoveryDuration).Observe(duration.Seconds())
	}()
	url := b.BlockRequest(b.rpcHost, height)
	buf, err := b.getFromHttp(url)
	if err != nil {
		b.errCounter.WithLabelValues("fail_get_block", url).Inc()
		time.Sleep(300 * time.Millisecond)
		return 0, nil, err
	}

	rawTxns, err := b.UnmarshalBlock(buf)
	if err != nil {
		b.errCounter.WithLabelValues("fail_unmarshal_block", url).Inc()
	}
	return height, rawTxns, err
}

func (b *BinanceBlockScanner) BlockRequest(rpcHost string, height int64) string {
	u, _ := url.Parse(rpcHost)
	u.Path = "block"
	if height > 0 {
		u.RawQuery = fmt.Sprintf("height=%d", height)
	}
	return u.String()
}

func (b *BinanceBlockScanner) UnmarshalBlock(buf []byte) ([]string, error) {
	// check if the block is null. This can happen when binance gets the block,
	// but not the data within it. In which case, we'll never have the data and
	// we should just move onto the next block.
	// { "jsonrpc": "2.0", "id": "", "result": { "block_meta": null, "block": null } }
	if bytes.Contains(buf, []byte(`"block": null`)) {
		return nil, nil
	}

	var block item
	err := json.Unmarshal(buf, &block)
	if err != nil {
		return nil, errors.Wrap(err, "fail to unmarshal body to rpcBlock")
	}

	return block.Result.Block.Data.Txs, nil
}

func (b *BinanceBlockScanner) FetchTxs(height int64) (stypes.TxIn, error) {
	return stypes.TxIn{}, nil
}

/*
func (b *BinanceBlockScanner) searchTxInABlock(idx int) {
	b.logger.Debug().Int("idx", idx).Msg("start searching tx in a block")
	defer b.logger.Debug().Int("idx", idx).Msg("stop searching tx in a block")
	defer b.wg.Done()

	for {
		select {
		case <-b.stopChan: // time to get out
			return
		case block, more := <-b.commonBlockScanner.GetMessages():
			if !more {
				return
			}
			b.logger.Debug().Int64("block", block.Height).Msg("processing block")
			if err := b.processBlock(block); err != nil {
				if errStatus := b.db.SetBlockScanStatus(block, blockscanner.Failed); errStatus != nil {
					b.errCounter.WithLabelValues("fail_set_block_status", "").Inc()
					b.logger.Error().Err(err).Int64("height", block.Height).Msg("fail to set block to fail status")
				}
				b.errCounter.WithLabelValues("fail_search_block", "").Inc()
				b.logger.Error().Err(err).Int64("height", block.Height).Msg("fail to search tx in block")
				// THORNode will have a retry go routine to check it.
				continue
			}
			// set a block as success
			if err := b.db.RemoveBlockStatus(block.Height); err != nil {
				b.errCounter.WithLabelValues("fail_remove_block_status", "").Inc()
				b.logger.Error().Err(err).Int64("block", block.Height).Msg("fail to remove block status from data store, thus block will be re processed")
			}
		}
	}
}
*/

func (b BinanceBlockScanner) MatchedAddress(txInItem stypes.TxInItem) bool {
	// Check if we are migrating our funds...
	if ok := b.isMigration(txInItem.Sender, txInItem.Memo); ok {
		b.logger.Debug().Str("memo", txInItem.Memo).Msg("migrate")
		return true
	}

	// Check if our pool is registering a new yggdrasil pool. Ie
	// sending the staked assets to the user
	if ok := b.isRegisterYggdrasil(txInItem.Sender, txInItem.Memo); ok {
		b.logger.Debug().Str("memo", txInItem.Memo).Msg("yggdrasil+")
		return true
	}

	// Check if out pool is de registering a yggdrasil pool. Ie sending
	// the bond back to the user
	if ok := b.isDeregisterYggdrasil(txInItem.Sender, txInItem.Memo); ok {
		b.logger.Debug().Str("memo", txInItem.Memo).Msg("yggdrasil-")
		return true
	}

	// Check if THORNode are sending from a yggdrasil address
	if ok := b.isYggdrasil(txInItem.Sender); ok {
		b.logger.Debug().Str("assets sent from yggdrasil pool", txInItem.Memo).Msg("fill order")
		return true
	}

	// Check if THORNode are sending to a yggdrasil address
	if ok := b.isYggdrasil(txInItem.To); ok {
		b.logger.Debug().Str("assets to yggdrasil pool", txInItem.Memo).Msg("refill")
		return true
	}

	// outbound message from pool, when it is outbound, it does not matter how much coins THORNode send to customer for now
	if ok := b.isOutboundMsg(txInItem.Sender, txInItem.Memo); ok {
		b.logger.Debug().Str("memo", txInItem.Memo).Msg("outbound")
		return true
	}

	return false
}

// Check if memo is for registering an Asgard vault
func (b *BinanceBlockScanner) isMigration(addr, memo string) bool {
	return b.isAddrWithMemo(addr, memo, "migrate")
}

// Check if memo is for registering a Yggdrasil vault
func (b *BinanceBlockScanner) isRegisterYggdrasil(addr, memo string) bool {
	return b.isAddrWithMemo(addr, memo, "yggdrasil+")
}

// Check if memo is for de registering a Yggdrasil vault
func (b *BinanceBlockScanner) isDeregisterYggdrasil(addr, memo string) bool {
	return b.isAddrWithMemo(addr, memo, "yggdrasil-")
}

// Check if THORNode have an outbound yggdrasil transaction
func (b *BinanceBlockScanner) isYggdrasil(addr string) bool {
	ok, _ := b.pubkeyMgr.IsValidPoolAddress(addr, common.BNBChain)
	return ok
}

func (b *BinanceBlockScanner) isOutboundMsg(addr, memo string) bool {
	return b.isAddrWithMemo(addr, memo, "outbound")
}

func (b *BinanceBlockScanner) isAddrWithMemo(addr, memo, targetMemo string) bool {
	match, _ := b.pubkeyMgr.IsValidPoolAddress(addr, common.BNBChain)
	if !match {
		return false
	}
	lowerMemo := strings.ToLower(memo)
	if strings.HasPrefix(lowerMemo, targetMemo) {
		return true
	}
	return false
}

func (b *BinanceBlockScanner) getCoinsForTxIn(outputs []bmsg.Output) (common.Coins, error) {
	cc := common.Coins{}
	for _, output := range outputs {
		for _, c := range output.Coins {
			asset, err := common.NewAsset(fmt.Sprintf("BNB.%s", c.Denom))
			if err != nil {
				b.errCounter.WithLabelValues("fail_create_ticker", c.Denom).Inc()
				return nil, errors.Wrapf(err, "fail to create asset, %s is not valid", c.Denom)
			}
			amt := sdk.NewUint(uint64(c.Amount))
			cc = append(cc, common.NewCoin(asset, amt))
		}
	}
	return cc, nil
}

func (b *BinanceBlockScanner) fromTxToTxIn(hash, encodedTx string) ([]stypes.TxInItem, error) {
	if len(encodedTx) == 0 {
		return nil, errors.New("tx is empty")
	}
	buf, err := base64.StdEncoding.DecodeString(encodedTx)
	if err != nil {
		b.errCounter.WithLabelValues("fail_decode_tx", hash).Inc()
		return nil, errors.Wrap(err, "fail to decode tx")
	}
	var t tx.StdTx
	if err := tx.Cdc.UnmarshalBinaryLengthPrefixed(buf, &t); err != nil {
		b.errCounter.WithLabelValues("fail_unmarshal_tx", hash).Inc()
		return nil, errors.Wrap(err, "fail to unmarshal tx.StdTx")
	}

	return b.fromStdTx(hash, t)
}

// fromStdTx - process a stdTx
func (b *BinanceBlockScanner) fromStdTx(hash string, stdTx tx.StdTx) ([]stypes.TxInItem, error) {
	var err error
	var txs []stypes.TxInItem

	// TODO: It is also possible to have multiple inputs/outputs within a
	// single stdTx, which THORNode are not yet accounting for.
	for _, msg := range stdTx.Msgs {
		switch sendMsg := msg.(type) {
		case bmsg.SendMsg:
			txInItem := stypes.TxInItem{
				Tx: hash,
			}
			txInItem.Memo = stdTx.Memo
			// THORNode take the first Input as sender, first Output as receiver
			// so if THORNode send to multiple different receiver within one tx, this won't be able to process it.
			sender := sendMsg.Inputs[0]
			receiver := sendMsg.Outputs[0]
			txInItem.Sender = sender.Address.String()
			txInItem.To = receiver.Address.String()
			txInItem.Coins, err = b.getCoinsForTxIn(sendMsg.Outputs)
			if err != nil {
				return nil, errors.Wrap(err, "fail to convert coins")
			}

			// Calculate gas for this tx
			txInItem.Gas = common.CalcGasPrice(common.Tx{Coins: txInItem.Coins}, common.BNBAsset, []sdk.Uint{sdk.NewUint(b.singleFee), sdk.NewUint(b.multiFee)})

			if ok := b.MatchedAddress(txInItem); !ok {
				continue
			}

			// NOTE: the following could result in the same tx being added
			// twice, which is expected. We want to make sure we generate both
			// a inbound and outbound txn, if we both apply.

			// check if the from address is a valid pool
			if ok, cpi := b.pubkeyMgr.IsValidPoolAddress(txInItem.Sender, common.BNBChain); ok {
				txInItem.ObservedPoolAddress = cpi.PubKey.String()
				txs = append(txs, txInItem)
			}
			// check if the to address is a valid pool address
			if ok, cpi := b.pubkeyMgr.IsValidPoolAddress(txInItem.To, common.BNBChain); ok {
				txInItem.ObservedPoolAddress = cpi.PubKey.String()
				txs = append(txs, txInItem)
			} else {
				// Apparently we don't recognize where we are sending funds to.
				// Lets check if we should because its an internal transaction
				// moving funds between vaults (for example). If it is, lets
				// manually trigger an update of pubkeys, then check again...
				switch strings.ToLower(txInItem.Memo) {
				case "migrate", "yggdrasil-", "yggdrasil+":
					b.pubkeyMgr.FetchPubKeys()
					if ok, cpi := b.pubkeyMgr.IsValidPoolAddress(txInItem.To, common.BNBChain); ok {
						txInItem.ObservedPoolAddress = cpi.PubKey.String()
						txs = append(txs, txInItem)
					}
				}
			}

		default:
			continue
		}
	}
	return txs, nil
}
