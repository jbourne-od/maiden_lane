package semantic

import (
	"bytes"
	"encoding/hex"
	"slices"
	"testing"
)

// compilationDiagnostics returns the diagnostics of a refused compilation, or fails: a test
// that expected a refusal and silently found none must not go on to search an empty slice.
func compilationDiagnostics(t *testing.T, compilation Compilation) []CompilationDiagnostic {
	t.Helper()
	if _, accepted := compilation.Plan(); accepted {
		t.Fatal("compilation was accepted, want a refusal")
	}
	failure, ok := compilation.Failure()
	if !ok {
		t.Fatal("compilation is neither an accepted plan nor a failure")
	}
	return failure.Diagnostics()
}

func hasDiagnostic(diagnostics []CompilationDiagnostic, code CompilationDiagnosticCode, detail string) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Code() == code && diagnostic.Detail() == detail {
			return true
		}
	}
	return false
}

// selectAssignSchema is a fleet, not a pair: nothing here names a driver.
func selectAssignSchema(t *testing.T) Schema {
	t.Helper()
	schema, err := NewSchema([]EntityDeclaration{{
		Kind: "driver",
		Fields: []FieldDeclaration{
			{Name: "depot", Kind: ValueString},
			{Name: "driving_hours", Kind: ValueInt64},
			{Name: "rest_hours", Kind: ValueInt64},
			{Name: "shift_total", Kind: ValueInt64},
			{Name: "status", Kind: ValueString},
			// Read by the guard and by nothing else. Without a field private to one tree,
			// a test that deletes it from the declared reads cannot tell which tree derived
			// it -- and the guard-derivation mutation survived exactly that way.
			{Name: "violations", Kind: ValueInt64},
		},
	}, {
		// A second kind, so "the path names an entity this selector does not bind" can be
		// tested with a path the schema actually declares.
		Kind:   "depot",
		Fields: []FieldDeclaration{{Name: "region", Kind: ValueString}},
	}}, nil)
	if err != nil {
		t.Fatalf("NewSchema: %v", err)
	}
	return schema
}

func fieldExpr(path FieldPath) Expr { return Expr{Kind: ExprField, Field: path} }

// selectAssignRule is the rule the whole slice exists to make reachable: applied to every
// depot, not to two drivers named in the declaration.
//
// violationsBelow is the only thing callers vary, and it is the group predicate's threshold.
func selectAssignRule(t *testing.T, violationsBelow int64) TransformationDeclaration {
	t.Helper()
	groupBy := fieldExpr("driver.depot")
	return TransformationDeclaration{
		ID:       "certify_depot.v1",
		Operator: OperatorSelectAndAssign,
		DeclaredReads: []FieldPath{
			"driver.depot", "driver.driving_hours", "driver.rest_hours", "driver.violations",
		},
		DeclaredWrites: []FieldPath{"driver.shift_total", "driver.status"},
		SelectAssign: &SelectAssignDeclaration{
			Selector: Selector{
				Kind:    "driver",
				GroupBy: &groupBy,
				Members: Cardinality{Kind: CardinalityAtLeast, Count: 1},
			},
			Guard: Expr{Kind: ExprAllMembers, Args: []Expr{{
				Kind: ExprLess,
				Args: []Expr{fieldExpr("driver.violations"), intLiteral(violationsBelow)},
			}}},
			Assignments: []FieldAssignment{
				{Target: "driver.shift_total", Value: Expr{Kind: ExprAdd, Args: []Expr{
					fieldExpr("driver.driving_hours"), fieldExpr("driver.rest_hours"),
				}}},
				{Target: "driver.status", Value: stringLiteral(t, "certified")},
			},
		},
	}
}

func selectAssignRequest(t *testing.T, rule TransformationDeclaration) CompileRequest {
	t.Helper()
	return CompileRequest{
		Schema:                   selectAssignSchema(t).Declaration(),
		Rules:                    RulesetDeclaration{Transformations: []TransformationDeclaration{rule}},
		CompilerSemanticsVersion: "semantics.v1",
	}
}

func mustSelectAssignPlan(t *testing.T, rule TransformationDeclaration) Plan {
	t.Helper()
	compilation, err := Compile(selectAssignRequest(t, rule))
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	plan, ok := compilation.Plan()
	if !ok {
		failure, _ := compilation.Failure()
		t.Fatalf("ruleset did not compile: %v", failure.Diagnostics())
	}
	return plan
}

type driverFixture struct {
	key        string
	depot      string
	driving    int64
	rest       int64
	violations int64
}

func selectAssignState(t *testing.T, schema Schema, drivers []driverFixture) State {
	t.Helper()
	lineage, err := NewInputLineageID("maiden-lane.sanitized-fixture", "depot-fleet")
	if err != nil {
		t.Fatalf("NewInputLineageID: %v", err)
	}
	entities := make([]Entity, 0, len(drivers))
	for _, driver := range drivers {
		entities = append(entities, mustEntity(t, "driver", SourceEntityID(lineage, "driver", driver.key), map[FieldName]Value{
			"depot":         mustString(t, driver.depot),
			"driving_hours": NewInt64Value(driver.driving),
			"rest_hours":    NewInt64Value(driver.rest),
			"violations":    NewInt64Value(driver.violations),
		}))
	}
	state, err := NewState(schema, lineage, entities, nil)
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	return state
}

func selectAssignBinding(t *testing.T, plan Plan, state State) RunBinding {
	t.Helper()
	world, err := NewWorld(nil)
	if err != nil {
		t.Fatalf("NewWorld: %v", err)
	}
	return mustBindRun(t, plan, state, world, mustExecutorIdentityForTests("test", Digest("sha256:"+
		"0000000000000000000000000000000000000000000000000000000000000000")))
}

// certifiedDrivers reports which entities came out of a transition carrying a status.
func certifiedDrivers(t *testing.T, state State) []string {
	t.Helper()
	names := make([]string, 0)
	for _, entity := range state.Entities() {
		if value, present := entity.Field("status"); present {
			text, isText := value.String()
			if !isText {
				t.Fatalf("status on %v is not a string", entity.Ref())
			}
			names = append(names, string(entity.Ref().ID)+"="+text)
		}
	}
	slices.Sort(names)
	return names
}

