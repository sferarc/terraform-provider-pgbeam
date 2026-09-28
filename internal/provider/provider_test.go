package provider

import (
	"context"
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
	if len(resources) != 12 {
		t.Errorf("Resources() returned %d factories, want 12", len(resources))
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
