package rest

import (
	"fmt"
	"net/http"

	"github.com/cosmos/cosmos-sdk/client/context"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/rest"
	"github.com/cosmos/cosmos-sdk/x/auth/client/utils"

	"github.com/jpthor/cosmos-swap/x/swapservice/types"

	"github.com/gorilla/mux"
)

const (
	restPoolStruct = "poolstruct"
	restPoolData   = "pooldata"
	swapData       = "swapdata"
	stakeData      = "stakedata"
	accData        = "accdata"
)

// TODO add the new features to Restful routes
// pool staker , staker pool etc
// pool index etc
// RegisterRoutes - Central function to define routes that get registered by the main application
func RegisterRoutes(cliCtx context.CLIContext, r *mux.Router, storeName string) {
	r.HandleFunc(fmt.Sprintf("/%s/pool/{%s}", storeName, restPoolStruct), poolStructHandler(cliCtx, storeName)).Methods("GET")
	r.HandleFunc(fmt.Sprintf("/%s/pools", storeName), poolHandler(cliCtx, storeName)).Methods("GET")
	r.HandleFunc(fmt.Sprintf("/%s/account/{%s}", storeName, accData), accHandler(cliCtx, storeName)).Methods("GET")
	r.HandleFunc(fmt.Sprintf("/%s/stake/{%s}", storeName, stakeData), stakeHandler(cliCtx, storeName)).Methods("GET")
	r.HandleFunc(fmt.Sprintf("/%s/swaprecord/{%s}", storeName, swapData), swapRecordHandler(cliCtx, storeName)).Methods("GET")
	r.HandleFunc(fmt.Sprintf("/%s/unstakerecord/{%s}", storeName, swapData), unStakeRecordHandler(cliCtx, storePoolData)).Methods("GET")
	r.HandleFunc(fmt.Sprintf("/%s/stake", storeName), setStakeDataHandler(cliCtx)).Methods("PUT")
}

type setStakeData struct {
	BaseReq       rest.BaseReq `json:"base_req"`
	Name          string       `json:"name"`
	Ticker        string       `json:"ticker"`
	Rune          string       `json:"rune_amount"`
	Token         string       `json:"token_amount"`
	PublicAddress string       `json:"public_address"`
	RequestTxHash string       `json:"request_tx_hash"`
}

func setStakeDataHandler(cliCtx context.CLIContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var err error

		var req setStakeData
		if !rest.ReadRESTReq(w, r, cliCtx.Codec, &req) {
			rest.WriteErrorResponse(w, http.StatusBadRequest, "failed to parse request")
			return
		}

		baseReq := req.BaseReq.Sanitize()
		if !baseReq.ValidateBasic(w) {
			return
		}

		// create the message
		msg := types.NewMsgSetStakeData(req.Name, req.Ticker, req.Rune, req.Token, req.PublicAddress, req.RequestTxHash, cliCtx.GetFromAddress())
		err = msg.ValidateBasic()
		if err != nil {
			rest.WriteErrorResponse(w, http.StatusBadRequest, err.Error())
			return
		}

		utils.WriteGenerateStdTxResponse(w, cliCtx, baseReq, []sdk.Msg{msg})
	}
}

func poolStructHandler(cliCtx context.CLIContext, storePoolData string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		paramType := vars[restPoolData]
		res, _, err := cliCtx.QueryWithData(fmt.Sprintf("custom/%s/poolstruct/%s", storePoolData, paramType), nil)
		if err != nil {
			rest.WriteErrorResponse(w, http.StatusNotFound, err.Error())
			return
		}

		rest.PostProcessResponse(w, cliCtx, res)
	}
}

func poolHandler(cliCtx context.CLIContext, storePoolData string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		res, _, err := cliCtx.QueryWithData(fmt.Sprintf("custom/%s/pooldatas", storePoolData), nil)
		if err != nil {
			rest.WriteErrorResponse(w, http.StatusNotFound, err.Error())
			return
		}
		rest.PostProcessResponse(w, cliCtx, res)
	}
}

func accHandler(cliCtx context.CLIContext, storePoolData string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		paramType := vars[accData]
		res, _, err := cliCtx.QueryWithData(fmt.Sprintf("custom/%s/accstruct/%s", storePoolData, paramType), nil)
		if err != nil {
			rest.WriteErrorResponse(w, http.StatusNotFound, err.Error())
			return
		}
		rest.PostProcessResponse(w, cliCtx, res)
	}
}

func stakeHandler(cliCtx context.CLIContext, storePoolData string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		paramType := vars[stakeData]
		res, _, err := cliCtx.QueryWithData(fmt.Sprintf("custom/%s/stakestruct/%s", storePoolData, paramType), nil)
		if err != nil {
			rest.WriteErrorResponse(w, http.StatusNotFound, err.Error())
			return
		}
		rest.PostProcessResponse(w, cliCtx, res)
	}
}
func swapRecordHandler(cliCtx context.CLIContext, storePoolData string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		paramType := vars[swapData]
		res, _, err := cliCtx.QueryWithData(fmt.Sprintf("custom/%s/swaprecord/%s", storePoolData, paramType), nil)
		if err != nil {
			rest.WriteErrorResponse(w, http.StatusNotFound, err.Error())
			return
		}
		rest.PostProcessResponse(w, cliCtx, res)
	}
}

func unStakeRecordHandler(cliCtx context.CLIContext, storePoolData string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		paramType := vars[swapData]
		res, _, err := cliCtx.QueryWithData(fmt.Sprintf("custom/%s/unstakerecord/%s", storePoolData, paramType), nil)
		if err != nil {
			rest.WriteErrorResponse(w, http.StatusNotFound, err.Error())
			return
		}
		rest.PostProcessResponse(w, cliCtx, res)
	}
}
