package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/provider"
	provschema "github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	pgbeam "go.pgbeam.com/sdk"
)

func newTestProvider() *pgbeamProvider {
	return New("test")().(*pgbeamProvider)
}

func providerSchema(t *testing.T) provschema.Schema {
	t.Helper()
	p := newTestProvider()
	resp := &provider.SchemaResponse{}
	p.Schema(context.Background(), provider.SchemaRequest{}, resp)
	return resp.Schema
}

func TestProviderMetadata(t *testing.T) {
	t.Parallel()

	p := New("1.2.3")().(*pgbeamProvider)
	resp := &provider.MetadataResponse{}
	p.Metadata(context.Background(), provider.MetadataRequest{}, resp)

	if resp.TypeName != "pgbeam" {
		t.Errorf("TypeName = %q, want pgbeam", resp.TypeName)
	}
	if resp.Version != "1.2.3" {
		t.Errorf("Version = %q, want 1.2.3", resp.Version)
	}
}

func TestProviderSchema_ValidImplementation(t *testing.T) {
	t.Parallel()

	s := providerSchema(t)
	if diags := s.ValidateImplementation(context.Background()); diags.HasError() {
		t.Fatalf("provider schema failed validation: %v", diags)
	}

	apiKey, ok := s.Attributes["api_key"]
	if !ok {
		t.Fatal("expected api_key attribute")
	}
	if !apiKey.IsSensitive() {
		t.Error("api_key must be marked sensitive")
	}
	if !apiKey.IsOptional() {
		t.Error("api_key must be optional (falls back to env var)")
	}
	if _, ok := s.Attributes["base_url"]; !ok {
		t.Fatal("expected base_url attribute")
	}
}

func TestProviderResourcesAndDataSources(t *testing.T) {
	t.Parallel()

	p := newTestProvider()
	resources := p.Resources(context.Background())
	if len(resources) != 14 {
		t.Errorf("Resources() returned %d factories, want 14", len(resources))
	}
	for i, factory := range resources {
		if factory() == nil {
			t.Errorf("resource factory %d returned nil", i)
		}
	}
	dataSources := p.DataSources(context.Background())
	if len(dataSources) != 1 {
		t.Errorf("DataSources() returned %d factories, want 1", len(dataSources))
	}
	for i, factory := range dataSources {
		if factory() == nil {
			t.Errorf("data source factory %d returned nil", i)
		}
	}
}

// buildProviderConfig builds a tfsdk.Config for the provider schema from the
// given api_key and base_url values. Nil pointers become null tftypes values.
func buildProviderConfig(t *testing.T, apiKey, baseURL *string) tfsdk.Config {
	t.Helper()

	s := providerSchema(t)
	objType := s.Type().TerraformType(context.Background()).(tftypes.Object)

	strVal := func(p *string) tftypes.Value {
		if p == nil {
			return tftypes.NewValue(tftypes.String, nil)
		}
		return tftypes.NewValue(tftypes.String, *p)
	}

	raw := tftypes.NewValue(objType, map[string]tftypes.Value{
		"api_key":  strVal(apiKey),
		"base_url": strVal(baseURL),
	})

	return tfsdk.Config{Raw: raw, Schema: s}
}

func strPtr(s string) *string { return &s }

func TestProviderConfigure_MissingAPIKey(t *testing.T) {
	t.Setenv("PGBEAM_API_KEY", "")
	t.Setenv("PGBEAM_API_URL", "")

	p := newTestProvider()
	resp := &provider.ConfigureResponse{}
	p.Configure(context.Background(), provider.ConfigureRequest{
		Config: buildProviderConfig(t, nil, nil),
	}, resp)

	if !resp.Diagnostics.HasError() {
		t.Fatal("expected error diagnostic when API key is missing")
	}
	if resp.ResourceData != nil || resp.DataSourceData != nil {
		t.Error("expected no client to be configured on error")
	}
}

func TestProviderConfigure_APIKeyFromEnv(t *testing.T) {
	t.Setenv("PGBEAM_API_KEY", "pgb_from_env")
	t.Setenv("PGBEAM_API_URL", "")

	p := newTestProvider()
	resp := &provider.ConfigureResponse{}
	p.Configure(context.Background(), provider.ConfigureRequest{
		Config: buildProviderConfig(t, nil, nil),
	}, resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected error: %v", resp.Diagnostics)
	}
	if _, ok := resp.ResourceData.(*pgbeam.Client); !ok {
		t.Fatalf("ResourceData = %T, want *pgbeam.Client", resp.ResourceData)
	}
	if _, ok := resp.DataSourceData.(*pgbeam.Client); !ok {
		t.Fatalf("DataSourceData = %T, want *pgbeam.Client", resp.DataSourceData)
	}
}

