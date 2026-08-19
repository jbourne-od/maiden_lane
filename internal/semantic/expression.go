package semantic

import (
	"bytes"
	"fmt"
	"slices"
)

// ExprKind is the closed v1 expression vocabulary.
//
// Values are APPEND-ONLY. A kind byte participates in the canonical encoding, so reusing or
// renumbering one silently changes the identity of every expression that contains it. Adding
// a kind at the end leaves existing expressions byte-identical.
type ExprKind uint8

const (
	// ExprLiteral carries a Value. There is deliberately no boolean literal: Value ranges over
	// string, atom and int64, and widening it to carry a bool would change its canonical
	// encoding and therefore every identity in the system. Bool arises only from operators,
	// which is also why ExprAll and ExprAny require at least one argument rather than treating
	// zero as the identity element.
	ExprLiteral ExprKind = iota + 1

	// ExprField reads a declared field. It resolves against the entity kind named in its own
	// path and against nothing else -- see the note on ambient scope below.
	ExprField

	// ExprExists and ExprIsNull ask about presence rather than value.
	ExprExists
	ExprIsNull

	ExprNot
	ExprAll
	ExprAny
	ExprEqual
	ExprLess
	ExprAdd
)

// ExprType is the type an expression evaluates to.
//
// It is not ValueKind. Expressions range over {Bool, String, Atom, Int64} while a Value ranges
// over {String, Atom, Int64}, and adding a boolean Value would change the canonical encoding
// of every value in the system to buy a literal nothing needs.
type ExprType uint8

const (
	// TypeInvalid is the zero value and is not a type. Every exported struct here has a
	// constructible zero value, so a CompiledExpression nobody built reports a type that
	// refuses rather than one that happens to be first in the enumeration.
	TypeInvalid ExprType = iota
	TypeBool
	TypeString
	TypeAtom
	TypeInt64
)

// String names the type for diagnostics.
func (t ExprType) String() string {
	switch t {
	case TypeBool:
		return "bool"
	case TypeString:
		return "string"
	case TypeAtom:
		return "atom"
	case TypeInt64:
		return "int64"
	default:
		return "invalid"
	}
}

// Expr is one authored expression node.
//
// It is a plain declaration with exported fields, like RulesetDeclaration and unlike Plan: an
// author writes it, and CompileExpression turns it into something the kernel vouches for. The
// sketch's node struct additionally carried relation, key and value operands for a lookup
// node; those are absent because lookup is deferred, and every absent field is one fewer
// combination the compiler has to refuse.
//
// AMBIENT SCOPE IS DELIBERATELY ABSENT, and this is the decision slice 1 exists to settle
// rather than defer. A Field path resolves against the entity kind it names, with no implicit
// "current member" anywhere. If bare paths were given an ambient meaning now, introducing an
// explicit binder later would change what existing paths mean WITHOUT changing their bytes --
// an identity that silently denotes something else, which no golden vector can catch. When
// grouping arrives it must introduce an explicit member reference as a new kind rather than
// reinterpreting these.
type Expr struct {
	Kind    ExprKind
	Literal *Value
	Field   FieldPath
	Args    []Expr
}

// CompiledExpression is a type-checked expression with its canonical bytes.
//
// Unexported fields and no exported constructor: the only way to obtain one is
// CompileExpression, so an expression that exists has been checked against a schema.
type CompiledExpression struct {
	expr      Expr
	exprType  ExprType
	canonical []byte
}

// Type returns the type this expression evaluates to.
func (c CompiledExpression) Type() ExprType { return c.exprType }

// Expression returns a copy of the authored node.
func (c CompiledExpression) Expression() Expr { return cloneExpr(c.expr) }

// CanonicalBytes returns a copy of the v1 expression bytes.
func (c CompiledExpression) CanonicalBytes() []byte { return bytes.Clone(c.canonical) }

// maxExprDepth bounds authored nesting.
//
// HLD §8 requires statically analyzable constructs and bounded arithmetic; an unbounded
// authored tree is both a stack hazard in every walk and an encoder hazard. Exceeding it is a
// refusal rather than a truncation, because a silently truncated expression means something
// other than what was written.
const maxExprDepth = 64

