package blockscanner

import (
	"strconv"
	"strings"
	"sync"

	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"gitlab.com/thorchain/thornode/bifrost/config"
	"gitlab.com/thorchain/thornode/bifrost/metrics"
	"gitlab.com/thorchain/thornode/bifrost/thorclient"
	"gitlab.com/thorchain/thornode/bifrost/thorclient/types"
)

type BlockScannerFetcher interface {
	FetchTxs(height int64) (types.TxIn, error)
}

// BlockScanner is used to discover block height
type BlockScanner struct {
	cfg             config.BlockScannerConfiguration
	logger          zerolog.Logger
	wg              *sync.WaitGroup
	scanChan        chan int64
	stopChan        chan struct{}
	scannerStorage  ScannerStorage
	metrics         *metrics.Metrics
	previousBlock   int64
	globalTxsQueue  chan types.TxIn
	errorCounter    *prometheus.CounterVec
	thorchainBridge *thorclient.ThorchainBridge
	chainScanner    BlockScannerFetcher
}

// NewBlockScanner create a new instance of BlockScanner
func NewBlockScanner(cfg config.BlockScannerConfiguration, startBlockHeight int64, scannerStorage ScannerStorage, m *metrics.Metrics, thorchainBridge *thorclient.ThorchainBridge, chainScanner BlockScannerFetcher) (*BlockScanner, error) {
	if scannerStorage == nil {
		return nil, errors.New("scannerStorage is nil")
	}
	if m == nil {
		return nil, errors.New("metrics instance is nil")
	}
	if thorchainBridge == nil {
		return nil, errors.New("thorchain bridge is nil")
	}
	logger := log.Logger.With().Str("module", "blockscanner").Str("chain", cfg.ChainID.String()).Logger()
	return &BlockScanner{
		cfg:             cfg,
		logger:          logger,
		wg:              &sync.WaitGroup{},
		stopChan:        make(chan struct{}),
		scanChan:        make(chan int64),
		scannerStorage:  scannerStorage,
		metrics:         m,
		previousBlock:   startBlockHeight,
		errorCounter:    m.GetCounterVec(metrics.CommonBlockScannerError),
		thorchainBridge: thorchainBridge,
		chainScanner:    chainScanner,
	}, nil
}

// GetMessages return the channel
func (b *BlockScanner) GetMessages() <-chan int64 {
	return b.scanChan
}

// Start block scanner
func (b *BlockScanner) Start(globalTxsQueue chan types.TxIn) {
	b.globalTxsQueue = globalTxsQueue
	b.wg.Add(1)
	go b.scanBlocks()
}

// scanBlocks
func (b *BlockScanner) scanBlocks() {
	b.logger.Debug().Msg("start to scan blocks")
	defer b.logger.Debug().Msg("stop scan blocks")
	defer b.wg.Done()
	currentPos, err := b.scannerStorage.GetScanPos()
	if err != nil {
		b.errorCounter.WithLabelValues("fail_get_scan_pos", "").Inc()
		b.logger.Error().Err(err).Msgf("fail to get current block scan pos, %s will start from %d", b.cfg.ChainID, b.previousBlock)
	} else {
		b.previousBlock = currentPos
	}
	b.metrics.GetCounter(metrics.CurrentPosition).Add(float64(currentPos))

	// start up to grab those blocks
	for {
		select {
		case <-b.stopChan:
			return
		default:
			currentBlock := b.previousBlock + 1
			txIn, err := b.chainScanner.FetchTxs(currentBlock)
			if err != nil {
				// don't log an error if its because the block doesn't exist yet
				if !strings.Contains(err.Error(), "Height must be less than or equal to the current blockchain height") {

					b.errorCounter.WithLabelValues("fail_get_block", "").Inc()
					b.logger.Error().Err(err).Msg("fail to get RPCBlock")
				}
				continue
			}
			b.logger.Debug().Int64("block height", currentBlock).Int("txs", len(txIn.TxArray))
			b.previousBlock++
			b.metrics.GetCounter(metrics.TotalBlockScanned).Inc()
			if len(txIn.TxArray) == 0 {
				continue
			}
			select {
			case <-b.stopChan:
				return
			case b.globalTxsQueue <- txIn:
			}
			b.metrics.GetCounter(metrics.CurrentPosition).Inc()
			if err := b.scannerStorage.SetScanPos(b.previousBlock); err != nil {
				b.errorCounter.WithLabelValues("fail_save_block_pos", strconv.FormatInt(b.previousBlock, 10)).Inc()
				b.logger.Error().Err(err).Msg("fail to save block scan pos")
				// alert!!
				continue
			}
		}
	}
}

func (b *BlockScanner) FetchLastHeight() (startBlockHeight int64, err error) {
	startBlockHeight = b.cfg.StartBlockHeight
	if startBlockHeight == 0 {
		startBlockHeight, err = b.thorchainBridge.GetLastObservedInHeight(b.cfg.ChainID)
		if err != nil {
			return 0, errors.Wrap(err, "fail to get start block height from thorchain")
		}
		b.logger.Info().Int64("height", startBlockHeight).Msgf("Current block height is indeterminate; using current height from %s.", b.cfg.ChainID)
	}
	return
}

func (b *BlockScanner) Stop() error {
	b.logger.Debug().Msg("receive stop request")
	defer b.logger.Debug().Msg("common block scanner stopped")
	close(b.stopChan)
	b.wg.Wait()
	return nil
}
