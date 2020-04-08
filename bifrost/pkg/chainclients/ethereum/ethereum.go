package ethereum

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"

	ecommon "github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	tssp "gitlab.com/thorchain/tss/go-tss/tss"

	"gitlab.com/thorchain/thornode/bifrost/config"
	"gitlab.com/thorchain/thornode/bifrost/metrics"
	"gitlab.com/thorchain/thornode/bifrost/pkg/chainclients/ethereum/types"
	pubkeymanager "gitlab.com/thorchain/thornode/bifrost/pubkeymanager"
	"gitlab.com/thorchain/thornode/bifrost/thorclient"
	stypes "gitlab.com/thorchain/thornode/bifrost/thorclient/types"
	"gitlab.com/thorchain/thornode/common"
)

// Client is a structure to sign and broadcast tx to Ethereum chain used by signer mostly
type Client struct {
	logger             zerolog.Logger
	RPCHost            string
	cfg                config.ChainConfiguration
	chainID            types.ChainID
	isTestNet          bool
	pk                 common.PubKey
	client             *ethclient.Client
	currentBlockHeight int64
	thorchainBridge    *thorclient.ThorchainBridge
}

// NewClient create new instance of Ethereum client
func NewClient(thorKeys *thorclient.Keys, cfg config.ChainConfiguration, server *tssp.TssServer, thorchainBridge *thorclient.ThorchainBridge) (*Client, error) {
	if len(cfg.RPCHost) == 0 {
		return nil, errors.New("rpc host is empty")
	}
	rpcHost := cfg.RPCHost

	priv, err := thorKeys.GetPrivateKey()
	if err != nil {
		return nil, fmt.Errorf("fail to get private key: %w", err)
	}

	pk, err := common.NewPubKeyFromCrypto(priv.PubKey())
	if err != nil {
		return nil, fmt.Errorf("fail to get pub key: %w", err)
	}
	if thorchainBridge == nil {
		return nil, errors.New("thorchain bridge is nil")
	}

	if !strings.HasPrefix(rpcHost, "http") {
		rpcHost = fmt.Sprintf("http://%s", rpcHost)
	}

	ctx := context.Background()
	ethClient, err := ethclient.DialContext(ctx, rpcHost)
	if err != nil {
		return nil, err
	}

	return &Client{
		logger:          log.With().Str("module", "ethereum").Logger(),
		RPCHost:         rpcHost,
		cfg:             cfg,
		client:          ethClient,
		pk:              pk,
		thorchainBridge: thorchainBridge,
	}, nil
}

func (c *Client) Start(globalTxsQueue chan stypes.TxIn, pubkeyMgr pubkeymanager.PubKeyValidator, m *metrics.Metrics) error {
	return nil
}

func (c *Client) Stop() error {
	return nil
}

// IsTestNet determinate whether we are running on test net by checking the status
func (c *Client) CheckIsTestNet() bool {
	// Cached data after first call
	if c.chainID > 0 {
		return c.isTestNet
	}
	ctx := context.Background()
	chainID, err := c.client.ChainID(ctx)
	if err != nil {
		log.Fatal().Msgf("Unable to get chain id %s", err.Error())
		return false
	}

	c.chainID = types.ChainID(chainID.Int64())
	c.isTestNet = c.chainID > 1
	return c.isTestNet
}

func (c *Client) GetChain() common.Chain {
	return common.ETHChain
}

func (c *Client) GetHeight() (int64, error) {
	ctx := context.Background()
	block, err := c.client.BlockByNumber(ctx, nil)
	if err != nil {
		return -1, err
	}
	return block.Number().Int64(), nil
}

// GetAddress return current signer address, it will be bech32 encoded address
func (c *Client) GetAddress(poolPubKey common.PubKey) string {
	addr, err := poolPubKey.GetAddress(common.ETHChain)
	if err != nil {
		c.logger.Error().Err(err).Str("pool_pub_key", poolPubKey.String()).Msg("fail to get pool address")
		return ""
	}
	return addr.String()
}

func (c *Client) GetGasPrice() (*big.Int, error) {
	ctx := context.Background()
	return c.client.SuggestGasPrice(ctx)
}

func (c *Client) GetGasFee(count uint64) common.Gas {
	return common.GetETHGasFee()
}

func (c *Client) ValidateMetadata(inter interface{}) bool {
	return true
}

// SignTx sign the the given TxArrayItem
func (c *Client) SignTx(tx stypes.TxOutItem, height int64) ([]byte, error) {
	return nil, nil
}

// GetAccount gets account by address in eth client
func (c *Client) GetAccount(addr string) (common.Account, error) {
	ctx := context.Background()
	nonce, err := c.client.NonceAt(ctx, ecommon.HexToAddress(addr), nil)
	if err != nil {
		return common.Account{}, fmt.Errorf("fail to get account nonce: %w", err)
	}
	balance, err := c.client.BalanceAt(ctx, ecommon.HexToAddress(addr), nil)
	if err != nil {
		return common.Account{}, fmt.Errorf("fail to get account nonce: %w", err)
	}
	account := common.NewAccount(int64(nonce), 0, common.AccountCoins{common.AccountCoin{Amount: balance.Uint64(), Denom: "ETH.ETH"}})
	return account, nil
}

// BroadcastTx decodes tx using rlp and broadcasts to Ethereum chain
func (c *Client) BroadcastTx(stx stypes.TxOutItem, hexTx []byte) error {
	return nil
}