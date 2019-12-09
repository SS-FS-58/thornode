package thorclient

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"time"

	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/hashicorp/go-retryablehttp"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"gitlab.com/thorchain/thornode/cmd"
	"gitlab.com/thorchain/thornode/common"
	stypes "gitlab.com/thorchain/thornode/x/thorchain/types"

	"gitlab.com/thorchain/thornode/bifrostv2/config"
	"gitlab.com/thorchain/thornode/bifrostv2/metrics"
	"gitlab.com/thorchain/thornode/bifrostv2/thorclient/types"
)

const (
	BaseEndpoint   = "/thorchain"
	VaultsEndpoint = "/vaults/pubkeys"
)

// Client will be used to send tx to thorchain
type Client struct {
	logger        zerolog.Logger
	cdc           *codec.Codec
	cfg           config.ThorChainConfiguration
	keys          *Keys
	errCounter    *prometheus.CounterVec
	m             *metrics.Metrics
	accountNumber uint64
	seqNumber     uint64
	client        *retryablehttp.Client
}

// NewClient create a new instance of Client
func NewClient(cfg config.ThorChainConfiguration, m *metrics.Metrics) (*Client, error) {
	if len(cfg.ChainID) == 0 {
		return nil, errors.New("chain id is empty")
	}
	if len(cfg.ChainHost) == 0 {
		return nil, errors.New("chain host is empty")
	}
	if len(cfg.SignerName) == 0 {
		return nil, errors.New("signer name is empty")
	}
	if len(cfg.SignerPasswd) == 0 {
		return nil, errors.New("signer password is empty")
	}
	k, err := NewKeys(cfg.ChainHomeFolder, cfg.SignerName, cfg.SignerPasswd)
	if nil != err {
		return nil, fmt.Errorf("fail to get keybase: %w", err)
	}

	// CosmosSDKConfig()

	return &Client{
		logger:     log.With().Str("module", "thorClient").Logger(),
		cdc:        MakeCodec(),
		cfg:        cfg,
		keys:       k,
		errCounter: m.GetCounterVec(metrics.ThorChainClientError),
		client:     retryablehttp.NewClient(),
		m:          m,
	}, nil
}

func MakeCodec() *codec.Codec {
	var cdc = codec.New()
	sdk.RegisterCodec(cdc)
	stypes.RegisterCodec(cdc)
	codec.RegisterCrypto(cdc)
	return cdc
}

// CosmosSDKConfig set's the default address prefixes from thorChain
func CosmosSDKConfig() {
	cosmosSDKConfig := sdk.GetConfig()
	cosmosSDKConfig.SetBech32PrefixForAccount(cmd.Bech32PrefixAccAddr, cmd.Bech32PrefixAccPub)
	cosmosSDKConfig.Seal()
}

func (c *Client) WithRetryableHttpClient(client *retryablehttp.Client) {
	c.client = client
}

func (c *Client) Start() error {
	if err := c.ensureNodeWhitelistedWithTimeout(); err != nil {
		c.logger.Error().Err(err).Msg("node account is not whitelisted, can't start")
		return errors.Wrap(err, "node account is not whitelisted, can't start")
	}

	accountNumber, sequenceNumber, err := c.getAccountNumberAndSequenceNumber(c.getAccountInfoUrl())
	if nil != err {
		return errors.Wrap(err, "fail to get account number and sequence number from thorchain")
	}

	c.logger.Info().Uint64("account number", accountNumber).Uint64("sequence no", sequenceNumber).Msg("account information")
	c.accountNumber = accountNumber
	c.seqNumber = sequenceNumber
	return nil
}

func (c *Client) getAccountInfoUrl() string {
	return c.getThorChainUrl(fmt.Sprintf("/auth/accounts/%s", c.keys.GetSignerInfo().GetAddress()))
}

func (c *Client) getAccountNumberAndSequenceNumber(requestUrl string) (uint64, uint64, error) {
	if len(requestUrl) == 0 {
		return 0, 0, errors.New("request url is empty")
	}

	body, err := c.get(requestUrl)
	if err != nil {
		return 0, 0, errors.Wrap(err, "failed to call: "+requestUrl)
	}
	var accountResp types.AccountResp
	if err := json.Unmarshal(body, &accountResp); nil != err {
		return 0, 0, errors.Wrap(err, "fail to unmarshal account resp")
	}
	var baseAccount authtypes.BaseAccount
	err = authtypes.ModuleCdc.UnmarshalJSON(accountResp.Result, &baseAccount)
	if err != nil {
		return 0, 0, errors.Wrap(err, "fail to unmarshal base account")
	}

	return baseAccount.AccountNumber, baseAccount.Sequence, nil

}

