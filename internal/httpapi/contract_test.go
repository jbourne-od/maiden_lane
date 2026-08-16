package httpapi_test

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	openapiv1 "github.com/optimaldynamics/maiden-lane/internal/httpapi/openapiv1"
)

// These tests protect the direction of authority. api/openapi.yaml is the
// contract; Go is generated from it. A published contract stops being true in
// exactly two ways, and `make openapi-check` catches both mechanically: the
// spec changes without regeneration, or a generated file is edited by hand.
// What that gate cannot check is whether the generated surface still matches
// the operations this service actually intends to expose, which is what these
// tests pin.

// wantOperations is the complete set of operationIds the contract declares,
// written out by hand rather than derived from the generated code. Deriving it
// would make the test agree with whatever was generated and prove nothing.
var wantOperations = []string{
	"GetHealth",
	"GetReadiness",
	"CreatePlan",
	"GetPlan",
	"CreateExecution",
}

// Production break caught: adding an operation to the contract without
// implementing it, or quietly dropping one, would ship a client whose
// generated method has no server behind it.
func TestGeneratedServerInterfaceMatchesTheContract(t *testing.T) {
	serverInterface := reflect.TypeFor[openapiv1.ServerInterface]()

	got := make([]string, 0, serverInterface.NumMethod())
	for method := range serverInterface.Methods() {
		got = append(got, method.Name)
	}
	slices.Sort(got)

	want := slices.Clone(wantOperations)
	slices.Sort(want)

	if !slices.Equal(got, want) {
		t.Fatalf("generated server operations = %v, want %v", got, want)
	}
}

// Production break caught: a tenant parameter dropped from an operation would
// silently remove the scoping boundary from that route while the handler kept
// compiling.
func TestEveryVersionedOperationRequiresTheTenantHeader(t *testing.T) {
	tenanted := map[string]bool{
		"CreatePlanParams":      true,
		"GetPlanParams":         true,
		"CreateExecutionParams": true,
	}
	for name := range tenanted {
		params, ok := paramsTypeByName(name)
		if !ok {
			t.Errorf("generated code has no %s type; the operation lost its parameters", name)
			continue
		}
		field, present := params.FieldByName("XMaidenLaneTenant")
		if !present {
			t.Errorf("%s does not carry the tenant header parameter", name)
			continue
		}
		// A required header must be a value, not a pointer: an optional tenant
		// would let a request through with no scope at all.
		if field.Type.Kind() == reflect.Pointer {
			t.Errorf("%s tenant header is optional (%s); scoping must be required", name, field.Type)
		}
	}
}

// Production break caught: the health endpoints must stay untenanted and
// unauthenticated, or a load balancer probe starts failing closed.
func TestHealthOperationsTakeNoParameters(t *testing.T) {
	for _, name := range []string{"GetHealthParams", "GetReadinessParams"} {
		if _, ok := paramsTypeByName(name); ok {
			t.Errorf("%s exists; health endpoints must not require parameters", name)
		}
	}
}

// Production break caught: renaming a response field renames the accessor in
// every generated client. These JSON names are the published contract, so they
// are pinned by hand here rather than read back out of the generated struct.
//
// Note the contract's Digest schema generates as `type Digest = string`, an
// alias, so the Go type carries no distinct identity at runtime. The rule that
// a handler must project a kernel identity rather than construct one is
// therefore enforced against the handler code, not against these DTOs.
func TestExecutionResponseFieldNamesAreStable(t *testing.T) {
	want := map[string]string{
		"PlanID":        "planID",
		"SpineStatus":   "spineStatus",
		"SemanticRunID": "semanticRunID",
		"ExecutionID":   "executionID",
		"Checkpoints":   "checkpoints",
		"Assessments":   "assessments",
		"Failure":       "failure",
	}
	execution := reflect.TypeFor[openapiv1.Execution]()
	for field, jsonName := range want {
		found, ok := execution.FieldByName(field)
		if !ok {
			t.Errorf("Execution has no %s field", field)
			continue
		}
		tag := found.Tag.Get("json")
		if name, _, _ := strings.Cut(tag, ","); name != jsonName {
			t.Errorf("Execution.%s serializes as %q, want %q", field, name, jsonName)
		}
	}
}

func paramsTypeByName(name string) (reflect.Type, bool) {
	switch name {
	case "CreatePlanParams":
		return reflect.TypeFor[openapiv1.CreatePlanParams](), true
	case "GetPlanParams":
		return reflect.TypeFor[openapiv1.GetPlanParams](), true
	case "CreateExecutionParams":
		return reflect.TypeFor[openapiv1.CreateExecutionParams](), true
	default:
		return nil, false
	}
}
