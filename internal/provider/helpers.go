package provider

import (
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

// objectAsOptions returns default options for Object.As() calls.
func objectAsOptions() basetypes.ObjectAsOptions {
	return basetypes.ObjectAsOptions{UnhandledNullAsEmpty: true, UnhandledUnknownAsEmpty: true}
}

// splitCompositeID splits an import ID on '/' into one non-blank segment per
// name. The SDK interpolates segments into request paths unescaped, so an
// empty or extra segment would address a different API route.
func splitCompositeID(id string, names ...string) ([]string, diag.Diagnostics) {
	var diags diag.Diagnostics
	parts := strings.Split(id, "/")
	valid := len(parts) == len(names)
	for _, p := range parts {
		if strings.TrimSpace(p) == "" {
			valid = false
		}
	}
	if !valid {
		diags.AddAttributeError(
			path.Root("id"),
			"Invalid Import ID",
			fmt.Sprintf("Expected import ID in the format %q, got %q.", strings.Join(names, "/"), id),
		)
		return nil, diags
	}
	return parts, diags
}
