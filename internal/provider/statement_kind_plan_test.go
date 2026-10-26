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

// statement_kind is derived from sql, so editing sql from a SELECT to an UPDATE
// changes it in place. It must plan as unknown rather than keep the prior value.
func TestStatementKind_PlansUnknownOnUpdate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	seen := 0
	for _, factory := range pgbeamResources() {
		r := factory()
		typeName := resourceTypeName(t, r)
		s := resourceSchema(t, r)

		attr, ok := s.Attributes["statement_kind"]
		if !ok {
			continue
		}
		strAttr, ok := attr.(resschema.StringAttribute)
		if !ok {
			t.Errorf("%s.statement_kind is %T, want a StringAttribute", typeName, attr)
			continue
		}
		seen++

		objType, ok := s.Type().TerraformType(ctx).(tftypes.Object)
		if !ok {
			t.Fatalf("schema type is not an object")
		}
		vals := make(map[string]tftypes.Value, len(objType.AttributeTypes))
		for name, typ := range objType.AttributeTypes {
			vals[name] = tftypes.NewValue(typ, nil)
		}
		vals["statement_kind"] = tftypes.NewValue(tftypes.String, "select")
		state := tfsdk.State{Schema: s, Raw: tftypes.NewValue(objType, vals)}

		req := planmodifier.StringRequest{
			Path:        path.Root("statement_kind"),
			ConfigValue: types.StringNull(),
			PlanValue:   types.StringUnknown(),
			StateValue:  types.StringValue("select"),
			State:       state,
			Plan:        tfsdk.Plan{Schema: s, Raw: state.Raw},
		}
		resp := &planmodifier.StringResponse{PlanValue: req.PlanValue}
		for _, m := range strAttr.PlanModifiers {
			m.PlanModifyString(ctx, req, resp)
		}
		if !resp.PlanValue.IsUnknown() {
			t.Errorf("%s.statement_kind plans %s on an in-place update, want unknown", typeName, resp.PlanValue)
		}
	}

	if seen == 0 {
		t.Fatal("no resource exposes statement_kind, so this test checked nothing")
	}
}
