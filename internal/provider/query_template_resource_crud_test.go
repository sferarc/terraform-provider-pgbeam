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

// newQueryTemplateResource wires a queryTemplateResource to a client pointing at srv.
func newQueryTemplateResource(t *testing.T, srv *httptest.Server) *queryTemplateResource {
	t.Helper()
	r := NewQueryTemplateResource().(*queryTemplateResource)
	resp := &resource.ConfigureResponse{}
	client := pgbeam.NewClient(&pgbeam.ClientOptions{APIKey: "pgb_test", BaseURL: srv.URL})
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: client}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Configure: %v", resp.Diagnostics)
	}
	return r
}

const queryTemplateJSON = `{
	"id": "qtp_1",
	"project_id": "prj_1",
	"name": "orders_for_customer",
	"description": "Open orders for one customer.",
	"sql": "SELECT id FROM orders WHERE customer_id = $1 AND placed_after > $2",
	"params": [
		{"name": "customer_id", "type": "bigint", "nullable": false},
		{"name": "placed_after", "type": "timestamptz", "nullable": true}
	],
	"statement_kind": "select",
	"include_sql": false,
	"enabled": true,
	"created_at": "2026-01-01T00:00:00Z",
	"updated_at": "2026-01-02T00:00:00Z"
}`

// queryTemplateNoParamsJSON is a template with no parameters: the API returns an empty list.
const queryTemplateNoParamsJSON = `{
	"id": "qtp_1",
	"project_id": "prj_1",
	"name": "all_open_orders",
	"description": "",
	"sql": "SELECT id FROM orders WHERE status = 'open'",
	"params": [],
	"statement_kind": "select",
	"include_sql": false,
	"enabled": true,
	"created_at": "2026-01-01T00:00:00Z",
	"updated_at": "2026-01-02T00:00:00Z"
}`

type testParam struct {
	name     string
	typ      string
	nullable bool
}

func paramsList(ps ...testParam) types.List {
	vals := make([]attr.Value, len(ps))
	for i, p := range ps {
		vals[i] = types.ObjectValueMust(paramsElemAttrTypes(), map[string]attr.Value{
			"name":     types.StringValue(p.name),
			"type":     types.StringValue(p.typ),
			"nullable": types.BoolValue(p.nullable),
		})
	}
	return types.ListValueMust(types.ObjectType{AttrTypes: paramsElemAttrTypes()}, vals)
}

// requireParams checks the params a request body carried, in order: the order
// is what binds $1, $2, so a reordering binds the wrong argument to each.
func requireParams(t *testing.T, body map[string]any, want ...testParam) {
	t.Helper()
	got, _ := body["params"].([]any)
	if len(got) != len(want) {
		t.Fatalf("params = %v, want %d entries", body["params"], len(want))
	}
	for i, w := range want {
		p, _ := got[i].(map[string]any)
		if p["name"] != w.name || p["type"] != w.typ || p["nullable"] != w.nullable {
			t.Errorf("params[%d] = %v, want %+v", i, p, w)
		}
	}
}

func TestQueryTemplateResource_Create_SendsParamsInOrder(t *testing.T) {
	t.Parallel()

	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost || req.URL.Path != "/v1/projects/prj_1/query-templates" {
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
		}
		body = decodeBody(t, req)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(queryTemplateJSON))
	}))
	defer srv.Close()

	r := newQueryTemplateResource(t, srv)
	s := resourceSchema(t, NewQueryTemplateResource())

	params := []testParam{
		{name: "customer_id", typ: "bigint"},
		{name: "placed_after", typ: "timestamptz", nullable: true},
	}
	plan := tfsdk.Plan{Schema: s}
	if d := plan.Set(context.Background(), &queryTemplateResourceModel{
		ProjectID:     types.StringValue("prj_1"),
		Name:          types.StringValue("orders_for_customer"),
		Description:   types.StringValue("Open orders for one customer."),
		SQL:           types.StringValue("SELECT id FROM orders WHERE customer_id = $1 AND placed_after > $2"),
		Params:        paramsList(params...),
		StatementKind: types.StringUnknown(),
		IncludeSQL:    types.BoolValue(false),
		Enabled:       types.BoolValue(true),
	}); d.HasError() {
		t.Fatalf("seed plan: %v", d)
	}

	resp := &resource.CreateResponse{State: tfsdk.State{Schema: s}}
	r.Create(context.Background(), resource.CreateRequest{Plan: plan}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Create diagnostics: %v", resp.Diagnostics)
	}

	requireParams(t, body, params...)
	if body["include_sql"] != false || body["enabled"] != true {
		t.Errorf("include_sql = %v, enabled = %v, want false and true", body["include_sql"], body["enabled"])
	}

	var out queryTemplateResourceModel
	if d := resp.State.Get(context.Background(), &out); d.HasError() {
		t.Fatalf("create state: %v", d)
	}
	if out.ID.ValueString() != "qtp_1" || out.StatementKind.ValueString() != "select" {
		t.Errorf("state = %+v, want the created template with its statement kind", out)
	}
	if !out.Params.Equal(paramsList(params...)) {
		t.Errorf("state params = %v, want the planned params", out.Params)
	}
}

