package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	pgbeam "go.pgbeam.com/sdk"
)

// newCacheRuleResource wires a cache ruleResource to a client pointing at srv.
func newCacheRuleResource(t *testing.T, srv *httptest.Server) *cacheRuleResource {
	t.Helper()
	r := NewCacheRuleResource().(*cacheRuleResource)
	resp := &resource.ConfigureResponse{}
	client := pgbeam.NewClient(&pgbeam.ClientOptions{APIKey: "pgb_test", BaseURL: srv.URL})
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: client}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Configure: %v", resp.Diagnostics)
	}
	return r
}

const (
	cacheRulesPath = "/v1/projects/prj_1/databases/db_1/cache-rules"
	cacheRuleJSON  = `{
		"query_hash": "a1b2c3d4e5f60718",
		"normalized_sql": "SELECT * FROM users WHERE id = $1",
		"query_type": "read",
		"cache_enabled": true,
		"cache_ttl_seconds": 300,
		"call_count": 42,
		"avg_latency_ms": 1.5,
		"p95_latency_ms": 4.0,
		"avg_response_bytes": 512,
		"stability_rate": 0.99,
		"recommendation": "great",
		"first_seen_at": "2026-01-01T00:00:00Z",
		"last_seen_at": "2026-01-02T00:00:00Z"
	}`
	otherCacheRuleJSON = `{
		"query_hash": "ffffffffffffffff",
		"normalized_sql": "SELECT 1",
		"query_type": "read",
		"cache_enabled": false,
		"call_count": 1,
		"avg_latency_ms": 0.1,
		"p95_latency_ms": 0.1,
		"avg_response_bytes": 8,
		"stability_rate": 1,
		"recommendation": "minimal",
		"first_seen_at": "2026-01-01T00:00:00Z",
		"last_seen_at": "2026-01-01T00:00:00Z"
	}`
)

func cacheRuleState(t *testing.T) tfsdk.State {
	t.Helper()
	state := tfsdk.State{Schema: resourceSchema(t, NewCacheRuleResource())}
	if d := state.Set(context.Background(), &cacheRuleResourceModel{
		ID:           types.StringValue("prj_1/db_1/a1b2c3d4e5f60718"),
		ProjectID:    types.StringValue("prj_1"),
		DatabaseID:   types.StringValue("db_1"),
		QueryHash:    types.StringValue("a1b2c3d4e5f60718"),
		CacheEnabled: types.BoolValue(false),
	}); d.HasError() {
		t.Fatalf("seed state: %v", d)
	}
	return state
}

func readCacheRule(t *testing.T, r *cacheRuleResource) *resource.ReadResponse {
	t.Helper()
	return readState(t, r, cacheRuleState(t))
}

// TestCacheRuleResource_Read_FindsEntryOnLaterPage asserts Read follows
// next_page_token until it finds the cache rule, sending the cursor back.
func TestCacheRuleResource_Read_FindsEntryOnLaterPage(t *testing.T) {
	t.Parallel()

	var lp listPages
	srv := httptest.NewServer(lp.handler(t, cacheRulesPath, map[string]string{
		"":   `{"entries": [` + otherCacheRuleJSON + `], "next_page_token": "p2"}`,
		"p2": `{"entries": [` + cacheRuleJSON + `]}`,
	}))
	defer srv.Close()

	resp := readCacheRule(t, newCacheRuleResource(t, srv))
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read diagnostics: %v", resp.Diagnostics)
	}
	assertTokens(t, &lp, "", "p2")

	var out cacheRuleResourceModel
	if d := resp.State.Get(context.Background(), &out); d.HasError() {
		t.Fatalf("read state: %v", d)
	}
	if !out.CacheEnabled.ValueBool() || out.CacheTTLSeconds.ValueInt64() != 300 {
		t.Errorf("state = (%v, %d), want (true, 300)", out.CacheEnabled.ValueBool(), out.CacheTTLSeconds.ValueInt64())
	}
}

