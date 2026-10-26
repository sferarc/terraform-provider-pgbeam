package provider

import (
	"slices"
	"strings"
	"testing"
)

func TestSplitCompositeID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		id        string
		names     []string
		wantParts []string
	}{
		{"two parts", "proj/db", []string{"project_id", "database_id"}, []string{"proj", "db"}},
		{"three parts", "proj/db/hash", []string{"project_id", "database_id", "query_hash"}, []string{"proj", "db", "hash"}},
		{"too few parts", "proj", []string{"project_id", "database_id"}, nil},
		{"extra segment", "a/b/c", []string{"project_id", "database_id"}, nil},
		{"empty tail", "a/", []string{"project_id", "database_id"}, nil},
		{"empty head", "/b", []string{"project_id", "database_id"}, nil},
		{"empty middle", "a//b", []string{"project_id", "database_id"}, nil},
		{"whitespace segment", " /b", []string{"project_id", "database_id"}, nil},
		{"empty id", "", []string{"project_id", "database_id"}, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			parts, diags := splitCompositeID(tt.id, tt.names...)
			if tt.wantParts == nil {
				if !diags.HasError() {
					t.Fatalf("splitCompositeID(%q) = %v, want error", tt.id, parts)
				}
				if parts != nil {
					t.Errorf("splitCompositeID(%q) parts = %v, want nil on error", tt.id, parts)
				}
				detail := diags.Errors()[0].Detail()
				if want := strings.Join(tt.names, "/"); !strings.Contains(detail, want) {
					t.Errorf("error detail %q does not name expected format %q", detail, want)
				}
				return
			}
			if diags.HasError() {
				t.Fatalf("splitCompositeID(%q) unexpected error: %v", tt.id, diags)
			}
			if !slices.Equal(parts, tt.wantParts) {
				t.Errorf("splitCompositeID(%q) = %v, want %v", tt.id, parts, tt.wantParts)
			}
		})
	}
}

func TestObjectAsOptions(t *testing.T) {
	t.Parallel()

	opts := objectAsOptions()
	if !opts.UnhandledNullAsEmpty {
		t.Error("expected UnhandledNullAsEmpty to be true")
	}
	if !opts.UnhandledUnknownAsEmpty {
		t.Error("expected UnhandledUnknownAsEmpty to be true")
	}
}