// THE ACCEPTANCE PROPERTY OF THIS SLICE, and the reason it is a test rather than a hoped-for
// consequence. Every slice before this one shipped machinery behind a door nobody opened:
// CompileExpression, CompileSelector, Select and the three group node kinds all had no
// non-test caller, so what existed was potential semantics.
//
// This asserts the door opens. One authored ruleset, compiled by the production compiler,
// executed by the production executor, where the ONLY thing that varies is the threshold
// inside a group-scoped predicate -- and the set of entities the resulting state has written
// changes because of it.
func TestSelectAssignGroupPredicateChangesTheTransformResult(t *testing.T) {
	drivers := []driverFixture{
		{key: "A", depot: "north", driving: 8, rest: 10, violations: 1},
		{key: "B", depot: "north", driving: 9, rest: 11, violations: 2},
		{key: "C", depot: "south", driving: 12, rest: 5, violations: 5},
	}
	schema := selectAssignSchema(t)

	strictPlan := mustSelectAssignPlan(t, selectAssignRule(t, 3))
	loosePlan := mustSelectAssignPlan(t, selectAssignRule(t, 10))

	// IDENTITY FIRST. Two rulesets that mean different things must not share a digest, and a
	// payload missing from encodeTransformationDeclaration would be invisible exactly here
	// and correct everywhere else.
	if strictPlan.RulesetDigest() == loosePlan.RulesetDigest() {
		t.Fatal("rulesets differing only in the group predicate share a ruleset digest")
	}
	if strictPlan.ID() == loosePlan.ID() {
		t.Fatal("plans differing only in the group predicate share a plan identity")
	}

	strictState := selectAssignState(t, schema, drivers)
	strict := mustAcceptedTransition(t, selectAssignBinding(t, strictPlan, strictState),
		"certify_depot.v1", strictState, Journal{})
	looseState := selectAssignState(t, schema, drivers)
	loose := mustAcceptedTransition(t, selectAssignBinding(t, loosePlan, looseState),
		"certify_depot.v1", looseState, Journal{})

	gotStrict := certifiedDrivers(t, strict.State())
	gotLoose := certifiedDrivers(t, loose.State())
	if len(gotStrict) != 2 {
		t.Fatalf("strict predicate certified %v, want the two north drivers only", gotStrict)
	}
	if len(gotLoose) != 3 {
		t.Fatalf("loose predicate certified %v, want every driver", gotLoose)
	}
	if slices.Equal(gotStrict, gotLoose) {
		t.Fatal("altering the group predicate did not change which entities the patch touched")
	}
	if strict.State().Digest() == loose.State().Digest() {
		t.Fatal("altering the group predicate did not change the resulting state identity")
	}

	// And the per-member assignment really is per member: shift_total is an expression over
	// each driver's own fields, so two members of one group hold different values. A literal
	// would pass every assertion above.
	totals := make(map[EntityID]int64, 2)
	for _, entity := range strict.State().Entities() {
		if value, present := entity.Field("shift_total"); present {
			number, isNumber := value.Int64()
			if !isNumber {
				t.Fatalf("shift_total on %v is not an int64", entity.Ref())
			}
			totals[entity.Ref().ID] = number
		}
	}
	if len(totals) != 2 {
		t.Fatalf("shift_total written to %d entities, want 2", len(totals))
	}
	seen := make(map[int64]struct{}, 2)
	for _, total := range totals {
		if total != 18 && total != 20 {
			t.Fatalf("shift_total %d is neither driver's own sum", total)
		}
		seen[total] = struct{}{}
	}
	if len(seen) != 2 {
		t.Fatal("both members received the same shift_total, so the value is not member-scoped")
	}
}

func TestSelectAssignRefusalsAreAttributable(t *testing.T) {
	schema := selectAssignSchema(t)
	tests := []struct {
		name    string
		mutate  func(*TransformationDeclaration)
		drivers []driverFixture
		want    InvariantCode
	}{{
		name:    "no group at all",
		drivers: nil,
		want:    SelectionEmpty,
	}, {
		name: "a group violates the declared cardinality",
		mutate: func(rule *TransformationDeclaration) {
			rule.SelectAssign.Selector.Members = Cardinality{Kind: CardinalityExactly, Count: 2}
		},
		drivers: []driverFixture{
			{key: "A", depot: "north", driving: 8, rest: 10, violations: 1},
			{key: "B", depot: "north", driving: 9, rest: 11, violations: 2},
			{key: "C", depot: "south", driving: 4, rest: 5, violations: 1},
		},
		want: SelectionCardinalityInvalid,
	}, {
		name: "groups exist but the guard admits none",
		drivers: []driverFixture{
			{key: "C", depot: "south", driving: 12, rest: 5, violations: 9},
		},
		want: SelectionGuardUnsatisfied,
	}}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rule := selectAssignRule(t, 3)
			if test.mutate != nil {
				test.mutate(&rule)
			}
			plan := mustSelectAssignPlan(t, rule)
			state := selectAssignState(t, schema, test.drivers)
			outcome, err := ExecuteTransition(selectAssignBinding(t, plan, state), "certify_depot.v1", state, Journal{})
			if err != nil {
				t.Fatalf("ExecuteTransition: %v", err)
			}
			failure := mustTransitionFailure(t, outcome)
			if got := failure.Code(); got != string(test.want) {
				t.Fatalf("refusal code %s, want %s", got, test.want)
			}
			if outcome.HasPatch() {
				t.Fatal("a refused transition proposed a patch")
			}
		})
	}
}

// An expression the data cannot answer refuses; it is never read as "this group does not
// qualify". The distinction is the whole reason SelectionExpressionUnavailable exists, and
// without this test a guard that raised would be indistinguishable from one returning false.
func TestSelectAssignRefusesRatherThanSkippingAnUnevaluableGroup(t *testing.T) {
	schema := selectAssignSchema(t)
	lineage, err := NewInputLineageID("maiden-lane.sanitized-fixture", "depot-fleet")
	if err != nil {
		t.Fatalf("NewInputLineageID: %v", err)
	}
	// One qualifying group, and one whose members lack the field the guard reads. If the
	// executor treated the error as a non-qualifying group it would accept, write the north
	// drivers, and record nothing about the south one.
	entities := []Entity{
		mustEntity(t, "driver", SourceEntityID(lineage, "driver", "A"), map[FieldName]Value{
			"depot": mustString(t, "north"), "driving_hours": NewInt64Value(8),
			"rest_hours": NewInt64Value(10), "violations": NewInt64Value(1),
		}),
		// No violations count, so the guard cannot be evaluated for the south group.
		mustEntity(t, "driver", SourceEntityID(lineage, "driver", "C"), map[FieldName]Value{
			"depot": mustString(t, "south"), "rest_hours": NewInt64Value(5),
		}),
	}
	state, err := NewState(schema, lineage, entities, nil)
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	plan := mustSelectAssignPlan(t, selectAssignRule(t, 3))
	outcome, err := ExecuteTransition(selectAssignBinding(t, plan, state), "certify_depot.v1", state, Journal{})
	if err != nil {
		t.Fatalf("ExecuteTransition: %v", err)
	}
	failure := mustTransitionFailure(t, outcome)
	if got := failure.Code(); got != string(SelectionExpressionUnavailable) {
		t.Fatalf("refusal code %s, want %s", got, SelectionExpressionUnavailable)
	}
}

