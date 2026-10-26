package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	pgbeam "go.pgbeam.com/sdk"
)

// newHoneytokenResource wires a honeytokenResource to a client pointing at srv.
func newHoneytokenResource(t *testing.T, srv *httptest.Server) *honeytokenResource {
	t.Helper()
	r := NewHoneytokenResource().(*honeytokenResource)
	resp := &resource.ConfigureResponse{}
	client := pgbeam.NewClient(&pgbeam.ClientOptions{APIKey: "pgb_test", BaseURL: srv.URL})
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: client}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Configure: %v", resp.Diagnostics)
	}
	if r.client == nil {
		t.Fatal("Configure did not set the client")
	}
	return r
}

const honeytokenJSON = `{
	"id": "hnt_1",
	"project_id": "prj_1",
	"schema_name": "public",
	"relation_name": "customer_ssns",
	"action": "audit_only",
	"created_at": "2026-01-01T00:00:00Z",
	"updated_at": "2026-01-02T00:00:00Z"
}`

// decodeBody reads a request body as a generic JSON object.
func decodeBody(t *testing.T, req *http.Request) map[string]any {
	t.Helper()
	raw, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode body %q: %v", raw, err)
	}
	return out
}

// TestHoneytokenResource_Create_SendsTypedAction asserts the create body carries
// the enum-valued action. The generated create builds the request as a struct
// literal, which is the path that has to cast a required enum.
func TestHoneytokenResource_Create_SendsTypedAction(t *testing.T) {
	t.Parallel()

	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost || req.URL.Path != "/v1/projects/prj_1/honeytokens" {
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
		}
		body = decodeBody(t, req)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(honeytokenJSON))
	}))
	defer srv.Close()

	r := newHoneytokenResource(t, srv)
	s := resourceSchema(t, NewHoneytokenResource())

	plan := tfsdk.Plan{Schema: s}
	if d := plan.Set(context.Background(), &honeytokenResourceModel{
		ProjectID:    types.StringValue("prj_1"),
		SchemaName:   types.StringValue("public"),
		RelationName: types.StringValue("customer_ssns"),
		Action:       types.StringValue("audit_only"),
	}); d.HasError() {
		t.Fatalf("seed plan: %v", d)
	}

	resp := &resource.CreateResponse{State: tfsdk.State{Schema: s}}
	r.Create(context.Background(), resource.CreateRequest{Plan: plan}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Create diagnostics: %v", resp.Diagnostics)
	}

	if body["relation_name"] != "customer_ssns" {
		t.Errorf("relation_name = %v, want customer_ssns", body["relation_name"])
	}
	if body["action"] != "audit_only" {
		t.Errorf("action = %v, want audit_only", body["action"])
	}
	if body["schema_name"] != "public" {
		t.Errorf("schema_name = %v, want public", body["schema_name"])
	}

	var out honeytokenResourceModel
	if d := resp.State.Get(context.Background(), &out); d.HasError() {
		t.Fatalf("create state: %v", d)
	}
	if out.ID.ValueString() != "hnt_1" {
		t.Errorf("ID = %q, want hnt_1", out.ID.ValueString())
	}
}

