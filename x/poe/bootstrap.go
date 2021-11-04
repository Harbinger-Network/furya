package poe

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/confio/tgrade/x/twasm"

	wasmtypes "github.com/CosmWasm/wasmd/x/wasm/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"

	"github.com/confio/tgrade/x/poe/contract"
	"github.com/confio/tgrade/x/poe/keeper"
	"github.com/confio/tgrade/x/poe/types"
)

var (
	//go:embed contract/tg4_engagement.wasm
	tg4Engagement []byte
	//go:embed contract/tg4_stake.wasm
	tg4Stake []byte
	//go:embed contract/tg4_mixer.wasm
	tg4Mixer []byte
	//go:embed contract/tgrade_valset.wasm
	tgValset []byte
	//go:embed contract/tgrade_trusted_circle.wasm
	tgTrustedCircles []byte
	//go:embed contract/tgrade_oc_proposals.wasm
	tgOCGovProposalsCircles []byte
	//go:embed contract/version.txt
	contractVersion []byte
)

// ClearEmbeddedContracts release memory
func ClearEmbeddedContracts() {
	tg4Engagement = nil
	tg4Stake = nil
	tg4Mixer = nil
	tgValset = nil
	tgTrustedCircles = nil
}

type poeKeeper interface {
	keeper.ContractSource
	SetPoEContractAddress(ctx sdk.Context, ctype types.PoEContractType, contractAddr sdk.AccAddress)
	ValsetContract(ctx sdk.Context) keeper.ValsetContract
}