// The same distinction one level down: a group that PASSES the guard and then cannot produce
// an assignment value refuses too, rather than writing a partial patch or skipping a member.
func TestSelectAssignRefusesAnUnevaluableAssignmentValue(t *testing.T) {
	schema := selectAssignSchema(t)
	lineage, err := NewInputLineageID("maiden-lane.sanitized-fixture", "depot-fleet")
	if err != nil {
		t.Fatalf("NewInputLineageID: %v", err)
	}
	entities := []Entity{
		// violations is present, so the guard succeeds; rest_hours is absent, so the sum
		// cannot be formed. Reaching the assignment at all is what this fixture is for.
		mustEntity(t, "driver", SourceEntityID(lineage, "driver", "A"), map[FieldName]Value{
			"depot": mustString(t, "north"), "driving_hours": NewInt64Value(8),
			"violations": NewInt64Value(1),
		}),
	}
	state, err := NewState(schema, lineage, entities, nil)
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	plan := mustSelectAssignPlan(t, selectAssignRule(t, 3))
	outcome, err := ExecuteTransition(selectAssignBinding(t, plan, state), "certify_depot.v1", state, Journal{})
	if err != nil {
		t.Fatalf("ExecuteTransition: %v", err)
	}
	failure := mustTransitionFailure(t, outcome)
	if got := failure.Code(); got != string(SelectionExpressionUnavailable) {
		t.Fatalf("refusal code %s, want %s", got, SelectionExpressionUnavailable)
	}
}

// Decision 3's missing half: derivation over an EXPRESSION TREE. Each subtest removes one
// path from the authored declaration, and each path is reachable from a different tree, so a
// derivation that walked two of the three would pass the other two subtests.
func TestSelectAssignDerivesReadsFromEveryExpressionTree(t *testing.T) {
	trees := map[string]FieldPath{
		"grouping expression": "driver.depot",
		"group guard":         "driver.violations",
		"assignment value":    "driver.rest_hours",
	}
	for name, path := range trees {
		t.Run(name, func(t *testing.T) {
			rule := selectAssignRule(t, 3)
			rule.DeclaredReads = slices.DeleteFunc(rule.DeclaredReads, func(p FieldPath) bool { return p == path })
			compilation, err := Compile(selectAssignRequest(t, rule))
			if err != nil {
				t.Fatalf("Compile: %v", err)
			}
			diagnostics := compilationDiagnostics(t, compilation)
			if !hasDiagnostic(diagnostics, DeclaredAccessMismatch, "reads") {
				t.Fatalf("omitting %s produced %v, want a reads mismatch", path, diagnostics)
			}
		})
	}
	t.Run("assignment target", func(t *testing.T) {
		rule := selectAssignRule(t, 3)
		rule.DeclaredWrites = []FieldPath{"driver.status"}
		compilation, err := Compile(selectAssignRequest(t, rule))
		if err != nil {
			t.Fatalf("Compile: %v", err)
		}
		diagnostics := compilationDiagnostics(t, compilation)
		if !hasDiagnostic(diagnostics, DeclaredAccessMismatch, "writes") {
			t.Fatalf("diagnostics %v, want a writes mismatch", diagnostics)
		}
	})
}

