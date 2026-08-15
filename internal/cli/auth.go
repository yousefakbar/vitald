package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/yousefakbar/vitald/internal/config"
	"github.com/yousefakbar/vitald/internal/provider/googlehealth"
	"golang.org/x/oauth2"
)

func newAuthCommand() *cobra.Command {
	var noOpen bool
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Authorize vitald to read Google Health data",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			if err := cfg.ValidateOAuth(); err != nil {
				return err
			}
			oauthCfg := googlehealth.OAuthConfig(cfg.GoogleClientID, cfg.GoogleClientSecret, cfg.GoogleRedirectURL)
			store := googlehealth.TokenStore{Path: cfg.TokenPath}
			token, err := googlehealth.Authorize(cmd.Context(), oauthCfg, store, !noOpen, func(format string, values ...any) { fmt.Fprintf(cmd.OutOrStdout(), format, values...) })
			if err != nil {
				return err
			}
			httpClient := oauth2.NewClient(cmd.Context(), oauthCfg.TokenSource(cmd.Context(), token))
			httpClient.Timeout = cfg.HTTPTimeout
			identity, err := googlehealth.NewClient(httpClient).Identity(cmd.Context())
			if err != nil {
				return fmt.Errorf("authorization succeeded but identity check failed: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Authorized Google Health user %s. Token saved to %s\n", identity.HealthUserId, cfg.TokenPath)
			return nil
		},
	}
	cmd.Flags().BoolVar(&noOpen, "no-open", false, "print the authorization URL without opening a browser")
	return cmd
}

func newIdentityCommand() *cobra.Command {
	return &cobra.Command{
		Use: "identity", Short: "Verify Google Health authentication",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			if err := cfg.ValidateOAuth(); err != nil {
				return err
			}
			oauthCfg := googlehealth.OAuthConfig(cfg.GoogleClientID, cfg.GoogleClientSecret, cfg.GoogleRedirectURL)
			store := googlehealth.TokenStore{Path: cfg.TokenPath}
			token, err := store.Load()
			if err != nil {
				return err
			}
			source := oauthCfg.TokenSource(cmd.Context(), token)
			fresh, err := source.Token()
			if err != nil {
				return fmt.Errorf("refresh OAuth token: %w", err)
			}
			if err := store.Save(fresh); err != nil {
				return err
			}
			httpClient := oauth2.NewClient(cmd.Context(), oauth2.ReuseTokenSource(fresh, source))
			httpClient.Timeout = cfg.HTTPTimeout
			identity, err := googlehealth.NewClient(httpClient).Identity(cmd.Context())
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Google Health user: %s\n", identity.HealthUserId)
			return nil
		},
	}
}