// bootstrapPoEContracts stores and instantiates all PoE contracts:
//
// * [tg4-group](https://github.com/confio/tgrade-contracts/tree/main/contracts/tg4-group) - engagement group with weighted
//  members
// * [tg4-stake](https://github.com/confio/tgrade-contracts/tree/main/contracts/tg4-stake) - validator group weighted by
//  staked amount
// * [valset](https://github.com/confio/tgrade-contracts/tree/main/contracts/tgrade-valset) - privileged contract to map a
//  trusted cw4 contract to the Tendermint validator set running the chain
// * [mixer](https://github.com/confio/tgrade-contracts/tree/main/contracts/tg4-mixer) - calculates the combined value of
//  stake and engagement points. Source for the valset contract.
// * [trusted circle](https://github.com/confio/tgrade-contracts/tree/main/contracts/tgrade-trusted-circle) - oversight community
func bootstrapPoEContracts(ctx sdk.Context, k wasmtypes.ContractOpsKeeper, tk twasmKeeper, poeKeeper poeKeeper, gs types.GenesisState) error {
	systemAdminAddr, err := sdk.AccAddressFromBech32(gs.SystemAdminAddress)
	if err != nil {
		return sdkerrors.Wrap(err, "system admin")
	}

	// precalculate contract address for bootstrap without updates
	expOCGovProposalContractAddr := twasm.ContractAddress(3, 3)

	// setup engagement contract
	tg4EngagementInitMsg := newEngagementInitMsg(gs, expOCGovProposalContractAddr)
	engagementCodeID, err := k.Create(ctx, systemAdminAddr, tg4Engagement, &wasmtypes.AllowEverybody)
	if err != nil {
		return sdkerrors.Wrap(err, "store tg4 engagement contract")
	}
	engagementContractAddr, _, err := k.Instantiate(ctx, engagementCodeID, systemAdminAddr, systemAdminAddr, mustMarshalJson(tg4EngagementInitMsg), "engagement", nil)
	if err != nil {
		return sdkerrors.Wrap(err, "instantiate tg4 engagement")
	}
	poeKeeper.SetPoEContractAddress(ctx, types.PoEContractTypeEngagement, engagementContractAddr)
	if err := k.PinCode(ctx, engagementCodeID); err != nil {
		return sdkerrors.Wrap(err, "pin tg4 engagement contract")
	}
	logger := keeper.ModuleLogger(ctx)
	logger.Info("engagement group contract", "address", engagementContractAddr, "code_id", engagementCodeID)

	// setup trusted circle for oversight community
	ocCodeID, err := k.Create(ctx, systemAdminAddr, tgTrustedCircles, &wasmtypes.AllowEverybody)
	if err != nil {
		return sdkerrors.Wrap(err, "store tg trusted circle contract")
	}
	ocInitMsg := newOCInitMsg(gs)
	deposit := sdk.NewCoins(gs.OversightCommitteeContractConfig.EscrowAmount)
	ocContractAddr, _, err := k.Instantiate(ctx, ocCodeID, systemAdminAddr, systemAdminAddr, mustMarshalJson(ocInitMsg), "oversight_committee", deposit)
	if err != nil {
		return sdkerrors.Wrap(err, "instantiate tg trusted circle contract")
	}
	poeKeeper.SetPoEContractAddress(ctx, types.PoEContractTypeOversightCommunity, ocContractAddr)
	if err := k.PinCode(ctx, ocCodeID); err != nil {
		return sdkerrors.Wrap(err, "pin tg trusted circle contract")
	}
	logger.Info("oversight community contract", "address", ocContractAddr, "code_id", ocCodeID)

	// setup oversight community gov proposals contract
	ocGovCodeID, err := k.Create(ctx, systemAdminAddr, tgOCGovProposalsCircles, &wasmtypes.AllowEverybody)
	if err != nil {
		return sdkerrors.Wrap(err, "store tg oc gov proposals contract: ")
	}
	ocGovInitMsg := newOCGovProposalsInitMsg(gs, ocContractAddr, engagementContractAddr)
	ocGovProposalsContractAddr, _, err := k.Instantiate(ctx, ocGovCodeID, systemAdminAddr, systemAdminAddr, mustMarshalJson(ocGovInitMsg), "oversight_committee gov proposals", deposit)
	if err != nil {
		return sdkerrors.Wrap(err, "instantiate tg oc gov proposals contract")
	}
	poeKeeper.SetPoEContractAddress(ctx, types.PoEContractTypeOversightCommunityGovProposals, ocGovProposalsContractAddr)
	if err := k.PinCode(ctx, ocGovCodeID); err != nil {
		return sdkerrors.Wrap(err, "pin tg oc gov proposals contract")
	}
	logger.Info("oversight community gov proposal contract", "address", ocGovProposalsContractAddr, "code_id", ocGovCodeID)
	if !expOCGovProposalContractAddr.Equals(ocGovProposalsContractAddr) { // sanity check
		return sdkerrors.Wrapf(types.ErrInvalid, "calculated gov proposal contract address does not match instance: %s", expOCGovProposalContractAddr)
	}

	// setup stake contract
	stakeCodeID, err := k.Create(ctx, systemAdminAddr, tg4Stake, &wasmtypes.AllowEverybody)
	if err != nil {
		return sdkerrors.Wrap(err, "store tg4 stake contract")
	}
	tg4StakeInitMsg := newStakeInitMsg(gs, systemAdminAddr)
	stakeContractAddr, _, err := k.Instantiate(ctx, stakeCodeID, systemAdminAddr, systemAdminAddr, mustMarshalJson(tg4StakeInitMsg), "stakers", nil)
	if err != nil {
		return sdkerrors.Wrap(err, "instantiate tg4 stake")
	}
	poeKeeper.SetPoEContractAddress(ctx, types.PoEContractTypeStaking, stakeContractAddr)
	if err := tk.SetPrivileged(ctx, stakeContractAddr); err != nil {
		return sdkerrors.Wrap(err, "grant privileges to stake contract")
	}
	logger.Info("stake contract", "address", stakeContractAddr, "code_id", stakeCodeID)

	// setup mixer contract
	tg4MixerInitMsg := contract.TG4MixerInitMsg{
		LeftGroup:  engagementContractAddr.String(),
		RightGroup: stakeContractAddr.String(),
		// TODO: allow to configure the other types.
		// We need to analyze benchmarks and discuss first.
		// This maintains same behavior
		FunctionType: contract.MixerFunction{
			GeometricMean: &struct{}{},
		},
	}
	mixerCodeID, err := k.Create(ctx, systemAdminAddr, tg4Mixer, &wasmtypes.AllowEverybody)
	if err != nil {
		return sdkerrors.Wrap(err, "store tg4 mixer contract")
	}
	mixerContractAddr, _, err := k.Instantiate(ctx, mixerCodeID, systemAdminAddr, systemAdminAddr, mustMarshalJson(tg4MixerInitMsg), "poe", nil)
	if err != nil {
		return sdkerrors.Wrap(err, "instantiate tg4 mixer")
	}
	poeKeeper.SetPoEContractAddress(ctx, types.PoEContractTypeMixer, mixerContractAddr)
	if err := k.PinCode(ctx, mixerCodeID); err != nil {
		return sdkerrors.Wrap(err, "pin tg4 mixer contract")
	}
	logger.Info("mixer contract", "address", mixerContractAddr, "code_id", mixerCodeID)

	// setup valset contract
	valSetCodeID, err := k.Create(ctx, systemAdminAddr, tgValset, &wasmtypes.AllowEverybody)
	if err != nil {
		return sdkerrors.Wrap(err, "store valset contract")
	}

	valsetInitMsg := newValsetInitMsg(gs, mixerContractAddr, engagementContractAddr, engagementCodeID)
	valsetJSON := mustMarshalJson(valsetInitMsg)
	valsetContractAddr, _, err := k.Instantiate(ctx, valSetCodeID, systemAdminAddr, systemAdminAddr, valsetJSON, "valset", nil)
	if err != nil {
		return sdkerrors.Wrap(err, "instantiate valset")
	}
	poeKeeper.SetPoEContractAddress(ctx, types.PoEContractTypeValset, valsetContractAddr)

	// setup distribution contract address
	valsetCfg, err := poeKeeper.ValsetContract(ctx).QueryConfig(ctx)
	if err != nil {
		return sdkerrors.Wrap(err, "query valset config")
	}

	distrAddr, err := sdk.AccAddressFromBech32(valsetCfg.DistributionContract)
	if err != nil {
		return sdkerrors.Wrap(err, "distribution contract address")
	}
	poeKeeper.SetPoEContractAddress(ctx, types.PoEContractTypeDistribution, distrAddr)

	if err := tk.SetPrivileged(ctx, valsetContractAddr); err != nil {
		return sdkerrors.Wrap(err, "grant privileges to valset contract")
	}
	logger.Info("valset contract", "address", valsetContractAddr, "code_id", valSetCodeID)
	return nil
}

