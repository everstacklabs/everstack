package auth

import "github.com/spf13/cobra"

// New returns the root `evs auth` (login/logout/whoami) command group.
func New() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Authenticate with Everstack",
		Long:  "Authenticate with Everstack. Use `evs login` and `evs logout` directly, or `evs auth` subcommands.",
	}
	cmd.AddCommand(newLoginCmd())
	cmd.AddCommand(newLogoutCmd())
	cmd.AddCommand(newWhoamiCmd())
	return cmd
}
