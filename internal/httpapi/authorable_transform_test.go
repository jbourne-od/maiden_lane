package httpapi

import (
	"net/http"
	"testing"

	openapiv1 "github.com/optimaldynamics/maiden-lane/internal/httpapi/openapiv1"
)

// A selector-scoped rule authored as JSON, submitted over HTTP, and executed.
//
// THIS IS THE ACCEPTANCE PROPERTY OF THIS SLICE, and it is the same property slice 4 had, one
// layer out. That slice proved the operator reachable from ExecuteTransition and recorded
// plainly that it was not reachable from the API: rulesetFromWire carried a closed two-value
// operator enum and refused anything else, so no client could author one. "Reachable" is not a
// property of a package, it is a property of a path, and the path a customer uses starts at
// JSON.
//
// Nothing here builds a semantic declaration and projects it. The rule is written as the wire
// document a client would send, because a test that starts from the kernel's own types proves
// the projection works and says nothing about whether the contract can express the rule.
func depotSchema() openapiv1.SchemaDeclaration {
	field := func(name, kind string) openapiv1.FieldDeclaration {
		return openapiv1.FieldDeclaration{Name: name, Kind: openapiv1.ValueKind(kind)}
	}
	return openapiv1.SchemaDeclaration{
		Entities: []openapiv1.EntityDeclaration{{
			Kind: "driver",
			Fields: []openapiv1.FieldDeclaration{
				field("depot", "string"), field("driving_hours", "int64"),
				field("rest_hours", "int64"), field("shift_total", "int64"),
				field("status", "string"), field("violations", "int64"),
			},
		}},
	}
}

func wireField(path string) openapiv1.Expr {
	return openapiv1.Expr{Kind: openapiv1.ExprKindField, Field: &path}
}

func wireInt(value int64) openapiv1.Expr {
	return openapiv1.Expr{Kind: openapiv1.ExprKindLiteral,
		Literal: &openapiv1.Value{Kind: openapiv1.ValueKindInt64, Int64: &value}}
}

func wireString(t *testing.T, text string) openapiv1.Expr {
	t.Helper()
	return openapiv1.Expr{Kind: openapiv1.ExprKindLiteral,
		Literal: &openapiv1.Value{Kind: openapiv1.ValueKindString, String: &text}}
}

// depotRuleDeclarations is the JSON a client sends. violationsBelow is the only thing callers
// vary, and it is the threshold inside the group predicate.
func depotRuleDeclarations(t *testing.T, violationsBelow int64) openapiv1.PlanDeclarations {
	t.Helper()
	groupBy := wireField("driver.depot")
	count := int64(1)
	return openapiv1.PlanDeclarations{
		CompilerSemanticsVersion: "semantics.v1",
		Schema:                   depotSchema(),
		Rules: openapiv1.RulesetDeclaration{
			Transformations: []openapiv1.TransformationDeclaration{{
				Id:       "certify_depot.v1",
				Operator: openapiv1.TransformationDeclarationOperatorSelectAndAssign,
				DeclaredReads: &[]string{
					"driver.depot", "driver.driving_hours", "driver.rest_hours", "driver.violations",
				},
				DeclaredWrites: &[]string{"driver.shift_total", "driver.status"},
				SelectAssign: &openapiv1.SelectAndAssign{
					Selector: openapiv1.Selector{
						Kind:    "driver",
						GroupBy: &groupBy,
						Members: openapiv1.Cardinality{Kind: openapiv1.CardinalityKindAtLeast, Count: &count},
					},
					Guard: openapiv1.Expr{Kind: openapiv1.ExprKindAllMembers, Args: &[]openapiv1.Expr{{
						Kind: openapiv1.ExprKindLess,
						Args: &[]openapiv1.Expr{wireField("driver.violations"), wireInt(violationsBelow)},
					}}},
					Assignments: []openapiv1.FieldAssignment{{
						Target: "driver.shift_total",
						Value: openapiv1.Expr{Kind: openapiv1.ExprKindAdd, Args: &[]openapiv1.Expr{
							wireField("driver.driving_hours"), wireField("driver.rest_hours"),
						}},
					}, {
						Target: "driver.status",
						Value:  wireString(t, "certified"),
					}},
				},
			}},
		},
	}
}

func depotEntity(key, depot string, driving, rest, violations int64) openapiv1.EntityInput {
	number := func(value int64) openapiv1.Value {
		held := value
		return openapiv1.Value{Kind: openapiv1.ValueKindInt64, Int64: &held}
	}
	name := depot
	return openapiv1.EntityInput{
		Kind: "driver", CanonicalSourceKey: key,
		Fields: map[string]openapiv1.Value{
			"depot":         {Kind: openapiv1.ValueKindString, String: &name},
			"driving_hours": number(driving),
			"rest_hours":    number(rest),
			"violations":    number(violations),
		},
	}
}

