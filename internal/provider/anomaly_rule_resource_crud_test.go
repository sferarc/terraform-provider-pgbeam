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

// The anomaly rule is the first IaC resource whose inputs include a nullable
// number, and null carries meaning on all three of them: a null sigma_threshold
// or floor means "leave the deployment default in place", and a null
// credential_id means "every credential in the project". The API refuses a
// sigma or a floor at or below zero, so a nullable float that round-trips
// through state as 0 is not merely wrong, it is a value the next plan would try
// to send and the API would reject with 400. These tests drive the generated
// resource against a fake API to pin that, plus the full-replacement update
// the PUT requires.

// newAnomalyRuleResource wires an anomalyRuleResource to a client pointing at srv.
func newAnomalyRuleResource(t *testing.T, srv *httptest.Server) *anomalyRuleResource {
	t.Helper()
	r := NewAnomalyRuleResource().(*anomalyRuleResource)
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

// anomalyRuleJSON is a credential-scoped rule with both numbers set.
const anomalyRuleJSON = `{
	"id": "anr_1",
	"project_id": "prj_1",
	"credential_id": "agt_1",
	"metric": "queries_per_hour",
	"sigma_threshold": 4.5,
	"floor": 100,
	"enabled": true,
	"created_at": "2026-01-01T00:00:00Z",
	"updated_at": "2026-01-02T00:00:00Z"
}`

// anomalyRuleDefaultsJSON is a project-wide rule left on the deployment
// defaults: credential_id, sigma_threshold and floor are all absent.
const anomalyRuleDefaultsJSON = `{
	"id": "anr_1",
	"project_id": "prj_1",
	"metric": "bytes_per_hour",
	"enabled": true,
	"created_at": "2026-01-01T00:00:00Z",
	"updated_at": "2026-01-02T00:00:00Z"
}`

// TestAnomalyRuleResource_Create_SendsTypedMetric asserts the create body carries
// the enum-valued metric. metric is required, so it reaches the SDK through the
// request struct literal rather than the guarded optional path, which is the
// path that has to cast the enum.
func TestAnomalyRuleResource_Create_SendsTypedMetric(t *testing.T) {
	t.Parallel()

	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost || req.URL.Path != "/v1/projects/prj_1/anomaly-rules" {
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
		}
		body = decodeBody(t, req)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(anomalyRuleJSON))
	}))
	defer srv.Close()

	r := newAnomalyRuleResource(t, srv)
	s := resourceSchema(t, NewAnomalyRuleResource())

	plan := tfsdk.Plan{Schema: s}
	if d := plan.Set(context.Background(), &anomalyRuleResourceModel{
		ProjectID:      types.StringValue("prj_1"),
		CredentialID:   types.StringValue("agt_1"),
		Metric:         types.StringValue("queries_per_hour"),
		SigmaThreshold: types.Float64Value(4.5),
		Floor:          types.Float64Value(100),
		Enabled:        types.BoolValue(true),
	}); d.HasError() {
		t.Fatalf("seed plan: %v", d)
	}

	resp := &resource.CreateResponse{State: tfsdk.State{Schema: s}}
	r.Create(context.Background(), resource.CreateRequest{Plan: plan}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Create diagnostics: %v", resp.Diagnostics)
	}

	if body["metric"] != "queries_per_hour" {
		t.Errorf("metric = %v, want queries_per_hour", body["metric"])
	}
	if body["credential_id"] != "agt_1" {
		t.Errorf("credential_id = %v, want agt_1", body["credential_id"])
	}
	if body["sigma_threshold"] != 4.5 {
		t.Errorf("sigma_threshold = %v, want 4.5", body["sigma_threshold"])
	}
	if body["floor"] != float64(100) {
		t.Errorf("floor = %v, want 100", body["floor"])
	}

	var out anomalyRuleResourceModel
	if d := resp.State.Get(context.Background(), &out); d.HasError() {
		t.Fatalf("create state: %v", d)
	}
	if out.ID.ValueString() != "anr_1" {
		t.Errorf("ID = %q, want anr_1", out.ID.ValueString())
	}
}

