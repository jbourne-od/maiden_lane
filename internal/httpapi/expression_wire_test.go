package httpapi

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/optimaldynamics/maiden-lane/internal/httpapi/openapiv1"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

func mustLiteral(t *testing.T, text string) *semantic.Value {
	t.Helper()
	value, err := semantic.NewStringValue(text)
	if err != nil {
		t.Fatalf("NewStringValue: %v", err)
	}
	return &value
}

// A well-formed node of every kind, so a round trip can cover the vocabulary rather than the
// handful of kinds one fixture happens to use. Shapes match what the compiler will accept, so
// these same trees are usable in an end-to-end test.
func exemplarExprs(t *testing.T) map[semantic.ExprKind]semantic.Expr {
	t.Helper()
	field := semantic.Expr{Kind: semantic.ExprField, Field: "driver.depot"}
	exists := semantic.Expr{Kind: semantic.ExprExists, Field: "driver.depot"}
	number := semantic.Expr{Kind: semantic.ExprField, Field: "driver.violations"}
	return map[semantic.ExprKind]semantic.Expr{
		semantic.ExprLiteral:    {Kind: semantic.ExprLiteral, Literal: mustLiteral(t, "north")},
		semantic.ExprField:      field,
		semantic.ExprExists:     exists,
		semantic.ExprNot:        {Kind: semantic.ExprNot, Args: []semantic.Expr{exists}},
		semantic.ExprAll:        {Kind: semantic.ExprAll, Args: []semantic.Expr{exists, exists}},
		semantic.ExprAny:        {Kind: semantic.ExprAny, Args: []semantic.Expr{exists}},
		semantic.ExprEqual:      {Kind: semantic.ExprEqual, Args: []semantic.Expr{field, {Kind: semantic.ExprLiteral, Literal: mustLiteral(t, "north")}}},
		semantic.ExprLess:       {Kind: semantic.ExprLess, Args: []semantic.Expr{number, number}},
		semantic.ExprAdd:        {Kind: semantic.ExprAdd, Args: []semantic.Expr{number, number}},
		semantic.ExprAllMembers: {Kind: semantic.ExprAllMembers, Args: []semantic.Expr{exists}},
		semantic.ExprAnyMembers: {Kind: semantic.ExprAnyMembers, Args: []semantic.Expr{exists}},
		semantic.ExprAllEqual:   {Kind: semantic.ExprAllEqual, Field: "driver.depot"},
		semantic.ExprCount:      {Kind: semantic.ExprCount},
		semantic.ExprSum:        {Kind: semantic.ExprSum, Field: "driver.violations"},
		semantic.ExprMin:        {Kind: semantic.ExprMin, Field: "driver.violations"},
		semantic.ExprMax:        {Kind: semantic.ExprMax, Field: "driver.violations"},
	}
}

// EVERY NODE KIND MUST SURVIVE THE ROUND TRIP, and the list is the kernel's vocabulary rather
// than one copied into this file.
//
// The wire tokens are a third enumeration of the expression union, after the kernel's kind
// bytes and its String rendering. A kind present in the kernel and absent from the map here is
// a rule that can be compiled and not authored, or authored and not read back -- and both
// directions fail silently in the sense that nothing but a test would notice. Driving this
// from semantic.AllExprKinds makes forgetting one a failure rather than an omission.
func TestEveryExpressionKindSurvivesTheRoundTrip(t *testing.T) {
	exemplars := exemplarExprs(t)
	kinds := semantic.AllExprKinds()
	if len(kinds) == 0 {
		t.Fatal("the expression vocabulary is empty, so this test asserts nothing")
	}
	for _, kind := range kinds {
		exemplar, present := exemplars[kind]
		if !present {
			t.Errorf("expression kind %s has no exemplar, so the round trip does not cover it", kind)
			continue
		}
		projected, err := exprToWire(exemplar)
		if err != nil {
			t.Errorf("exprToWire(%s): %v", kind, err)
			continue
		}
		// Through JSON, not just through the structs: the contract is what a client sends,
		// and a field the schema drops would be invisible to an in-memory comparison.
		encoded, err := json.Marshal(projected)
		if err != nil {
			t.Errorf("marshal %s: %v", kind, err)
			continue
		}
		var decoded openapiv1.Expr
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			t.Errorf("unmarshal %s: %v", kind, err)
			continue
		}
		returned, err := exprFromWire(decoded)
		if err != nil {
			t.Errorf("exprFromWire(%s): %v", kind, err)
			continue
		}
		if !reflect.DeepEqual(exemplar, returned) {
			t.Errorf("kind %s did not survive the round trip:\n sent: %+v\n got:  %+v", kind, exemplar, returned)
		}
	}
}

