package version

import (
	"fmt"
	"time"

	"github.com/everstacklabs/everstack/cmd/build"
	"github.com/spf13/cobra"
)

// New creates the `version` subcommand that prints version information.
func New() *cobra.Command {
	var verbose bool

	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		RunE: func(cmd *cobra.Command, args []string) error {
			w := cmd.OutOrStdout()
			if verbose {
				date := build.Date()
				fmt.Fprintf(w, "Version\t: %s\n", build.Version())
				fmt.Fprintf(w, "Commit\t: %s\n", build.Commit())
				fmt.Fprintf(w, "Date\t: %s\n", date.UTC().Format(time.RFC3339))
				return nil
			}
			fmt.Fprintln(w, build.Version())
			return nil
		},
	}

	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "show detailed build information")
	return cmd
}
