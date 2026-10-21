package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	resschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// The API sets updated_at = now() on every write, so an in-place update always
// returns a new value. When a resource has a planned change, the framework marks
// its Computed attributes unknown and then runs their plan modifiers.
// UseStateForUnknown copies the prior state value back in, so the plan carries
// the old timestamp as a known value. The Update then writes the new timestamp
// into state, and Terraform Core fails the apply with "Provider produced
// inconsistent result after apply: .updated_at", after the API call has
// already succeeded (#2925).
//
// The *_resource_crud_test.go files call Create and Update directly against a
// fake server with fixed timestamps, so they never run the plan-versus-result
// check. This test runs the plan modifiers the generator emitted for every
// resource's updated_at, in the position the framework runs them for an
// in-place update, and requires the planned value to stay unknown.

// nullStateWith returns a non-null prior state for schema s in which every
// attribute is null except updated_at, which holds prior.
func nullStateWith(t *testing.T, s resschema.Schema, prior string) tfsdk.State {
	t.Helper()
	objType, ok := s.Type().TerraformType(context.Background()).(tftypes.Object)
	if !ok {
		t.Fatalf("schema type is not an object")
	}
	vals := make(map[string]tftypes.Value, len(objType.AttributeTypes))
	for name, typ := range objType.AttributeTypes {
		vals[name] = tftypes.NewValue(typ, nil)
	}
	vals["updated_at"] = tftypes.NewValue(tftypes.String, prior)
	return tfsdk.State{Schema: s, Raw: tftypes.NewValue(objType, vals)}
}

func TestUpdatedAt_PlansUnknownOnUpdate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	const prior = "2026-01-02T00:00:00Z"

	seen := 0
	for _, factory := range pgbeamResources() {
		r := factory()
		typeName := resourceTypeName(t, r)
		s := resourceSchema(t, r)

		attr, ok := s.Attributes["updated_at"]
		if !ok {
			continue
		}
		strAttr, ok := attr.(resschema.StringAttribute)
		if !ok {
			t.Errorf("%s.updated_at is %T, want a StringAttribute", typeName, attr)
			continue
		}
		if !strAttr.IsComputed() || strAttr.IsOptional() || strAttr.IsRequired() {
			t.Errorf("%s.updated_at must be Computed only", typeName)
			continue
		}
		seen++

		state := nullStateWith(t, s, prior)
		req := planmodifier.StringRequest{
			Path:        path.Root("updated_at"),
			ConfigValue: types.StringNull(),
			PlanValue:   types.StringUnknown(),
			StateValue:  types.StringValue(prior),
			State:       state,
			Plan:        tfsdk.Plan{Schema: s, Raw: state.Raw},
		}
		resp := &planmodifier.StringResponse{PlanValue: req.PlanValue}
		for _, m := range strAttr.PlanModifiers {
			m.PlanModifyString(ctx, req, resp)
		}
		if resp.Diagnostics.HasError() {
			t.Errorf("%s.updated_at plan modifiers returned diagnostics: %v", typeName, resp.Diagnostics)
			continue
		}
		if !resp.PlanValue.IsUnknown() {
			t.Errorf("%s.updated_at plans %s on an in-place update, want unknown. The API "+
				"rewrites it on every write, so a known planned value fails the apply with "+
				"\"Provider produced inconsistent result after apply\".", typeName, resp.PlanValue)
		}
	}

	// A guard on the guard: if no resource exposed updated_at this would pass
	// while checking nothing.
	if seen == 0 {
		t.Fatal("no resource exposes a computed updated_at, so this test checked nothing")
	}
}
