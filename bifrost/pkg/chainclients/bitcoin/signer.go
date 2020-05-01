package bitcoin

import (
	"bytes"
	"errors"
	"fmt"
	"sort"

	"github.com/btcsuite/btcd/btcec"
	"github.com/btcsuite/btcd/btcjson"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/mempool"
	"github.com/btcsuite/btcd/wire"
	"github.com/btcsuite/btcutil"
	"github.com/btcsuite/btcutil/txsort"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/tendermint/tendermint/crypto"
	"github.com/tendermint/tendermint/crypto/secp256k1"
	"gitlab.com/thorchain/txscript"

	stypes "gitlab.com/thorchain/thornode/bifrost/thorclient/types"
	"gitlab.com/thorchain/thornode/bifrost/tss"
	"gitlab.com/thorchain/thornode/common"
)

// SatsPervBytes it should be enough , this one will only be used if signer can't find any previous UTXO , and fee info from local storage.
const (
	SatsPervBytes       = 25
	MinUTXOConfirmation = 10
)

func getBTCPrivateKey(key crypto.PrivKey) (*btcec.PrivateKey, error) {
	priKey, ok := key.(secp256k1.PrivKeySecp256k1)
	if !ok {
		return nil, errors.New("invalid private key type")
	}
	privateKey, _ := btcec.PrivKeyFromBytes(btcec.S256(), priKey[:])
	return privateKey, nil
}

func (c *Client) getChainCfg() *chaincfg.Params {
	cn := common.GetCurrentChainNetwork()
	switch cn {
	case common.MockNet:
		return &chaincfg.RegressionNetParams
	case common.TestNet:
		return &chaincfg.TestNet3Params
	case common.MainNet:
		return &chaincfg.MainNetParams
	}
	return nil
}

func (c *Client) getGasCoin(tx stypes.TxOutItem, vSize int64) common.Coin {
	if !tx.MaxGas.IsEmpty() {
		return tx.MaxGas.ToCoins().GetCoin(common.BTCAsset)
	}
	gasRate := int64(SatsPervBytes)
	fee, vBytes, err := c.blockMetaAccessor.GetTransactionFee()
	if err != nil {
		c.logger.Error().Err(err).Msg("fail to get previous transaction fee from local storage")
		return common.NewCoin(common.BTCAsset, sdk.NewUint(uint64(vSize*gasRate)))
	}
	if fee != 0.0 && vSize != 0 {
		amt, err := btcutil.NewAmount(fee)
		if err != nil {
			c.logger.Err(err).Msg("fail to convert amount from float64 to int64")
		} else {
			gasRate = int64(amt) / int64(vBytes) // sats per vbyte
		}
	}
	return common.NewCoin(common.BTCAsset, sdk.NewUint(uint64(gasRate*vSize)))
}

// getAllUtxos is going to spend all UTXOs in a block that might be evicted from local storage, on the top of that
// it also try to spend enough UTXOs that can add up to more than the given total
func (c *Client) getAllUtxos(height int64, pubKey common.PubKey, total float64) ([]UnspentTransactionOutput, error) {
	utxoes := make([]UnspentTransactionOutput, 0)
	stopHeight := height - MinUTXOConfirmation
	// as bifrost only keep the last BlockCacheSize(100) blocks , so it will need to consume all the utxos that is older than that.
	consumeAllHeight := height - BlockCacheSize + 1
	blockMetas, err := c.blockMetaAccessor.GetBlockMetas()
	if err != nil {
		return nil, fmt.Errorf("fail to get block metas: %w", err)
	}
	target := 0.0
	sort.SliceStable(blockMetas, func(i, j int) bool {
		return blockMetas[i].Height < blockMetas[j].Height
	})
	for _, b := range blockMetas {
		// not enough confirmations, skip it
		if b.Height > stopHeight {
			continue
		}

		// blocks that might be evicted from storage , so spent it all
		if b.Height <= consumeAllHeight || target < total {
			blockUtxoes := b.GetUTXOs(pubKey)
			for _, u := range blockUtxoes {
				target += u.Value
			}
			utxoes = append(utxoes, blockUtxoes...)
			continue
		}

		if target > total {
			return utxoes, nil
		}
	}
	return utxoes, nil
}

func (c *Client) getBlockHeight() (int64, error) {
	hash, err := c.client.GetBestBlockHash()
	if err != nil {
		return 0, fmt.Errorf("fail to get best block hash: %w", err)
	}
	blockInfo, err := c.client.GetBlockVerbose(hash)
	if err != nil {
		return 0, fmt.Errorf("fail to get the best block detail: %w", err)
	}

	return blockInfo.Height, nil
}

