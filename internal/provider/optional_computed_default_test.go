package provider

import (
	"context"
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	resschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/defaults"
)

// An attribute that is Optional in the request and always present in the
// response has a server-side default, and the generator emits it
// Optional+Computed so the apply does not fail with "Provider produced
// inconsistent result after apply".
//
// Computed alone then breaks something else. For an Optional+Computed attribute
// whose CONFIGURATION value is null, Terraform Core proposes the PRIOR STATE
// value, so deleting the line from a config that once set it plans no change at
// all. On access_mode and the two enabled flags the held value is the looser
// one: removing `access_mode = "read_write"` keeps read_write, and removing
// `enabled = false` keeps a detection metric silenced, with plan and state
// agreeing that nothing drifted.
//
// A framework Default is applied when the configuration value is null, after
// Core has proposed the prior state, so it wins and the attribute plans the
// value the spec declares. These tests pin that every Optional+Computed scalar
// either carries one or is named below as an attribute whose spec declares no
// default to plan towards.
//
// The spec side of the same partition is pinned in
// scripts/src/iac-optional-computed-defaults.test.ts.

// Optional+Computed scalars that carry no framework Default on purpose, and
// why. Every entry is checked in both directions, so one that stops being true
// fails this test rather than going stale.
//
// Two reasons appear. The first four declare no `default` in the OpenAPI
// request schema, so there is no value to plan towards and Computed alone is
// correct: an omitted attribute keeps whatever the server last said, which is
// the honest answer when the contract does not name one.
//
// The last two do declare one (`cloud: aws`, `self_hosted: false`) and must
// still not get it. Both are create-only and carry RequiresReplace, so a
// Default would turn deleting the line out of a config into a planned change
// from the held value to the declared one, and that plan destroys and recreates
// the project. Holding the prior value is the safe reading for an immutable
// attribute, and it is the behaviour on main today rather than anything this
// change introduced.
var optionalComputedWithoutDefault = map[string]string{
	"pgbeam_agent_credential.status": "the spec declares no default",
	"pgbeam_database.ssl_mode":       "the spec declares no default",
	"pgbeam_project.status":          "the spec declares no default",
	"pgbeam_replica.ssl_mode":        "the spec declares no default",
	"pgbeam_project.cloud":           "immutable: a default would plan a replacement",
	"pgbeam_project.self_hosted":     "immutable: a default would plan a replacement",
}

func resourceTypeName(t *testing.T, r resource.Resource) string {
	t.Helper()
	resp := &resource.MetadataResponse{}
	r.Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "pgbeam"}, resp)
	return resp.TypeName
}

// attributeDefault reports whether a scalar attribute carries a framework
// Default, and what that default plans to. The second return is the planned
// value rendered as a string, so one table can cover bools and strings.
func attributeDefault(t *testing.T, attr resschema.Attribute) (hasDefault bool, planned string) {
	t.Helper()
	ctx := context.Background()

	switch a := attr.(type) {
	case resschema.BoolAttribute:
		if a.Default == nil {
			return false, ""
		}
		resp := &defaults.BoolResponse{}
		a.Default.DefaultBool(ctx, defaults.BoolRequest{}, resp)
		return true, fmt.Sprintf("%v", resp.PlanValue.ValueBool())
	case resschema.StringAttribute:
		if a.Default == nil {
			return false, ""
		}
		resp := &defaults.StringResponse{}
		a.Default.DefaultString(ctx, defaults.StringRequest{}, resp)
		return true, resp.PlanValue.ValueString()
	case resschema.Int64Attribute:
		if a.Default == nil {
			return false, ""
		}
		resp := &defaults.Int64Response{}
		a.Default.DefaultInt64(ctx, defaults.Int64Request{}, resp)
		return true, fmt.Sprintf("%d", resp.PlanValue.ValueInt64())
	case resschema.Float64Attribute:
		if a.Default == nil {
			return false, ""
		}
		resp := &defaults.Float64Response{}
		a.Default.DefaultFloat64(ctx, defaults.Float64Request{}, resp)
		return true, fmt.Sprintf("%v", resp.PlanValue.ValueFloat64())
	default:
		// Lists and objects are swept in optional_computed_collections_test.go.
		return false, ""
	}
}

