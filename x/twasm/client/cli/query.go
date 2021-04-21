package cli

import (
	"encoding/base64"
	"fmt"
	wasmcli "github.com/CosmWasm/wasmd/x/wasm/client/cli"
	"github.com/confio/tgrade/x/twasm/types"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/spf13/cobra"
	flag "github.com/spf13/pflag"
	"strings"
)

func GetQueryCmd() *cobra.Command {
	queryCmd := &cobra.Command{
		Use:                        types.ModuleName,
		Short:                      "Querying commands for the twasm module",
		DisableFlagParsing:         true,
		SuggestionsMinimumDistance: 2,
		RunE:                       client.ValidateCmd,
	}
	queryCmd.AddCommand(
		GetCmdShowCallbackContracts(),
		GetCmdListPrivilegedContracts(),

		// wasm
		wasmcli.GetCmdListCode(),
		wasmcli.GetCmdListContractByCode(),
		wasmcli.GetCmdQueryCode(),
		wasmcli.GetCmdGetContractInfo(),
		wasmcli.GetCmdGetContractHistory(),
		wasmcli.GetCmdGetContractState(),
	)
	return queryCmd
}

func GetCmdShowCallbackContracts() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "callback-contracts <callback_type>",
		Short: "List all contract addresses for given callback type",
		Long:  fmt.Sprintf("List all contracts for callback type [%s]", strings.Join(types.AllCallbackTypeNames(), ", ")),
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return err
			}

			cbt := types.PrivilegedCallbackTypeFrom(args[0])
			if cbt == nil {
				return fmt.Errorf("unknown callback type: %q", args[0])
			}

			queryClient := types.NewQueryClient(clientCtx)
			res, err := queryClient.ContractsByCallbackType(
				cmd.Context(),
				&types.QueryContractsByCallbackTypeRequest{
					CallbackType: cbt.String(),
				},
			)
			if err != nil {
				return err
			}
			return clientCtx.WithJSONMarshaler(&wasmcli.VanillaStdJsonMarshaller{}).PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

// GetCmdListPrivilegedContracts lists all privileged contracts
func GetCmdListPrivilegedContracts() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "privileged-contracts",
		Short: "List all privileged contract addresses",
		Long:  "List all contract addresses with privileged permission set",
		Args:  cobra.ExactArgs(0),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return err
			}

			queryClient := types.NewQueryClient(clientCtx)
			res, err := queryClient.PrivilegedContracts(
				cmd.Context(),
				&types.QueryPrivilegedContractsRequest{},
			)
			if err != nil {
				return err
			}
			return clientCtx.WithJSONMarshaler(&wasmcli.VanillaStdJsonMarshaller{}).PrintProto(res)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

// sdk ReadPageRequest expects binary but we encoded to base64 in our marshaller
func withPageKeyDecoded(flagSet *flag.FlagSet) *flag.FlagSet {
	encoded, err := flagSet.GetString(flags.FlagPageKey)
	if err != nil {
		panic(err.Error())
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		panic(err.Error())
	}
	flagSet.Set(flags.FlagPageKey, string(raw))
	return flagSet
}