// A token outside the closed enum has no meaning for the boundary to invent.
func TestExpressionTranslationRefusesWhatItCannotMap(t *testing.T) {
	if _, err := exprFromWire(openapiv1.Expr{Kind: openapiv1.ExprKind("all_the_members")}); err == nil {
		t.Fatal("an unknown expression token was translated")
	}
	// Nested, because the refusal must hold wherever the node sits and not only at the root.
	nested := openapiv1.Expr{Kind: openapiv1.ExprKindNot, Args: &[]openapiv1.Expr{
		{Kind: openapiv1.ExprKind("")},
	}}
	if _, err := exprFromWire(nested); err == nil {
		t.Fatal("an unknown expression token nested inside a legal one was translated")
	}
	if _, err := exprToWire(semantic.Expr{Kind: semantic.ExprKind(200)}); err == nil {
		t.Fatal("an unrepresentable expression kind was projected")
	}
	deep := semantic.Expr{Kind: semantic.ExprNot, Args: []semantic.Expr{{Kind: semantic.ExprKind(200)}}}
	if _, err := exprToWire(deep); err == nil {
		t.Fatal("an unrepresentable kind nested inside a legal one was projected")
	}
}

// A negative count must be refused rather than wrapped.
//
// The contract's count is int64 and the kernel's is uint64, so -1 converts to 2^64-1: an
// author's typo silently becoming a cardinality no group could satisfy, which then refuses
// every transition with SELECTION_CARDINALITY_INVALID and no clue why. The schema states a
// minimum; this does not rely on it, because a generated validator is not the kernel.
func TestCardinalityTranslationRefusesACountItCannotHold(t *testing.T) {
	negative := int64(-1)
	if _, err := cardinalityFromWire(openapiv1.Cardinality{
		Kind: openapiv1.CardinalityKindExactly, Count: &negative,
	}); err == nil {
		t.Fatal("a negative cardinality count was translated")
	}
	// The kernel's range is wider than the contract's, so the projection has the same duty in
	// the other direction.
	if _, err := cardinalityToWire(semantic.Cardinality{
		Kind: semantic.CardinalityExactly, Count: 1 << 63,
	}); err == nil {
		t.Fatal("a count past the contract's range was projected")
	}

	// THE THRESHOLD ITSELF, because a fixture that only tries 1<<63 cannot see where the
	// bound actually sits. An earlier version refused at 1<<62 -- half the signed maximum --
	// while inbound accepted anything non-negative, so a count in between was authorable,
	// storable and unreadable, and this test passed either way.
	if _, err := cardinalityToWire(semantic.Cardinality{
		Kind: semantic.CardinalityExactly, Count: maxContractCount,
	}); err != nil {
		t.Fatalf("the largest count the contract can hold was refused: %v", err)
	}
	if _, err := cardinalityToWire(semantic.Cardinality{
		Kind: semantic.CardinalityExactly, Count: maxContractCount + 1,
	}); err == nil {
		t.Fatal("a count one past the contract's range was projected")
	}

	// And the two directions admit the same set: whatever inbound accepts must project back.
	largest := int64(maxContractCount)
	admitted, err := cardinalityFromWire(openapiv1.Cardinality{
		Kind: openapiv1.CardinalityKindAtLeast, Count: &largest,
	})
	if err != nil {
		t.Fatalf("the contract's largest count was refused inbound: %v", err)
	}
	if _, err := cardinalityToWire(admitted); err != nil {
		t.Fatalf("a count this boundary accepted cannot be read back: %v", err)
	}
	if _, err := cardinalityFromWire(openapiv1.Cardinality{Kind: openapiv1.CardinalityKind("some")}); err == nil {
		t.Fatal("an unknown cardinality token was translated")
	}
	if _, err := cardinalityToWire(semantic.Cardinality{Kind: semantic.CardinalityInvalid}); err == nil {
		t.Fatal("the unstated cardinality was projected as though it were a choice")
	}
	// And the legal ones still work.
	for _, kind := range []semantic.CardinalityKind{
		semantic.CardinalityAny, semantic.CardinalityExactly, semantic.CardinalityAtLeast,
	} {
		if _, err := cardinalityToWire(semantic.Cardinality{Kind: kind, Count: 2}); err != nil {
			t.Fatalf("cardinality kind %d: %v", kind, err)
		}
	}
}