// TestCacheRuleResource_Read_NoMatchRemovesResource asserts a cache rule missing from
// every page drops out of state.
func TestCacheRuleResource_Read_NoMatchRemovesResource(t *testing.T) {
	t.Parallel()

	var lp listPages
	srv := httptest.NewServer(lp.handler(t, cacheRulesPath, map[string]string{
		"":   `{"entries": [` + otherCacheRuleJSON + `], "next_page_token": "p2"}`,
		"p2": `{"entries": [], "next_page_token": ""}`,
	}))
	defer srv.Close()

	resp := readCacheRule(t, newCacheRuleResource(t, srv))
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read diagnostics: %v", resp.Diagnostics)
	}
	if !resp.State.Raw.IsNull() {
		t.Error("state was not removed for a cache rule that is gone upstream")
	}
	assertTokens(t, &lp, "", "p2")
}

// TestCacheRuleResource_Read_ListErrorKeepsResource asserts a failing list call
// surfaces an error rather than being read as "not found".
func TestCacheRuleResource_Read_ListErrorKeepsResource(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	resp := readCacheRule(t, newCacheRuleResource(t, srv))
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected diagnostics for a failing list call")
	}
	if resp.State.Raw.IsNull() {
		t.Error("state was removed on a list error")
	}
}

// TestCacheRuleResource_Delete_TolerantOf404 asserts destroy disables caching
// for the query shape (there is no DELETE) and that a shape already gone from
// the database does not fail it.
func TestCacheRuleResource_Delete_TolerantOf404(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPut || req.URL.Path != cacheRulesPath+"/a1b2c3d4e5f60718" {
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
		}
		if body := decodeBody(t, req); body["cache_enabled"] != false {
			t.Errorf("cache_enabled = %v, want false", body["cache_enabled"])
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	state := cacheRuleState(t)
	resp := &resource.DeleteResponse{State: state}
	newCacheRuleResource(t, srv).Delete(context.Background(), resource.DeleteRequest{State: state}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Delete diagnostics: %v", resp.Diagnostics)
	}
}

// TestCacheRuleResource_ImportState splits project_id/database_id/query_hash and
// hydrates state from the list. The entry carries neither parent ID, so both
// must come from the import ID or the next refresh lists an empty path.
func TestCacheRuleResource_ImportState(t *testing.T) {
	t.Parallel()

	var lp listPages
	srv := httptest.NewServer(lp.handler(t, cacheRulesPath, map[string]string{
		"": `{"entries": [` + cacheRuleJSON + `]}`,
	}))
	defer srv.Close()

	s := resourceSchema(t, NewCacheRuleResource())
	resp := &resource.ImportStateResponse{State: tfsdk.State{Schema: s}}
	newCacheRuleResource(t, srv).ImportState(context.Background(), resource.ImportStateRequest{ID: "prj_1/db_1/a1b2c3d4e5f60718"}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("ImportState diagnostics: %v", resp.Diagnostics)
	}

	var out cacheRuleResourceModel
	if d := resp.State.Get(context.Background(), &out); d.HasError() {
		t.Fatalf("import state: %v", d)
	}
	if out.ID.ValueString() != "prj_1/db_1/a1b2c3d4e5f60718" {
		t.Errorf("ID = %q", out.ID.ValueString())
	}
	if out.ProjectID.ValueString() != "prj_1" || out.DatabaseID.ValueString() != "db_1" {
		t.Errorf("parents = (%q, %q), want (prj_1, db_1)", out.ProjectID.ValueString(), out.DatabaseID.ValueString())
	}
	if out.QueryHash.ValueString() != "a1b2c3d4e5f60718" || !out.CacheEnabled.ValueBool() {
		t.Errorf("imported (%q, %v)", out.QueryHash.ValueString(), out.CacheEnabled.ValueBool())
	}
}

func TestCacheRuleResource_ImportState_RejectsTwoPartID(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("no request expected")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	s := resourceSchema(t, NewCacheRuleResource())
	resp := &resource.ImportStateResponse{State: tfsdk.State{Schema: s}}
	newCacheRuleResource(t, srv).ImportState(context.Background(), resource.ImportStateRequest{ID: "db_1/a1b2c3d4e5f60718"}, resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected diagnostics for an import ID missing the project")
	}
}
