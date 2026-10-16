package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
)

// listPages serves a paginated list endpoint for the resources that have no Get
// and find themselves by paging List. Each body is keyed by the page_token that
// requests it ("" for the first page), and every token received is recorded so a
// test can assert the cursor was sent back.
type listPages struct {
	mu     sync.Mutex
	tokens []string
}

func (lp *listPages) handler(t *testing.T, path string, pages map[string]string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet || req.URL.Path != path {
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		token := req.URL.Query().Get("page_token")
		lp.mu.Lock()
		lp.tokens = append(lp.tokens, token)
		lp.mu.Unlock()
		body, ok := pages[token]
		if !ok {
			t.Errorf("unexpected page_token %q", token)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}
}

func (lp *listPages) sent() []string {
	lp.mu.Lock()
	defer lp.mu.Unlock()
	return append([]string(nil), lp.tokens...)
}

func assertTokens(t *testing.T, lp *listPages, want ...string) {
	t.Helper()
	got := lp.sent()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("page tokens sent = %q, want %q", got, want)
	}
}

// repeatingTokenServer answers every list call with an empty page and the same
// next_page_token. It gives up after a bound so an unguarded loop fails the test
// instead of hanging it.
func repeatingTokenServer(t *testing.T, listKey string) (*httptest.Server, func() int) {
	t.Helper()
	var mu sync.Mutex
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n > 50 {
			t.Error("find kept paging a repeated next_page_token")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{%q: [], "next_page_token": "stuck"}`, listKey)
	}))
	t.Cleanup(srv.Close)
	return srv, func() int {
		mu.Lock()
		defer mu.Unlock()
		return calls
	}
}

// TestListLookup_RepeatedPageTokenStops pins the guard in every generated
// find helper: a server that hands back the same cursor forever must end the
// refresh with an error. It must not be read as "not found", which would drop
// a live resource from state and plan a duplicate.
func TestListLookup_RepeatedPageTokenStops(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		listKey string
		read    func(t *testing.T, srv *httptest.Server) *resource.ReadResponse
	}{
		{"replica", "replicas", func(t *testing.T, srv *httptest.Server) *resource.ReadResponse {
			return readReplica(t, newReplicaResource(t, srv))
		}},
		{"custom_domain", "domains", func(t *testing.T, srv *httptest.Server) *resource.ReadResponse {
			return readCustomDomain(t, newCustomDomainResource(t, srv))
		}},
		{"cache_rule", "entries", func(t *testing.T, srv *httptest.Server) *resource.ReadResponse {
			return readCacheRule(t, newCacheRuleResource(t, srv))
		}},
		{"self_host_enrollment", "enrollments", func(t *testing.T, srv *httptest.Server) *resource.ReadResponse {
			return readSelfHostEnrollment(t, newSelfHostEnrollmentResource(t, srv), "")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv, calls := repeatingTokenServer(t, tc.listKey)
			resp := tc.read(t, srv)
			if !resp.Diagnostics.HasError() {
				t.Error("expected an error for a repeated page token")
			}
			if resp.State.Raw.IsNull() {
				t.Error("state was removed on a pagination failure")
			}
			if n := calls(); n != 2 {
				t.Errorf("list calls = %d, want 2", n)
			}
		})
	}
}

// readState runs Read against seeded state and returns the response.
func readState(t *testing.T, r resource.Resource, state tfsdk.State) *resource.ReadResponse {
	t.Helper()
	resp := &resource.ReadResponse{State: state}
	r.Read(context.Background(), resource.ReadRequest{State: state}, resp)
	return resp
}
