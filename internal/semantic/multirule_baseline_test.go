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
// team.assignment_key for a DIFFERENT instance produce C(N,2) unresolved conflicts, because
// nothing in a field path distinguishes one team from another. That is why one rule per team
// is not a workaround for the missing set-scoped selector.
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
			// One diagnostic per overlapping field path per unordered pair. Each rule writes
			// exactly one path, so the count is exactly the number of pairs.
			wantConflicts := instances * (instances - 1) / 2
			if conflicts != wantConflicts {
				t.Fatalf("write conflicts = %d, want %d (C(%d,2))",
					conflicts, wantConflicts, instances)
			}
		})
	}
}

// multiInstanceRequest builds a ruleset with one form transformation per instance, each
// naming its own two source drivers, which is the only way to express a multi-team fleet with
// today's operators.
func multiInstanceRequest(t *testing.T, instances int) CompileRequest {
	t.Helper()

	schema, err := NewSchema(
		[]EntityDeclaration{
			{Kind: "driver", Fields: []FieldDeclaration{
				{Name: "assignment_key", Kind: ValueString},
			}},
			{Kind: "team", Fields: []FieldDeclaration{
				{Name: "assignment_key", Kind: ValueString},
			}},
		},
		[]RelationDeclaration{{Kind: "member", FromKind: "team", ToKind: "driver"}},
	)
	if err != nil {
		t.Fatalf("NewSchema: %v", err)
	}

	transformations := make([]TransformationDeclaration, 0, instances)
	for i := range instances {
		rule := RuleID(fmt.Sprintf("form_team_%d", i))
		transformations = append(transformations, TransformationDeclaration{
			ID:             rule,
			Operator:       OperatorFormRelatedEntity,
			DeclaredReads:  []FieldPath{"driver.assignment_key"},
			DeclaredWrites: []FieldPath{"team.assignment_key"},
			Form: &FormRelatedEntityDeclaration{
				SourceKind: "driver",
				Sources: []SourceReference{
					{Kind: "driver", CanonicalSourceKey: fmt.Sprintf("driver-%d-a", i)},
					{Kind: "driver", CanonicalSourceKey: fmt.Sprintf("driver-%d-b", i)},
				},
				OutputKind:    "team",
				OutputSlot:    OutputSlotKey(fmt.Sprintf("team_%d", i)),
				GroupingField: "driver.assignment_key",
				SourceCount:   2,
				CopiedFields: []FieldCopy{
					{Source: "driver.assignment_key", Destination: "team.assignment_key"},
				},
				RelationKind: "member",
				OutputKey: &OutputKeyExpression{
					Kind: OutputKeyCommonSourceField, Field: "driver.assignment_key"},
			},
		})
	}

	return CompileRequest{
		Schema:                   schema.Declaration(),
		Rules:                    RulesetDeclaration{Transformations: transformations},
		CompilerSemanticsVersion: "maiden-lane.compiler-semantics.v1",
	}
}