func depotExecutionRequest(planID openapiv1.Digest) openapiv1.CreateExecutionRequest {
	return openapiv1.CreateExecutionRequest{
		PlanID: planID,
		InitialState: openapiv1.StateInput{
			Lineage: openapiv1.InputLineage{
				Namespace: "maiden-lane.sanitized-fixture",
				RootKey:   "depot-fleet",
			},
			Entities: []openapiv1.EntityInput{
				depotEntity("A", "north", 8, 10, 1),
				depotEntity("B", "north", 9, 11, 2),
				depotEntity("C", "south", 12, 5, 5),
			},
		},
		ExecutorIdentity: openapiv1.ExecutorIdentity{
			Backend: "go",
			Version: "sha256:1c0d5a3e9b7f2c4d6a8e0b1f3d5c7a9e2b4d6f8a0c2e4b6d8f0a2c4e6b8d0f2a",
		},
		ProvenancePolicy: openapiv1.CreateExecutionRequestProvenancePolicyChangesV1,
	}
}

func TestSelectorScopedRuleIsAuthorableAndRunnableOverHTTP(t *testing.T) {
	fixture := newExecutionFixture(t)

	strict := createPlan(t, fixture.router, "acme", depotRuleDeclarations(t, 3))
	loose := createPlan(t, fixture.router, "acme", depotRuleDeclarations(t, 10))

	// Two rulesets differing only in the group predicate's threshold must not share a plan
	// identity, and the JSON path must preserve that -- a boundary that dropped the guard
	// would produce one plan from two rules and nothing else here would notice.
	if strict.PlanID == loose.PlanID {
		t.Fatal("rules differing only in the group predicate produced one plan identity")
	}

	run := func(planID openapiv1.Digest) openapiv1.Execution {
		t.Helper()
		accepted := acceptExecution(t, fixture.router, "acme", depotExecutionRequest(planID))
		fixture.drain(t)
		execution := getExecution(t, fixture.router, "acme", accepted.ExecutionID)
		if execution.ExecutionStatus != openapiv1.ExecutionStatusSucceeded {
			t.Fatalf("status = %s, want succeeded", execution.ExecutionStatus)
		}
		if execution.Result == nil {
			t.Fatal("a completed execution carries no result")
		}
		if execution.Result.Failure != nil {
			t.Fatalf("the run refused: %+v", execution.Result.Failure)
		}
		return execution
	}

	strictRun, looseRun := run(strict.PlanID), run(loose.PlanID)

	// The rule committed. Without this the test would pass on a plan that compiled and
	// transformed nothing.
	for name, execution := range map[string]openapiv1.Execution{"strict": strictRun, "loose": looseRun} {
		accepted := execution.Result.AcceptedRules
		if accepted == nil || len(*accepted) != 1 || (*accepted)[0] != "certify_depot.v1" {
			t.Fatalf("%s run committed %v, want the authored rule", name, accepted)
		}
	}

	// AND THE GROUP PREDICATE DECIDED THE OUTCOME. Under the strict threshold the south depot
	// does not qualify and its driver is untouched; under the loose one every driver is
	// written. Same schema, same state, same assignments -- so a final state digest that did
	// not move would mean the guard reached the kernel and changed nothing.
	if strictRun.Result.FinalStateDigest == nil || looseRun.Result.FinalStateDigest == nil {
		t.Fatal("a completed run reported no final state digest")
	}
	if *strictRun.Result.FinalStateDigest == *looseRun.Result.FinalStateDigest {
		t.Fatal("altering the group predicate over HTTP did not change the resulting state")
	}
}

