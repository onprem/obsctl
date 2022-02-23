package config

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

type APIName string

type TenantName string

type Config struct {
	APIs    map[APIName]API `json:"apis"`
	Current struct {
		API    APIName    `json:"api"`
		Tenant TenantName `json:"tenant"`
	} `json:"current"`
}

type API struct {
	URL      string                 `json:"url"`
	Contexts map[TenantName]Context `json:"contexts"`
}

type Context struct {
	Tenant string      `json:"tenant"`
	OIDC   *OIDCConfig `json:"oidc"`
}

type OIDCConfig struct {
	AccessToken  string    `json:"accessToken"`
	RefreshToken string    `json:"refreshToken"`
	Expiry       time.Time `json:"expiry"`

	Audience     string `json:"audience"`
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
	IssuerURL    string `json:"issuerUrl"`
}

func Read() (*Config, error) {
	if err := ensureConfigDir(); err != nil {
		return nil, err
	}

	file, err := os.OpenFile(getConfigPath(), os.O_RDONLY|os.O_CREATE, 0600)
	if err != nil {
		return nil, fmt.Errorf("opening config file: %w", err)
	}
	defer file.Close()

	var cfg Config

	if err := json.NewDecoder(file).Decode(&cfg); err != nil && err != io.EOF {
		return nil, fmt.Errorf("parsing config file: %w", err)
	}

	return &cfg, nil
}

func getConfigPath() string {
	var (
		configFileName string = "config.json"
		configDirName  string = "obsctl"
	)

	dir, err := os.UserConfigDir()
	if err != nil {
		return configFileName
	}

	return path.Join(dir, configDirName, configFileName)
}

func ensureConfigDir() error {
	if err := os.MkdirAll(path.Dir(getConfigPath()), 0700); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}

	return nil
}

func (c *Config) Save() error {
	if err := ensureConfigDir(); err != nil {
		return err
	}

	file, err := os.OpenFile(getConfigPath(), os.O_RDWR|os.O_TRUNC|os.O_CREATE, 0600)
	if err != nil {
		return fmt.Errorf("opening config file: %w", err)
	}
	defer file.Close()

	if err := json.NewEncoder(file).Encode(c); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}

	return nil
}

func (c *Config) AddAPI(name APIName, url string) error {
	if c.APIs == nil {
		c.APIs = make(map[APIName]API)
	}

	if _, ok := c.APIs[name]; ok {
		return fmt.Errorf("api with name %s already exists", name)
	}

	c.APIs[name] = API{URL: url}

	return c.Save()
}

func (c *Config) RemoveAPI(name APIName) error {
	if _, ok := c.APIs[name]; !ok {
		return fmt.Errorf("api with name %s doesn't exist", name)
	}

	delete(c.APIs, name)

	return c.Save()
}

func (c *Config) AddTenant(name TenantName, api APIName, tenant string, oidcCfg *OIDCConfig) error {
	if _, ok := c.APIs[api]; !ok {
		return fmt.Errorf("api with name %s doesn't exist", api)
	}

	if c.APIs[api].Contexts == nil {
		a := c.APIs[api]
		a.Contexts = make(map[TenantName]Context)

		c.APIs[api] = a
	}

	if _, ok := c.APIs[api].Contexts[name]; ok {
		return fmt.Errorf("tenant with name %s already exists in api %s", name, api)
	}

	c.APIs[api].Contexts[name] = Context{
		Tenant: tenant,
		OIDC:   oidcCfg,
	}

	// If the current context is empty, set the newly added tenant as current.
	if c.Current.API == "" && c.Current.Tenant == "" {
		c.Current.API = api
		c.Current.Tenant = name
	}

	return c.Save()
}

func (c *Config) RemoveTenant(name TenantName, api APIName) error {
	if _, ok := c.APIs[api]; !ok {
		return fmt.Errorf("api with name %s doesn't exist", api)
	}

	if _, ok := c.APIs[api].Contexts[name]; !ok {
		return fmt.Errorf("tenant with name %s doesn't exist in api %s", name, api)
	}

	delete(c.APIs[api].Contexts, name)

	return c.Save()
}

func (c *Config) SetCurrent(api APIName, tenant TenantName) error {
	if _, ok := c.APIs[api]; !ok {
		return fmt.Errorf("api with name %s doesn't exist", api)
	}

	if _, ok := c.APIs[api].Contexts[tenant]; !ok {
		return fmt.Errorf("tenant with name %s doesn't exist in api %s", tenant, api)
	}

	c.Current.API = api
	c.Current.Tenant = tenant

	return c.Save()
}

func (c *Config) GetCurrent() (Context, error) {
	if c.Current.API == "" || c.Current.Tenant == "" {
		return Context{}, fmt.Errorf("current context is empty")
	}

	if _, ok := c.APIs[c.Current.API]; !ok {
		return Context{}, fmt.Errorf("api with name %s doesn't exist", c.Current.API)
	}

	if _, ok := c.APIs[c.Current.API].Contexts[c.Current.Tenant]; !ok {
		return Context{}, fmt.Errorf("tenant with name %s doesn't exist in api %s", c.Current.Tenant, c.Current.API)
	}

	return c.APIs[c.Current.API].Contexts[c.Current.Tenant], nil
}

func (c *Config) updateOIDCToken(ctx context.Context) error {
	cctx, err := c.GetCurrent()
	if err != nil {
		return fmt.Errorf("getting current context: %w", err)
	}

	tkn := new(oauth2.Token)

	tkn.AccessToken = cctx.OIDC.AccessToken
	tkn.RefreshToken = cctx.OIDC.RefreshToken
	tkn.Expiry = cctx.OIDC.Expiry

	if tkn.Valid() {
		return nil
	}

	ccc, err := cctx.OIDC.clientCredentialsConfig(ctx)
	if err != nil {
		return fmt.Errorf("creating clinet credentials config: %w", err)
	}

	tkn, err = ccc.Token(ctx)
	if err != nil {
		return fmt.Errorf("fetching token: %w", err)
	}

	cctx.OIDC.AccessToken = tkn.AccessToken
	cctx.OIDC.RefreshToken = tkn.RefreshToken
	cctx.OIDC.Expiry = tkn.Expiry

	c.APIs[c.Current.API].Contexts[c.Current.Tenant] = cctx

	return c.Save()
}

func (c *Config) Client(ctx context.Context) (*http.Client, error) {
	cctx, err := c.GetCurrent()
	if err != nil {
		return nil, fmt.Errorf("getting current context: %w", err)
	}

	if cctx.OIDC != nil {
		if err := c.updateOIDCToken(ctx); err != nil {
			return nil, err
		}

		ccc, err := cctx.OIDC.clientCredentialsConfig(ctx)
		if err != nil {
			return nil, fmt.Errorf("creating clinet credentials config: %w", err)
		}

		return oauth2.NewClient(ctx, ccc.TokenSource(ctx)), nil
	}

	return http.DefaultClient, nil
}

func (o OIDCConfig) clientCredentialsConfig(ctx context.Context) (clientcredentials.Config, error) {
	provider, err := oidc.NewProvider(ctx, o.IssuerURL)
	if err != nil {
		return clientcredentials.Config{}, fmt.Errorf("constructing oidc provider: %w", err)
	}

	ccc := clientcredentials.Config{
		ClientID:     o.ClientID,
		ClientSecret: o.ClientSecret,
		TokenURL:     provider.Endpoint().TokenURL,
		Scopes:       []string{"openid", "offline_access"},
	}

	if o.Audience != "" {
		ccc.EndpointParams = url.Values{
			"audience": []string{o.Audience},
		}
	}

	return ccc, nil
}