// An omitted params plans [] (no parameters), so state must read back [] to match the plan.
func TestQueryTemplateResource_Create_NoParamsReadBackEmpty(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(queryTemplateNoParamsJSON))
	}))
	defer srv.Close()

	r := newQueryTemplateResource(t, srv)
	s := resourceSchema(t, NewQueryTemplateResource())

	plan := tfsdk.Plan{Schema: s}
	if d := plan.Set(context.Background(), &queryTemplateResourceModel{
		ProjectID:     types.StringValue("prj_1"),
		Name:          types.StringValue("all_open_orders"),
		Description:   types.StringValue(""),
		SQL:           types.StringValue("SELECT id FROM orders WHERE status = 'open'"),
		Params:        paramsList(),
		StatementKind: types.StringUnknown(),
		IncludeSQL:    types.BoolValue(false),
		Enabled:       types.BoolValue(true),
	}); d.HasError() {
		t.Fatalf("seed plan: %v", d)
	}

	resp := &resource.CreateResponse{State: tfsdk.State{Schema: s}}
	r.Create(context.Background(), resource.CreateRequest{Plan: plan}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Create diagnostics: %v", resp.Diagnostics)
	}

	var out queryTemplateResourceModel
	if d := resp.State.Get(context.Background(), &out); d.HasError() {
		t.Fatalf("create state: %v", d)
	}
	if out.Params.IsNull() || len(out.Params.Elements()) != 0 {
		t.Errorf("params = %v, want [] to match the planned default", out.Params)
	}
	if out.Description.IsNull() || out.Description.ValueString() != "" {
		t.Errorf("description = %v, want \"\" to match the planned default", out.Description)
	}
}

// The update is a PUT, so fields the plan did not change must still be sent,
// params above all: a PUT without them stores a template with no parameters,
// which then fails to compile against its own placeholders.
func TestQueryTemplateResource_Update_SendsWholeBody(t *testing.T) {
	t.Parallel()

	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if req.Method == http.MethodPut {
			if req.URL.Path != "/v1/projects/prj_1/query-templates/qtp_1" {
				t.Errorf("unexpected update path: %s", req.URL.Path)
			}
			body = decodeBody(t, req)
		}
		_, _ = w.Write([]byte(queryTemplateJSON))
	}))
	defer srv.Close()

	r := newQueryTemplateResource(t, srv)
	s := resourceSchema(t, NewQueryTemplateResource())

	params := []testParam{
		{name: "customer_id", typ: "bigint"},
		{name: "placed_after", typ: "timestamptz", nullable: true},
	}
	model := queryTemplateResourceModel{
		ID:            types.StringValue("qtp_1"),
		ProjectID:     types.StringValue("prj_1"),
		Name:          types.StringValue("orders_for_customer"),
		Description:   types.StringValue("Open orders for one customer."),
		SQL:           types.StringValue("SELECT id FROM orders WHERE customer_id = $1 AND placed_after > $2"),
		Params:        paramsList(params...),
		StatementKind: types.StringValue("select"),
		IncludeSQL:    types.BoolValue(false),
		Enabled:       types.BoolValue(true),
		CreatedAt:     types.StringValue("2026-01-01T00:00:00Z"),
		UpdatedAt:     types.StringValue("2026-01-02T00:00:00Z"),
	}
	state := tfsdk.State{Schema: s}
	if d := state.Set(context.Background(), &model); d.HasError() {
		t.Fatalf("seed state: %v", d)
	}
	model.Enabled = types.BoolValue(false)
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
	if body["enabled"] != false {
		t.Errorf("enabled = %v, want false", body["enabled"])
	}
	if body["name"] != "orders_for_customer" || body["sql"] != model.SQL.ValueString() ||
		body["description"] != "Open orders for one customer." {
		t.Errorf("unchanged fields must still be sent, body = %v", body)
	}
	requireParams(t, body, params...)
}
