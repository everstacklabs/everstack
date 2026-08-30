//go:build internal

package cmd

import (
	"github.com/everstacklabs/everstack/cmd/features"
	"github.com/spf13/cobra"
)

func registerInternalCommands(rootCmd *cobra.Command) {
	rootCmd.AddCommand(features.New())
}