// CompileExpression validates an authored expression against a schema and identifies it.
//
// Every refusal here is a refusal to produce a value at all, rather than a flag on one that
// was produced anyway: an ill-typed expression cannot be built, so no later stage can be
// handed one and left to notice.
func CompileExpression(schema Schema, expr Expr) (CompiledExpression, error) {
	exprType, err := checkExpr(schema, expr, 0)
	if err != nil {
		return CompiledExpression{}, err
	}
	var encoder canonicalEncoder
	encoder.tag(expressionDomainTag)
	encodeExpr(&encoder, expr)
	canonical, err := encoder.bytes()
	if err != nil {
		return CompiledExpression{}, fmt.Errorf("canonicalize expression: %w", err)
	}
	return CompiledExpression{expr: cloneExpr(expr), exprType: exprType, canonical: canonical}, nil
}

// checkExpr derives the type of one node, refusing anything the schema or the vocabulary does
// not admit.
func checkExpr(schema Schema, expr Expr, depth int) (ExprType, error) {
	if depth > maxExprDepth {
		return TypeInvalid, fmt.Errorf("expression nests deeper than %d", maxExprDepth)
	}
	if err := checkOperandShape(expr); err != nil {
		return TypeInvalid, err
	}

	switch expr.Kind {
	case ExprLiteral:
		return literalType(*expr.Literal)

	case ExprField:
		kind, declared := schema.fieldKind(expr.Field)
		if !declared {
			return TypeInvalid, fmt.Errorf("expression reads undeclared field %q", expr.Field)
		}
		return valueKindType(kind)

	case ExprExists, ExprIsNull:
		if _, declared := schema.fieldKind(expr.Field); !declared {
			return TypeInvalid, fmt.Errorf("expression asks about undeclared field %q", expr.Field)
		}
		return TypeBool, nil

	case ExprNot:
		argument, err := checkExpr(schema, expr.Args[0], depth+1)
		if err != nil {
			return TypeInvalid, err
		}
		if argument != TypeBool {
			return TypeInvalid, fmt.Errorf("not requires bool, got %s", argument)
		}
		return TypeBool, nil

	case ExprAll, ExprAny:
		for i := range expr.Args {
			argument, err := checkExpr(schema, expr.Args[i], depth+1)
			if err != nil {
				return TypeInvalid, err
			}
			if argument != TypeBool {
				return TypeInvalid, fmt.Errorf("boolean composition requires bool arguments, "+
					"argument %d is %s", i, argument)
			}
		}
		return TypeBool, nil

	case ExprEqual:
		left, right, err := checkPair(schema, expr, depth)
		if err != nil {
			return TypeInvalid, err
		}
		// HLD §9.1 lists type-incompatible comparisons as a static validation failure.
		if left != right {
			return TypeInvalid, fmt.Errorf("equal compares %s with %s", left, right)
		}
		return TypeBool, nil

	case ExprLess:
		left, right, err := checkPair(schema, expr, depth)
		if err != nil {
			return TypeInvalid, err
		}
		// Int64 only, and the restriction is deliberate rather than unfinished. An atom is an
		// opaque token, so ordering atoms asserts a meaning they do not carry; ordering
		// strings needs a collation this kernel deliberately does not define, since v1
		// performs no Unicode normalization. Reaching for byte order here would invent one.
		if left != TypeInt64 || right != TypeInt64 {
			return TypeInvalid, fmt.Errorf("less requires int64 on both sides, got %s and %s",
				left, right)
		}
		return TypeBool, nil

	case ExprAdd:
		left, right, err := checkPair(schema, expr, depth)
		if err != nil {
			return TypeInvalid, err
		}
		if left != TypeInt64 || right != TypeInt64 {
			return TypeInvalid, fmt.Errorf("add requires int64 on both sides, got %s and %s",
				left, right)
		}
		return TypeInt64, nil

	default:
		return TypeInvalid, fmt.Errorf("unknown expression kind %d", expr.Kind)
	}
}

// checkPair types both arguments of a binary node.
func checkPair(schema Schema, expr Expr, depth int) (ExprType, ExprType, error) {
	left, err := checkExpr(schema, expr.Args[0], depth+1)
	if err != nil {
		return TypeInvalid, TypeInvalid, err
	}
	right, err := checkExpr(schema, expr.Args[1], depth+1)
	if err != nil {
		return TypeInvalid, TypeInvalid, err
	}
	return left, right, nil
}