// build instantiate message for the trusted circle contract that contains the oversight committee
func newOCInitMsg(gs types.GenesisState) contract.TrustedCircleInitMsg {
	cfg := gs.OversightCommitteeContractConfig
	return contract.TrustedCircleInitMsg{
		Name:                      cfg.Name,
		EscrowAmount:              cfg.EscrowAmount.Amount,
		VotingPeriod:              cfg.VotingPeriod,
		Quorum:                    *contract.DecimalFromPercentage(cfg.Quorum),
		Threshold:                 *contract.DecimalFromPercentage(cfg.Threshold),
		AllowEndEarly:             cfg.AllowEndEarly,
		InitialMembers:            []string{}, // no non voting members
		DenyList:                  cfg.DenyListContractAddress,
		EditTrustedCircleDisabled: true, // product requirement for OC
	}
}

// build instantiate message for OC Proposals contract
func newOCGovProposalsInitMsg(gs types.GenesisState, ocContract, engagementContract sdk.AccAddress) contract.OCProposalsInitMsg {
	cfg := gs.OversightCommitteeContractConfig
	return contract.OCProposalsInitMsg{
		GroupContractAddress:     ocContract.String(),
		EngagemenContractAddress: engagementContract.String(),
		VotingRules: contract.VotingRules{
			VotingPeriod:  cfg.VotingPeriod,
			Quorum:        *contract.DecimalFromPercentage(cfg.Quorum),
			Threshold:     *contract.DecimalFromPercentage(cfg.Threshold),
			AllowEndEarly: cfg.AllowEndEarly,
		},
	}
}