func (c *Client) getBTCPaymentAmount(tx stypes.TxOutItem) float64 {
	amtToPay := tx.Coins.GetCoin(common.BTCAsset).Amount.Uint64()
	amtToPayInBTC := btcutil.Amount(int64(amtToPay)).ToBTC()
	if !tx.MaxGas.IsEmpty() {
		gasAmt := tx.MaxGas.ToCoins().GetCoin(common.BTCAsset).Amount
		amtToPayInBTC += btcutil.Amount(int64(gasAmt.Uint64())).ToBTC()
	}
	return amtToPayInBTC
}

// SignTx is going to generate the outbound transaction, and also sign it
func (c *Client) SignTx(tx stypes.TxOutItem, thorchainHeight int64) ([]byte, error) {
	if !tx.Chain.Equals(common.BTCChain) {
		return nil, errors.New("not BTC chain")
	}
	sourceAddr, err := tx.VaultPubKey.GetAddress(common.BTCChain)
	if err != nil {
		return nil, fmt.Errorf("fail to get source address: %w", err)
	}

	addr, err := btcutil.DecodeAddress(sourceAddr.String(), c.getChainCfg())
	if err != nil {
		return nil, fmt.Errorf("fail to decode source address(%s): %w", sourceAddr.String(), err)
	}
	sourceScript, err := txscript.PayToAddrScript(addr)
	if err != nil {
		return nil, fmt.Errorf("fail to get source pay to address script: %w", err)
	}
	chainBlockHeight, err := c.getBlockHeight()
	if err != nil {
		return nil, fmt.Errorf("fail to get chain block height: %w", err)
	}
	txes, err := c.getAllUtxos(chainBlockHeight, tx.VaultPubKey, c.getBTCPaymentAmount(tx))
	if err != nil {
		return nil, fmt.Errorf("fail to get unspent UTXO")
	}
	redeemTx := wire.NewMsgTx(wire.TxVersion)
	totalAmt := float64(0)
	individualAmounts := make(map[chainhash.Hash]btcutil.Amount, len(txes))
	for _, item := range txes {
		// double check that the utxo is still valid
		outputPoint := wire.NewOutPoint(&item.TxID, item.N)
		sourceTxIn := wire.NewTxIn(outputPoint, nil, nil)
		redeemTx.AddTxIn(sourceTxIn)
		totalAmt += item.Value
		amt, err := btcutil.NewAmount(item.Value)
		if err != nil {
			return nil, fmt.Errorf("fail to parse amount(%f): %w", item.Value, err)
		}
		individualAmounts[item.TxID] = amt
	}

	outputAddr, err := btcutil.DecodeAddress(tx.ToAddress.String(), c.getChainCfg())
	if err != nil {
		return nil, fmt.Errorf("fail to decode next address: %w", err)
	}
	buf, err := txscript.PayToAddrScript(outputAddr)
	if err != nil {
		return nil, fmt.Errorf("fail to get pay to address script: %w", err)
	}

	total, err := btcutil.NewAmount(totalAmt)
	if err != nil {
		return nil, fmt.Errorf("fail to parse total amount(%f),err: %w", totalAmt, err)
	}
	vSize := mempool.GetTxVirtualSize(btcutil.NewTx(redeemTx))
	gasCoin := c.getGasCoin(tx, vSize)
	gasAmt := btcutil.Amount(int64(gasCoin.Amount.Uint64()))
	if err := c.blockMetaAccessor.UpsertTransactionFee(gasAmt.ToBTC(), int32(vSize)); err != nil {
		c.logger.Err(err).Msg("fail to save gas info to UTXO storage")
	}
	coinToCustomer := tx.Coins.GetCoin(common.BTCAsset)

	// pay to customer
	redeemTxOut := wire.NewTxOut(int64(coinToCustomer.Amount.Uint64()), buf)
	redeemTx.AddTxOut(redeemTxOut)

	if len(tx.Memo) != 0 {
		// memo
		nullDataScript, err := txscript.NullDataScript([]byte(tx.Memo))
		if err != nil {
			return nil, fmt.Errorf("fail to generate null data script: %w", err)
		}
		redeemTx.AddTxOut(wire.NewTxOut(0, nullDataScript))
	}
	// balance to ourselves
	// add output to pay the balance back ourselves
	balance := int64(total) - redeemTxOut.Value - int64(gasCoin.Amount.Uint64())
	if balance < 0 {
		return nil, errors.New("not enough balance to pay customer")
	}
	if balance > 0 {
		redeemTx.AddTxOut(wire.NewTxOut(balance, sourceScript))
	}
	txsort.InPlaceSort(redeemTx)
	for idx, txIn := range redeemTx.TxIn {
		sigHashes := txscript.NewTxSigHashes(redeemTx)
		sig := c.ksWrapper.GetSignable(tx.VaultPubKey)
		outputAmount := int64(individualAmounts[txIn.PreviousOutPoint.Hash])
		witness, err := txscript.WitnessSignature(redeemTx, sigHashes, idx, outputAmount, sourceScript, txscript.SigHashAll, sig, true)
		if err != nil {
			var keysignError tss.KeysignError
			if errors.As(err, &keysignError) {
				if len(keysignError.Blame.BlameNodes) == 0 {
					// TSS doesn't know which node to blame
					return nil, err
				}

				// key sign error forward the keysign blame to thorchain
				txID, err := c.bridge.PostKeysignFailure(keysignError.Blame, thorchainHeight, tx.Memo, tx.Coins)
				if err != nil {
					c.logger.Error().Err(err).Msg("fail to post keysign failure to thorchain")
					return nil, err
				} else {
					c.logger.Info().Str("tx_id", txID.String()).Msgf("post keysign failure to thorchain")
					return nil, fmt.Errorf("sent keysign failure to thorchain")
				}
			}
			return nil, fmt.Errorf("fail to get witness: %w", err)
		}

		redeemTx.TxIn[idx].Witness = witness
		flag := txscript.StandardVerifyFlags
		engine, err := txscript.NewEngine(sourceScript, redeemTx, idx, flag, nil, nil, outputAmount)
		if err != nil {
			return nil, fmt.Errorf("fail to create engine: %w", err)
		}
		if err := engine.Execute(); err != nil {
			return nil, fmt.Errorf("fail to execute the script: %w", err)
		}
	}

	var signedTx bytes.Buffer
	if err := redeemTx.Serialize(&signedTx); err != nil {
		return nil, fmt.Errorf("fail to serialize tx to bytes: %w", err)
	}
	// only send the balance back to ourselves
	if balance > 0 {
		if err := c.saveNewUTXO(redeemTx, balance, sourceScript, chainBlockHeight, tx.VaultPubKey); nil != err {
			return nil, fmt.Errorf("fail to save the new UTXO to storage: %w", err)
		}
	}
	if err := c.removeSpentUTXO(txes); err != nil {
		return nil, fmt.Errorf("fail to remove already spent transaction output: %w", err)
	}
	return signedTx.Bytes(), nil
}