// TestAnomalyRuleResource_Create_OmitsUnsetNumbers asserts a rule that leaves
// the sensitivity alone sends no sigma_threshold and no floor at all. Sending
// either as 0 would be rejected (the API refuses a non-positive value, because
// the detector reads one as "use the default" and could not store it), and
// sending null is how a customer asks for the deployment default.
func TestAnomalyRuleResource_Create_OmitsUnsetNumbers(t *testing.T) {
	t.Parallel()

	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body = decodeBody(t, req)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(anomalyRuleDefaultsJSON))
	}))
	defer srv.Close()

	r := newAnomalyRuleResource(t, srv)
	s := resourceSchema(t, NewAnomalyRuleResource())

	plan := tfsdk.Plan{Schema: s}
	if d := plan.Set(context.Background(), &anomalyRuleResourceModel{
		ProjectID: types.StringValue("prj_1"),
		Metric:    types.StringValue("bytes_per_hour"),
	}); d.HasError() {
		t.Fatalf("seed plan: %v", d)
	}

	resp := &resource.CreateResponse{State: tfsdk.State{Schema: s}}
	r.Create(context.Background(), resource.CreateRequest{Plan: plan}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Create diagnostics: %v", resp.Diagnostics)
	}

	for _, key := range []string{"sigma_threshold", "floor", "credential_id"} {
		if v, ok := body[key]; ok {
			t.Errorf("%s = %v, want absent when the attribute is null", key, v)
		}
	}
	if body["metric"] != "bytes_per_hour" {
		t.Errorf("metric = %v, want bytes_per_hour", body["metric"])
	}

	var out anomalyRuleResourceModel
	if d := resp.State.Get(context.Background(), &out); d.HasError() {
		t.Fatalf("create state: %v", d)
	}
	if !out.SigmaThreshold.IsNull() {
		t.Errorf("SigmaThreshold = %v, want null (0 is a value the API refuses)", out.SigmaThreshold.ValueFloat64())
	}
	if !out.Floor.IsNull() {
		t.Errorf("Floor = %v, want null (0 is a value the API refuses)", out.Floor.ValueFloat64())
	}
	if !out.CredentialID.IsNull() {
		t.Errorf("CredentialID = %q, want null for a project-wide rule", out.CredentialID.ValueString())
	}
}

