package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	resschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/defaults"
	"github.com/hashicorp/terraform-plugin-framework/types"
	pgbeam "go.pgbeam.com/sdk"
)

// listDefaultPlan returns what a list attribute's framework Default plans, and
// false when it carries none.
func listDefaultPlan(t *testing.T, attr resschema.Attribute) (types.List, bool) {
	t.Helper()
	var d interface {
		DefaultList(context.Context, defaults.ListRequest, *defaults.ListResponse)
	}
	switch a := attr.(type) {
	case resschema.ListAttribute:
		d = a.Default
	case resschema.ListNestedAttribute:
		d = a.Default
	default:
		return types.List{}, false
	}
	if d == nil {
		return types.List{}, false
	}
	resp := &defaults.ListResponse{}
	d.DefaultList(context.Background(), defaults.ListRequest{}, resp)
	return resp.PlanValue, true
}

// TestOptionalComputedLists_DefaultToEmpty sweeps every resource: an
// Optional+Computed list without a Default would hold its prior value when the
// line is deleted, so a table allowlist or a masking rule could never be
// removed by removing it from the config.
func TestOptionalComputedLists_DefaultToEmpty(t *testing.T) {
	t.Parallel()

	seen := 0
	for _, factory := range pgbeamResources() {
		r := factory()
		typeName := resourceTypeName(t, r)
		for name, attr := range resourceSchema(t, r).Attributes {
			switch attr.(type) {
			case resschema.ListAttribute, resschema.ListNestedAttribute:
			default:
				continue
			}
			if !attr.IsOptional() || !attr.IsComputed() {
				continue
			}
			seen++
			qualified := typeName + "." + name
			planned, ok := listDefaultPlan(t, attr)
			if !ok {
				t.Errorf("%s is Optional+Computed with no Default, so deleting it from a "+
					"config silently keeps the prior list", qualified)
				continue
			}
			if planned.IsNull() || planned.IsUnknown() || len(planned.Elements()) != 0 {
				t.Errorf("%s: default plans %s, want an empty list", qualified, planned)
			}
		}
	}
	if seen == 0 {
		t.Fatal("no Optional+Computed list attributes found, so this test checked nothing")
	}
}

// TestPolicyProfileEmptyLists_StateMatchesPlan is the apply-time half: the value
// written to state for an empty API list must equal what the Default planned, or
// Terraform Core fails the apply with "Provider produced inconsistent result".
func TestPolicyProfileEmptyLists_StateMatchesPlan(t *testing.T) {
	t.Parallel()

	r := &policyProfileResource{}
	s := resourceSchema(t, r)

	var state policyProfileResourceModel
	var diags diag.Diagnostics
	r.mapPolicyProfileToState(context.Background(), &state, &pgbeam.PolicyProfile{
		AccessMode:     pgbeam.PolicyProfileAccessMode("read_only"),
		TableAllowlist: []string{},
		TableDenylist:  []string{},
		MaskingRules:   []pgbeam.MaskingRule{},
	}, &diags)
	if diags.HasError() {
		t.Fatalf("mapPolicyProfileToState: %v", diags)
	}

	for name, got := range map[string]types.List{
		"table_allowlist": state.TableAllowlist,
		"table_denylist":  state.TableDenylist,
		"masking_rules":   state.MaskingRules,
	} {
		planned, ok := listDefaultPlan(t, s.Attributes[name])
		if !ok {
			t.Fatalf("%s: no Default", name)
		}
		if !got.Equal(planned) {
			t.Errorf("%s: state %s after an empty response, plan %s", name, got, planned)
		}
	}
}

// TestDatabaseConfigObjects_OptionalComputed pins the object half: the API
// always returns cache_config and pool_config, so Optional alone fails the
// apply of any config that omits them.
func TestDatabaseConfigObjects_OptionalComputed(t *testing.T) {
	t.Parallel()

	s := resourceSchema(t, &databaseResource{})
	for _, name := range []string{"cache_config", "pool_config"} {
		attr, ok := s.Attributes[name].(resschema.SingleNestedAttribute)
		if !ok {
			t.Fatalf("%s: want a SingleNestedAttribute", name)
		}
		if !attr.Optional || !attr.Computed {
			t.Errorf("%s: want Optional+Computed, got optional=%v computed=%v",
				name, attr.Optional, attr.Computed)
		}
		if attr.Default != nil {
			t.Errorf("%s: carries a Default, but the contract declares no object default", name)
		}
	}
}
