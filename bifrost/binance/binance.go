package binance

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/binance-chain/go-sdk/common/types"
	ctypes "github.com/binance-chain/go-sdk/common/types"
	"github.com/binance-chain/go-sdk/keys"
	ttypes "github.com/binance-chain/go-sdk/types"
	"github.com/binance-chain/go-sdk/types/msg"
	"github.com/binance-chain/go-sdk/types/tx"
	"github.com/pkg/errors"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"gitlab.com/thorchain/thornode/bifrost/config"
	"gitlab.com/thorchain/thornode/bifrost/thorclient"
	stypes "gitlab.com/thorchain/thornode/bifrost/thorclient/types"
	"gitlab.com/thorchain/thornode/bifrost/tss"
	"gitlab.com/thorchain/thornode/common"
)

// Binance is a structure to sign and broadcast tx to binance chain used by signer mostly
type Binance struct {
	logger             zerolog.Logger
	cfg                config.BinanceConfiguration
	RPCHost            string
	chainID            string
	IsTestNet          bool
	client             *http.Client
	accountNumber      int64
	seqNumber          int64
	currentBlockHeight int64
	signLock           *sync.Mutex
	tssKeyManager      keys.KeyManager
	localKeyManager    *keyManager
}

// NewBinance create new instance of binance client
func NewBinance(thorKeys *thorclient.Keys, cfg config.BinanceConfiguration, keySignCfg config.TSSConfiguration) (*Binance, error) {
	if len(cfg.RPCHost) == 0 {
		return nil, errors.New("rpc host is empty")
	}
	tssKm, err := tss.NewKeySign(keySignCfg)
	if nil != err {
		return nil, errors.Wrap(err, "fail to create tss signer")
	}

	priv, err := thorKeys.GetPrivateKey()
	if err != nil {
		return nil, errors.Wrap(err, "fail to get private key")
	}

	pk, err := common.NewPubKeyFromCrypto(priv.PubKey())
	if err != nil {
		return nil, errors.Wrap(err, "fail to get pub key")
	}

	localKm := &keyManager{
		privKey: priv,
		addr:    ctypes.AccAddress(priv.PubKey().Address()),
		pubkey:  pk,
	}

	rpcHost := cfg.RPCHost
	if !strings.HasPrefix(rpcHost, "http") {
		rpcHost = fmt.Sprintf("http://%s", rpcHost)
	}

	bnb := &Binance{
		logger:          log.With().Str("module", "binance").Logger(),
		cfg:             cfg,
		RPCHost:         rpcHost,
		client:          &http.Client{},
		signLock:        &sync.Mutex{},
		tssKeyManager:   tssKm,
		localKeyManager: localKm,
	}

	chainID, isTestNet := bnb.CheckIsTestNet()
	if isTestNet {
		types.Network = types.TestNetwork
	} else {
		types.Network = types.ProdNetwork
	}

	bnb.IsTestNet = isTestNet
	bnb.chainID = chainID
	return bnb, nil
}

// IsTestNet determinate whether we are running on test net by checking the status
func (b *Binance) CheckIsTestNet() (string, bool) {
	u, err := url.Parse(b.RPCHost)
	if err != nil {
		log.Fatal().Msgf("Unable to parse rpc host: %s\n", b.RPCHost)
	}

	u.Path = "/status"

	resp, err := b.client.Get(u.String())
	if err != nil {
		log.Fatal().Msgf("%v\n", err)
	}

	defer func() {
		if err := resp.Body.Close(); nil != err {
			log.Error().Err(err).Msg("fail to close resp body")
		}
	}()

	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Fatal().Err(err).Msg("fail to read body")
	}

	type Status struct {
		Jsonrpc string `json:"jsonrpc"`
		ID      string `json:"id"`
		Result  struct {
			NodeInfo struct {
				Network string `json:"network"`
			} `json:"node_info"`
		} `json:"result"`
	}

	var status Status
	if err := json.Unmarshal(data, &status); nil != err {
		log.Fatal().Err(err).Msg("fail to unmarshal body")
	}

	isTestNet := status.Result.NodeInfo.Network == "Binance-Chain-Nile"
	return status.Result.NodeInfo.Network, isTestNet
}

