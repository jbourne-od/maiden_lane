package httpapi

import (
	"github.com/optimaldynamics/maiden-lane/internal/httpapi/openapiv1"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

// Translation for the selector-scoped operator: expressions, selectors, assignments.
//
// THIS FILE MAPS TOKENS ONTO TYPES AND DECIDES NOTHING. Whether a kind may carry a literal,
// how many operands it takes, which entity kinds its paths may name, how deep it may nest --
// every one of those is a semantic rule the compiler owns, and enforcing any of them here
// would be one proposition in two places with nothing forcing them to agree. The boundary's
// whole job is that an author who writes an illegal rule gets a compiler diagnostic naming
// the rule, rather than a schema violation naming a JSON path.
//
// What the boundary DOES owe: refusing what it cannot represent. A token outside the closed
// enum has no meaning to invent, and a semantic value with no token cannot be projected --
// both refuse rather than guess.

var exprKindFromToken = map[openapiv1.ExprKind]semantic.ExprKind{
	openapiv1.ExprKindLiteral:    semantic.ExprLiteral,
	openapiv1.ExprKindField:      semantic.ExprField,
	openapiv1.ExprKindExists:     semantic.ExprExists,
	openapiv1.ExprKindNot:        semantic.ExprNot,
	openapiv1.ExprKindAll:        semantic.ExprAll,
	openapiv1.ExprKindAny:        semantic.ExprAny,
	openapiv1.ExprKindEqual:      semantic.ExprEqual,
	openapiv1.ExprKindLess:       semantic.ExprLess,
	openapiv1.ExprKindAdd:        semantic.ExprAdd,
	openapiv1.ExprKindAllMembers: semantic.ExprAllMembers,
	openapiv1.ExprKindAnyMembers: semantic.ExprAnyMembers,
	openapiv1.ExprKindAllEqual:   semantic.ExprAllEqual,
}

// exprKindToToken is derived from the inbound map rather than written twice.
//
// Two hand-written switches over one vocabulary is the shape that lets a kind be authorable
// and not projectable, or the reverse, with nothing forcing them to agree. Inverting one map
// makes the correspondence structural: a kind is round-trippable or it is in neither
// direction. The build panics on a duplicated token, which is the only way the inversion can
// be wrong and is a programming error rather than a request.
var exprKindToToken = func() map[semantic.ExprKind]openapiv1.ExprKind {
	inverted := make(map[semantic.ExprKind]openapiv1.ExprKind, len(exprKindFromToken))
	for token, kind := range exprKindFromToken {
		if existing, duplicate := inverted[kind]; duplicate {
			panic("two wire tokens map to one expression kind: " + string(existing) + " and " + string(token))
		}
		inverted[kind] = token
	}
	return inverted
}()

func exprFromWire(expr openapiv1.Expr) (semantic.Expr, error) {
	kind, known := exprKindFromToken[expr.Kind]
	if !known {
		return semantic.Expr{}, translationError("unknown expression kind %q", string(expr.Kind))
	}
	translated := semantic.Expr{Kind: kind}
	if expr.Field != nil {
		translated.Field = semantic.FieldPath(*expr.Field)
	}
	if expr.Literal != nil {
		literal, err := valueFromWire(*expr.Literal)
		if err != nil {
			return semantic.Expr{}, err
		}
		translated.Literal = &literal
	}
	if expr.Args != nil {
		// Recursion mirrors the tree. It is bounded in practice by the JSON decoder's own
		// nesting limit, and the compiler applies the language's depth and node budgets to
		// the result -- neither of which belongs here, because a boundary that enforced them
		// would be a second compiler with its own opinion about what is too deep.
		translated.Args = make([]semantic.Expr, 0, len(*expr.Args))
		for _, argument := range *expr.Args {
			operand, err := exprFromWire(argument)
			if err != nil {
				return semantic.Expr{}, err
			}
			translated.Args = append(translated.Args, operand)
		}
	}
	return translated, nil
}

func exprToWire(expr semantic.Expr) (openapiv1.Expr, error) {
	token, representable := exprKindToToken[expr.Kind]
	if !representable {
		return openapiv1.Expr{}, translationError("expression kind %s has no representation in this contract version", expr.Kind)
	}
	projected := openapiv1.Expr{Kind: token}
	if expr.Field != "" {
		field := string(expr.Field)
		projected.Field = &field
	}
	if expr.Literal != nil {
		literal := valueToWire(*expr.Literal)
		projected.Literal = &literal
	}
	if len(expr.Args) > 0 {
		arguments := make([]openapiv1.Expr, 0, len(expr.Args))
		for _, argument := range expr.Args {
			operand, err := exprToWire(argument)
			if err != nil {
				return openapiv1.Expr{}, err
			}
			arguments = append(arguments, operand)
		}
		projected.Args = &arguments
	}
	return projected, nil
}

// maxContractCount is the largest count the contract's signed integer can carry. The kernel's
// is wider, so this is the boundary of what can be projected, and it must equal what
// cardinalityFromWire admits or a rule becomes authorable and unreadable.
const maxContractCount = uint64(1<<63 - 1)

var cardinalityFromToken = map[openapiv1.CardinalityKind]semantic.CardinalityKind{
	openapiv1.CardinalityKindAny:     semantic.CardinalityAny,
	openapiv1.CardinalityKindExactly: semantic.CardinalityExactly,
	openapiv1.CardinalityKindAtLeast: semantic.CardinalityAtLeast,
}

// Inverted like the expression map, and checked like it too. The duplicate check was on one
// of the two and not the other, which is one proposition enforced in one place out of the two
// that need it -- the same map silently overwriting would have made a cardinality kind
// project as the wrong token with nothing to say so.
var cardinalityToToken = func() map[semantic.CardinalityKind]openapiv1.CardinalityKind {
	inverted := make(map[semantic.CardinalityKind]openapiv1.CardinalityKind, len(cardinalityFromToken))
	for token, kind := range cardinalityFromToken {
		if existing, duplicate := inverted[kind]; duplicate {
			panic("two wire tokens map to one cardinality kind: " + string(existing) + " and " + string(token))
		}
		inverted[kind] = token
	}
	return inverted
}()

func cardinalityFromWire(cardinality openapiv1.Cardinality) (semantic.Cardinality, error) {
	kind, known := cardinalityFromToken[cardinality.Kind]
	if !known {
		return semantic.Cardinality{}, translationError("unknown cardinality kind %q", string(cardinality.Kind))
	}
	translated := semantic.Cardinality{Kind: kind}
	if cardinality.Count != nil {
		// REFUSED, NOT WRAPPED. The contract says int64 and the kernel says uint64, so a
		// negative count converts to an enormous positive one -- an author's typo becoming a
		// cardinality no group could ever satisfy, silently. The schema also states a
		// minimum, and this does not rely on it: a generated validator is not the kernel.
		if *cardinality.Count < 0 {
			return semantic.Cardinality{}, translationError("cardinality count is negative")
		}
		translated.Count = uint64(*cardinality.Count)
	}
	return translated, nil
}

func cardinalityToWire(cardinality semantic.Cardinality) (openapiv1.Cardinality, error) {
	token, representable := cardinalityToToken[cardinality.Kind]
	if !representable {
		return openapiv1.Cardinality{}, translationError("cardinality kind %d has no representation in this contract version", cardinality.Kind)
	}
	projected := openapiv1.Cardinality{Kind: token}
	if cardinality.Count > 0 {
		// The kernel's count is uint64 and the contract's is int64, so a count past the
		// signed maximum has no representation and must not be projected as a negative one.
		//
		// THE THRESHOLD IS THE SIGNED MAXIMUM, and an earlier version wrote 1<<62 -- half of
		// it -- while the comment claimed otherwise. Inbound accepted any non-negative int64
		// and the kernel accepted any positive count, so a count in between was authorable,
		// storable, and then unreadable: GetPlan answered a permanent 500 on a plan the same
		// server had just accepted. Two directions of one boundary disagreeing about the
		// admissible set is the exact shape this file's header claims the design prevents.
		if cardinality.Count > maxContractCount {
			return openapiv1.Cardinality{}, translationError("cardinality count exceeds the contract's range")
		}
		count := int64(cardinality.Count)
		projected.Count = &count
	}
	return projected, nil
}

func selectAssignFromWire(payload openapiv1.SelectAndAssign) (semantic.SelectAssignDeclaration, error) {
	members, err := cardinalityFromWire(payload.Selector.Members)
	if err != nil {
		return semantic.SelectAssignDeclaration{}, err
	}
	selector := semantic.Selector{Kind: semantic.EntityKind(payload.Selector.Kind), Members: members}
	if payload.Selector.Where != nil {
		where, err := exprFromWire(*payload.Selector.Where)
		if err != nil {
			return semantic.SelectAssignDeclaration{}, err
		}
		selector.Where = &where
	}
	if payload.Selector.GroupBy != nil {
		groupBy, err := exprFromWire(*payload.Selector.GroupBy)
		if err != nil {
			return semantic.SelectAssignDeclaration{}, err
		}
		selector.GroupBy = &groupBy
	}
	guard, err := exprFromWire(payload.Guard)
	if err != nil {
		return semantic.SelectAssignDeclaration{}, err
	}
	assignments := make([]semantic.FieldAssignment, 0, len(payload.Assignments))
	for _, assignment := range payload.Assignments {
		value, err := exprFromWire(assignment.Value)
		if err != nil {
			return semantic.SelectAssignDeclaration{}, err
		}
		assignments = append(assignments, semantic.FieldAssignment{
			Target: semantic.FieldPath(assignment.Target), Value: value,
		})
	}
	return semantic.SelectAssignDeclaration{Selector: selector, Guard: guard, Assignments: assignments}, nil
}

func selectAssignToWire(payload semantic.SelectAssignDeclaration) (openapiv1.SelectAndAssign, error) {
	members, err := cardinalityToWire(payload.Selector.Members)
	if err != nil {
		return openapiv1.SelectAndAssign{}, err
	}
	selector := openapiv1.Selector{Kind: string(payload.Selector.Kind), Members: members}
	if payload.Selector.Where != nil {
		where, err := exprToWire(*payload.Selector.Where)
		if err != nil {
			return openapiv1.SelectAndAssign{}, err
		}
		selector.Where = &where
	}
	if payload.Selector.GroupBy != nil {
		groupBy, err := exprToWire(*payload.Selector.GroupBy)
		if err != nil {
			return openapiv1.SelectAndAssign{}, err
		}
		selector.GroupBy = &groupBy
	}
	guard, err := exprToWire(payload.Guard)
	if err != nil {
		return openapiv1.SelectAndAssign{}, err
	}
	assignments := make([]openapiv1.FieldAssignment, 0, len(payload.Assignments))
	for _, assignment := range payload.Assignments {
		value, err := exprToWire(assignment.Value)
		if err != nil {
			return openapiv1.SelectAndAssign{}, err
		}
		assignments = append(assignments, openapiv1.FieldAssignment{
			Target: string(assignment.Target), Value: value,
		})
	}
	return openapiv1.SelectAndAssign{Selector: selector, Guard: guard, Assignments: assignments}, nil
}