// TestHoneytokenResource_Update_SendsWholeBody is the regression test for the
// full-replacement update. PUT /honeytokens/{id} takes the same body as POST,
// with relation_name and action required, so an update that only changes
// schema_name must still carry the other two. A diff-only body is rejected with
// 400 by the API and, worse, would silently clear a field the plan left alone.
func TestHoneytokenResource_Update_SendsWholeBody(t *testing.T) {
	t.Parallel()

	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case http.MethodPut:
			body = decodeBody(t, req)
			_, _ = w.Write([]byte(honeytokenJSON))
		case http.MethodGet:
			_, _ = w.Write([]byte(honeytokenJSON))
		default:
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
		}
	}))
	defer srv.Close()

	r := newHoneytokenResource(t, srv)
	s := resourceSchema(t, NewHoneytokenResource())

	state := tfsdk.State{Schema: s}
	if d := state.Set(context.Background(), &honeytokenResourceModel{
		ID:           types.StringValue("hnt_1"),
		ProjectID:    types.StringValue("prj_1"),
		SchemaName:   types.StringValue("billing"),
		RelationName: types.StringValue("customer_ssns"),
		Action:       types.StringValue("audit_only"),
	}); d.HasError() {
		t.Fatalf("seed state: %v", d)
	}

	// Only schema_name moves.
	plan := tfsdk.Plan{Schema: s}
	if d := plan.Set(context.Background(), &honeytokenResourceModel{
		ID:           types.StringValue("hnt_1"),
		ProjectID:    types.StringValue("prj_1"),
		SchemaName:   types.StringValue("public"),
		RelationName: types.StringValue("customer_ssns"),
		Action:       types.StringValue("audit_only"),
	}); d.HasError() {
		t.Fatalf("seed plan: %v", d)
	}

	resp := &resource.UpdateResponse{State: tfsdk.State{Schema: s}}
	r.Update(context.Background(), resource.UpdateRequest{Plan: plan, State: state}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Update diagnostics: %v", resp.Diagnostics)
	}

	if body == nil {
		t.Fatal("no update request was sent")
	}
	if body["schema_name"] != "public" {
		t.Errorf("schema_name = %v, want public", body["schema_name"])
	}
	if body["relation_name"] != "customer_ssns" {
		t.Errorf("relation_name = %v, want customer_ssns (unchanged fields must still be sent)", body["relation_name"])
	}
	if body["action"] != "audit_only" {
		t.Errorf("action = %v, want audit_only (unchanged fields must still be sent)", body["action"])
	}
}

// TestHoneytokenResource_Update_NoDiffSkipsCall asserts the plan/state diff
// still gates the call: an update with nothing changed must not PUT.
func TestHoneytokenResource_Update_NoDiffSkipsCall(t *testing.T) {
	t.Parallel()

	puts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodPut {
			puts++
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(honeytokenJSON))
	}))
	defer srv.Close()

	r := newHoneytokenResource(t, srv)
	s := resourceSchema(t, NewHoneytokenResource())

	same := func() *honeytokenResourceModel {
		return &honeytokenResourceModel{
			ID:           types.StringValue("hnt_1"),
			ProjectID:    types.StringValue("prj_1"),
			SchemaName:   types.StringValue("public"),
			RelationName: types.StringValue("customer_ssns"),
			Action:       types.StringValue("audit_only"),
		}
	}

	state := tfsdk.State{Schema: s}
	if d := state.Set(context.Background(), same()); d.HasError() {
		t.Fatalf("seed state: %v", d)
	}
	plan := tfsdk.Plan{Schema: s}
	if d := plan.Set(context.Background(), same()); d.HasError() {
		t.Fatalf("seed plan: %v", d)
	}

	resp := &resource.UpdateResponse{State: tfsdk.State{Schema: s}}
	r.Update(context.Background(), resource.UpdateRequest{Plan: plan, State: state}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Update diagnostics: %v", resp.Diagnostics)
	}
	if puts != 0 {
		t.Errorf("PUT count = %d, want 0 when nothing changed", puts)
	}
}