// Send the signed transaction to thorchain
func (c *Client) Send(signed authtypes.StdTx, mode types.TxMode) (common.TxID, error) {
	var noTxID = common.TxID("")
	if !mode.IsValid() {
		return noTxID, fmt.Errorf("transaction Mode (%s) is invalid", mode)
	}
	start := time.Now()
	defer func() {
		c.m.GetHistograms(metrics.SendToThorChainDuration).Observe(time.Since(start).Seconds())
	}()
	var setTx types.SetTx
	setTx.Mode = mode.String()
	setTx.Tx.Msg = signed.Msgs
	setTx.Tx.Fee = signed.Fee
	setTx.Tx.Signatures = signed.Signatures
	setTx.Tx.Memo = signed.Memo
	result, err := c.cdc.MarshalJSON(setTx)
	if nil != err {
		c.errCounter.WithLabelValues("fail_marshal_settx", "").Inc()
		return noTxID, errors.Wrap(err, "fail to marshal settx to json")
	}
	c.logger.Info().Str("payload", string(result)).Msg("post to thorchain")

	resp, err := c.client.Post(c.getThorChainUrl("/txs"), "application/json", bytes.NewBuffer(result))
	if err != nil {
		c.errCounter.WithLabelValues("fail_post_to_thorchain", "").Inc()
		return noTxID, errors.Wrap(err, "fail to post tx to thorchain")
	}
	defer func() {
		if err := resp.Body.Close(); nil != err {
			c.logger.Error().Err(err).Msg("fail to close response body")
		}
	}()
	body, err := ioutil.ReadAll(resp.Body)
	if nil != err {
		c.errCounter.WithLabelValues("fail_read_thorchain_resp", "").Inc()
		return noTxID, errors.Wrap(err, "fail to read response body")
	}
	var commit types.Commit
	err = json.Unmarshal(body, &commit)
	if err != nil {
		c.errCounter.WithLabelValues("fail_unmarshal_commit", "").Inc()
		return noTxID, errors.Wrap(err, "fail to unmarshal commit")
	}
	c.m.GetCounter(metrics.TxToThorChain).Inc()
	c.logger.Info().Msgf("Received a BlockHash of %v from the thorchain", commit.TxHash)
	return common.NewTxID(commit.TxHash)
}

// GetLastObservedInHeight returns the lastobservedin value for the chain past in
func (c *Client) GetLastObservedInHeight(chain common.Chain) (uint64, error) {
	lastblock, err := c.getLastBlock(chain)
	if err != nil {
		return 0, errors.Wrap(err, "Failed to GetLastObservedInHeight")
	}
	return lastblock.LastChainHeight.Uint64(), nil
}

// GetLastSignedOutheight returns the lastsignedout value for the chain past in
func (c *Client) GetLastSignedOutHeight(chain common.Chain) (uint64, error) {
	lastblock, err := c.getLastBlock(chain)
	if err != nil {
		return 0, errors.Wrap(err, "Failed to GetLastSignedOutheight")
	}
	return lastblock.LastSignedHeight.Uint64(), nil
}

// getLastBlock calls the /lastblock/{chain} endpoint and Unmarshal's into the QueryResHeights type
func (c *Client) getLastBlock(chain common.Chain) (stypes.QueryResHeights, error) {
	path := fmt.Sprintf("/thorchain/lastblock/%s", chain.String())
	buf, err := c.get(path)
	if err != nil {
		return stypes.QueryResHeights{}, errors.Wrap(err, "failed to get lastblock")
	}
	var lastBlock stypes.QueryResHeights
	if err := c.cdc.UnmarshalJSON(buf, &lastBlock); nil != err {
		c.errCounter.WithLabelValues("fail_unmarshal_lastblock", "").Inc()
		return stypes.QueryResHeights{}, errors.Wrap(err, "fail to unmarshal last block")
	}
	return lastBlock, nil
}

func (c *Client) get(path string) ([]byte, error) {
	resp, err := c.client.Get(c.getThorChainUrl(path))
	if err != nil {
		return nil, errors.Wrap(err, "fail to get from thorchain")
	}
	defer func() {
		if err := resp.Body.Close(); nil != err {
			c.logger.Error().Err(err).Msg("fail to close response body")
		}
	}()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("fail to get last block height from thorchain")

	}
	buf, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.Wrap(err, "failed to read response body")
	}
	return buf, nil
}

// getThorChainUrl with the given path
func (c *Client) getThorChainUrl(path string) string {
	uri, err := url.Parse(c.cfg.ChainHost + path)
	if err != nil {
		// TODO handle!
	}
	return uri.String()
}

func (c *Client) ensureNodeWhitelistedWithTimeout() error {
	for {
		select {
		case <-time.After(time.Hour):
			return errors.New("bifrost is not whitelisted yet")
		default:
			err := c.ensureNodeWhitelisted()
			if err == nil {
				// node had been whitelisted
				return nil
			}
			c.logger.Error().Err(err).Msg("bifrost is not whitelisted , will retry a bit later")
			time.Sleep(time.Second * 30)
		}
	}
}

// ensureNodeWhitelisted will call to thorchain to check whether the bifrost had been whitelist or not
func (c *Client) ensureNodeWhitelisted() error {
	bepAddr := c.keys.GetSignerInfo().GetAddress().String()
	if len(bepAddr) == 0 {
		return errors.New("bep address is empty")
	}
	requestUrl := c.getThorChainUrl("/thorchain/observer/" + bepAddr)
	c.logger.Debug().Str("request_url", requestUrl).Msg("check node account status")
	buf, err := c.get(requestUrl)
	if err != nil {
		return errors.Wrap(err, "failed to call:"+requestUrl)
	}
	var nodeAccount stypes.NodeAccount
	if err := c.cdc.UnmarshalJSON(buf, &nodeAccount); nil != err {
		c.errCounter.WithLabelValues("fail_unmarshal_nodeaccount", "").Inc()
		return errors.Wrap(err, "fail to unmarshal node account")
	}

	if nodeAccount.Status == stypes.Disabled || nodeAccount.Status == stypes.Unknown {
		return errors.Errorf("node account status %s , will not be able to forward transaction to thorchain", nodeAccount.Status)
	}
	return nil
}
