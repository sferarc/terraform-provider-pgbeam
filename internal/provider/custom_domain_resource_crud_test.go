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

// newCustomDomainResource wires a custom domainResource to a client pointing at srv.
func newCustomDomainResource(t *testing.T, srv *httptest.Server) *customDomainResource {
	t.Helper()
	r := NewCustomDomainResource().(*customDomainResource)
	resp := &resource.ConfigureResponse{}
	client := pgbeam.NewClient(&pgbeam.ClientOptions{APIKey: "pgb_test", BaseURL: srv.URL})
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: client}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Configure: %v", resp.Diagnostics)
	}
	return r
}

const (
	domainsPath      = "/v1/projects/prj_1/domains"
	customDomainJSON = `{
		"id": "dom_1",
		"project_id": "prj_1",
		"domain": "db.example.com",
		"verified": true,
		"verified_at": "2026-01-03T00:00:00Z",
		"dns_verification_token": "pgbeam-verify=abc",
		"dns_instructions": {"txt_host": "_pgbeam.db.example.com", "txt_value": "pgbeam-verify=abc"},
		"created_at": "2026-01-01T00:00:00Z",
		"updated_at": "2026-01-02T00:00:00Z"
	}`
	otherCustomDomainJSON = `{
		"id": "dom_0",
		"project_id": "prj_1",
		"domain": "other.example.com",
		"verified": false,
		"dns_verification_token": "pgbeam-verify=zzz",
		"created_at": "2026-01-01T00:00:00Z",
		"updated_at": "2026-01-01T00:00:00Z"
	}`
)

func customDomainState(t *testing.T) tfsdk.State {
	t.Helper()
	state := tfsdk.State{Schema: resourceSchema(t, NewCustomDomainResource())}
	if d := state.Set(context.Background(), &customDomainResourceModel{
		ID:              types.StringValue("dom_1"),
		ProjectID:       types.StringValue("prj_1"),
		Domain:          types.StringValue("db.example.com"),
		Verified:        types.BoolValue(false),
		DNSInstructions: types.ObjectNull(dnsInstructionsAttrTypes()),
	}); d.HasError() {
		t.Fatalf("seed state: %v", d)
	}
	return state
}

func readCustomDomain(t *testing.T, r *customDomainResource) *resource.ReadResponse {
	t.Helper()
	return readState(t, r, customDomainState(t))
}

// TestCustomDomainResource_Read_FindsEntryOnLaterPage asserts Read follows
// next_page_token until it finds the custom domain, sending the cursor back.
func TestCustomDomainResource_Read_FindsEntryOnLaterPage(t *testing.T) {
	t.Parallel()

	var lp listPages
	srv := httptest.NewServer(lp.handler(t, domainsPath, map[string]string{
		"":   `{"domains": [` + otherCustomDomainJSON + `], "next_page_token": "p2"}`,
		"p2": `{"domains": [` + customDomainJSON + `]}`,
	}))
	defer srv.Close()

	resp := readCustomDomain(t, newCustomDomainResource(t, srv))
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read diagnostics: %v", resp.Diagnostics)
	}
	assertTokens(t, &lp, "", "p2")

	var out customDomainResourceModel
	if d := resp.State.Get(context.Background(), &out); d.HasError() {
		t.Fatalf("read state: %v", d)
	}
	if !out.Verified.ValueBool() || out.VerifiedAt.ValueString() != "2026-01-03T00:00:00Z" {
		t.Errorf("state = (%v, %q), want verified at 2026-01-03", out.Verified.ValueBool(), out.VerifiedAt.ValueString())
	}
}

// TestCustomDomainResource_Read_NoMatchRemovesResource asserts a custom domain missing from
// every page drops out of state.
func TestCustomDomainResource_Read_NoMatchRemovesResource(t *testing.T) {
	t.Parallel()

	var lp listPages
	srv := httptest.NewServer(lp.handler(t, domainsPath, map[string]string{
		"":   `{"domains": [` + otherCustomDomainJSON + `], "next_page_token": "p2"}`,
		"p2": `{"domains": [], "next_page_token": ""}`,
	}))
	defer srv.Close()

	resp := readCustomDomain(t, newCustomDomainResource(t, srv))
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read diagnostics: %v", resp.Diagnostics)
	}
	if !resp.State.Raw.IsNull() {
		t.Error("state was not removed for a custom domain that is gone upstream")
	}
	assertTokens(t, &lp, "", "p2")
}

// TestCustomDomainResource_Read_ListErrorKeepsResource asserts a failing list call
// surfaces an error rather than being read as "not found".
func TestCustomDomainResource_Read_ListErrorKeepsResource(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	resp := readCustomDomain(t, newCustomDomainResource(t, srv))
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected diagnostics for a failing list call")
	}
	if resp.State.Raw.IsNull() {
		t.Error("state was removed on a list error")
	}
}

// TestCustomDomainResource_Delete_TolerantOf404 asserts a custom domain already removed out
// of band does not fail the destroy.
func TestCustomDomainResource_Delete_TolerantOf404(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodDelete || req.URL.Path != domainsPath+"/dom_1" {
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	state := customDomainState(t)
	resp := &resource.DeleteResponse{State: state}
	newCustomDomainResource(t, srv).Delete(context.Background(), resource.DeleteRequest{State: state}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Delete diagnostics: %v", resp.Diagnostics)
	}
}

// TestCustomDomainResource_ImportState splits project_id/domain_id and hydrates
// state from the list.
func TestCustomDomainResource_ImportState(t *testing.T) {
	t.Parallel()

	var lp listPages
	srv := httptest.NewServer(lp.handler(t, domainsPath, map[string]string{
		"": `{"domains": [` + customDomainJSON + `]}`,
	}))
	defer srv.Close()

	s := resourceSchema(t, NewCustomDomainResource())
	resp := &resource.ImportStateResponse{State: tfsdk.State{Schema: s}}
	newCustomDomainResource(t, srv).ImportState(context.Background(), resource.ImportStateRequest{ID: "prj_1/dom_1"}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("ImportState diagnostics: %v", resp.Diagnostics)
	}

	var out customDomainResourceModel
	if d := resp.State.Get(context.Background(), &out); d.HasError() {
		t.Fatalf("import state: %v", d)
	}
	if out.ID.ValueString() != "dom_1" || out.ProjectID.ValueString() != "prj_1" {
		t.Errorf("imported (%q, %q), want (dom_1, prj_1)", out.ID.ValueString(), out.ProjectID.ValueString())
	}
	if out.Domain.ValueString() != "db.example.com" || out.DNSVerificationToken.ValueString() != "pgbeam-verify=abc" {
		t.Errorf("imported (%q, %q)", out.Domain.ValueString(), out.DNSVerificationToken.ValueString())
	}
}

func TestCustomDomainResource_ImportState_RejectsBareID(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("no request expected")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	s := resourceSchema(t, NewCustomDomainResource())
	resp := &resource.ImportStateResponse{State: tfsdk.State{Schema: s}}
	newCustomDomainResource(t, srv).ImportState(context.Background(), resource.ImportStateRequest{ID: "dom_1"}, resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected diagnostics for a bare import ID")
	}
}
