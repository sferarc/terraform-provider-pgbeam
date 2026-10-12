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

// newApprovalRuleResource wires an approvalRuleResource to a client pointing at srv.
func newApprovalRuleResource(t *testing.T, srv *httptest.Server) *approvalRuleResource {
	t.Helper()
	r := NewApprovalRuleResource().(*approvalRuleResource)
	resp := &resource.ConfigureResponse{}
	client := pgbeam.NewClient(&pgbeam.ClientOptions{APIKey: "pgb_test", BaseURL: srv.URL})
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: client}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Configure: %v", resp.Diagnostics)
	}
	return r
}

const approvalRuleJSON = `{
	"id": "apl_1",
	"project_id": "prj_1",
	"name": "payments deletes",
	"statement_kinds": ["delete", "ddl"],
	"relation_name": "payments",
	"min_affected_rows": 10,
	"enabled": true,
	"created_at": "2026-01-01T00:00:00Z",
	"updated_at": "2026-01-02T00:00:00Z"
}`

// approvalRuleEveryKindJSON is a rule holding every kind: the API returns an empty list, not null.
const approvalRuleEveryKindJSON = `{
	"id": "apl_1",
	"project_id": "prj_1",
	"name": "all writes",
	"statement_kinds": [],
	"enabled": true,
	"created_at": "2026-01-01T00:00:00Z",
	"updated_at": "2026-01-02T00:00:00Z"
}`

func kindsList(kinds ...string) types.List {
	vals := make([]attr.Value, len(kinds))
	for i, k := range kinds {
		vals[i] = types.StringValue(k)
	}
	return types.ListValueMust(types.StringType, vals)
}

func TestApprovalRuleResource_Create_SendsKindsAndThreshold(t *testing.T) {
	t.Parallel()

	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost || req.URL.Path != "/v1/projects/prj_1/approval-rules" {
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
		}
		body = decodeBody(t, req)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(approvalRuleJSON))
	}))
	defer srv.Close()

	r := newApprovalRuleResource(t, srv)
	s := resourceSchema(t, NewApprovalRuleResource())

	plan := tfsdk.Plan{Schema: s}
	if d := plan.Set(context.Background(), &approvalRuleResourceModel{
		ProjectID:       types.StringValue("prj_1"),
		Name:            types.StringValue("payments deletes"),
		StatementKinds:  kindsList("delete", "ddl"),
		SchemaName:      types.StringNull(),
		RelationName:    types.StringValue("payments"),
		MinAffectedRows: types.Int64Value(10),
		Enabled:         types.BoolValue(true),
	}); d.HasError() {
		t.Fatalf("seed plan: %v", d)
	}

	resp := &resource.CreateResponse{State: tfsdk.State{Schema: s}}
	r.Create(context.Background(), resource.CreateRequest{Plan: plan}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Create diagnostics: %v", resp.Diagnostics)
	}

	kinds, _ := body["statement_kinds"].([]any)
	if len(kinds) != 2 || kinds[0] != "delete" || kinds[1] != "ddl" {
		t.Errorf("statement_kinds = %v, want [delete ddl]", body["statement_kinds"])
	}
	if body["min_affected_rows"] != float64(10) {
		t.Errorf("min_affected_rows = %v, want 10", body["min_affected_rows"])
	}
	if _, ok := body["schema_name"]; ok {
		t.Errorf("a null schema_name must be omitted, got %v", body["schema_name"])
	}

	var out approvalRuleResourceModel
	if d := resp.State.Get(context.Background(), &out); d.HasError() {
		t.Fatalf("create state: %v", d)
	}
	if out.ID.ValueString() != "apl_1" || out.MinAffectedRows.ValueInt64() != 10 {
		t.Errorf("state = %+v, want the created rule", out)
	}
	if !out.SchemaName.IsNull() {
		t.Errorf("schema_name = %v, want null (every schema)", out.SchemaName)
	}
}

// An omitted statement_kinds plans [] (every kind), so state must read back [] to match the plan.
func TestApprovalRuleResource_Create_OmittedKindsReadBackEmpty(t *testing.T) {
	t.Parallel()

	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body = decodeBody(t, req)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(approvalRuleEveryKindJSON))
	}))
	defer srv.Close()

	r := newApprovalRuleResource(t, srv)
	s := resourceSchema(t, NewApprovalRuleResource())

	plan := tfsdk.Plan{Schema: s}
	if d := plan.Set(context.Background(), &approvalRuleResourceModel{
		ProjectID:       types.StringValue("prj_1"),
		Name:            types.StringValue("all writes"),
		StatementKinds:  kindsList(),
		SchemaName:      types.StringNull(),
		RelationName:    types.StringNull(),
		MinAffectedRows: types.Int64Null(),
		Enabled:         types.BoolValue(true),
	}); d.HasError() {
		t.Fatalf("seed plan: %v", d)
	}

	resp := &resource.CreateResponse{State: tfsdk.State{Schema: s}}
	r.Create(context.Background(), resource.CreateRequest{Plan: plan}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Create diagnostics: %v", resp.Diagnostics)
	}
	if _, ok := body["min_affected_rows"]; ok {
		t.Errorf("an unset min_affected_rows must be omitted, got %v", body["min_affected_rows"])
	}

	var out approvalRuleResourceModel
	if d := resp.State.Get(context.Background(), &out); d.HasError() {
		t.Fatalf("create state: %v", d)
	}
	if out.StatementKinds.IsNull() || len(out.StatementKinds.Elements()) != 0 {
		t.Errorf("statement_kinds = %v, want [] to match the planned default", out.StatementKinds)
	}
	if !out.MinAffectedRows.IsNull() {
		t.Errorf("min_affected_rows = %v, want null (no threshold), not 0", out.MinAffectedRows)
	}
}

// The update is a PUT, so fields the plan did not change must still be sent.
func TestApprovalRuleResource_Update_SendsWholeBody(t *testing.T) {
	t.Parallel()

	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if req.Method == http.MethodPut {
			body = decodeBody(t, req)
		}
		_, _ = w.Write([]byte(approvalRuleJSON))
	}))
	defer srv.Close()

	r := newApprovalRuleResource(t, srv)
	s := resourceSchema(t, NewApprovalRuleResource())

	model := approvalRuleResourceModel{
		ID:              types.StringValue("apl_1"),
		ProjectID:       types.StringValue("prj_1"),
		Name:            types.StringValue("payments deletes"),
		StatementKinds:  kindsList("delete", "ddl"),
		SchemaName:      types.StringNull(),
		RelationName:    types.StringValue("payments"),
		MinAffectedRows: types.Int64Value(5),
		Enabled:         types.BoolValue(true),
	}
	state := tfsdk.State{Schema: s}
	if d := state.Set(context.Background(), &model); d.HasError() {
		t.Fatalf("seed state: %v", d)
	}
	model.MinAffectedRows = types.Int64Value(10)
	plan := tfsdk.Plan{Schema: s}
	if d := plan.Set(context.Background(), &model); d.HasError() {
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
	if body["min_affected_rows"] != float64(10) {
		t.Errorf("min_affected_rows = %v, want 10", body["min_affected_rows"])
	}
	if body["name"] != "payments deletes" || body["relation_name"] != "payments" {
		t.Errorf("unchanged fields must still be sent, body = %v", body)
	}
	if kinds, _ := body["statement_kinds"].([]any); len(kinds) != 2 {
		t.Errorf("statement_kinds = %v, want the unchanged two kinds", body["statement_kinds"])
	}
}