// The plan a client reads back describes the rule the client sent.
//
// Projection and translation are separate switches over one vocabulary, so a rule can be
// authorable and unreadable. GetPlan returns declarations, and this asserts the returned
// document round-trips to the submitted one rather than merely being non-empty.
func TestAuthoredSelectorRuleReadsBackUnchanged(t *testing.T) {
	fixture := newExecutionFixture(t)
	submitted := depotRuleDeclarations(t, 3)
	created := createPlan(t, fixture.router, "acme", submitted)

	recorder := get(t, fixture.router, "/v1/plans/"+string(created.PlanID), "acme")
	if recorder.Code != http.StatusOK {
		t.Fatalf("get plan status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	var read openapiv1.Plan
	decodeBody(t, recorder, &read)
	if read.Declarations == nil {
		t.Fatal("the stored plan reads back with no declarations")
	}
	returned := read.Declarations.Rules.Transformations
	if len(returned) != 1 {
		t.Fatalf("plan holds %d transformations, want 1", len(returned))
	}
	rule := returned[0]
	if rule.Operator != openapiv1.TransformationDeclarationOperatorSelectAndAssign {
		t.Fatalf("operator read back as %q", rule.Operator)
	}
	if rule.SelectAssign == nil {
		t.Fatal("the select-and-assign payload did not survive storage and projection")
	}

	// Recompiling the document that was read back must reproduce the same plan, which is the
	// property that matters: the projection is not merely populated, it is faithful.
	sent := submitted
	sent.Rules.Transformations = returned
	recreated := createPlan(t, fixture.router, "acme", sent)
	if recreated.PlanID != created.PlanID {
		t.Fatalf("the projected declarations recompiled to plan %s, want %s",
			recreated.PlanID, created.PlanID)
	}
}

func TestGroupReductionRuleIsAuthorableAndRunnableOverHTTP(t *testing.T) {
	fixture := newExecutionFixture(t)
	groupBy := wireField("driver.depot")
	count := int64(1)

	declarations := openapiv1.PlanDeclarations{
		CompilerSemanticsVersion: "semantics.v1",
		Schema:                   depotSchema(),
		Rules: openapiv1.RulesetDeclaration{
			Transformations: []openapiv1.TransformationDeclaration{{
				Id:       "certify_team_reductions.v1",
				Operator: openapiv1.TransformationDeclarationOperatorSelectAndAssign,
				DeclaredReads: &[]string{
					"driver.depot", "driver.driving_hours",
				},
				DeclaredWrites: &[]string{"driver.shift_total", "driver.status"},
				SelectAssign: &openapiv1.SelectAndAssign{
					Selector: openapiv1.Selector{
						Kind:    "driver",
						GroupBy: &groupBy,
						Members: openapiv1.Cardinality{Kind: openapiv1.CardinalityKindAtLeast, Count: &count},
					},
					Guard: openapiv1.Expr{Kind: openapiv1.ExprKindAll, Args: &[]openapiv1.Expr{
						{Kind: openapiv1.ExprKindLess, Args: &[]openapiv1.Expr{
							{Kind: openapiv1.ExprKindMax, Field: stringPtr("driver.driving_hours")},
							wireInt(14),
						}},
						{Kind: openapiv1.ExprKindEqual, Args: &[]openapiv1.Expr{
							{Kind: openapiv1.ExprKindCount},
							wireInt(2),
						}},
					}},
					Assignments: []openapiv1.FieldAssignment{{
						Target: "driver.shift_total",
						Value:  openapiv1.Expr{Kind: openapiv1.ExprKindSum, Field: stringPtr("driver.driving_hours")},
					}, {
						Target: "driver.status",
						Value:  wireString(t, "certified_team"),
					}},
				},
			}},
		},
	}

	created := createPlan(t, fixture.router, "acme", declarations)
	if created.PlanID == "" {
		t.Fatal("plan creation yielded an empty PlanID")
	}

	accepted := acceptExecution(t, fixture.router, "acme", depotExecutionRequest(created.PlanID))
	fixture.drain(t)
	execution := getExecution(t, fixture.router, "acme", accepted.ExecutionID)
	if execution.ExecutionStatus != openapiv1.ExecutionStatusSucceeded {
		t.Fatalf("status = %s, want succeeded", execution.ExecutionStatus)
	}
	if execution.Result == nil || execution.Result.Failure != nil {
		t.Fatalf("run failed: %+v", execution.Result)
	}

	// Verify plan round-trips through GetPlan.
	recorder := get(t, fixture.router, "/v1/plans/"+string(created.PlanID), "acme")
	if recorder.Code != http.StatusOK {
		t.Fatalf("GetPlan: %d: %s", recorder.Code, recorder.Body.String())
	}
	var read openapiv1.Plan
	decodeBody(t, recorder, &read)
	if read.Declarations == nil {
		t.Fatal("the stored plan reads back with no declarations")
	}
	returned := read.Declarations.Rules.Transformations
	if len(returned) != 1 {
		t.Fatalf("plan holds %d transformations, want 1", len(returned))
	}
	recreated := createPlan(t, fixture.router, "acme", openapiv1.PlanDeclarations{
		CompilerSemanticsVersion: "semantics.v1",
		Schema:                   depotSchema(),
		Rules:                    openapiv1.RulesetDeclaration{Transformations: returned},
	})
	if recreated.PlanID != created.PlanID {
		t.Fatalf("projected reduction plan recompiled to %s, want %s", recreated.PlanID, created.PlanID)
	}
}