// The boundary decides nothing about expression shape.
//
// A node whose operands are illegal must reach the compiler and come back as a diagnostic
// naming the rule, not be rejected here as a malformed request. Stated as a test because the
// tempting mistake is to "help" by validating shape at the edge, which puts one proposition in
// two places and makes the author's error message worse.
func TestExpressionTranslationDoesNotJudgeShape(t *testing.T) {
	field := "driver.depot"
	// `exists` carrying arguments and no field, and `not` with three operands: both are
	// refused by checkOperandShape and neither is this function's business.
	for _, malformed := range []openapiv1.Expr{
		{Kind: openapiv1.ExprKindExists, Args: &[]openapiv1.Expr{{Kind: openapiv1.ExprKindField, Field: &field}}},
		{Kind: openapiv1.ExprKindNot, Args: &[]openapiv1.Expr{
			{Kind: openapiv1.ExprKindField, Field: &field},
			{Kind: openapiv1.ExprKindField, Field: &field},
			{Kind: openapiv1.ExprKindField, Field: &field},
		}},
		{Kind: openapiv1.ExprKindLiteral},
	} {
		if _, err := exprFromWire(malformed); err != nil {
			t.Fatalf("the boundary rejected a shape the compiler owns: %v", err)
		}
	}
}

// The selector's filter must survive both directions, and no fixture exercised either arm.
//
// Grep found `Where` only in the translation and the generated type: the acceptance fixture
// sets GroupBy alone, and the read-back fidelity test recompiles a document with no filter, so
// a projection that dropped it would still recompile to the same PlanID. Dropping it inbound
// is worse than losing a field: encodeSelector writes `where` under an optional marker, so a
// rule whose filter vanished shares a ruleset digest with the identical rule that never had
// one -- two materially different authored rulesets collapsing onto one semantic identity --
// and the derived read set silently shrinks, which is the fail-open direction.
func TestSelectorFilterSurvivesBothDirections(t *testing.T) {
	path := "driver.status"
	active := "active"
	where := openapiv1.Expr{Kind: openapiv1.ExprKindEqual, Args: &[]openapiv1.Expr{
		{Kind: openapiv1.ExprKindField, Field: &path},
		{Kind: openapiv1.ExprKindLiteral, Literal: &openapiv1.Value{
			Kind: openapiv1.ValueKindString, String: &active,
		}},
	}}
	groupBy := openapiv1.Expr{Kind: openapiv1.ExprKindField, Field: &path}
	count := int64(1)
	target := "driver.status"
	payload := openapiv1.SelectAndAssign{
		Selector: openapiv1.Selector{
			Kind: "driver", Where: &where, GroupBy: &groupBy,
			Members: openapiv1.Cardinality{Kind: openapiv1.CardinalityKindAtLeast, Count: &count},
		},
		Guard: openapiv1.Expr{Kind: openapiv1.ExprKindAllEqual, Field: &path},
		Assignments: []openapiv1.FieldAssignment{{
			Target: target,
			Value:  openapiv1.Expr{Kind: openapiv1.ExprKindLiteral, Literal: &openapiv1.Value{Kind: openapiv1.ValueKindString, String: &active}},
		}},
	}

	translated, err := selectAssignFromWire(payload)
	if err != nil {
		t.Fatalf("selectAssignFromWire: %v", err)
	}
	if translated.Selector.Where == nil {
		t.Fatal("the filter was discarded inbound")
	}
	if translated.Selector.GroupBy == nil {
		t.Fatal("the grouping expression was discarded inbound")
	}
	// A filter that arrived as something else is as bad as one that vanished, so this
	// compares the tree rather than merely checking for a non-nil pointer.
	expected, err := exprFromWire(where)
	if err != nil {
		t.Fatalf("exprFromWire: %v", err)
	}
	if !reflect.DeepEqual(*translated.Selector.Where, expected) {
		t.Fatalf("filter arrived as %+v, want %+v", *translated.Selector.Where, expected)
	}

	projected, err := selectAssignToWire(translated)
	if err != nil {
		t.Fatalf("selectAssignToWire: %v", err)
	}
	if projected.Selector.Where == nil {
		t.Fatal("the filter was discarded outbound")
	}
	if !reflect.DeepEqual(*projected.Selector.Where, where) {
		t.Fatalf("filter projected as %+v, want %+v", *projected.Selector.Where, where)
	}
	if projected.Selector.GroupBy == nil || !reflect.DeepEqual(*projected.Selector.GroupBy, groupBy) {
		t.Fatal("the grouping expression did not survive the projection")
	}

	// And a selector with NO filter must not acquire one, or the arm would be untested in the
	// direction that matters least and wrong in the one that matters most.
	bare := payload
	bare.Selector.Where = nil
	plain, err := selectAssignFromWire(bare)
	if err != nil {
		t.Fatalf("selectAssignFromWire without a filter: %v", err)
	}
	if plain.Selector.Where != nil {
		t.Fatal("a selector with no filter acquired one")
	}
}
