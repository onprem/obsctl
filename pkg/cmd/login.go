package cmd

import (
	"context"
	"fmt"
	"net/url"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/go-kit/log"
	"github.com/spf13/cobra"
	"golang.org/x/oauth2/clientcredentials"

	"github.com/observatorium/obsctl/pkg/config"
)

type loginConfig struct {
	tenant string
	api    string
	ca     string
	oidc   struct {
		issuerURL    string
		clientID     string
		clientSecret string
		audience     string
	}
}

func NewLoginCmd(ctx context.Context) *cobra.Command {
	var cfg loginConfig

	cmd := &cobra.Command{
		Use:   "login",
		Short: "Login as a tenant. Will also save tenant details locally.",
		Long:  "Login as a tenant. Will also save tenant details locally.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLogin(ctx, logger, cfg)
		},
	}

	cmd.Flags().StringVar(&cfg.tenant, "tenant", "", "The name of the tenant.")
	cmd.Flags().StringVar(&cfg.api, "api", "", "The URL or name of the Observatorium API.")
	cmd.Flags().StringVar(&cfg.ca, "ca", "", "Path to the TLS CA against which to verify the Observatorium API. If no server CA is specified, the client will use the system certificates.")
	cmd.Flags().StringVar(&cfg.oidc.issuerURL, "oidc.issuer-url", "", "The OIDC issuer URL, see https://openid.net/specs/openid-connect-discovery-1_0.html#IssuerDiscovery.")
	cmd.Flags().StringVar(&cfg.oidc.clientSecret, "oidc.client-secret", "", "The OIDC client secret, see https://tools.ietf.org/html/rfc6749#section-2.3.")
	cmd.Flags().StringVar(&cfg.oidc.clientID, "oidc.client-id", "", "The OIDC client ID, see https://tools.ietf.org/html/rfc6749#section-2.3.")
	cmd.Flags().StringVar(&cfg.oidc.audience, "oidc.audience", "", "The audience for whom the access token is intended, see https://openid.net/specs/openid-connect-core-1_0.html#IDToken.")

	return cmd
}

func runLogin(ctx context.Context, logger log.Logger, cfg loginConfig) error {
	provider, err := oidc.NewProvider(ctx, cfg.oidc.issuerURL)
	if err != nil {
		return fmt.Errorf("constructing oidc provider: %w", err)
	}

	ccc := clientcredentials.Config{
		ClientID:     cfg.oidc.clientID,
		ClientSecret: cfg.oidc.clientSecret,
		TokenURL:     provider.Endpoint().TokenURL,
		Scopes:       []string{"openid", "offline_access"},
	}

	if cfg.oidc.audience != "" {
		ccc.EndpointParams = url.Values{
			"audience": []string{cfg.oidc.audience},
		}
	}

	tkn, err := ccc.Token(ctx)
	if err != nil {
		return fmt.Errorf("fetching token: %w", err)
	}

	conf, err := config.Read()
	if err != nil {
		return err
	}

	if _, ok := conf.APIs[config.APIName(cfg.api)]; !ok {
		apiURL, err := url.Parse(cfg.api)
		if err != nil {
			return fmt.Errorf("%s is not a valid URL or existing api name", cfg.api)
		}

		cfg.api = apiURL.Host

		if err := conf.AddAPI(config.APIName(cfg.api), apiURL.String()); err != nil {
			return fmt.Errorf("adding new api: %w", err)
		}
	}

	return conf.AddTenant(
		config.TenantName(cfg.tenant),
		config.APIName(cfg.api),
		cfg.tenant,
		&config.OIDCConfig{
			AccessToken:  tkn.AccessToken,
			RefreshToken: tkn.RefreshToken,
			Expiry:       tkn.Expiry,

			Audience:     cfg.oidc.audience,
			ClientID:     cfg.oidc.clientID,
			ClientSecret: cfg.oidc.clientSecret,
			IssuerURL:    cfg.oidc.issuerURL,
		},
	)
}
