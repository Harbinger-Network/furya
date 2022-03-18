package poe_test

import (
	"encoding/json"
	"sort"
	"testing"

	wasmtypes "github.com/CosmWasm/wasmd/x/wasm/types"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	fuzz "github.com/google/gofuzz"
	abci "github.com/tendermint/tendermint/abci/types"

	"github.com/confio/tgrade/x/poe/keeper"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/confio/tgrade/x/poe"
	"github.com/confio/tgrade/x/poe/contract"
	"github.com/confio/tgrade/x/poe/types"
)

func TestIntegrationBootstrapPoEContracts(t *testing.T) {
	// setup contracts and seed some data
	ctx, example, _, members := setupPoEContracts(t)

	ocContractAddr, err := example.PoEKeeper.GetPoEContractAddress(ctx, types.PoEContractTypeOversightCommunity)
	require.NoError(t, err)
	tcAdapter := contract.NewTrustedCircleContractAdapter(ocContractAddr, example.TWasmKeeper, nil)

	voters, err := tcAdapter.QueryListVoters(ctx)
	require.NoError(t, err)

	//verify the members list are all voting members
	notVotingMembers := make(map[string]struct{}, len(members))
	for _, m := range members {
		notVotingMembers[m] = struct{}{}
	}

	for _, v := range voters.Members {
		delete(notVotingMembers, v.Addr)
	}
	assert.Empty(t, notVotingMembers)
}

func setupPoEContracts(t *testing.T, mutators ...func(m *types.GenesisState)) (sdk.Context, keeper.TestKeepers, []stakingtypes.Validator, []string) {
	t.Helper()
	ctx, example := keeper.CreateDefaultTestInput(t)
	deliverTXFn := unAuthorizedDeliverTXFn(t, ctx, example.PoEKeeper, example.TWasmKeeper.GetContractKeeper(), example.EncodingConfig.TxConfig.TxDecoder())
	module := poe.NewAppModule(example.PoEKeeper, example.TWasmKeeper, deliverTXFn, example.EncodingConfig.TxConfig, example.TWasmKeeper.GetContractKeeper())

	mutator, expValidators := withRandomValidators(t, ctx, example, 3)
	gs := types.GenesisStateFixture(append([]func(m *types.GenesisState){mutator}, mutators...)...)
	adminAddress, _ := sdk.AccAddressFromBech32(gs.SystemAdminAddress)
	example.Faucet.Fund(ctx, adminAddress, sdk.NewCoin(types.DefaultBondDenom, sdk.NewInt(100_000_000_000)))

	for _, member := range gs.OversightCommunityMembers {
		addr, err := sdk.AccAddressFromBech32(member)
		require.NoError(t, err)
		example.Faucet.Fund(ctx, addr, sdk.NewCoin(types.DefaultBondDenom, sdk.NewInt(1_000_000)))
	}

	genesisBz := example.EncodingConfig.Marshaler.MustMarshalJSON(&gs)
	module.InitGenesis(ctx, example.EncodingConfig.Marshaler, genesisBz)
	return ctx, example, expValidators, gs.OversightCommunityMembers
}

// unAuthorizedDeliverTXFn applies the TX without ante handler checks for testing purpose
func unAuthorizedDeliverTXFn(t *testing.T, ctx sdk.Context, k keeper.Keeper, contractKeeper wasmtypes.ContractOpsKeeper, txDecoder sdk.TxDecoder) func(tx abci.RequestDeliverTx) abci.ResponseDeliverTx {
	t.Helper()
	h := poe.NewHandler(k, contractKeeper, nil)
	return func(tx abci.RequestDeliverTx) abci.ResponseDeliverTx {
		genTx, err := txDecoder(tx.GetTx())
		require.NoError(t, err)
		msgs := genTx.GetMsgs()
		require.Len(t, msgs, 1)
		msg := msgs[0].(*types.MsgCreateValidator)
		_, err = h(ctx, msg)
		require.NoError(t, err)
		t.Logf("+++ create validator: %s\n", msg.OperatorAddress)
		return abci.ResponseDeliverTx{}
	}
}

// return genesis mutator that adds the given number of validators to the genesis
func withRandomValidators(t *testing.T, ctx sdk.Context, example keeper.TestKeepers, numValidators int) (func(m *types.GenesisState), []stakingtypes.Validator) {
	collectValidators := make([]stakingtypes.Validator, numValidators)
	return func(m *types.GenesisState) {
		f := fuzz.New()
		m.GenTxs = make([]json.RawMessage, numValidators)
		m.Engagement = make([]types.TG4Member, numValidators)
		for i := 0; i < numValidators; i++ {
			var ( // power * engagement must be less than 10^18 (constraint is in the contract)
				desc stakingtypes.Description
			)
			power := i*75 + 100 // with 3 nodes : 525 total power: 1+2 power < 350 consensus
			engagement := i*100 + 1000

			for len(desc.Moniker) < 3 { // ensure min length is met
				f.Fuzz(&desc)
			}
			desc.Website = "https://" + desc.Website

			genTx, opAddr, pubKey := types.RandomGenTX(t, uint32(power), func(m *types.MsgCreateValidator) {
				m.Description = desc
			})
			any, err := codectypes.NewAnyWithValue(pubKey)
			require.NoError(t, err)
			stakedAmount := sdk.TokensFromConsensusPower(int64(power), sdk.DefaultPowerReduction)
			collectValidators[i] = types.ValidatorFixture(func(m *stakingtypes.Validator) {
				m.OperatorAddress = opAddr.String()
				m.ConsensusPubkey = any
				m.Description = desc
				m.Tokens = stakedAmount
				m.DelegatorShares = sdk.OneDec()
			})

			m.GenTxs[i] = genTx
			m.Engagement[i] = types.TG4Member{Address: opAddr.String(), Points: uint64(engagement)}
			example.AccountKeeper.NewAccountWithAddress(ctx, opAddr)
			example.Faucet.Fund(ctx, opAddr, sdk.NewCoin(types.DefaultBondDenom, stakedAmount))
		}
		sort.Slice(collectValidators, func(i, j int) bool {
			return collectValidators[i].Tokens.LT(collectValidators[j].Tokens) // sort ASC
		})
	}, collectValidators
}