func (c *Client) removeSpentUTXO(txs []UnspentTransactionOutput) error {
	for _, item := range txs {
		blockMeta, err := c.blockMetaAccessor.GetBlockMeta(item.BlockHeight)
		if err != nil {
			return fmt.Errorf("fail to get block meta: %w", err)
		}
		key := item.GetKey()
		blockMeta.RemoveUTXO(key)
		if err := c.blockMetaAccessor.SaveBlockMeta(blockMeta.Height, blockMeta); err != nil {
			return fmt.Errorf("fail to save block meta back to storage: %w", err)
		}
	}
	return nil
}

// saveUTXO save the newly created UTXO which transfer balance back our own address to storage
func (c *Client) saveNewUTXO(tx *wire.MsgTx, balance int64, script []byte, blockHeight int64, pubKey common.PubKey) error {
	txID := tx.TxHash()
	n := 0
	// find the position of output that we send balance back to ourselves
	for idx, item := range tx.TxOut {
		if item.Value == balance && bytes.Equal(script, item.PkScript) {
			n = idx
			break
		}
	}

	amt := btcutil.Amount(balance)
	blockMeta, err := c.blockMetaAccessor.GetBlockMeta(blockHeight)
	if err != nil {
		return fmt.Errorf("fail to get block meta: %w", err)
	}
	if blockMeta == nil {
		blockMeta = NewBlockMeta("", blockHeight, "")
	}
	blockMeta.AddUTXO(NewUnspentTransactionOutput(txID, uint32(n), amt.ToBTC(), blockHeight, pubKey))
	return c.blockMetaAccessor.SaveBlockMeta(blockHeight, blockMeta)
}

// BroadcastTx will broadcast the given payload to BTC chain
func (c *Client) BroadcastTx(txOut stypes.TxOutItem, payload []byte) error {
	redeemTx := wire.NewMsgTx(wire.TxVersion)
	buf := bytes.NewBuffer(payload)
	if err := redeemTx.Deserialize(buf); err != nil {
		return fmt.Errorf("fail to deserialize payload: %w", err)
	}
	txHash, err := c.client.SendRawTransaction(redeemTx, true)
	if err != nil {

		if rpcErr, ok := err.(*btcjson.RPCError); ok && rpcErr.Code == btcjson.ErrRPCTxAlreadyInChain {
			// this means the tx had been broadcast to chain, it must be another signer finished quicker then us
			return nil
		}
		return fmt.Errorf("fail to broadcast transaction to chain: %w", err)
	}
	c.logger.Info().Str("hash", txHash.String()).Msg("broadcast to BTC chain successfully")
	return nil
}
