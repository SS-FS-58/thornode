package eth

import (
	"context"
	"math/big"

	etypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/pkg/errors"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"gitlab.com/thorchain/thornode/bifrostv2/txscanner/types"
	"gitlab.com/thorchain/thornode/common"

	"gitlab.com/thorchain/thornode/bifrostv2/config"
)

type Client struct {
	client                   *ethclient.Client
	cfg                      config.ETHConfiguration
	ctx                      context.Context
	logger                   zerolog.Logger
	fnLastScannedBlockHeight types.FnLastScannedBlockHeight
	lastScannedBlockHeight   uint64
}

func NewClient(cfg config.ETHConfiguration) (*Client, error) {
	ctx := context.Background()
	ethClient, err := ethclient.DialContext(ctx, cfg.ChainHost)
	if err != nil {
		return nil, err
	}

	return &Client{
		logger: log.Logger.With().Str("module", "ethClient").Logger(),
		cfg:    cfg,
		client: ethClient,
		ctx:    ctx,
	}, nil
}

func (c *Client) getBlock(blockNumber uint64) (*etypes.Block, error) {
	return c.client.BlockByNumber(c.ctx, big.NewInt(int64(blockNumber)))
}

func (c *Client) getCurrentBlock() (*etypes.Block, error) {
	return c.client.BlockByNumber(c.ctx, nil)
}

func (c *Client) Start(txInChan chan<- types.TxIn, fnStartHeight types.FnLastScannedBlockHeight) error {
	c.logger.Info().Msg("starting")
	c.fnLastScannedBlockHeight = fnStartHeight

	var err error
	c.lastScannedBlockHeight, err = c.fnLastScannedBlockHeight(common.ETHChain)
	if err != nil {
		return errors.Wrap(err, "fnLastScannedBlockHeight")

	}

	go c.scanBlocks(txInChan)
	return nil
}

func (c *Client) scanBlocks(txInChan chan<- types.TxIn) {
	c.logger.Info().Msg("scanBlocks")
	for {
		block, err := c.getBlock(c.lastScannedBlockHeight)
		if err != nil {
			c.logger.Error().Err(err).Uint64("lastScannedBlockHeight", c.lastScannedBlockHeight).Msg("getBlock failed")
			continue
		}

		// extract TxIns from block
		var txIn types.TxIn
		txIn.BlockHeight = block.Number().Uint64()
		txIn.BlockHash = block.Hash().String()
		txIn.Chain = common.ETHChain

		txInChan <- txIn
		c.lastScannedBlockHeight++
	}
}

func (c *Client) Stop() error {
	c.logger.Info().Msg("stopped")
	return nil
}