func TestProviderConfigure_ExplicitConfigOverridesEnv(t *testing.T) {
	// Explicit config values take precedence over environment variables.
	t.Setenv("PGBEAM_API_KEY", "pgb_from_env")
	t.Setenv("PGBEAM_API_URL", "https://env.example.com")

	p := newTestProvider()
	resp := &provider.ConfigureResponse{}
	p.Configure(context.Background(), provider.ConfigureRequest{
		Config: buildProviderConfig(t, strPtr("pgb_explicit"), strPtr("https://explicit.example.com")),
	}, resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected error: %v", resp.Diagnostics)
	}
	if _, ok := resp.ResourceData.(*pgbeam.Client); !ok {
		t.Fatalf("ResourceData = %T, want *pgbeam.Client", resp.ResourceData)
	}
}

func TestProviderConfigure_Validation(t *testing.T) {
	tests := []struct {
		name       string
		envKey     string
		envURL     string
		apiKey     *string
		baseURL    *string
		wantErr    string
		wantErrEnv bool
	}{
		{name: "https base URL", apiKey: strPtr("pgb_key"), baseURL: strPtr("https://api.pgbeam.com")},
		{name: "http localhost with port", apiKey: strPtr("pgb_key"), baseURL: strPtr("http://localhost:8080")},
		{name: "http loopback IPv4", apiKey: strPtr("pgb_key"), baseURL: strPtr("http://127.0.0.1:8080")},
		{name: "http loopback IPv6", apiKey: strPtr("pgb_key"), baseURL: strPtr("http://[::1]:8080")},
		{name: "default base URL", apiKey: strPtr("pgb_key")},
		{name: "http remote host", apiKey: strPtr("pgb_key"), baseURL: strPtr("http://api.pgbeam.com"), wantErr: "plaintext"},
		{name: "scheme-less", apiKey: strPtr("pgb_key"), baseURL: strPtr("api.pgbeam.com"), wantErr: "absolute URL"},
		{name: "unsupported scheme", apiKey: strPtr("pgb_key"), baseURL: strPtr("ftp://api.pgbeam.com"), wantErr: "unsupported scheme"},
		{name: "missing host", apiKey: strPtr("pgb_key"), baseURL: strPtr("https://"), wantErr: "absolute URL"},
		{name: "env base URL rejected", envKey: "pgb_key", envURL: "http://api.pgbeam.com", wantErr: "plaintext", wantErrEnv: true},
		{name: "trailing newline key", apiKey: strPtr("pgb_key\n")},
		{name: "whitespace-only key", apiKey: strPtr(" \n\t"), wantErr: "API key is required"},
		{name: "whitespace-only env key", envKey: " \n", wantErr: "API key is required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("PGBEAM_API_KEY", tt.envKey)
			t.Setenv("PGBEAM_API_URL", tt.envURL)

			p := newTestProvider()
			resp := &provider.ConfigureResponse{}
			p.Configure(context.Background(), provider.ConfigureRequest{
				Config: buildProviderConfig(t, tt.apiKey, tt.baseURL),
			}, resp)

			if tt.wantErr == "" {
				if resp.Diagnostics.HasError() {
					t.Fatalf("unexpected error: %v", resp.Diagnostics)
				}
				if _, ok := resp.ResourceData.(*pgbeam.Client); !ok {
					t.Fatalf("ResourceData = %T, want *pgbeam.Client", resp.ResourceData)
				}
				return
			}

			if !resp.Diagnostics.HasError() {
				t.Fatalf("expected error containing %q", tt.wantErr)
			}
			if resp.ResourceData != nil || resp.DataSourceData != nil {
				t.Error("expected no client to be configured on error")
			}
			detail := resp.Diagnostics.Errors()[0].Detail()
			if !strings.Contains(detail, tt.wantErr) {
				t.Errorf("detail = %q, want it to contain %q", detail, tt.wantErr)
			}
			if mentionsEnv := strings.Contains(detail, "PGBEAM_API_URL"); mentionsEnv != tt.wantErrEnv {
				t.Errorf("detail = %q, mentions PGBEAM_API_URL = %v, want %v", detail, mentionsEnv, tt.wantErrEnv)
			}
		})
	}
}

func TestProviderConfigure_TrimsAPIKey(t *testing.T) {
	t.Setenv("PGBEAM_API_KEY", "")
	t.Setenv("PGBEAM_API_URL", "")

	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	p := newTestProvider()
	resp := &provider.ConfigureResponse{}
	p.Configure(context.Background(), provider.ConfigureRequest{
		Config: buildProviderConfig(t, strPtr("pgb_from_file\n"), strPtr(srv.URL)),
	}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected error: %v", resp.Diagnostics)
	}

	client := resp.ResourceData.(*pgbeam.Client)
	if _, err := client.Projects.ListProjects(context.Background(), &pgbeam.ListProjectsParams{}); err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if gotAuth != "Bearer pgb_from_file" {
		t.Errorf("Authorization = %q, want the trimmed key", gotAuth)
	}
}