// TestHoneytokenResource_Read_MapsNullSchema covers the unqualified form: a
// honeytoken with no schema reads back as a null attribute rather than "".
func TestHoneytokenResource_Read_MapsNullSchema(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/v1/projects/prj_1/honeytokens/hnt_1" {
			t.Errorf("path = %s", req.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "hnt_1",
			"project_id": "prj_1",
			"relation_name": "customer_ssns",
			"action": "kill",
			"created_at": "2026-01-01T00:00:00Z",
			"updated_at": "2026-01-02T00:00:00Z"
		}`))
	}))
	defer srv.Close()

	r := newHoneytokenResource(t, srv)
	s := resourceSchema(t, NewHoneytokenResource())

	state := tfsdk.State{Schema: s}
	if d := state.Set(context.Background(), &honeytokenResourceModel{
		ID:           types.StringValue("hnt_1"),
		ProjectID:    types.StringValue("prj_1"),
		SchemaName:   types.StringValue("public"),
		RelationName: types.StringValue("customer_ssns"),
		Action:       types.StringValue("audit_only"),
	}); d.HasError() {
		t.Fatalf("seed state: %v", d)
	}

	resp := &resource.ReadResponse{State: tfsdk.State{Schema: s}}
	r.Read(context.Background(), resource.ReadRequest{State: state}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read diagnostics: %v", resp.Diagnostics)
	}

	var out honeytokenResourceModel
	if d := resp.State.Get(context.Background(), &out); d.HasError() {
		t.Fatalf("read state: %v", d)
	}
	if !out.SchemaName.IsNull() {
		t.Errorf("SchemaName = %q, want null", out.SchemaName.ValueString())
	}
	if out.Action.ValueString() != "kill" {
		t.Errorf("Action = %q, want kill", out.Action.ValueString())
	}
}

// TestHoneytokenResource_Delete_TolerantOf404 asserts a decoy already removed
// out of band does not fail the destroy.
func TestHoneytokenResource_Delete_TolerantOf404(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	r := newHoneytokenResource(t, srv)
	s := resourceSchema(t, NewHoneytokenResource())

	state := tfsdk.State{Schema: s}
	if d := state.Set(context.Background(), &honeytokenResourceModel{
		ID:           types.StringValue("hnt_1"),
		ProjectID:    types.StringValue("prj_1"),
		RelationName: types.StringValue("customer_ssns"),
		Action:       types.StringValue("audit_only"),
	}); d.HasError() {
		t.Fatalf("seed state: %v", d)
	}

	resp := &resource.DeleteResponse{State: tfsdk.State{Schema: s}}
	r.Delete(context.Background(), resource.DeleteRequest{State: state}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Delete diagnostics: %v", resp.Diagnostics)
	}
}

// TestHoneytokenResource_ImportState splits the composite import ID and hydrates
// state from the API.
func TestHoneytokenResource_ImportState(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/v1/projects/prj_1/honeytokens/hnt_1" {
			t.Errorf("path = %s", req.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(honeytokenJSON))
	}))
	defer srv.Close()

	r := newHoneytokenResource(t, srv)
	s := resourceSchema(t, NewHoneytokenResource())

	resp := &resource.ImportStateResponse{State: tfsdk.State{Schema: s}}
	r.ImportState(context.Background(), resource.ImportStateRequest{ID: "prj_1/hnt_1"}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("ImportState diagnostics: %v", resp.Diagnostics)
	}

	var out honeytokenResourceModel
	if d := resp.State.Get(context.Background(), &out); d.HasError() {
		t.Fatalf("import state: %v", d)
	}
	if out.ID.ValueString() != "hnt_1" || out.ProjectID.ValueString() != "prj_1" {
		t.Errorf("imported (%q, %q), want (hnt_1, prj_1)", out.ID.ValueString(), out.ProjectID.ValueString())
	}
	if out.RelationName.ValueString() != "customer_ssns" {
		t.Errorf("RelationName = %q", out.RelationName.ValueString())
	}
}

// TestHoneytokenResource_ImportState_RejectsBareID pins the error on an import
// ID missing the project prefix.
func TestHoneytokenResource_ImportState_RejectsBareID(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("no request expected")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	r := newHoneytokenResource(t, srv)
	s := resourceSchema(t, NewHoneytokenResource())

	resp := &resource.ImportStateResponse{State: tfsdk.State{Schema: s}}
	r.ImportState(context.Background(), resource.ImportStateRequest{ID: "hnt_1"}, resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected diagnostics for a bare import ID")
	}
}

func TestHoneytokenResource_ImportState_RejectsMalformedSegments(t *testing.T) {
	t.Parallel()

	for _, id := range []string{"prj_1/hnt_1/extra", "prj_1/", "/hnt_1", " /hnt_1"} {
		t.Run(id, func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				t.Error("no request expected")
				w.WriteHeader(http.StatusInternalServerError)
			}))
			defer srv.Close()

			r := newHoneytokenResource(t, srv)
			s := resourceSchema(t, NewHoneytokenResource())

			resp := &resource.ImportStateResponse{State: tfsdk.State{Schema: s}}
			r.ImportState(context.Background(), resource.ImportStateRequest{ID: id}, resp)
			if !resp.Diagnostics.HasError() {
				t.Fatalf("expected diagnostics for import ID %q", id)
			}
		})
	}
}