// checkOperandShape refuses a node whose populated operands do not match its kind.
//
// THIS IS NOT TIDINESS. The encoder writes only the operands a kind actually uses, so a node
// carrying an ignored operand would encode identically to one without it. Two materially
// different authored expressions would then share one identity, and the ruleset digest would
// stop committing to what the author wrote. Refusing at the door is what keeps the encoding
// injective over authored content.
func checkOperandShape(expr Expr) error {
	var wantArgs int
	var wantLiteral, wantField bool

	switch expr.Kind {
	case ExprLiteral:
		wantLiteral = true
	case ExprField, ExprExists, ExprIsNull:
		wantField = true
	case ExprNot:
		wantArgs = 1
	case ExprAll, ExprAny:
		// Variadic, but never empty: with no boolean literal there is no way to write the
		// identity element, so an empty conjunction would have to mean something the author
		// could not otherwise express.
		if len(expr.Args) == 0 {
			return fmt.Errorf("boolean composition requires at least one argument")
		}
		wantArgs = len(expr.Args)
	case ExprEqual, ExprLess, ExprAdd:
		wantArgs = 2
	default:
		return fmt.Errorf("unknown expression kind %d", expr.Kind)
	}

	if got := len(expr.Args); got != wantArgs {
		return fmt.Errorf("expression kind %d takes %d arguments, got %d",
			expr.Kind, wantArgs, got)
	}
	if wantLiteral != (expr.Literal != nil) {
		return fmt.Errorf("expression kind %d carries a literal it does not use", expr.Kind)
	}
	if wantField != (expr.Field != "") {
		return fmt.Errorf("expression kind %d carries a field path it does not use", expr.Kind)
	}
	return nil
}

// literalType maps a literal's value kind to an expression type.
func literalType(value Value) (ExprType, error) {
	if !value.Valid() {
		return TypeInvalid, fmt.Errorf("literal carries no value")
	}
	return valueKindType(value.Kind())
}

// valueKindType maps the value vocabulary into the expression vocabulary.
func valueKindType(kind ValueKind) (ExprType, error) {
	switch kind {
	case ValueString:
		return TypeString, nil
	case ValueAtom:
		return TypeAtom, nil
	case ValueInt64:
		return TypeInt64, nil
	default:
		return TypeInvalid, fmt.Errorf("unknown value kind %d", kind)
	}
}

// encodeExpr writes one node and its operands.
//
// Recursive, with no domain tag of its own: the tag is written once at the top by
// CompileExpression, exactly as encodeTransformationDeclaration nests its payloads.
func encodeExpr(encoder *canonicalEncoder, expr Expr) {
	encoder.byte(byte(expr.Kind))
	switch expr.Kind {
	case ExprLiteral:
		encoder.value(*expr.Literal)
	case ExprField, ExprExists, ExprIsNull:
		encoder.string(string(expr.Field))
	default:
		encoder.uint64(uint64(len(expr.Args)))
		for i := range expr.Args {
			encodeExpr(encoder, expr.Args[i])
		}
	}
}

// cloneExpr deep copies an authored node, so a compiled expression shares nothing with the
// declaration it was built from.
func cloneExpr(expr Expr) Expr {
	cloned := expr
	if expr.Literal != nil {
		literal := *expr.Literal
		cloned.Literal = &literal
	}
	if expr.Args != nil {
		cloned.Args = make([]Expr, len(expr.Args))
		for i := range expr.Args {
			cloned.Args[i] = cloneExpr(expr.Args[i])
		}
	}
	return cloned
}

// fieldKind resolves a field path to its declared value kind.
//
// A path naming an entity kind or field the schema does not declare is reported as
// undeclared rather than defaulted, which is what makes "this schema declares that field" a
// statement a caller obtains rather than assumes.
func (s Schema) fieldKind(path FieldPath) (ValueKind, bool) {
	entityKind, fieldName := splitFieldPath(path)
	if entityKind == "" || fieldName == "" {
		return 0, false
	}
	declaration, declared := s.entityDeclaration(entityKind)
	if !declared {
		return 0, false
	}
	index := slices.IndexFunc(declaration.Fields, func(field FieldDeclaration) bool {
		return field.Name == fieldName
	})
	if index < 0 {
		return 0, false
	}
	return declaration.Fields[index].Kind, true
}