func newEngagementInitMsg(gs types.GenesisState, adminAddr sdk.AccAddress) contract.TG4EngagementInitMsg {
	tg4EngagementInitMsg := contract.TG4EngagementInitMsg{
		Admin:            adminAddr.String(),
		Members:          make([]contract.TG4Member, len(gs.Engagement)),
		PreAuthsHooks:    1,
		PreAuthsSlashing: 1,
		Token:            gs.BondDenom,
		Halflife:         uint64(gs.EngagmentContractConfig.Halflife.Seconds()),
	}
	for i, v := range gs.Engagement {
		tg4EngagementInitMsg.Members[i] = contract.TG4Member{
			Addr:   v.Address,
			Weight: v.Weight,
		}
	}
	return tg4EngagementInitMsg
}

func newStakeInitMsg(gs types.GenesisState, adminAddr sdk.AccAddress) contract.TG4StakeInitMsg {
	var claimLimit = uint64(gs.StakeContractConfig.ClaimAutoreturnLimit)
	return contract.TG4StakeInitMsg{
		Admin:            adminAddr.String(),
		Denom:            gs.BondDenom,
		MinBond:          gs.StakeContractConfig.MinBond,
		TokensPerWeight:  gs.StakeContractConfig.TokensPerWeight,
		UnbondingPeriod:  uint64(gs.StakeContractConfig.UnbondingPeriod.Seconds()),
		AutoReturnLimit:  &claimLimit,
		PreAuthsHooks:    1,
		PreAuthsSlashing: 1,
	}
}

func newValsetInitMsg(
	gs types.GenesisState,
	mixerContractAddr sdk.AccAddress,
	engagementAddr sdk.AccAddress,
	engagementCodeID uint64,
) contract.ValsetInitMsg {
	return contract.ValsetInitMsg{
		Membership:            mixerContractAddr.String(),
		MinWeight:             gs.ValsetContractConfig.MinWeight,
		MaxValidators:         gs.ValsetContractConfig.MaxValidators,
		EpochLength:           uint64(gs.ValsetContractConfig.EpochLength.Seconds()),
		EpochReward:           gs.ValsetContractConfig.EpochReward,
		InitialKeys:           []contract.Validator{},
		Scaling:               gs.ValsetContractConfig.Scaling,
		FeePercentage:         contract.DecimalFromPercentage(gs.ValsetContractConfig.FeePercentage),
		AutoUnjail:            gs.ValsetContractConfig.AutoUnjail,
		ValidatorsRewardRatio: contract.DecimalFromPercentage(sdk.NewDec(int64(gs.ValsetContractConfig.ValidatorsRewardRatio))),
		DistributionContract:  engagementAddr.String(),
		RewardsCodeID:         engagementCodeID,
	}
}

// verifyPoEContracts verifies all PoE contracts are setup as expected
func verifyPoEContracts(ctx sdk.Context, k wasmtypes.ContractOpsKeeper, tk twasmKeeper, poeKeeper poeKeeper, gs types.GenesisState) error {
	return errors.New("not supported, yet")
	// all poe contracts pinned
	// valset privileged
	// valset has registered for endblock valset update privilege
	// admin set matches genesis system admin address for engagement and staking contract
}

// mustMarshalJson with stdlib json
func mustMarshalJson(s interface{}) []byte {
	jsonBz, err := json.Marshal(s)
	if err != nil {
		panic(fmt.Sprintf("failed to marshal json: %s", err))
	}
	return jsonBz
}