// TestAnomalyRuleResource_Update_SendsWholeBody is the regression test for the
// full-replacement update. PUT /anomaly-rules/{id} takes the same body as the
// POST and the handler rebuilds the row from it, so an update that only moves
// the floor must still carry the metric. A diff-only body would be rejected with
// 400 for the missing required metric and, on the optional fields, would clear
// whatever the plan left alone.
func TestAnomalyRuleResource_Update_SendsWholeBody(t *testing.T) {
	t.Parallel()

	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case http.MethodPut:
			body = decodeBody(t, req)
			_, _ = w.Write([]byte(anomalyRuleJSON))
		case http.MethodGet:
			_, _ = w.Write([]byte(anomalyRuleJSON))
		default:
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
		}
	}))
	defer srv.Close()

	r := newAnomalyRuleResource(t, srv)
	s := resourceSchema(t, NewAnomalyRuleResource())

	state := tfsdk.State{Schema: s}
	if d := state.Set(context.Background(), &anomalyRuleResourceModel{
		ID:             types.StringValue("anr_1"),
		ProjectID:      types.StringValue("prj_1"),
		CredentialID:   types.StringValue("agt_1"),
		Metric:         types.StringValue("queries_per_hour"),
		SigmaThreshold: types.Float64Value(4.5),
		Floor:          types.Float64Value(50),
		Enabled:        types.BoolValue(true),
	}); d.HasError() {
		t.Fatalf("seed state: %v", d)
	}

	// Only the floor moves.
	plan := tfsdk.Plan{Schema: s}
	if d := plan.Set(context.Background(), &anomalyRuleResourceModel{
		ID:             types.StringValue("anr_1"),
		ProjectID:      types.StringValue("prj_1"),
		CredentialID:   types.StringValue("agt_1"),
		Metric:         types.StringValue("queries_per_hour"),
		SigmaThreshold: types.Float64Value(4.5),
		Floor:          types.Float64Value(100),
		Enabled:        types.BoolValue(true),
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
	if body["floor"] != float64(100) {
		t.Errorf("floor = %v, want 100", body["floor"])
	}
	if body["metric"] != "queries_per_hour" {
		t.Errorf("metric = %v, want queries_per_hour (unchanged fields must still be sent)", body["metric"])
	}
	if body["sigma_threshold"] != 4.5 {
		t.Errorf("sigma_threshold = %v, want 4.5 (unchanged fields must still be sent)", body["sigma_threshold"])
	}
	if body["credential_id"] != "agt_1" {
		t.Errorf("credential_id = %v, want agt_1 (unchanged fields must still be sent)", body["credential_id"])
	}
}

// TestAnomalyRuleResource_Update_ClearingSigmaOmitsIt is the other half of the
// full-replacement contract, and the one a presence-guarded optional field could
// get wrong in the opposite direction. Removing sigma_threshold from the config
// is how a customer hands the metric back to the deployment default, so the
// update has to be sent and the body must not carry the old value.
func TestAnomalyRuleResource_Update_ClearingSigmaOmitsIt(t *testing.T) {
	t.Parallel()

	var body map[string]any
	puts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case http.MethodPut:
			puts++
			body = decodeBody(t, req)
			_, _ = w.Write([]byte(anomalyRuleDefaultsJSON))
		case http.MethodGet:
			_, _ = w.Write([]byte(anomalyRuleDefaultsJSON))
		default:
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
		}
	}))
	defer srv.Close()

	r := newAnomalyRuleResource(t, srv)
	s := resourceSchema(t, NewAnomalyRuleResource())

	state := tfsdk.State{Schema: s}
	if d := state.Set(context.Background(), &anomalyRuleResourceModel{
		ID:             types.StringValue("anr_1"),
		ProjectID:      types.StringValue("prj_1"),
		Metric:         types.StringValue("bytes_per_hour"),
		SigmaThreshold: types.Float64Value(4.5),
		Enabled:        types.BoolValue(true),
	}); d.HasError() {
		t.Fatalf("seed state: %v", d)
	}

	plan := tfsdk.Plan{Schema: s}
	if d := plan.Set(context.Background(), &anomalyRuleResourceModel{
		ID:        types.StringValue("anr_1"),
		ProjectID: types.StringValue("prj_1"),
		Metric:    types.StringValue("bytes_per_hour"),
		Enabled:   types.BoolValue(true),
	}); d.HasError() {
		t.Fatalf("seed plan: %v", d)
	}

	resp := &resource.UpdateResponse{State: tfsdk.State{Schema: s}}
	r.Update(context.Background(), resource.UpdateRequest{Plan: plan, State: state}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Update diagnostics: %v", resp.Diagnostics)
	}

	if puts != 1 {
		t.Fatalf("PUT count = %d, want 1: dropping a sensitivity is a change", puts)
	}
	if v, ok := body["sigma_threshold"]; ok {
		t.Errorf("sigma_threshold = %v, want absent so the metric returns to the deployment default", v)
	}

	var out anomalyRuleResourceModel
	if d := resp.State.Get(context.Background(), &out); d.HasError() {
		t.Fatalf("update state: %v", d)
	}
	if !out.SigmaThreshold.IsNull() {
		t.Errorf("SigmaThreshold = %v, want null after clearing it", out.SigmaThreshold.ValueFloat64())
	}
}

