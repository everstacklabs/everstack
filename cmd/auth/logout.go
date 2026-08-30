package auth

import (
	"fmt"
	"os"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/internal/cli/client"
	clicfg "github.com/everstacklabs/everstack/internal/cli/config"
	"github.com/everstacklabs/everstack/internal/cli/credentials"
	"github.com/everstacklabs/everstack/internal/cli/oauthflow"
	authv1 "github.com/everstacklabs/everstack/pkg/grpc/everstack/auth/v1"
	"github.com/spf13/cobra"
)

func newLogoutCmd() *cobra.Command {
	var localOnly bool
	cmd := &cobra.Command{
		Use:   "logout",
		Short: "Log out and clear stored credentials",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := clicfg.Load()
			if err != nil {
				return err
			}

			tok, err := credentials.Load(cfg.ActiveContext)
			if err != nil {
				return err
			}

			// OAuth refresh credentials must be revoked before their local copy
			// is removed. Legacy device/API-key sign-out remains best-effort.
			if !tok.IsEmpty() && !localOnly {
				resolved := clicfg.Resolve(cfg, "", "", "", "", "")
				if tok.RefreshToken != "" {
					if err := oauthflow.Revoke(cmd.Context(), resolved.APIURL, tok.RefreshToken, nil); err != nil {
						return fmt.Errorf("revoke OAuth credentials: %w", err)
					}
				} else {
					f := client.New(client.Options{
						APIURL:      resolved.APIURL,
						AccessToken: tok.AccessToken,
						APIKey:      tok.APIKey,
					})
					_, _ = f.Auth().SignOut(cmd.Context(), connect.NewRequest(&authv1.SignOutRequest{}))
				}
			}

			if err := credentials.Delete(cfg.ActiveContext); err != nil {
				return fmt.Errorf("clear credentials: %w", err)
			}

			fmt.Fprintln(os.Stdout, "Logged out.")
			return nil
		},
	}
	cmd.Flags().BoolVar(&localOnly, "local", false, "remove the local credential without contacting the server")
	return cmd
}