func (b *Binance) GetHeight() (int64, error) {
	u, err := url.Parse(b.RPCHost)
	if err != nil {
		return 0, errors.Wrap(err, "Unable to parse dex host")
	}
	u.Path = "abci_info"
	resp, err := b.client.Get(u.String())
	if err != nil {
		return 0, fmt.Errorf("fail to get request(%s): %w", u.String(), err) // errors.Wrap(err, "Get request failed")
	}

	defer func() {
		if err := resp.Body.Close(); nil != err {
			log.Error().Err(err).Msg("fail to close resp body")
		}
	}()

	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return 0, errors.Wrap(err, "fail to read resp body")
	}

	type ABCIinfo struct {
		Jsonrpc string `json:"jsonrpc"`
		ID      string `json:"id"`
		Result  struct {
			Response struct {
				BlockHeight string `json:"last_block_height"`
			} `json:"response"`
		} `json:"result"`
	}

	var abci ABCIinfo
	if err := json.Unmarshal(data, &abci); nil != err {
		return 0, errors.Wrap(err, "failed to unmarshal")
	}

	return strconv.ParseInt(abci.Result.Response.BlockHeight, 10, 64)
}

func (b *Binance) input(addr types.AccAddress, coins types.Coins) msg.Input {
	return msg.Input{
		Address: addr,
		Coins:   coins,
	}
}

func (b *Binance) output(addr types.AccAddress, coins types.Coins) msg.Output {
	return msg.Output{
		Address: addr,
		Coins:   coins,
	}
}

func (b *Binance) msgToSend(in []msg.Input, out []msg.Output) msg.SendMsg {
	return msg.SendMsg{Inputs: in, Outputs: out}
}

func (b *Binance) createMsg(from types.AccAddress, fromCoins types.Coins, transfers []msg.Transfer) msg.SendMsg {
	input := b.input(from, fromCoins)
	output := make([]msg.Output, 0, len(transfers))
	for _, t := range transfers {
		t.Coins = t.Coins.Sort()
		output = append(output, b.output(t.ToAddr, t.Coins))
	}
	return b.msgToSend([]msg.Input{input}, output)
}

func (b *Binance) parseTx(fromAddr string, transfers []msg.Transfer) msg.SendMsg {
	addr, err := types.AccAddressFromBech32(fromAddr)
	if nil != err {
		b.logger.Error().Str("address", fromAddr).Err(err).Msg("fail to parse address")
	}
	fromCoins := types.Coins{}
	for _, t := range transfers {
		t.Coins = t.Coins.Sort()
		fromCoins = fromCoins.Plus(t.Coins)
	}
	return b.createMsg(addr, fromCoins, transfers)
}

// GetAddress return current signer address, it will be bech32 encoded address
func (b *Binance) GetAddress(poolPubKey common.PubKey) string {
	if !b.localKeyManager.Pubkey().Equals(poolPubKey) {
		return b.tssKeyManager.GetAddr().String()
	}
	addr, err := poolPubKey.GetAddress(common.BNBChain)
	if nil != err {
		b.logger.Error().Err(err).Str("pool_pub_key", poolPubKey.String()).Msg("fail to get pool address")
		return ""
	}
	return addr.String()
}

func (b *Binance) isSignerAddressMatch(pubKey common.PubKey, signerAddr string) bool {
	bnbAddress, err := pubKey.GetAddress(common.BNBChain)
	if nil != err {
		b.logger.Error().Err(err).Msg("fail to create bnb address from the pub key")
		return false
	}
	b.logger.Info().Msg(bnbAddress.String())
	return strings.EqualFold(bnbAddress.String(), signerAddr)
}

