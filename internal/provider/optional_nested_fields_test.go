package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	pgbeam "go.pgbeam.com/sdk"
)

// A null optional sub-field must be omitted from the request, not sent as its zero value.
const databaseWithPoolDefaultsJSON = `{
	"id": "db_1",
	"project_id": "prj_1",
	"host": "db.example.com",
	"port": 5432,
	"name": "app",
	"username": "app",
	"ssl_mode": "require",
	"cache_config": {"enabled": false, "ttl_seconds": 60, "max_entries": 10000, "swr_seconds": 30},
	"pool_config": {"pool_size": 20, "min_pool_size": 5, "pool_mode": "transaction", "max_active": 200},
	"created_at": "2026-01-01T00:00:00Z",
	"updated_at": "2026-01-02T00:00:00Z"
}`

func TestDatabaseResource_Create_OmitsNullNestedMaxActive(t *testing.T) {
	t.Parallel()

	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost || req.URL.Path != "/v1/projects/prj_1/databases" {
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
		}
		body = decodeBody(t, req)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(databaseWithPoolDefaultsJSON))
	}))
	defer srv.Close()

	r := NewDatabaseResource().(*databaseResource)
	cfgResp := &resource.ConfigureResponse{}
	client := pgbeam.NewClient(&pgbeam.ClientOptions{APIKey: "pgb_test", BaseURL: srv.URL})
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: client}, cfgResp)
	if cfgResp.Diagnostics.HasError() {
		t.Fatalf("Configure: %v", cfgResp.Diagnostics)
	}
	s := resourceSchema(t, NewDatabaseResource())

	pool := types.ObjectValueMust(poolConfigAttrTypes(), map[string]attr.Value{
		"pool_size":     types.Int64Value(20),
		"min_pool_size": types.Int64Value(5),
		"pool_mode":     types.StringValue("transaction"),
		"max_active":    types.Int64Null(),
	})
	plan := tfsdk.Plan{Schema: s}
	if d := plan.Set(context.Background(), &databaseResourceModel{
		ProjectID:   types.StringValue("prj_1"),
		Host:        types.StringValue("db.example.com"),
		Port:        types.Int64Value(5432),
		Name:        types.StringValue("app"),
		Username:    types.StringValue("app"),
		Password:    types.StringValue("secret"),
		CacheConfig: types.ObjectNull(cacheConfigAttrTypes()),
		PoolConfig:  pool,
	}); d.HasError() {
		t.Fatalf("seed plan: %v", d)
	}

	resp := &resource.CreateResponse{State: tfsdk.State{Schema: s}}
	r.Create(context.Background(), resource.CreateRequest{Plan: plan}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Create diagnostics: %v", resp.Diagnostics)
	}

	sent, ok := body["pool_config"].(map[string]any)
	if !ok {
		t.Fatalf("pool_config = %v, want an object", body["pool_config"])
	}
	if v, present := sent["max_active"]; present {
		t.Errorf("pool_config.max_active = %v, want absent when the attribute is null", v)
	}

	var out databaseResourceModel
	if d := resp.State.Get(context.Background(), &out); d.HasError() {
		t.Fatalf("create state: %v", d)
	}
	var outPool poolConfigModel
	if d := out.PoolConfig.As(context.Background(), &outPool, objectAsOptions()); d.HasError() {
		t.Fatalf("pool_config state: %v", d)
	}
	if outPool.MaxActive.ValueInt64() != 200 {
		t.Errorf("state pool_config.max_active = %v, want the API's 200", outPool.MaxActive)
	}
}

func TestProjectResource_Create_OmitsNullCidrLabel(t *testing.T) {
	t.Parallel()

	const project = `{"id":"prj_1","org_id":"org_1","name":"p","status":"active",` +
		`"allowed_cidrs":[{"cidr":"10.0.0.0/8"}],` +
		`"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}`

	var patch map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case req.Method == http.MethodPost && req.URL.Path == "/v1/projects":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"project":` + project + `}`))
		case req.Method == http.MethodPatch && req.URL.Path == "/v1/projects/prj_1":
			patch = decodeBody(t, req)
			_, _ = w.Write([]byte(project))
		case req.Method == http.MethodGet && req.URL.Path == "/v1/projects/prj_1":
			_, _ = w.Write([]byte(project))
		default:
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	r := newProjectResource(t, srv)
	s := resourceSchema(t, NewProjectResource())

	elemType := types.ObjectType{AttrTypes: allowedCidrsElemAttrTypes()}
	cidrs := types.ListValueMust(elemType, []attr.Value{
		types.ObjectValueMust(allowedCidrsElemAttrTypes(), map[string]attr.Value{
			"cidr":  types.StringValue("10.0.0.0/8"),
			"label": types.StringNull(),
		}),
	})
	model := seedModel("")
	model.ID = types.StringUnknown()
	model.OrgID = types.StringValue("org_1")
	model.Name = types.StringValue("p")
	model.AllowedCidrs = cidrs
	plan := tfsdk.Plan{Schema: s}
	if d := plan.Set(context.Background(), model); d.HasError() {
		t.Fatalf("seed plan: %v", d)
	}

	resp := &resource.CreateResponse{State: tfsdk.State{Schema: s}}
	r.Create(context.Background(), resource.CreateRequest{Plan: plan}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Create diagnostics: %v", resp.Diagnostics)
	}

	entries, ok := patch["allowed_cidrs"].([]any)
	if !ok || len(entries) != 1 {
		t.Fatalf("allowed_cidrs = %v, want one entry", patch["allowed_cidrs"])
	}
	entry, _ := entries[0].(map[string]any)
	if v, present := entry["label"]; present {
		t.Errorf("allowed_cidrs[0].label = %q, want absent when the attribute is null", v)
	}
}
