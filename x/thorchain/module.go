package thorchain

import (
	"encoding/json"
	"fmt"

	"github.com/cosmos/cosmos-sdk/client/context"
	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module"
	sdkRest "github.com/cosmos/cosmos-sdk/x/auth/client/rest"
	"github.com/cosmos/cosmos-sdk/x/bank"
	"github.com/cosmos/cosmos-sdk/x/supply"
	"github.com/gorilla/mux"
	"github.com/spf13/cobra"
	abci "github.com/tendermint/tendermint/abci/types"

	"gitlab.com/thorchain/thornode/constants"

	"gitlab.com/thorchain/thornode/x/thorchain/client/cli"
	"gitlab.com/thorchain/thornode/x/thorchain/client/rest"
)

// type check to ensure the interface is properly implemented
var (
	_ module.AppModule      = AppModule{}
	_ module.AppModuleBasic = AppModuleBasic{}
)

// app module Basics object
type AppModuleBasic struct{}

func (AppModuleBasic) Name() string {
	return ModuleName
}

func (AppModuleBasic) RegisterCodec(cdc *codec.Codec) {
	RegisterCodec(cdc)
}

func (AppModuleBasic) DefaultGenesis() json.RawMessage {
	return ModuleCdc.MustMarshalJSON(DefaultGenesisState())
}

// Validation check of the Genesis
func (AppModuleBasic) ValidateGenesis(bz json.RawMessage) error {
	var data GenesisState
	err := ModuleCdc.UnmarshalJSON(bz, &data)
	if err != nil {
		return err
	}
	// Once json successfully marshalled, passes along to genesis.go
	return ValidateGenesis(data)
}

// Register rest routes
func (AppModuleBasic) RegisterRESTRoutes(ctx context.CLIContext, rtr *mux.Router) {
	rest.RegisterRoutes(ctx, rtr, StoreKey)
	sdkRest.RegisterTxRoutes(ctx, rtr)
}

// Get the root query command of this module
func (AppModuleBasic) GetQueryCmd(cdc *codec.Codec) *cobra.Command {
	return cli.GetQueryCmd(StoreKey, cdc)
}

// Get the root tx command of this module
func (AppModuleBasic) GetTxCmd(cdc *codec.Codec) *cobra.Command {
	return cli.GetTxCmd(StoreKey, cdc)
}

type AppModule struct {
	AppModuleBasic
	keeper                Keeper
	coinKeeper            bank.Keeper
	supplyKeeper          supply.Keeper
	txOutStore            VersionedTxOutStore
	validatorMgr          VersionedValidatorManager
	versionedVaultManager VersionedVaultManager
}

// NewAppModule creates a new AppModule Object
func NewAppModule(k Keeper, bankKeeper bank.Keeper, supplyKeeper supply.Keeper) AppModule {
	versionedTxOutStore := NewVersionedTxOutStore()
	versionedVaultMgr := NewVersionedVaultMgr(versionedTxOutStore)
	return AppModule{
		AppModuleBasic:        AppModuleBasic{},
		keeper:                k,
		coinKeeper:            bankKeeper,
		supplyKeeper:          supplyKeeper,
		txOutStore:            versionedTxOutStore,
		validatorMgr:          NewVersionedValidatorMgr(k, versionedTxOutStore, versionedVaultMgr),
		versionedVaultManager: versionedVaultMgr,
	}
}

func (AppModule) Name() string {
	return ModuleName
}

func (am AppModule) RegisterInvariants(ir sdk.InvariantRegistry) {}

func (am AppModule) Route() string {
	return RouterKey
}

func (am AppModule) NewHandler() sdk.Handler {
	return NewHandler(am.keeper, am.txOutStore, am.validatorMgr, am.versionedVaultManager)
}

func (am AppModule) QuerierRoute() string {
	return ModuleName
}

func (am AppModule) NewQuerierHandler() sdk.Querier {
	return NewQuerier(am.keeper, am.validatorMgr)
}

func (am AppModule) BeginBlock(ctx sdk.Context, req abci.RequestBeginBlock) {
	ctx.Logger().Debug("Begin Block", "height", req.Header.Height)

	version := am.keeper.GetLowestActiveVersion(ctx)
	constantValues := constants.GetConstantValues(version)
	if nil == constantValues {
		ctx.Logger().Error(fmt.Sprintf("constants for version(%s) is not available", version))
		return
	}
	if err := am.validatorMgr.BeginBlock(ctx, version, constantValues); err != nil {
		ctx.Logger().Error("Fail to begin block on validator", "error", err)
	}
	txStore, err := am.txOutStore.GetTxOutStore(am.keeper, version)
	if err != nil {
		ctx.Logger().Error("fail to get tx out store", "error", err)
		return
	}
	txStore.NewBlock(req.Header.Height, constantValues)
}

func (am AppModule) EndBlock(ctx sdk.Context, req abci.RequestEndBlock) []abci.ValidatorUpdate {
	ctx.Logger().Debug("End Block", "height", req.Height)

	version := am.keeper.GetLowestActiveVersion(ctx)
	constantValues := constants.GetConstantValues(version)
	if nil == constantValues {
		ctx.Logger().Error(fmt.Sprintf("constants for version(%s) is not available", version))
		return nil
	}
	txStore, err := am.txOutStore.GetTxOutStore(am.keeper, version)
	if err != nil {
		ctx.Logger().Error("fail to get tx out store", "error", err)
		return nil
	}
	slasher := NewSlasher(am.keeper, txStore)
	// slash node accounts for not observing any accepted inbound tx
	if err := slasher.LackObserving(ctx, constantValues); err != nil {
		ctx.Logger().Error("Unable to slash for lack of observing:", "error", err)
	}
	if err := slasher.LackSigning(ctx, constantValues); err != nil {
		ctx.Logger().Error("Unable to slash for lack of signing:", "error", err)
	}
	newPoolCycle := constantValues.GetInt64Value(constants.NewPoolCycle)
	// Enable a pool every newPoolCycle
	if ctx.BlockHeight()%newPoolCycle == 0 {
		if err := enableNextPool(ctx, am.keeper); err != nil {
			ctx.Logger().Error("Unable to enable a pool", "error", err)
		}
	}

	// Fill up Yggdrasil vaults
	err = Fund(ctx, am.keeper, txStore, constantValues)
	if err != nil {
		ctx.Logger().Error("Unable to fund Yggdrasil", "error", err)
	}

	// update vault data to account for block rewards and reward units
	if err := am.keeper.UpdateVaultData(ctx, constantValues); err != nil {
		ctx.Logger().Error("fail to save vault", "error", err)
	}
	vaultMgr, err := am.versionedVaultManager.GetVaultManager(ctx, am.keeper, version)
	if err != nil {
		ctx.Logger().Error("fail to get a valid vault manager", "error", err)
		return nil
	}

	if err := vaultMgr.EndBlock(ctx, version, constantValues); err != nil {
		ctx.Logger().Error("fail to end block for vault manager", "error", err)
	}

	return am.validatorMgr.EndBlock(ctx, version, constantValues)
}

func (am AppModule) InitGenesis(ctx sdk.Context, data json.RawMessage) []abci.ValidatorUpdate {
	var genesisState GenesisState
	ModuleCdc.MustUnmarshalJSON(data, &genesisState)
	return InitGenesis(ctx, am.keeper, genesisState)
}

func (am AppModule) ExportGenesis(ctx sdk.Context) json.RawMessage {
	gs := ExportGenesis(ctx, am.keeper)
	return ModuleCdc.MustMarshalJSON(gs)
}
