// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

func NewRootCommand() *cobra.Command {
	rootCmd, extCtx := azdext.NewExtensionRootCommand(azdext.ExtensionCommandOptions{
		Name:  "dream",
		Use:   "dream <command> [options]",
		Short: "Save, list, load, and interpret dreams.",
	})

	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	rootCmd.CompletionOptions = cobra.CompletionOptions{
		DisableDefaultCmd: true,
	}

	rootCmd.SetHelpCommand(&cobra.Command{Hidden: true})

	rootCmd.AddCommand(newDreamSaveCommand(extCtx))
	rootCmd.AddCommand(newDreamListCommand(extCtx))
	rootCmd.AddCommand(newDreamLoadCommand(extCtx))
	rootCmd.AddCommand(newDreamInterpretCommand(extCtx))
	rootCmd.AddCommand(newVersionCommand(&extCtx.OutputFormat))
	rootCmd.AddCommand(newMetadataCommand(rootCmd))

	return rootCmd
}