// isScalarAttribute reports whether an attribute is one of the four scalar
// kinds a static default can be expressed for.
func isScalarAttribute(attr resschema.Attribute) bool {
	switch attr.(type) {
	case resschema.BoolAttribute, resschema.StringAttribute,
		resschema.Int64Attribute, resschema.Float64Attribute:
		return true
	default:
		return false
	}
}

// TestOptionalComputedScalars_CarryADefaultOrAreNamed sweeps every registered
// resource rather than listing the attributes it expects, so a resource
// registered tomorrow with an API-defaulted scalar fails here instead of
// shipping the sticky-value behaviour unnoticed.
func TestOptionalComputedScalars_CarryADefaultOrAreNamed(t *testing.T) {
	t.Parallel()

	seen := 0
	matched := map[string]bool{}
	for _, factory := range pgbeamResources() {
		r := factory()
		typeName := resourceTypeName(t, r)
		s := resourceSchema(t, r)

		for name, attr := range s.Attributes {
			if !attr.IsOptional() || !attr.IsComputed() || !isScalarAttribute(attr) {
				continue
			}
			seen++
			qualified := typeName + "." + name

			hasDefault, _ := attributeDefault(t, attr)
			reason, exempt := optionalComputedWithoutDefault[qualified]
			if exempt {
				matched[qualified] = true
			}

			if !hasDefault && !exempt {
				t.Errorf("%s is Optional+Computed with no Default, so removing it from a "+
					"config silently holds the prior value. Declare a `default` on the "+
					"request schema, or name it in optionalComputedWithoutDefault with "+
					"the reason it must not have one.", qualified)
			}
			if hasDefault && exempt {
				t.Errorf("%s carries a Default but is named as not having one (%q). "+
					"Drop the entry, or stop the generator emitting the Default.",
					qualified, reason)
			}
		}
	}

	// A guard on the guard. If the generator stopped emitting Optional+Computed
	// altogether the loop above would pass while checking nothing.
	if seen == 0 {
		t.Fatal("no Optional+Computed scalar attributes found, so this test checked nothing")
	}

	// An entry naming an attribute that no longer exists is an exemption nobody
	// is reading, and it would go on excusing a name the provider has reused.
	for qualified := range optionalComputedWithoutDefault {
		if !matched[qualified] {
			t.Errorf("optionalComputedWithoutDefault names %s, which is not an "+
				"Optional+Computed scalar in any registered resource. Remove it.", qualified)
		}
	}
}

// TestApiDefaultedScalars_PlanTheDeclaredDefault pins the value itself, not
// only that some default exists. A default that planned the wrong way round
// (enabled defaulting to false, access_mode to read_write) would be worse than
// no default at all, and both of these gate a security control.
func TestApiDefaultedScalars_PlanTheDeclaredDefault(t *testing.T) {
	t.Parallel()

	cases := []struct {
		factory func() resource.Resource
		attr    string
		want    string
	}{
		{NewPolicyProfileResource, "access_mode", "read_only"},
		{NewAnomalyRuleResource, "enabled", "true"},
		{NewWebhookEndpointResource, "enabled", "true"},
		{NewWebhookEndpointResource, "format", "json"},
	}

	for _, tc := range cases {
		r := tc.factory()
		name := resourceTypeName(t, r) + "." + tc.attr
		t.Run(name, func(t *testing.T) {
			s := resourceSchema(t, r)
			attr, ok := s.Attributes[tc.attr]
			if !ok {
				t.Fatalf("%s: attribute missing from the schema", name)
			}
			if !attr.IsOptional() || !attr.IsComputed() {
				t.Fatalf("%s: want Optional+Computed, got optional=%v computed=%v",
					name, attr.IsOptional(), attr.IsComputed())
			}
			hasDefault, planned := attributeDefault(t, attr)
			if !hasDefault {
				t.Fatalf("%s: no framework Default, so an omitted attribute plans the prior "+
					"state value instead of %q", name, tc.want)
			}
			if planned != tc.want {
				t.Errorf("%s: default plans %q, want %q", name, planned, tc.want)
			}
		})
	}
}
