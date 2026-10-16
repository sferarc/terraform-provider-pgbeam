package provider

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	pgbeam "go.pgbeam.com/sdk"
)

var _ provider.Provider = (*pgbeamProvider)(nil)

type pgbeamProvider struct {
	version string
}

type pgbeamProviderModel struct {
	APIKey  types.String `tfsdk:"api_key"`
	BaseURL types.String `tfsdk:"base_url"`
}

func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &pgbeamProvider{version: version}
	}
}

func (p *pgbeamProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "pgbeam"
	resp.Version = p.version
}

func (p *pgbeamProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "The PgBeam provider configures access to the PgBeam Control Plane API for managing globally distributed PostgreSQL proxy infrastructure.",
		Attributes: map[string]schema.Attribute{
			"api_key": schema.StringAttribute{
				Description: "PgBeam API key for authentication. Can also be set via the PGBEAM_API_KEY environment variable.",
				Optional:    true,
				Sensitive:   true,
			},
			"base_url": schema.StringAttribute{
				Description: "Base URL for the PgBeam API. Defaults to https://api.pgbeam.com. Must use https; http is accepted only for localhost, 127.0.0.1 and ::1. Can also be set via the PGBEAM_API_URL environment variable.",
				Optional:    true,
			},
		},
	}
}

func (p *pgbeamProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var config pgbeamProviderModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	apiKey := os.Getenv("PGBEAM_API_KEY")
	if !config.APIKey.IsNull() && !config.APIKey.IsUnknown() {
		apiKey = config.APIKey.ValueString()
	}
	apiKey = strings.TrimSpace(apiKey)

	if apiKey == "" {
		resp.Diagnostics.AddError(
			"Missing API Key",
			"The PgBeam API key is required. Set it in the provider configuration or via the PGBEAM_API_KEY environment variable.",
		)
		return
	}

	baseURL := strings.TrimSpace(os.Getenv("PGBEAM_API_URL"))
	source := "the PGBEAM_API_URL environment variable"
	if !config.BaseURL.IsNull() && !config.BaseURL.IsUnknown() {
		baseURL = strings.TrimSpace(config.BaseURL.ValueString())
		source = "base_url"
	}

	if baseURL != "" {
		if err := validateBaseURL(baseURL); err != nil {
			resp.Diagnostics.AddAttributeError(
				path.Root("base_url"),
				"Invalid Base URL",
				fmt.Sprintf("The PgBeam API base URL from %s is invalid: %s.", source, err),
			)
			return
		}
	}

	client := pgbeam.NewClient(&pgbeam.ClientOptions{
		APIKey:  apiKey,
		BaseURL: baseURL,
	})

	resp.DataSourceData = client
	resp.ResourceData = client
}

func (p *pgbeamProvider) Resources(_ context.Context) []func() resource.Resource {
	return pgbeamResources()
}

func (p *pgbeamProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return pgbeamDataSources()
}

// validateBaseURL rejects URLs that would send the API key in plaintext or that the SDK cannot address.
func validateBaseURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return errors.New("it is not a valid URL")
	}
	if u.Scheme == "" || u.Host == "" {
		return errors.New("it must be an absolute URL such as https://api.pgbeam.com")
	}
	if u.Hostname() == "" {
		return errors.New("it must include a host")
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if isLoopbackHost(u.Hostname()) {
			return nil
		}
		return errors.New("http is only allowed for localhost, 127.0.0.1 and ::1; use https so the API key is not sent in plaintext")
	default:
		return fmt.Errorf("unsupported scheme %q; use https", u.Scheme)
	}
}

func isLoopbackHost(host string) bool {
	switch strings.ToLower(host) {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}
