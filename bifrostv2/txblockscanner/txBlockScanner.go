package txblockscanner

import (
	"sync"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"gitlab.com/thorchain/thornode/bifrostv2/config"
	"gitlab.com/thorchain/thornode/bifrostv2/pkg/blockclients"
	"gitlab.com/thorchain/thornode/bifrostv2/thorchain"
	"gitlab.com/thorchain/thornode/bifrostv2/types"
	"gitlab.com/thorchain/thornode/bifrostv2/vaultmanager"
)

type TxBlockScanner struct {
	cfg             config.TxScannerConfigurations
	logger          zerolog.Logger
	stopChan        chan struct{}
	thorchainClient *thorchain.Client
	chains          []blockclients.BlockChainClient
	wg              sync.WaitGroup
	closeOnce       sync.Once
	vaultMgr        *vaultmanager.VaultManager
	blockInChan     chan types.Block
}

func NewTxBlockScanner(cfg config.TxScannerConfigurations, vaultMgr *vaultmanager.VaultManager, thorchainClient *thorchain.Client) *TxBlockScanner {
	return &TxBlockScanner{
		logger:          log.Logger.With().Str("module", "txScanner").Logger(),
		cfg:             cfg,
		stopChan:        make(chan struct{}),
		thorchainClient: thorchainClient,
		wg:              sync.WaitGroup{},
		chains:          blockclients.LoadChains(cfg.BlockChains),
		vaultMgr:        vaultMgr,
		blockInChan:     make(chan types.Block),
	}
}

func (s *TxBlockScanner) Start() error {
	for _, chain := range s.chains {
		err := chain.Start(s.blockInChan, s.thorchainClient.GetLastObservedInHeight)
		if err != nil {
			s.logger.Err(err).Msg("failed to start chain")
			continue
		}
		s.wg.Add(1)
		go s.processBlocks(s.blockInChan)
	}
	return nil
}

func (s *TxBlockScanner) Stop() error {
	for _, chain := range s.chains {
		if err := chain.Stop(); err != nil {
			s.logger.Err(err).Msg("failed to stop chain")
		}
	}
	s.closeOnce.Do(func() {
		close(s.stopChan)
	})
	s.wg.Wait()
	s.logger.Info().Msg("stopped TxBlockScanner")
	return nil
}

func (s *TxBlockScanner) processBlocks(blockInsChan <-chan types.Block) {
	s.logger.Info().Msg("started processBlocks")
	defer s.logger.Info().Msg("stopped processBlocks")
	defer s.wg.Done()
	for {
		select {
		case <-s.stopChan:
			return
		case blockIn, more := <-blockInsChan:
			if !more {
				// channel closed
				return
			}
			// no tx's
			if len(blockIn.Txs) == 0 {
				s.logger.Debug().Msg("nothing to be forward to thorchain")
				continue
			}
			// TODO Add block/Tx processing logic
		}
	}
}