// Each case must be refused FOR ITS OWN REASON.
//
// An earlier version asserted only that a malformed declaration produced some diagnostic, and
// three mutations survived it: dropping the guard type check, the assignment type agreement,
// and the empty-assignment check all still refused, because each fixture had also changed the
// rule's derived reads or writes and tripped DECLARED_ACCESS_MISMATCH instead. The subtests
// passed while testing nothing they claimed to. So every case now states the diagnostic
// detail it expects and asserts that an access mismatch is NOT what refused it -- which makes
// an unbalanced fixture a loud failure rather than a silent pass.
func TestSelectAssignRefusesMalformedDeclarationsForTheStatedReason(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*TransformationDeclaration)
		wantDetail string
		// accessMismatchExpected marks the one case that cannot avoid it: an operator tag
		// disagreeing with its payload derives nothing at all, so the declared sets have
		// nothing to agree with.
		accessMismatchExpected bool
	}{{
		name: "ungrouped selector",
		mutate: func(rule *TransformationDeclaration) {
			rule.SelectAssign.Selector.GroupBy = nil
			rule.DeclaredReads = []FieldPath{"driver.driving_hours", "driver.rest_hours", "driver.violations"}
		},
		wantDetail: "selector_ungrouped",
	}, {
		name: "no assignments",
		mutate: func(rule *TransformationDeclaration) {
			rule.SelectAssign.Assignments = nil
			rule.DeclaredReads = []FieldPath{"driver.depot", "driver.violations"}
			rule.DeclaredWrites = nil
		},
		wantDetail: "assignments_empty",
	}, {
		name: "guard is member-scoped",
		mutate: func(rule *TransformationDeclaration) {
			// Reads the same field the group predicate did, so the derived sets are
			// untouched and the scope rule is the only thing left to object.
			rule.SelectAssign.Guard = Expr{Kind: ExprLess, Args: []Expr{
				fieldExpr("driver.violations"), intLiteral(3),
			}}
		},
		wantDetail: "guard",
	}, {
		name: "guard quantifies over another entity kind",
		mutate: func(rule *TransformationDeclaration) {
			rule.SelectAssign.Guard = Expr{Kind: ExprAllMembers, Args: []Expr{{
				Kind: ExprLess, Args: []Expr{fieldExpr("driver.violations"), intLiteral(3)},
			}}}
			rule.SelectAssign.Guard.Args[0] = Expr{Kind: ExprExists, Field: "depot.region"}
			rule.DeclaredReads = []FieldPath{
				"depot.region", "driver.depot", "driver.driving_hours", "driver.rest_hours",
			}
		},
		wantDetail: "guard",
	}, {
		name: "assignment writes the wrong type",
		mutate: func(rule *TransformationDeclaration) {
			rule.SelectAssign.Assignments[0].Value = stringLiteral(t, "eighteen")
			rule.DeclaredReads = []FieldPath{"driver.depot", "driver.violations"}
		},
		wantDetail: "assignment_00",
	}, {
		name: "assignment reads an entity kind the selector does not bind",
		mutate: func(rule *TransformationDeclaration) {
			rule.SelectAssign.Assignments[1].Value = fieldExpr("depot.region")
			rule.DeclaredReads = []FieldPath{
				"depot.region", "driver.depot", "driver.driving_hours",
				"driver.rest_hours", "driver.violations",
			}
		},
		wantDetail: "assignment_01",
	}, {
		name: "two assignments to one target",
		mutate: func(rule *TransformationDeclaration) {
			rule.SelectAssign.Assignments = append(rule.SelectAssign.Assignments,
				FieldAssignment{Target: "driver.status", Value: stringLiteral(t, "other")})
		},
		wantDetail: "assignment_02",
	}, {
		name:                   "payload disagrees with the operator tag",
		mutate:                 func(rule *TransformationDeclaration) { rule.Operator = OperatorFormRelatedEntity },
		wantDetail:             "operator_union",
		accessMismatchExpected: true,
	}, {
		// The premise the identity test relies on: a selector cannot name one kind while its
		// paths name another, which is what makes the encoded kind byte redundant.
		name: "selector kind disagrees with its paths",
		mutate: func(rule *TransformationDeclaration) {
			rule.SelectAssign.Selector.Kind = "depot"
		},
		wantDetail: "selector",
	}, {
		name: "cardinality was never stated",
		mutate: func(rule *TransformationDeclaration) {
			rule.SelectAssign.Selector.Members = Cardinality{}
		},
		wantDetail: "selector",
	}}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rule := selectAssignRule(t, 3)
			test.mutate(&rule)
			compilation, err := Compile(selectAssignRequest(t, rule))
			if err != nil {
				t.Fatalf("Compile: %v", err)
			}
			diagnostics := compilationDiagnostics(t, compilation)
			if !hasDiagnostic(diagnostics, UnsupportedOperator, test.wantDetail) {
				t.Fatalf("diagnostics %v, want UNSUPPORTED_OPERATOR/%s", diagnostics, test.wantDetail)
			}
			if test.accessMismatchExpected {
				return
			}
			for _, diagnostic := range diagnostics {
				if diagnostic.Code() == DeclaredAccessMismatch {
					t.Fatalf("fixture also mismatched its declared %s, so the refusal above "+
						"is not the only reason this would be refused", diagnostic.Detail())
				}
			}
		})
	}
}

// One proposition -- which obligations a rule carries -- derived in two places: once by
// deriveTransformation, which puts them on the plan, and once by encodeRuleset, which puts
// them in the identity. Nothing but this forces them to agree, and a rule whose plan carries
// obligations its digest does not cover is a rule whose identity is not its meaning.
func TestRulesetIdentityCoversEveryDerivedInvariant(t *testing.T) {
	rules := []TransformationDeclaration{selectAssignRule(t, 3)}
	rules = append(rules, compileFixtureRequest(t, false).Rules.Transformations...)
	for _, rule := range rules {
		t.Run(string(rule.ID), func(t *testing.T) {
			var derived []InvariantDeclaration
			switch rule.Operator {
			case OperatorFormRelatedEntity:
				derived = formInvariants(rule.ID, rule.Form.GroupingField)
			case OperatorAggregateRelatedFields:
				derived = aggregateInvariants(rule.ID, rule.Aggregate)
			case OperatorSelectAndAssign:
				derived = selectAssignInvariants(rule.ID, rule.SelectAssign)
			default:
				t.Fatalf("operator %d has no arm in this test, and encodeRuleset may not either", rule.Operator)
			}
			if len(derived) == 0 {
				t.Fatal("operator derives no obligations, so a rule can refuse with no declared code")
			}
			encoded, err := encodeRuleset(normalizedRuleset{transformations: []TransformationDeclaration{rule}})
			if err != nil {
				t.Fatalf("encodeRuleset: %v", err)
			}
			// The identity must move when an obligation does. Dropping one from the encoded
			// set has to change the bytes, which is only true if they participate at all.
			var encoder canonicalEncoder
			encoder.tag(rulesetDomainTag)
			encoder.uint64(1)
			encodeTransformationDeclaration(&encoder, rule)
			encoder.uint64(uint64(len(derived) - 1))
			for _, invariant := range derived[:len(derived)-1] {
				encodeInvariantDeclaration(&encoder, invariant)
			}
			encodeCheckpoints(&encoder, nil)
			short, err := encoder.bytes()
			if err != nil {
				t.Fatalf("encode short ruleset: %v", err)
			}
			if bytes.Equal(encoded, short) {
				t.Fatal("dropping a derived obligation left the ruleset bytes unchanged")
			}
		})
	}
}

