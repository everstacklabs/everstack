//go:build !internal

package cmd

import "github.com/spf13/cobra"

func registerInternalCommands(_ *cobra.Command) {
	// No internal commands in production builds.
}
