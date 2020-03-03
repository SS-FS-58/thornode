package types

type RPCBlock struct {
	Jsonrpc string `json:"jsonrpc"`
	ID      string `json:"id"`
	Result  struct {
		Block struct {
			Header struct {
				Height string `json:"height"`
				NumTxs string `json:"num_txs"`
			} `json:"header"`
		} `json:"block"`
	} `json:"result"`
}