// Golden vectors for the three group kinds, promised by encodeExpr's default arm when a
// Transform made them reachable. They pin the SCHEME -- that the kind byte participates and
// which operand shape each kind writes -- not an inventory of kinds, which the sketch
// predicts will change. Kind bytes are append-only.
func TestGroupExpressionCanonicalEncodingIsPinned(t *testing.T) {
	inner := Expr{Kind: ExprExists, Field: "driver.depot"}
	// Written as its parts so the vector can be checked by reading rather than by running:
	// ExprLiteral is iota+1 and nine kinds precede ExprAllMembers, so the three group kinds
	// are 0x0a, 0x0b and 0x0c; canonicalEncoder.string writes a uint64 length before its
	// bytes, and "driver.depot" is twelve of them.
	const (
		path      = "6472697665722e6465706f74"
		pathLen   = "000000000000000c"
		oneArg    = "0000000000000001"
		existsTag = "03"
	)
	tests := []struct {
		name string
		expr Expr
		want string
	}{
		{name: "all_members", expr: Expr{Kind: ExprAllMembers, Args: []Expr{inner}},
			want: "0a" + oneArg + existsTag + pathLen + path},
		{name: "any_members", expr: Expr{Kind: ExprAnyMembers, Args: []Expr{inner}},
			want: "0b" + oneArg + existsTag + pathLen + path},
		{name: "all_equal", expr: Expr{Kind: ExprAllEqual, Field: "driver.depot"},
			want: "0c" + pathLen + path},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var encoder canonicalEncoder
			encodeExpr(&encoder, test.expr)
			got, err := encoder.bytes()
			if err != nil {
				t.Fatalf("encodeExpr: %v", err)
			}
			if encoded := hex.EncodeToString(got); encoded != test.want {
				t.Fatalf("encoding\n got: %s\nwant: %s", encoded, test.want)
			}
		})
	}
	// The kind byte is what separates the two shapes that encode identically otherwise.
	var members, equal canonicalEncoder
	encodeExpr(&members, Expr{Kind: ExprAnyMembers, Args: []Expr{inner}})
	encodeExpr(&equal, Expr{Kind: ExprAllMembers, Args: []Expr{inner}})
	first, _ := members.bytes()
	second, _ := equal.bytes()
	if bytes.Equal(first, second) {
		t.Fatal("all_members and any_members encode identically")
	}
}

// A rule that overwrites must state the value it overwrote.
//
// Every other fixture here starts with the written fields absent, so the before-image is
// AbsentField on every path and a mutation hard-coding it passes them all -- verified by
// running that mutation. ApplyPatch compares the before-image against the state and refuses
// on disagreement, which is what makes a patch valid against one predecessor only, so an
// executor that always claimed absence would produce a rule usable only on states where its
// target happens to be empty.
func TestSelectAssignRecordsTheValueItOverwrites(t *testing.T) {
	schema := selectAssignSchema(t)
	lineage, err := NewInputLineageID("maiden-lane.sanitized-fixture", "depot-fleet")
	if err != nil {
		t.Fatalf("NewInputLineageID: %v", err)
	}
	entities := []Entity{
		mustEntity(t, "driver", SourceEntityID(lineage, "driver", "A"), map[FieldName]Value{
			"depot": mustString(t, "north"), "driving_hours": NewInt64Value(8),
			"rest_hours": NewInt64Value(10), "violations": NewInt64Value(1),
			// Present before the rule runs, and different from what the rule writes.
			"status":      mustString(t, "provisional"),
			"shift_total": NewInt64Value(-1),
		}),
	}
	state, err := NewState(schema, lineage, entities, nil)
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	plan := mustSelectAssignPlan(t, selectAssignRule(t, 3))
	outcome := mustAcceptedTransition(t, selectAssignBinding(t, plan, state), "certify_depot.v1", state, Journal{})

	if !outcome.HasPatch() {
		t.Fatal("accepted transition carried no patch")
	}
	operations := outcome.Patch().Operations()
	if len(operations) != 1 {
		t.Fatalf("patch holds %d operations, want 1", len(operations))
	}
	update, isUpdate := operations[0].Update()
	if !isUpdate {
		t.Fatal("operation is not an update")
	}
	before := make(map[FieldName]FieldImage, len(update.Fields()))
	for _, change := range update.Fields() {
		before[change.Name] = change.Before
	}
	status, recorded := before["status"]
	if !recorded || !status.Present() {
		t.Fatal("the overwritten status was recorded as absent")
	}
	if value, _ := status.Value(); !value.Equal(mustString(t, "provisional")) {
		t.Fatalf("before-image of status is %v, want provisional", value)
	}
	total, recorded := before["shift_total"]
	if !recorded || !total.Present() {
		t.Fatal("the overwritten shift_total was recorded as absent")
	}
	if value, _ := total.Value(); !value.Equal(NewInt64Value(-1)) {
		t.Fatalf("before-image of shift_total is %v, want -1", value)
	}

	// And the write landed, so the before-image is not merely recorded but consistent with
	// a patch the kernel actually accepted.
	for _, entity := range outcome.State().Entities() {
		assertFieldEquals(t, entity, "status", mustString(t, "certified"))
		assertFieldEquals(t, entity, "shift_total", NewInt64Value(18))
	}
}

