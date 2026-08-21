package semantic

import (
	"fmt"
	"testing"
)

// TestMultiInstanceRulesetBaseline records what the compiler does today when a ruleset
// declares the same transformation shape once per instance.
//
// THIS IS A BASELINE, NOT A SPECIFICATION. Nothing here asserts that the current behaviour is
// correct, and the closed-rule-language programme is expected to change it. It is committed
// because the programme's motivating claim — that a multi-instance ruleset does not compile at
// all — came from a throwaway harness that no longer exists, and a claim nobody can reproduce
// is not evidence. A later slice that changes these numbers should update this test with a
// reason, not treat the change as a regression.
//
// What it shows: write-conflict analysis is field-path granular, so N rules each writing
// driver.assignment_status without an ordering produce C(N,2) unresolved conflicts, because
// nothing in a field path distinguishes one group from another. That is why multiple duplicate
// rules are not a workaround when write-write overlaps exist without explicit ordering.
func TestMultiInstanceRulesetBaseline(t *testing.T) {
	for _, instances := range []int{2, 3, 10} {
		t.Run(fmt.Sprintf("instances=%d", instances), func(t *testing.T) {
			compilation, err := Compile(multiInstanceRequest(t, instances))
			if err != nil {
				t.Fatalf("Compile: %v", err)
			}
			failure, refused := compilation.Failure()
			if !refused {
				t.Fatalf("a %d-instance ruleset compiled; the baseline this test records "+
					"no longer holds and the programme's premise needs restating", instances)
			}

			conflicts := 0
			for _, diagnostic := range failure.Diagnostics() {
				if diagnostic.Code() == WriteConflictUnresolved {
					conflicts++
				}
			}
			// One diagnostic per overlapping field path per unordered pair — but only for
			// pairs no dependency path already orders. This fixture's rules
			// read driver.assignment_key and write driver.assignment_status, which never intersect,
			// so no edges exist and every pair is reported.
			wantConflicts := instances * (instances - 1) / 2
			if conflicts != wantConflicts {
				t.Fatalf("write conflicts = %d, want %d (C(%d,2))",
					conflicts, wantConflicts, instances)
			}
		})
	}
}

// multiInstanceRequest builds a ruleset with duplicate SelectAndAssign transformations,
// each writing driver.assignment_status without explicit After ordering.
func multiInstanceRequest(t *testing.T, instances int) CompileRequest {
	t.Helper()

	schema, err := NewSchema(
		[]EntityDeclaration{
			{Kind: "driver", Fields: []FieldDeclaration{
				{Name: "assignment_key", Kind: ValueString},
				{Name: "assignment_status", Kind: ValueString},
			}},
		},
		nil,
	)
	if err != nil {
		t.Fatalf("NewSchema: %v", err)
	}

	assignmentValue, err := NewStringValue("assigned")
	if err != nil {
		t.Fatalf("NewStringValue: %v", err)
	}

	transformations := make([]TransformationDeclaration, 0, instances)
	for i := range instances {
		rule := RuleID(fmt.Sprintf("form_team_%d", i))
		transformations = append(transformations, TransformationDeclaration{
			ID:             rule,
			Operator:       OperatorSelectAndAssign,
			DeclaredReads:  []FieldPath{"driver.assignment_key"},
			DeclaredWrites: []FieldPath{"driver.assignment_status"},
			SelectAssign: &SelectAssignDeclaration{
				Selector: Selector{
					Kind:    "driver",
					GroupBy: &Expr{Kind: ExprField, Field: "driver.assignment_key"},
					Members: Cardinality{Kind: CardinalityExactly, Count: 2},
				},
				Guard: Expr{Kind: ExprAllEqual, Field: "driver.assignment_key"},
				Assignments: []FieldAssignment{
					{Target: "driver.assignment_status", Value: Expr{Kind: ExprLiteral, Literal: &assignmentValue}},
				},
			},
		})
	}

	return CompileRequest{
		Schema:                   schema.Declaration(),
		Rules:                    RulesetDeclaration{Transformations: transformations},
		CompilerSemanticsVersion: "maiden-lane.compiler-semantics.v1",
	}
}