// TestAnomalyRuleResource_Update_NoDiffSkipsCall asserts the plan/state diff
// still gates the call: an update with nothing changed must not PUT.
func TestAnomalyRuleResource_Update_NoDiffSkipsCall(t *testing.T) {
	t.Parallel()

	puts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodPut {
			puts++
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(anomalyRuleJSON))
	}))
	defer srv.Close()

	r := newAnomalyRuleResource(t, srv)
	s := resourceSchema(t, NewAnomalyRuleResource())

	same := func() *anomalyRuleResourceModel {
		return &anomalyRuleResourceModel{
			ID:             types.StringValue("anr_1"),
			ProjectID:      types.StringValue("prj_1"),
			CredentialID:   types.StringValue("agt_1"),
			Metric:         types.StringValue("queries_per_hour"),
			SigmaThreshold: types.Float64Value(4.5),
			Floor:          types.Float64Value(100),
			Enabled:        types.BoolValue(true),
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

// TestAnomalyRuleResource_Read_SilencedRuleStaysFalse covers the polarity that
// is the opposite of the honeytoken's. enabled=false silences the metric rather
// than deactivating the rule, so a read of a silenced rule must land false in
// state; a read that dropped it would resume alerting on the next apply.
func TestAnomalyRuleResource_Read_SilencedRuleStaysFalse(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/v1/projects/prj_1/anomaly-rules/anr_1" {
			t.Errorf("path = %s", req.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "anr_1",
			"project_id": "prj_1",
			"metric": "active_hours",
			"enabled": false,
			"created_at": "2026-01-01T00:00:00Z",
			"updated_at": "2026-01-02T00:00:00Z"
		}`))
	}))
	defer srv.Close()

	r := newAnomalyRuleResource(t, srv)
	s := resourceSchema(t, NewAnomalyRuleResource())

	state := tfsdk.State{Schema: s}
	if d := state.Set(context.Background(), &anomalyRuleResourceModel{
		ID:        types.StringValue("anr_1"),
		ProjectID: types.StringValue("prj_1"),
		Metric:    types.StringValue("active_hours"),
		Enabled:   types.BoolValue(false),
	}); d.HasError() {
		t.Fatalf("seed state: %v", d)
	}

	resp := &resource.ReadResponse{State: tfsdk.State{Schema: s}}
	r.Read(context.Background(), resource.ReadRequest{State: state}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read diagnostics: %v", resp.Diagnostics)
	}

	var out anomalyRuleResourceModel
	if d := resp.State.Get(context.Background(), &out); d.HasError() {
		t.Fatalf("read state: %v", d)
	}
	if out.Enabled.IsNull() || out.Enabled.ValueBool() {
		t.Errorf("Enabled = %v, want false for a silenced metric", out.Enabled)
	}
	if out.Metric.ValueString() != "active_hours" {
		t.Errorf("Metric = %q, want active_hours", out.Metric.ValueString())
	}
}

// TestAnomalyRuleResource_Read_GoneRemovesResource asserts a rule deleted out of
// band drops out of state rather than failing the refresh. Deleting a rule
// returns its metric to the deployment default, so the next plan recreating it
// is the correct outcome.
func TestAnomalyRuleResource_Read_GoneRemovesResource(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	r := newAnomalyRuleResource(t, srv)
	s := resourceSchema(t, NewAnomalyRuleResource())

	state := tfsdk.State{Schema: s}
	if d := state.Set(context.Background(), &anomalyRuleResourceModel{
		ID:        types.StringValue("anr_1"),
		ProjectID: types.StringValue("prj_1"),
		Metric:    types.StringValue("queries_per_hour"),
		Enabled:   types.BoolValue(true),
	}); d.HasError() {
		t.Fatalf("seed state: %v", d)
	}

	resp := &resource.ReadResponse{State: tfsdk.State{Schema: s}}
	r.Read(context.Background(), resource.ReadRequest{State: state}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read diagnostics: %v", resp.Diagnostics)
	}
	if !resp.State.Raw.IsNull() {
		t.Error("state was not removed for a rule that is gone upstream")
	}
}

// TestAnomalyRuleResource_Delete_TolerantOf404 asserts a rule already removed out
// of band does not fail the destroy.
func TestAnomalyRuleResource_Delete_TolerantOf404(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	r := newAnomalyRuleResource(t, srv)
	s := resourceSchema(t, NewAnomalyRuleResource())

	state := tfsdk.State{Schema: s}
	if d := state.Set(context.Background(), &anomalyRuleResourceModel{
		ID:        types.StringValue("anr_1"),
		ProjectID: types.StringValue("prj_1"),
		Metric:    types.StringValue("queries_per_hour"),
		Enabled:   types.BoolValue(true),
	}); d.HasError() {
		t.Fatalf("seed state: %v", d)
	}

	resp := &resource.DeleteResponse{State: tfsdk.State{Schema: s}}
	r.Delete(context.Background(), resource.DeleteRequest{State: state}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Delete diagnostics: %v", resp.Diagnostics)
	}
}

// TestAnomalyRuleResource_ImportState splits the composite import ID and
// hydrates state from the API, including the two nullable numbers.
func TestAnomalyRuleResource_ImportState(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/v1/projects/prj_1/anomaly-rules/anr_1" {
			t.Errorf("path = %s", req.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(anomalyRuleJSON))
	}))
	defer srv.Close()

	r := newAnomalyRuleResource(t, srv)
	s := resourceSchema(t, NewAnomalyRuleResource())

	resp := &resource.ImportStateResponse{State: tfsdk.State{Schema: s}}
	r.ImportState(context.Background(), resource.ImportStateRequest{ID: "prj_1/anr_1"}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("ImportState diagnostics: %v", resp.Diagnostics)
	}

	var out anomalyRuleResourceModel
	if d := resp.State.Get(context.Background(), &out); d.HasError() {
		t.Fatalf("import state: %v", d)
	}
	if out.ID.ValueString() != "anr_1" || out.ProjectID.ValueString() != "prj_1" {
		t.Errorf("imported (%q, %q), want (anr_1, prj_1)", out.ID.ValueString(), out.ProjectID.ValueString())
	}
	if out.Metric.ValueString() != "queries_per_hour" {
		t.Errorf("Metric = %q", out.Metric.ValueString())
	}
	if out.SigmaThreshold.ValueFloat64() != 4.5 {
		t.Errorf("SigmaThreshold = %v, want 4.5", out.SigmaThreshold.ValueFloat64())
	}
	if out.Floor.ValueFloat64() != 100 {
		t.Errorf("Floor = %v, want 100", out.Floor.ValueFloat64())
	}
}

// TestAnomalyRuleResource_ImportState_RejectsBareID pins the error on an import
// ID missing the project prefix.
func TestAnomalyRuleResource_ImportState_RejectsBareID(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("no request expected")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	r := newAnomalyRuleResource(t, srv)
	s := resourceSchema(t, NewAnomalyRuleResource())

	resp := &resource.ImportStateResponse{State: tfsdk.State{Schema: s}}
	r.ImportState(context.Background(), resource.ImportStateRequest{ID: "anr_1"}, resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected diagnostics for a bare import ID")
	}
}

// TestAnomalyRuleResource_EnabledIsOptionalAndComputed pins the fix for an apply
// failure this resource would otherwise have INHERITED.
//
// enabled is optional in the request body and defaults to true, but required in
// the response. Emitted Optional alone, a config that omits it plans null, the
// API fills in true, the provider writes true into state, and Terraform Core
// compares the two and fails the apply with "Provider produced inconsistent
// result after apply". Optional plus Computed is the plugin framework's answer:
// the value plans unknown on create and carries forward from prior state after.
//
// The shape is not specific to anomaly rules, which is why the fix is in the
// generator rather than here. scripts/src/iac-optional-computed-defaults.test.ts
// derives every attribute in it from the spec and covers all eight scalars.
func TestAnomalyRuleResource_EnabledIsOptionalAndComputed(t *testing.T) {
	t.Parallel()

	s := resourceSchema(t, NewAnomalyRuleResource())
	attr, ok := s.Attributes["enabled"]
	if !ok {
		t.Fatal("no enabled attribute")
	}
	if !attr.IsOptional() {
		t.Error("enabled is not Optional; a config must be able to silence a metric")
	}
	if !attr.IsComputed() {
		t.Error("enabled is not Computed; a config that omits it fails apply with an inconsistent result")
	}
}

// TestAnomalyRuleResource_Create_EnabledUnknownIsOmitted is the other half of
// that fix. Computed means Terraform plans unknown rather than null for an
// attribute the config leaves out, and an unknown types.Bool answers false to
// ValueBool. If the request builder read the plan value instead of skipping it,
// omitting enabled would silence the metric, which is the exact opposite of the
// default it is meant to take.
func TestAnomalyRuleResource_Create_EnabledUnknownIsOmitted(t *testing.T) {
	t.Parallel()

	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body = decodeBody(t, req)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(anomalyRuleDefaultsJSON))
	}))
	defer srv.Close()

	r := newAnomalyRuleResource(t, srv)
	s := resourceSchema(t, NewAnomalyRuleResource())

	// What Terraform Core hands the provider for an Optional+Computed attribute
	// the config does not set, on a create where there is no prior state.
	plan := tfsdk.Plan{Schema: s}
	if d := plan.Set(context.Background(), &anomalyRuleResourceModel{
		ProjectID: types.StringValue("prj_1"),
		Metric:    types.StringValue("bytes_per_hour"),
		Enabled:   types.BoolUnknown(),
	}); d.HasError() {
		t.Fatalf("seed plan: %v", d)
	}

	resp := &resource.CreateResponse{State: tfsdk.State{Schema: s}}
	r.Create(context.Background(), resource.CreateRequest{Plan: plan}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Create diagnostics: %v", resp.Diagnostics)
	}

	if v, ok := body["enabled"]; ok {
		t.Errorf("enabled = %v, want absent: an unknown plan value must not be sent as false", v)
	}

	var out anomalyRuleResourceModel
	if d := resp.State.Get(context.Background(), &out); d.HasError() {
		t.Fatalf("create state: %v", d)
	}
	if out.Enabled.IsNull() || out.Enabled.IsUnknown() || !out.Enabled.ValueBool() {
		t.Errorf("Enabled = %v, want true: the API default lands in state", out.Enabled)
	}
}
