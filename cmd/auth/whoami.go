package auth

import (
	"fmt"
	"os"

	"github.com/everstacklabs/everstack/internal/cli/client"
	clicfg "github.com/everstacklabs/everstack/internal/cli/config"
	"github.com/everstacklabs/everstack/internal/cli/credentials"
	"github.com/everstacklabs/everstack/internal/cli/output"
	"github.com/spf13/cobra"
)

func newWhoamiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Show the currently authenticated identity",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := clicfg.Load()
			if err != nil {
				return err
			}

			tok, err := credentials.Load(cfg.ActiveContext)
			if err != nil {
				return err
			}
			if tok.IsEmpty() {
				fmt.Fprintln(os.Stderr, "Not logged in. Run `evs login` first.")
				os.Exit(1)
			}

			resolved := clicfg.Resolve(cfg, "", "", "", "", "")
			options := client.Options{
				APIURL:      resolved.APIURL,
				AccessToken: tok.AccessToken,
				APIKey:      tok.APIKey,
				OrgID:       tok.OrgID,
			}
			if tok.RefreshToken != "" {
				options.AccessTokenSource = credentials.NewSource(cfg.ActiveContext, resolved.APIURL, nil)
			}
			f := client.New(options)

			who, err := f.Whoami(cmd.Context())
			if err != nil {
				return err
			}

			p := output.NewPrinter(output.FormatTable, false)
			p.Table(
				[]string{"FIELD", "VALUE"},
				[][]string{
					{"User", who.Email},
					{"User ID", who.UserID},
					{"Org", who.OrgSlug},
					{"Org ID", who.OrgID},
					{"Endpoint", resolved.APIURL},
					{"Context", cfg.ActiveContext},
				},
			)
			return nil
		},
	}
}