// SignTx sign the the given TxArrayItem
func (b *Binance) signTx(tai stypes.TxOutItem, height int64) ([]byte, map[string]string, error) {
	b.signLock.Lock()
	defer b.signLock.Unlock()
	signerAddr := b.GetAddress(tai.VaultPubKey)
	var payload []msg.Transfer

	if !b.isSignerAddressMatch(tai.VaultPubKey, signerAddr) {
		b.logger.Info().Str("signer addr", signerAddr).Str("pool addr", tai.VaultPubKey.String()).Msg("address doesn't match ignore")
		return nil, nil, nil
	}

	toAddr, err := types.AccAddressFromBech32(tai.ToAddress.String())
	if nil != err {
		return nil, nil, errors.Wrapf(err, "fail to parse account address(%s)", tai.ToAddress.String())
	}

	var coins types.Coins
	for _, coin := range tai.Coins {
		coins = append(coins, types.Coin{
			Denom:  coin.Asset.Symbol.String(),
			Amount: int64(coin.Amount.Uint64()),
		})
	}

	payload = append(payload, msg.Transfer{
		ToAddr: toAddr,
		Coins:  coins,
	})

	if len(payload) == 0 {
		b.logger.Error().Msg("payload is empty , this should not happen")
		return nil, nil, nil
	}
	fromAddr := b.GetAddress(tai.VaultPubKey)
	sendMsg := b.parseTx(fromAddr, payload)
	if err := sendMsg.ValidateBasic(); nil != err {
		return nil, nil, errors.Wrap(err, "invalid send msg")
	}

	address, err := types.AccAddressFromBech32(fromAddr)
	if err != nil {
		b.logger.Error().Err(err).Msgf("fail to get parse address: %s", fromAddr)
		return nil, nil, err
	}
	currentHeight, err := b.GetHeight()
	if err != nil {
		b.logger.Error().Err(err).Msg("fail to get current binance block height")
		return nil, nil, err
	}
	if currentHeight > b.currentBlockHeight {
		acc, err := b.GetAccount(address)
		if err != nil {
			return nil, nil, errors.Wrap(err, "fail to get account info")
		}
		atomic.StoreInt64(&b.currentBlockHeight, currentHeight)
		atomic.StoreInt64(&b.accountNumber, acc.AccountNumber)
		atomic.StoreInt64(&b.seqNumber, acc.Sequence)
	}
	b.logger.Info().Int64("account_number", b.accountNumber).
		Int64("sequence_number", b.seqNumber).Msg("account info")
	signMsg := tx.StdSignMsg{
		ChainID:       b.chainID,
		Memo:          tai.Memo,
		Msgs:          []msg.Msg{sendMsg},
		Source:        tx.Source,
		Sequence:      b.seqNumber,
		AccountNumber: b.accountNumber,
	}
	param := map[string]string{
		"sync": "true",
	}
	rawBz, err := b.signWithRetry(signMsg, fromAddr, tai.VaultPubKey)
	if nil != err {
		return nil, nil, errors.Wrap(err, "fail to sign message")
	}

	if len(rawBz) == 0 {
		return nil, nil, nil
	}

	hexTx := []byte(hex.EncodeToString(rawBz))
	return hexTx, param, nil
}

func (b *Binance) sign(signMsg tx.StdSignMsg, poolPubKey common.PubKey) ([]byte, error) {
	if b.localKeyManager.Pubkey().Equals(poolPubKey) {
		return b.localKeyManager.Sign(signMsg)
	}
	k := b.tssKeyManager.(tss.ThorchainKeyManager)
	return k.SignWithPool(signMsg, poolPubKey)
}