// Every authored part of the payload must move the ruleset identity.
//
// PAIRS, NOT VARIANTS-AGAINST-A-BASE, and the difference is the whole test. An earlier version
// altered one payload part per case and compared against the unaltered rule -- but altering a
// grouping expression or an assignment target also changes the rule's DECLARED reads and
// writes, and those are encoded separately a few lines above the payload. Four mutations
// survived it: deleting the grouping, the filter, the targets and the selected kind from
// encodeTransformationDeclaration each left the digest moving anyway, for a reason that had
// nothing to do with the payload.
//
// Each pair below therefore holds the declared sets IDENTICAL and differs only inside the
// payload, so the payload encoding is the only thing that can separate them.
func TestSelectAssignIdentityCoversEveryAuthoredPart(t *testing.T) {
	certified := stringLiteral(t, "certified")
	sum := func(first, second FieldPath) Expr {
		return Expr{Kind: ExprAdd, Args: []Expr{fieldExpr(first), fieldExpr(second)}}
	}
	present := Expr{Kind: ExprExists, Field: "driver.status"}
	doubleNegated := Expr{Kind: ExprNot, Args: []Expr{{Kind: ExprNot, Args: []Expr{present}}}}

	pairs := []struct {
		name  string
		left  func(*TransformationDeclaration)
		right func(*TransformationDeclaration)
	}{{
		name:  "group predicate threshold",
		left:  func(rule *TransformationDeclaration) { rule.SelectAssign.Guard.Args[0].Args[1] = intLiteral(3) },
		right: func(rule *TransformationDeclaration) { rule.SelectAssign.Guard.Args[0].Args[1] = intLiteral(9) },
	}, {
		// Commuted operands: the same groups at runtime, a different authored rule. Identity
		// is over what the author wrote, not over what it happens to compute.
		name: "grouping expression",
		left: func(rule *TransformationDeclaration) {
			groupBy := sum("driver.driving_hours", "driver.rest_hours")
			rule.SelectAssign.Selector.GroupBy = &groupBy
			rule.DeclaredReads = []FieldPath{"driver.driving_hours", "driver.rest_hours", "driver.violations"}
		},
		right: func(rule *TransformationDeclaration) {
			groupBy := sum("driver.rest_hours", "driver.driving_hours")
			rule.SelectAssign.Selector.GroupBy = &groupBy
			rule.DeclaredReads = []FieldPath{"driver.driving_hours", "driver.rest_hours", "driver.violations"}
		},
	}, {
		name: "filter predicate",
		left: func(rule *TransformationDeclaration) {
			where := present
			rule.SelectAssign.Selector.Where = &where
			rule.DeclaredReads = append(rule.DeclaredReads, "driver.status")
			slices.Sort(rule.DeclaredReads)
		},
		right: func(rule *TransformationDeclaration) {
			where := doubleNegated
			rule.SelectAssign.Selector.Where = &where
			rule.DeclaredReads = append(rule.DeclaredReads, "driver.status")
			slices.Sort(rule.DeclaredReads)
		},
	}, {
		// The same two targets and the same two values, PAIRED THE OTHER WAY ROUND. Since
		// assignments are now sorted by target, a pair that merely reordered them would be
		// the same rule -- so what has to separate these is which value each target receives.
		name: "value paired with target",
		left: func(rule *TransformationDeclaration) {
			rule.SelectAssign.Assignments = []FieldAssignment{
				{Target: "driver.depot", Value: certified},
				{Target: "driver.status", Value: stringLiteral(t, "north")},
			}
			rule.DeclaredReads = []FieldPath{"driver.depot", "driver.violations"}
			rule.DeclaredWrites = []FieldPath{"driver.depot", "driver.status"}
		},
		right: func(rule *TransformationDeclaration) {
			rule.SelectAssign.Assignments = []FieldAssignment{
				{Target: "driver.depot", Value: stringLiteral(t, "north")},
				{Target: "driver.status", Value: certified},
			}
			rule.DeclaredReads = []FieldPath{"driver.depot", "driver.violations"}
			rule.DeclaredWrites = []FieldPath{"driver.depot", "driver.status"}
		},
	}, {
		name: "assignment value",
		left: func(rule *TransformationDeclaration) { rule.SelectAssign.Assignments[1].Value = certified },
		right: func(rule *TransformationDeclaration) {
			rule.SelectAssign.Assignments[1].Value = stringLiteral(t, "provisional")
		},
	}, {
		name: "declared cardinality",
		left: func(rule *TransformationDeclaration) {
			rule.SelectAssign.Selector.Members = Cardinality{Kind: CardinalityAtLeast, Count: 1}
		},
		right: func(rule *TransformationDeclaration) {
			rule.SelectAssign.Selector.Members = Cardinality{Kind: CardinalityAny}
		},
	}}
	// NO PAIR FOR THE SELECTED KIND, deliberately. Every path in a selector-scoped rule must
	// bind the selector's own kind, so two rules with identical declared reads and writes
	// cannot select different kinds -- the kind is recoverable from the paths, and the byte
	// is redundant rather than load-bearing. That premise is not assumed here; it is what the
	// "selector kind disagrees with its paths" case in the malformed table enforces.
	for _, pair := range pairs {
		t.Run(pair.name, func(t *testing.T) {
			left, right := selectAssignRule(t, 3), selectAssignRule(t, 3)
			pair.left(&left)
			pair.right(&right)
			if slices.Compare(left.DeclaredReads, right.DeclaredReads) != 0 ||
				slices.Compare(left.DeclaredWrites, right.DeclaredWrites) != 0 {
				t.Fatal("the pair differs in its declared access sets, so the digests would " +
					"separate whether or not the payload is encoded")
			}
			// Both must still COMPILE. A pair where one side is illegal would pass by never
			// producing a second digest.
			leftPlan, rightPlan := mustSelectAssignPlan(t, left), mustSelectAssignPlan(t, right)
			if leftPlan.RulesetDigest() == rightPlan.RulesetDigest() {
				t.Fatalf("two rules differing only in their %s share a ruleset digest", pair.name)
			}
		})
	}
}

