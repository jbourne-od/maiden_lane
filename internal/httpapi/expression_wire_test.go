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
