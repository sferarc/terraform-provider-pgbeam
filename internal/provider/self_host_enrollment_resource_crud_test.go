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

// newSelfHostEnrollmentResource wires an enrollmentResource to a client pointing at srv.
func newSelfHostEnrollmentResource(t *testing.T, srv *httptest.Server) *selfHostEnrollmentResource {
	t.Helper()
	r := NewSelfHostEnrollmentResource().(*selfHostEnrollmentResource)
	resp := &resource.ConfigureResponse{}
	client := pgbeam.NewClient(&pgbeam.ClientOptions{APIKey: "pgb_test", BaseURL: srv.URL})
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: client}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Configure: %v", resp.Diagnostics)
	}
	return r
}

const (
	enrollmentsPath = "/v1/organizations/org_1/self-host-enrollments"
	enrollmentJSON  = `{
		"id": "she_1",
		"org_id": "org_1",
		"region_label": "customer-vpc-us-east",
		"description": "prod cluster",
		"last_seen_at": "2026-01-03T00:00:00Z",
		"created_at": "2026-01-01T00:00:00Z"
	}`
	otherEnrollmentJSON = `{
		"id": "she_0",
		"org_id": "org_1",
		"created_at": "2026-01-01T00:00:00Z"
	}`
	enrollmentToken = "enrollment-token-fixture"
)

func selfHostEnrollmentState(t *testing.T, token string) tfsdk.State {
	t.Helper()
	state := tfsdk.State{Schema: resourceSchema(t, NewSelfHostEnrollmentResource())}
	if d := state.Set(context.Background(), &selfHostEnrollmentResourceModel{
		ID:    types.StringValue("she_1"),
		OrgID: types.StringValue("org_1"),
		Token: types.StringValue(token),
	}); d.HasError() {
		t.Fatalf("seed state: %v", d)
	}
	return state
}

func readSelfHostEnrollment(t *testing.T, r *selfHostEnrollmentResource, token string) *resource.ReadResponse {
	t.Helper()
	return readState(t, r, selfHostEnrollmentState(t, token))
}

// TestSelfHostEnrollmentResource_Read_FindsEntryOnLaterPage asserts Read follows
// next_page_token until it finds the enrollment, sending the cursor back, and
// keeps the token: the API returns it only on create, so losing it on refresh
// would leave the proxy with nothing to enroll with.
func TestSelfHostEnrollmentResource_Read_FindsEntryOnLaterPage(t *testing.T) {
	t.Parallel()

	var lp listPages
	srv := httptest.NewServer(lp.handler(t, enrollmentsPath, map[string]string{
		"":   `{"enrollments": [` + otherEnrollmentJSON + `], "next_page_token": "p2"}`,
		"p2": `{"enrollments": [` + enrollmentJSON + `]}`,
	}))
	defer srv.Close()

	resp := readSelfHostEnrollment(t, newSelfHostEnrollmentResource(t, srv), enrollmentToken)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read diagnostics: %v", resp.Diagnostics)
	}
	assertTokens(t, &lp, "", "p2")

	var out selfHostEnrollmentResourceModel
	if d := resp.State.Get(context.Background(), &out); d.HasError() {
		t.Fatalf("read state: %v", d)
	}
	if out.RegionLabel.ValueString() != "customer-vpc-us-east" || out.LastSeenAt.ValueString() != "2026-01-03T00:00:00Z" {
		t.Errorf("state = (%q, %q)", out.RegionLabel.ValueString(), out.LastSeenAt.ValueString())
	}
	if out.Token.ValueString() != enrollmentToken {
		t.Errorf("Token = %q, want it preserved across Read", out.Token.ValueString())
	}
}

// TestSelfHostEnrollmentResource_Read_NoMatchRemovesResource asserts an enrollment missing from
// every page drops out of state.
func TestSelfHostEnrollmentResource_Read_NoMatchRemovesResource(t *testing.T) {
	t.Parallel()

	var lp listPages
	srv := httptest.NewServer(lp.handler(t, enrollmentsPath, map[string]string{
		"":   `{"enrollments": [` + otherEnrollmentJSON + `], "next_page_token": "p2"}`,
		"p2": `{"enrollments": [], "next_page_token": ""}`,
	}))
	defer srv.Close()

	resp := readSelfHostEnrollment(t, newSelfHostEnrollmentResource(t, srv), enrollmentToken)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read diagnostics: %v", resp.Diagnostics)
	}
	if !resp.State.Raw.IsNull() {
		t.Error("state was not removed for an enrollment that is gone upstream")
	}
	assertTokens(t, &lp, "", "p2")
}

// TestSelfHostEnrollmentResource_Read_ListErrorKeepsResource asserts a failing list call
// surfaces an error rather than being read as "not found".
func TestSelfHostEnrollmentResource_Read_ListErrorKeepsResource(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	resp := readSelfHostEnrollment(t, newSelfHostEnrollmentResource(t, srv), enrollmentToken)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected diagnostics for a failing list call")
	}
	if resp.State.Raw.IsNull() {
		t.Error("state was removed on a list error")
	}
}

// TestSelfHostEnrollmentResource_Delete_TolerantOf404 asserts an enrollment already removed out
// of band does not fail the destroy.
func TestSelfHostEnrollmentResource_Delete_TolerantOf404(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodDelete || req.URL.Path != enrollmentsPath+"/she_1" {
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	state := selfHostEnrollmentState(t, enrollmentToken)
	resp := &resource.DeleteResponse{State: state}
	newSelfHostEnrollmentResource(t, srv).Delete(context.Background(), resource.DeleteRequest{State: state}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Delete diagnostics: %v", resp.Diagnostics)
	}
}

// TestSelfHostEnrollmentResource_ImportState splits org_id/enrollment_id and
// hydrates state from the list.
func TestSelfHostEnrollmentResource_ImportState(t *testing.T) {
	t.Parallel()

	var lp listPages
	srv := httptest.NewServer(lp.handler(t, enrollmentsPath, map[string]string{
		"": `{"enrollments": [` + enrollmentJSON + `]}`,
	}))
	defer srv.Close()

	s := resourceSchema(t, NewSelfHostEnrollmentResource())
	resp := &resource.ImportStateResponse{State: tfsdk.State{Schema: s}}
	newSelfHostEnrollmentResource(t, srv).ImportState(context.Background(), resource.ImportStateRequest{ID: "org_1/she_1"}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("ImportState diagnostics: %v", resp.Diagnostics)
	}

	var out selfHostEnrollmentResourceModel
	if d := resp.State.Get(context.Background(), &out); d.HasError() {
		t.Fatalf("import state: %v", d)
	}
	if out.ID.ValueString() != "she_1" || out.OrgID.ValueString() != "org_1" {
		t.Errorf("imported (%q, %q), want (she_1, org_1)", out.ID.ValueString(), out.OrgID.ValueString())
	}
	if out.Description.ValueString() != "prod cluster" {
		t.Errorf("Description = %q", out.Description.ValueString())
	}
}

func TestSelfHostEnrollmentResource_ImportState_RejectsBareID(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("no request expected")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	s := resourceSchema(t, NewSelfHostEnrollmentResource())
	resp := &resource.ImportStateResponse{State: tfsdk.State{Schema: s}}
	newSelfHostEnrollmentResource(t, srv).ImportState(context.Background(), resource.ImportStateRequest{ID: "she_1"}, resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected diagnostics for a bare import ID")
	}
}