// selectAssignStateWith builds a state from raw field maps, for fixtures whose point is an
// ABSENT field that the typed driverFixture cannot express.
func selectAssignStateWith(t *testing.T, schema Schema, drivers map[string]map[FieldName]Value) State {
	t.Helper()
	lineage, err := NewInputLineageID("maiden-lane.sanitized-fixture", "depot-fleet")
	if err != nil {
		t.Fatalf("NewInputLineageID: %v", err)
	}
	keys := make([]string, 0, len(drivers))
	for key := range drivers {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	entities := make([]Entity, 0, len(drivers))
	for _, key := range keys {
		entities = append(entities, mustEntity(t, "driver", SourceEntityID(lineage, "driver", key), drivers[key]))
	}
	state, err := NewState(schema, lineage, entities, nil)
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	return state
}

// SELECTING IS EVALUATION, so the selector's own two expressions fail on data exactly as the
// guard does -- and must refuse with a code rather than abort the transition.
//
// An earlier version of executeSelectAndAssign turned every Select error into a returned Go
// error, on a comment asserting Select only fails for artifact reasons. It does not:
// evaluateBool over Where and evaluateValue over GroupBy both refuse an absent field. One
// driver missing its depot would have left the spine on the machinery-inability channel as an
// internal error, with no SELECTION_* code and no invariant result -- an operational outage
// standing in for a deterministic refusal about one row of data.
//
// The guard fixtures could not catch it: they omit the guard's field and the assignment's
// field, never the grouping or filter field, and no other test executes a rule carrying a
// Where at all.
func TestSelectAssignRefusesWhenTheSELECTORCannotBeEvaluated(t *testing.T) {
	schema := selectAssignSchema(t)
	complete := map[FieldName]Value{
		"depot": mustString(t, "north"), "driving_hours": NewInt64Value(8),
		"rest_hours": NewInt64Value(10), "violations": NewInt64Value(1),
	}
	withoutDepot := map[FieldName]Value{
		"driving_hours": NewInt64Value(9), "rest_hours": NewInt64Value(11), "violations": NewInt64Value(2),
	}
	withoutStatus := map[FieldName]Value{
		"depot": mustString(t, "south"), "driving_hours": NewInt64Value(9),
		"rest_hours": NewInt64Value(11), "violations": NewInt64Value(2),
	}

	t.Run("grouping expression", func(t *testing.T) {
		state := selectAssignStateWith(t, schema, map[string]map[FieldName]Value{"A": complete, "B": withoutDepot})
		plan := mustSelectAssignPlan(t, selectAssignRule(t, 3))
		outcome, err := ExecuteTransition(selectAssignBinding(t, plan, state), "certify_depot.v1", state, Journal{})
		if err != nil {
			t.Fatalf("a driver with no depot aborted the transition instead of refusing it: %v", err)
		}
		failure := mustTransitionFailure(t, outcome)
		if got := failure.Code(); got != string(SelectionExpressionUnavailable) {
			t.Fatalf("refusal code %s, want %s", got, SelectionExpressionUnavailable)
		}
	})

	t.Run("filter predicate", func(t *testing.T) {
		// A rule with a Where, executed. Nothing else in this package executes one, so
		// without this the filter is compiled and encoded but never run.
		rule := selectAssignRule(t, 3)
		where := Expr{Kind: ExprLess, Args: []Expr{fieldExpr("driver.driving_hours"), intLiteral(100)}}
		rule.SelectAssign.Selector.Where = &where
		state := selectAssignStateWith(t, schema, map[string]map[FieldName]Value{
			"A": complete,
			"B": {"depot": mustString(t, "south"), "rest_hours": NewInt64Value(11), "violations": NewInt64Value(2)},
		})
		plan := mustSelectAssignPlan(t, rule)
		outcome, err := ExecuteTransition(selectAssignBinding(t, plan, state), "certify_depot.v1", state, Journal{})
		if err != nil {
			t.Fatalf("a driver the filter cannot evaluate aborted the transition: %v", err)
		}
		failure := mustTransitionFailure(t, outcome)
		if got := failure.Code(); got != string(SelectionExpressionUnavailable) {
			t.Fatalf("refusal code %s, want %s", got, SelectionExpressionUnavailable)
		}
	})

	t.Run("a filter that excludes is not a filter that fails", func(t *testing.T) {
		// The other half: a Where that evaluates and answers false must SELECT less, not
		// refuse. Without this, the subtest above would pass on a rule that refused whenever
		// a Where was present at all.
		rule := selectAssignRule(t, 3)
		where := Expr{Kind: ExprExists, Field: "driver.status"}
		rule.SelectAssign.Selector.Where = &where
		rule.DeclaredReads = append(rule.DeclaredReads, "driver.status")
		slices.Sort(rule.DeclaredReads)
		selected := make(map[FieldName]Value, len(complete)+1)
		for name, value := range complete {
			selected[name] = value
		}
		selected["status"] = mustString(t, "provisional")
		state := selectAssignStateWith(t, schema, map[string]map[FieldName]Value{
			"A": selected,      // has status, so the filter admits it
			"B": withoutStatus, // no status, so the filter excludes it
		})
		plan := mustSelectAssignPlan(t, rule)
		outcome := mustAcceptedTransition(t, selectAssignBinding(t, plan, state), "certify_depot.v1", state, Journal{})
		if got := len(outcome.Patch().Operations()); got != 1 {
			t.Fatalf("patch holds %d operations, want only the filtered-in driver", got)
		}
	})
}

// A refusal may not attest to obligations that were never established.
//
// evaluatedFailureResults marks every declaration ordered BEFORE the failing key as passed,
// and those results are sealed into a digested ProtectedInvariantFailureReport. So the
// obligation keys have to be numbered in the order the executor checks them; numbered any
// other way, a cardinality refusal records "the selection was non-empty" as passed for a
// selection that admitted no group at all.
func TestSelectAssignRefusalRecordsNoUnestablishedObligation(t *testing.T) {
	schema := selectAssignSchema(t)
	rule := selectAssignRule(t, 3)
	rule.SelectAssign.Selector.Members = Cardinality{Kind: CardinalityExactly, Count: 2}
	plan := mustSelectAssignPlan(t, rule)
	// ONE depot holding ONE driver: the only group violates the cardinality, so the set of
	// qualifying groups is empty and "the selection was non-empty" is false.
	state := selectAssignState(t, schema, []driverFixture{
		{key: "C", depot: "south", driving: 4, rest: 5, violations: 1},
	})
	outcome, err := ExecuteTransition(selectAssignBinding(t, plan, state), "certify_depot.v1", state, Journal{})
	if err != nil {
		t.Fatalf("ExecuteTransition: %v", err)
	}
	failure := mustTransitionFailure(t, outcome)
	if got := failure.Code(); got != string(SelectionCardinalityInvalid) {
		t.Fatalf("refusal code %s, want %s", got, SelectionCardinalityInvalid)
	}
	for _, result := range outcome.InvariantResults() {
		if result.Code() == SelectionEmpty && result.Passed() {
			t.Fatal("the refusal record claims the selection was non-empty, and it was empty")
		}
		if result.Code() == SelectionGuardUnsatisfied && result.Passed() {
			t.Fatal("the refusal record claims a group satisfied the guard, and none was assessed")
		}
	}
}

// Authored assignment order is not semantic, so it must not reach the identity.
//
// Two rulesets listing the same assignments in different order produce byte-identical
// patches, journals and states -- every value is evaluated against the same pre-state member,
// none observes another, and NewPatch sorts an update's fields by name. Before
// normalizeTransformationPayload gained its arm they nonetheless carried different ruleset
// digests, so the same rule written twice was two rules with two SemanticRunIDs.
func TestSelectAssignAuthoredOrderIsNotIdentity(t *testing.T) {
	certified := stringLiteral(t, "certified")
	total := Expr{Kind: ExprAdd, Args: []Expr{
		fieldExpr("driver.driving_hours"), fieldExpr("driver.rest_hours"),
	}}
	authored := func(assignments ...FieldAssignment) TransformationDeclaration {
		rule := selectAssignRule(t, 3)
		rule.SelectAssign.Assignments = assignments
		return rule
	}
	first := mustSelectAssignPlan(t, authored(
		FieldAssignment{Target: "driver.shift_total", Value: total},
		FieldAssignment{Target: "driver.status", Value: certified},
	))
	second := mustSelectAssignPlan(t, authored(
		FieldAssignment{Target: "driver.status", Value: certified},
		FieldAssignment{Target: "driver.shift_total", Value: total},
	))
	if first.RulesetDigest() != second.RulesetDigest() {
		t.Fatal("the same rule authored in two orders produced two ruleset digests")
	}
	if first.ID() != second.ID() {
		t.Fatal("the same rule authored in two orders produced two plan identities")
	}
}

// An authored tree deeper than the language admits must be REFUSED, not walked.
//
// maxExprDepth is enforced in checkExpr and checkExprInScope, which run inside
// deriveTransformation -- and normalizeRuleset clones the authored trees and encodeRuleset
// encodes them, both before that. Until checkAuthoredPayloadBounds ran first, those two
// recursions consumed a tree nothing had bounded, and a deep enough guard exhausted the
// goroutine stack: a fatal runtime error, not a refusal, and not recoverable.
//
// The nesting here is far past maxExprDepth but small enough to build and to survive if the
// bound holds, which is the point: the test must fail by REPORTING, not by crashing the
// process, so it cannot be written at the depth that actually overflows.
func TestSelectAssignRefusesAnUnboundedAuthoredTree(t *testing.T) {
	nested := func(leaf Expr, depth int) Expr {
		for i := 0; i < depth; i++ {
			leaf = Expr{Kind: ExprNot, Args: []Expr{leaf}}
		}
		return leaf
	}
	exists := Expr{Kind: ExprExists, Field: "driver.violations"}

	// EVERY TREE THE PAYLOAD CARRIES, not just the guard. A bound applied to one of the four
	// leaves the other three walked unbounded, and a version checking only the guard passed
	// a single-case version of this test.
	trees := map[string]func(*TransformationDeclaration, Expr){
		"guard": func(rule *TransformationDeclaration, deep Expr) {
			rule.SelectAssign.Guard = Expr{Kind: ExprAllMembers, Args: []Expr{deep}}
		},
		"assignment value": func(rule *TransformationDeclaration, deep Expr) {
			// A bool-typed value would not type-check against an int64 target, but the bound
			// must refuse the tree before any of that is reached.
			rule.SelectAssign.Assignments[0].Value = deep
		},
		"filter predicate": func(rule *TransformationDeclaration, deep Expr) {
			rule.SelectAssign.Selector.Where = &deep
		},
		"grouping expression": func(rule *TransformationDeclaration, deep Expr) {
			rule.SelectAssign.Selector.GroupBy = &deep
		},
	}
	for name, place := range trees {
		t.Run(name, func(t *testing.T) {
			rule := selectAssignRule(t, 3)
			place(&rule, nested(exists, maxExprDepth+10))
			if _, err := Compile(selectAssignRequest(t, rule)); err == nil {
				t.Fatalf("a %s nested past maxExprDepth was accepted by canonicalization", name)
			}
		})
	}

	// And the bound is a bound, not a blanket refusal: a tree just inside it still compiles,
	// or every subtest above would pass against an implementation that refused all guards.
	t.Run("a shallow tree still compiles", func(t *testing.T) {
		legal := selectAssignRule(t, 3)
		legal.SelectAssign.Guard = Expr{Kind: ExprAllMembers, Args: []Expr{nested(exists, 4)}}
		mustSelectAssignPlan(t, legal)
	})
}

// Distinct declarations must encode to distinct ruleset bytes, INCLUDING declarations the
// compiler will later refuse.
//
// The payload is written with no presence marker so that adding it left every existing
// declaration's bytes untouched. The comment justifying that once argued injectivity from the
// operator byte -- which is wrong, because encodeRuleset runs before the operator/payload
// agreement check, so a declaration carrying operator 0x01 AND a SelectAssign payload reaches
// the encoder. RulesetDigest feeds CompilationInputDigest, which is computed for refused
// compilations too, so the set this must be injective over is every declaration that reaches
// the encoder, not the subset that survives validation.
//
// This does not prove injectivity; it tests the cases the broken argument left uncovered.
func TestRulesetEncodingSeparatesDistinctDeclarations(t *testing.T) {
	base := selectAssignRule(t, 3)
	form := compileFixtureRequest(t, false).Rules.Transformations[0]
	form.ID = "certify_depot.v1"

	withStray := form
	withStray.SelectAssign = base.SelectAssign

	strayOnly := form
	strayOnly.Form = nil
	strayOnly.SelectAssign = base.SelectAssign

	aggregate := compileFixtureRequest(t, false).Rules.Transformations[1]
	aggregate.ID = "certify_depot.v1"

	aggregateWithStray := aggregate
	aggregateWithStray.SelectAssign = base.SelectAssign

	otherThreshold := selectAssignRule(t, 9)

	candidates := map[string]TransformationDeclaration{
		"select-assign":                         base,
		"select-assign with another threshold":  otherThreshold,
		"form only":                             form,
		"form carrying a stray select-assign":   withStray,
		"select-assign under the form operator": strayOnly,
		"aggregate only":                        aggregate,
		"aggregate carrying a stray payload":    aggregateWithStray,
	}
	seen := make(map[string]string, len(candidates))
	names := make([]string, 0, len(candidates))
	for name := range candidates {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		encoded, err := encodeRuleset(normalizedRuleset{
			transformations: []TransformationDeclaration{candidates[name]},
		})
		if err != nil {
			t.Fatalf("%s: encodeRuleset: %v", name, err)
		}
		key := string(encoded)
		if previous, collision := seen[key]; collision {
			t.Fatalf("%q and %q encode to the same ruleset bytes", previous, name)
		}
		seen[key] = name
	}
}

// A declaration the encoder cannot encode is an ERROR, not a diagnostic, and that is forced.
//
// Guard is a value type, so omitting it yields Expr{Kind: 0}, which encodeExpr's fail-closed
// default refuses -- and Compile returns an error rather than the UNSUPPORTED_OPERATOR/"guard"
// diagnostic that deriveTransformation would produce. This is not a choice that could go the
// other way: a CompilationFailure is identified by its CompilationInputDigest, that digest is
// derived from the ruleset bytes, and a declaration with no canonical bytes has no failure
// identity to return. The same is already true of a field path containing invalid UTF-8.
//
// Pinned so the behaviour is a recorded limitation rather than a surprise, and so that the
// error names the rule.
func TestSelectAssignUnencodableDeclarationIsAnError(t *testing.T) {
	rule := selectAssignRule(t, 3)
	rule.SelectAssign.Guard = Expr{}
	_, err := Compile(selectAssignRequest(t, rule))
	if err == nil {
		t.Fatal("a declaration with no canonical encoding compiled")
	}
	// The compiler must still be usable afterwards, and the neighbouring legal rule must
	// still compile -- otherwise this test would also pass against a compiler that had
	// simply stopped working.
	mustSelectAssignPlan(t, selectAssignRule(t, 3))
}
