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
	"GetExecution",
	"ReattemptExecution",
	"GetExecutionCheckpoint",
	"CreatePublication",
	"GetPublication",
	"CreateComparison",
	"GetComparison",
	"PutPolicy",
	"GetPolicy",
	"CreateCorpus",
	"GetCorpus",
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
	for _, op := range wantOperations {
		if op == "GetHealth" || op == "GetReadiness" {
			continue
		}
		name := op + "Params"
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
	// The execution response is split: the envelope carries identities and
	// lifecycle, and the result carries what the computation decided. The split
	// is the contract, so both halves are pinned.
	envelope := map[string]string{
		"ExecutionID":     "executionID",
		"SemanticRunID":   "semanticRunID",
		"PlanID":          "planID",
		"ExecutionStatus": "executionStatus",
		"FailureReason":   "failureReason",
		"Result":          "result",
	}
	assertJSONNames(t, reflect.TypeFor[openapiv1.Execution](), "Execution", envelope)

	result := map[string]string{
		"SpineStatus":         "spineStatus",
		"InputID":             "inputID",
		"WorldID":             "worldID",
		"FinalStateDigest":    "finalStateDigest",
		"JournalPrefixDigest": "journalPrefixDigest",
		"AcceptedRules":       "acceptedRules",
		"Checkpoints":         "checkpoints",
		"Assessments":         "assessments",
		"Failure":             "failure",
	}
	assertJSONNames(t, reflect.TypeFor[openapiv1.ExecutionResult](), "ExecutionResult", result)

	accepted := map[string]string{
		"ExecutionID":     "executionID",
		"SemanticRunID":   "semanticRunID",
		"PlanID":          "planID",
		"ExecutionStatus": "executionStatus",
	}
	assertJSONNames(t, reflect.TypeFor[openapiv1.ExecutionAccepted](), "ExecutionAccepted", accepted)
}

// Production break caught: a submission response that carried a result would
// reinstate synchronous execution in the contract even if the handler queued the
// work, and a client would reasonably expect one.
func TestAcceptedSubmissionCarriesNoResult(t *testing.T) {
	accepted := reflect.TypeFor[openapiv1.ExecutionAccepted]()
	for _, forbidden := range []string{"Result", "Checkpoints", "Assessments", "Failure"} {
		if _, present := accepted.FieldByName(forbidden); present {
			t.Errorf("ExecutionAccepted carries %s; submission must return identities only", forbidden)
		}
	}
}

func TestComparisonResponseFieldNamesAreStable(t *testing.T) {
	comparison := map[string]string{
		"ComparisonID":          "comparisonID",
		"BaselinePlanID":        "baselinePlanID",
		"CandidatePlanID":       "candidatePlanID",
		"BaselineCheckpointID":  "baselineCheckpointID",
		"CandidateCheckpointID": "candidateCheckpointID",
		"ProfileID":             "profileID",
		"WorldID":               "worldID",
		"CorpusID":              "corpusID",
		"PolicyID":              "policyID",
		"Correspondences":       "correspondences",
	}
	assertJSONNames(t, reflect.TypeFor[openapiv1.Comparison](), "Comparison", comparison)

	correspondence := map[string]string{
		"Baseline":  "baseline",
		"Candidate": "candidate",
	}
	assertJSONNames(t, reflect.TypeFor[openapiv1.ComparisonCorrespondence](), "ComparisonCorrespondence", correspondence)
}

func assertJSONNames(t *testing.T, target reflect.Type, name string, want map[string]string) {
	t.Helper()
	for field, jsonName := range want {
		found, ok := target.FieldByName(field)
		if !ok {
			t.Errorf("%s has no %s field", name, field)
			continue
		}
		tag := found.Tag.Get("json")
		if got, _, _ := strings.Cut(tag, ","); got != jsonName {
			t.Errorf("%s.%s serializes as %q, want %q", name, field, got, jsonName)
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
	case "GetExecutionParams":
		return reflect.TypeFor[openapiv1.GetExecutionParams](), true
	case "ReattemptExecutionParams":
		return reflect.TypeFor[openapiv1.ReattemptExecutionParams](), true
	case "GetExecutionCheckpointParams":
		return reflect.TypeFor[openapiv1.GetExecutionCheckpointParams](), true
	case "CreatePublicationParams":
		return reflect.TypeFor[openapiv1.CreatePublicationParams](), true
	case "GetPublicationParams":
		return reflect.TypeFor[openapiv1.GetPublicationParams](), true
	case "CreateComparisonParams":
		return reflect.TypeFor[openapiv1.CreateComparisonParams](), true
	case "GetComparisonParams":
		return reflect.TypeFor[openapiv1.GetComparisonParams](), true
	case "PutPolicyParams":
		return reflect.TypeFor[openapiv1.PutPolicyParams](), true
	case "GetPolicyParams":
		return reflect.TypeFor[openapiv1.GetPolicyParams](), true
	case "CreateCorpusParams":
		return reflect.TypeFor[openapiv1.CreateCorpusParams](), true
	case "GetCorpusParams":
		return reflect.TypeFor[openapiv1.GetCorpusParams](), true
	default:
		return nil, false
	}
}