// signWithRetry is design to sign a given message until it success or the same message had been send out by other signer
func (b *Binance) signWithRetry(signMsg tx.StdSignMsg, from string, poolPubKey common.PubKey) ([]byte, error) {
	for {
		rawBytes, err := b.sign(signMsg, poolPubKey)
		if nil == err {
			return rawBytes, nil
		}
		b.logger.Error().Err(err).Msgf("fail to sign msg with memo: %s", signMsg.Memo)
		// should THORNode give up? let's check the seq no on binance chain
		// keep in mind, when THORNode don't run our own binance full node, THORNode might get rate limited by binance
		address, err := types.AccAddressFromBech32(from)
		if err != nil {
			b.logger.Error().Err(err).Msgf("fail to get parse address: %s", from)
			return nil, err
		}

		acc, err := b.GetAccount(address)
		if nil != err {
			b.logger.Error().Err(err).Msg("fail to get account info from binance chain")
			continue
		}
		if acc.Sequence > signMsg.Sequence {
			b.logger.Debug().Msgf("msg with memo: %s , seqNo: %d had been processed", signMsg.Memo, signMsg.Sequence)
			return nil, nil
		}
	}
}

func (b *Binance) GetAccount(addr types.AccAddress) (types.BaseAccount, error) {
	u, err := url.Parse(b.RPCHost)
	if err != nil {
		log.Fatal().Msgf("Error parsing rpc (%s): %s", b.RPCHost, err)
		return types.BaseAccount{}, err
	}
	u.Path = "/abci_query"
	v := u.Query()
	v.Set("path", fmt.Sprintf("\"/account/%s\"", addr.String()))
	u.RawQuery = v.Encode()

	resp, err := http.Get(u.String())
	if err != nil {
		return types.BaseAccount{}, err
	}
	defer func() {
		if err := resp.Body.Close(); nil != err {
			b.logger.Error().Err(err).Msg("fail to close response body")
		}
	}()

	type queryResult struct {
		Jsonrpc string `json:"jsonrpc"`
		ID      string `json:"id"`
		Result  struct {
			Response struct {
				Key         string `json:"key"`
				Value       string `json:"value"`
				BlockHeight string `json:"height"`
			} `json:"response"`
		} `json:"result"`
	}

	var result queryResult
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return types.BaseAccount{}, err
	}

	err = json.Unmarshal(body, &result)
	if err != nil {
		return types.BaseAccount{}, err
	}

	data, err := base64.StdEncoding.DecodeString(result.Result.Response.Value)
	if err != nil {
		return types.BaseAccount{}, err
	}

	cdc := ttypes.NewCodec()
	var acc types.AppAccount
	err = cdc.UnmarshalBinaryBare(data, &acc)

	return acc.BaseAccount, err
}

// broadcastTx is to broadcast the tx to binance chain
func (b *Binance) broadcastTx(hexTx []byte) error {
	u, err := url.Parse(b.RPCHost)
	if err != nil {
		log.Error().Msgf("Error parsing rpc (%s): %s", b.RPCHost, err)
		return err
	}
	u.Path = "broadcast_tx_commit"
	values := u.Query()
	values.Set("tx", "0x"+string(hexTx))
	u.RawQuery = values.Encode()
	resp, err := http.Post(u.String(), "", nil)
	if err != nil {
		return errors.Wrap(err, "fail to broadcast tx to ")
	}
	defer func() {
		if err := resp.Body.Close(); nil != err {
			log.Error().Err(err).Msg("we fail to close response body")
		}
	}()
	if resp.StatusCode != http.StatusOK {
		result, err := ioutil.ReadAll(resp.Body)
		if nil != err {
			return fmt.Errorf("fail to read response body: %w", err)
		}
		log.Info().Msg(string(result))
		return fmt.Errorf("fail to broadcast tx to binance:(%s)", b.RPCHost)
	}

	return nil
}

func (b *Binance) SignAndBroadcastToBinanceChain(tai stypes.TxOutItem, height int64) error {
	hexTx, _, err := b.signTx(tai, height)
	if err != nil {
		return fmt.Errorf("fail to sign txout:%w", err)
	}
	if nil == hexTx {
		b.logger.Info().Msg("nothing need to be send")
		return nil
	}
	if err := b.broadcastTx(hexTx); nil != err {
		return fmt.Errorf("fail to broadcast to binance chain: %w", err)
	}
	atomic.AddInt64(&b.seqNumber, 1)
	return nil
}
