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

// newReplicaResource wires a replicaResource to a client pointing at srv.
func newReplicaResource(t *testing.T, srv *httptest.Server) *replicaResource {
	t.Helper()
	r := NewReplicaResource().(*replicaResource)
	resp := &resource.ConfigureResponse{}
	client := pgbeam.NewClient(&pgbeam.ClientOptions{APIKey: "pgb_test", BaseURL: srv.URL})
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: client}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Configure: %v", resp.Diagnostics)
	}
	return r
}

const (
	replicasPath = "/v1/databases/db_1/replicas"
	replicaJSON  = `{
		"id": "rep_1",
		"database_id": "db_1",
		"host": "replica.example.com",
		"port": 6432,
		"ssl_mode": "verify-full",
		"created_at": "2026-01-01T00:00:00Z",
		"updated_at": "2026-01-02T00:00:00Z"
	}`
	otherReplicaJSON = `{
		"id": "rep_0",
		"database_id": "db_1",
		"host": "other.example.com",
		"port": 5432,
		"ssl_mode": "require",
		"created_at": "2026-01-01T00:00:00Z",
		"updated_at": "2026-01-01T00:00:00Z"
	}`
)

func replicaState(t *testing.T) tfsdk.State {
	t.Helper()
	state := tfsdk.State{Schema: resourceSchema(t, NewReplicaResource())}
	if d := state.Set(context.Background(), &replicaResourceModel{
		ID:         types.StringValue("rep_1"),
		DatabaseID: types.StringValue("db_1"),
		Host:       types.StringValue("replica.example.com"),
		Port:       types.Int64Value(5432),
		SSLMode:    types.StringValue("require"),
	}); d.HasError() {
		t.Fatalf("seed state: %v", d)
	}
	return state
}

func readReplica(t *testing.T, r *replicaResource) *resource.ReadResponse {
	t.Helper()
	return readState(t, r, replicaState(t))
}

// TestReplicaResource_Read_FindsEntryOnLaterPage asserts Read follows
// next_page_token until it finds the replica, sending the cursor back.
func TestReplicaResource_Read_FindsEntryOnLaterPage(t *testing.T) {
	t.Parallel()

	var lp listPages
	srv := httptest.NewServer(lp.handler(t, replicasPath, map[string]string{
		"":   `{"replicas": [` + otherReplicaJSON + `], "next_page_token": "p2"}`,
		"p2": `{"replicas": [` + replicaJSON + `]}`,
	}))
	defer srv.Close()

	resp := readReplica(t, newReplicaResource(t, srv))
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read diagnostics: %v", resp.Diagnostics)
	}
	assertTokens(t, &lp, "", "p2")

	var out replicaResourceModel
	if d := resp.State.Get(context.Background(), &out); d.HasError() {
		t.Fatalf("read state: %v", d)
	}
	if out.Port.ValueInt64() != 6432 || out.SSLMode.ValueString() != "verify-full" {
		t.Errorf("state = (%d, %q), want (6432, verify-full)", out.Port.ValueInt64(), out.SSLMode.ValueString())
	}
}

// TestReplicaResource_Read_NoMatchRemovesResource asserts a replica missing from
// every page drops out of state.
func TestReplicaResource_Read_NoMatchRemovesResource(t *testing.T) {
	t.Parallel()

	var lp listPages
	srv := httptest.NewServer(lp.handler(t, replicasPath, map[string]string{
		"":   `{"replicas": [` + otherReplicaJSON + `], "next_page_token": "p2"}`,
		"p2": `{"replicas": [], "next_page_token": ""}`,
	}))
	defer srv.Close()

	resp := readReplica(t, newReplicaResource(t, srv))
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read diagnostics: %v", resp.Diagnostics)
	}
	if !resp.State.Raw.IsNull() {
		t.Error("state was not removed for a replica that is gone upstream")
	}
	assertTokens(t, &lp, "", "p2")
}

// TestReplicaResource_Read_ListErrorKeepsResource asserts a failing list call
// surfaces an error rather than being read as "not found".
func TestReplicaResource_Read_ListErrorKeepsResource(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	resp := readReplica(t, newReplicaResource(t, srv))
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected diagnostics for a failing list call")
	}
	if resp.State.Raw.IsNull() {
		t.Error("state was removed on a list error")
	}
}

// TestReplicaResource_Delete_TolerantOf404 asserts a replica already removed out
// of band does not fail the destroy.
func TestReplicaResource_Delete_TolerantOf404(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodDelete || req.URL.Path != replicasPath+"/rep_1" {
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	state := replicaState(t)
	resp := &resource.DeleteResponse{State: state}
	newReplicaResource(t, srv).Delete(context.Background(), resource.DeleteRequest{State: state}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Delete diagnostics: %v", resp.Diagnostics)
	}
}

// TestReplicaResource_ImportState splits database_id/replica_id and hydrates
// state from the list.
func TestReplicaResource_ImportState(t *testing.T) {
	t.Parallel()

	var lp listPages
	srv := httptest.NewServer(lp.handler(t, replicasPath, map[string]string{
		"": `{"replicas": [` + replicaJSON + `]}`,
	}))
	defer srv.Close()

	s := resourceSchema(t, NewReplicaResource())
	resp := &resource.ImportStateResponse{State: tfsdk.State{Schema: s}}
	newReplicaResource(t, srv).ImportState(context.Background(), resource.ImportStateRequest{ID: "db_1/rep_1"}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("ImportState diagnostics: %v", resp.Diagnostics)
	}

	var out replicaResourceModel
	if d := resp.State.Get(context.Background(), &out); d.HasError() {
		t.Fatalf("import state: %v", d)
	}
	if out.ID.ValueString() != "rep_1" || out.DatabaseID.ValueString() != "db_1" {
		t.Errorf("imported (%q, %q), want (rep_1, db_1)", out.ID.ValueString(), out.DatabaseID.ValueString())
	}
	if out.Host.ValueString() != "replica.example.com" {
		t.Errorf("Host = %q", out.Host.ValueString())
	}
}

func TestReplicaResource_ImportState_RejectsBareID(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("no request expected")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	s := resourceSchema(t, NewReplicaResource())
	resp := &resource.ImportStateResponse{State: tfsdk.State{Schema: s}}
	newReplicaResource(t, srv).ImportState(context.Background(), resource.ImportStateRequest{ID: "rep_1"}, resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected diagnostics for a bare import ID")
	}
}
