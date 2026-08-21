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

	// ExprExists asks about presence rather than value.
	//
	// There is deliberately no is-null companion. Value has three variants and none of them
	// is null (value.go), and Entity.Field reports absence through a second return rather
	// than through a value, so absence IS the only null this kernel has. An is-null node
	// could therefore only ever mean not(exists(f)) — a second spelling of one predicate,
	// with a permanently distinct identity, committing a kind byte to a distinction the
	// value model cannot make. An earlier version of this slice had one.
	ExprExists

	ExprNot
	ExprAll
	ExprAny
	ExprEqual
	ExprLess
	ExprAdd

	// GROUP-SCOPED, and appended rather than inserted, so every expression written before
	// they existed keeps its bytes. They are legal only where a group is in scope; see
	// group_expr.go for why they need no binder.
	ExprAllMembers
	ExprAnyMembers
	ExprAllEqual

	// GROUP-SCOPED REDUCTIONS, also appended. They reduce fields across group members or count
	// group cardinality, producing an int64 Value.
	ExprCount
	ExprSum
	ExprMin
	ExprMax
)

// AllExprKinds is the complete v1 node vocabulary, in kind-byte order.
//
// It exists so a boundary that must map every kind can be tested against the vocabulary
// rather than against a list re-typed into a test file. Like AllInvariantCodes, it is itself
// hand-maintained and nothing forces it to agree with the const block above -- Go cannot
// enumerate a const group -- so it reduces the number of hand-kept lists rather than removing
// the hazard.
func AllExprKinds() []ExprKind {
	return []ExprKind{
		ExprLiteral, ExprField, ExprExists, ExprNot, ExprAll, ExprAny,
		ExprEqual, ExprLess, ExprAdd, ExprAllMembers, ExprAnyMembers, ExprAllEqual,
		ExprCount, ExprSum, ExprMin, ExprMax,
	}
}

// String renders a kind for a human.
//
// IT IS NOT A WIRE TOKEN, however exactly the strings happen to coincide with one today. This
// function is total and falls back to kind(%d) for anything unrecognised, which is right for a
// diagnostic and wrong for a contract: a boundary using it would ship an off-enum token for a
// kind nobody had mapped, silently, in the fail-open direction. internal/httpapi keeps its own
// switch with no fallback for exactly that reason.
func (k ExprKind) String() string {
	switch k {
	case ExprLiteral:
		return "literal"
	case ExprField:
		return "field"
	case ExprExists:
		return "exists"
	case ExprNot:
		return "not"
	case ExprAll:
		return "all"
	case ExprAny:
		return "any"
	case ExprEqual:
		return "equal"
	case ExprLess:
		return "less"
	case ExprAdd:
		return "add"
	case ExprAllMembers:
		return "all_members"
	case ExprAnyMembers:
		return "any_members"
	case ExprAllEqual:
		return "all_equal"
	case ExprCount:
		return "count"
	case ExprSum:
		return "sum"
	case ExprMin:
		return "min"
	case ExprMax:
		return "max"
	default:
		return fmt.Sprintf("kind(%d)", uint8(k))
	}
}

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
	schema    SchemaDigest
	version   CompilerSemanticsVersion
	expr      Expr
	exprType  ExprType
	canonical []byte
}

// Type returns the type this expression evaluates to.
//
// It is a fact about (schema, expression), not about the expression alone, which is why the
// schema digest participates in the canonical bytes.
func (c CompiledExpression) Type() ExprType { return c.exprType }

// SchemaDigest and CompilerSemanticsVersion report what this expression was checked against.
func (c CompiledExpression) SchemaDigest() SchemaDigest                         { return c.schema }
func (c CompiledExpression) CompilerSemanticsVersion() CompilerSemanticsVersion { return c.version }

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
func CompileExpression(
	schema Schema, version CompilerSemanticsVersion, expr Expr,
) (CompiledExpression, error) {
	// validSemanticName, not an emptiness check, because Compile validates its version the
	// same way (compile.go). Two checks against two different references is how a hole opens
	// between them: today the accept sets coincide, and nothing was keeping them coincident.
	if !validSemanticName(string(version)) {
		return CompiledExpression{}, fmt.Errorf("expression has no usable compiler semantics version")
	}
	exprType, err := checkExpr(schema, expr, 0)
	if err != nil {
		return CompiledExpression{}, err
	}
	var encoder canonicalEncoder
	encoder.tag(expressionDomainTag)
	// The schema digest and compiler version come FIRST, exactly as encodeCompiledProfile
	// writes them, because this is a compiled artifact rather than authored content.
	//
	// Without them the bytes would not determine Type(). Two schemas declaring driver.hours
	// as int64 and as string produce byte-identical encodings of the same authored path with
	// different derived types, and add(driver.hours, 1) would be a well-typed expression
	// under one and refused under the other while sharing an identity. A consumer that
	// digested these bytes and then trusted Type() would be deciding from a name that does
	// not determine the answer.
	encoder.string(string(version))
	encoder.digest(string(schema.Digest()))
	encodeExpr(&encoder, expr)
	canonical, err := encoder.bytes()
	if err != nil {
		return CompiledExpression{}, fmt.Errorf("canonicalize expression: %w", err)
	}
	return CompiledExpression{
		schema:    schema.Digest(),
		version:   version,
		expr:      cloneExpr(expr),
		exprType:  exprType,
		canonical: canonical,
	}, nil
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

	case ExprExists:
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

	case ExprAllMembers, ExprAnyMembers, ExprAllEqual, ExprCount, ExprSum, ExprMin, ExprMax:
		// checkExpr is the MEMBER-scope checker. A group predicate or reduction reaching it
		// means the expression was checked in the wrong scope, which is refused rather than delegated:
		// answering it here would require picking a member.
		return TypeInvalid, fmt.Errorf(
			"%s is a group expression and cannot appear in member scope", expr.Kind)

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
	case ExprField, ExprExists:
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
	case ExprAllMembers, ExprAnyMembers:
		wantArgs = 1
	case ExprAllEqual, ExprSum, ExprMin, ExprMax:
		wantField = true
	case ExprCount:
		// Group cardinality takes no arguments, field, or literal.
	default:
		return fmt.Errorf("unknown expression kind %d", expr.Kind)
	}

	if got := len(expr.Args); got != wantArgs {
		return fmt.Errorf("expression kind %d takes %d arguments, got %d",
			expr.Kind, wantArgs, got)
	}
	// Each of these is an inequality, so it fires in two opposite situations, and an earlier
	// version reported both as "carries an operand it does not use" -- telling an author to
	// remove an operand that was in fact missing. The nil-literal arm is also the only thing
	// standing between Expr{Kind: ExprLiteral} and the dereference in checkExpr.
	if hasLiteral := expr.Literal != nil; wantLiteral != hasLiteral {
		if wantLiteral {
			return fmt.Errorf("expression kind %d requires a literal and carries none", expr.Kind)
		}
		return fmt.Errorf("expression kind %d carries a literal it does not use", expr.Kind)
	}
	if hasField := expr.Field != ""; wantField != hasField {
		if wantField {
			return fmt.Errorf("expression kind %d requires a field path and carries none", expr.Kind)
		}
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

// cloneCompiledExpression deep-copies a compiled expression, tree and bytes.
func cloneCompiledExpression(input CompiledExpression) CompiledExpression {
	return CompiledExpression{schema: input.schema, version: input.version, expr: cloneExpr(input.expr),
		exprType: input.exprType, canonical: bytes.Clone(input.canonical)}
}

// maxExprNodes bounds the NODE COUNT of an authored tree, which depth does not.
//
// Expr.Args is a slice of values, so one Expr may appear as several children without being
// copied: forty levels of Expr{Kind: ExprAll, Args: []Expr{node, node}} is depth forty --
// comfortably inside maxExprDepth -- and 2^40 nodes. An earlier version of the bound below
// checked only depth, so it walked all 2^40 of them and never returned. Iterative was the
// right shape and the wrong dimension: it stopped the stack overflow and left an unbounded
// amount of work, which is the fail-open half of the same hazard.
//
// The number is generous against any authored rule and negligible against an aliased DAG,
// which is the only gap it has to close.
const maxExprNodes = 4096

// checkAuthoredExprBound refuses an authored tree deeper, or larger, than the language admits.
//
// ITERATIVE, WITH AN EXPLICIT STACK, because it is the guard that protects the recursive
// walks and must not be one itself.
//
// maxExprDepth is enforced by checkExpr and checkExprInScope, which for a selector-scoped
// rule run inside deriveTransformation -- and normalizeRuleset clones the authored trees and
// encodeRuleset walks them, both BEFORE that. Those two recursions therefore consumed a tree
// nothing had bounded: a guard nested a million deep exhausted the goroutine stack in
// cloneExpr, which is a fatal runtime error rather than a refusal, and the bound that would
// have rejected it at 64 never ran. The same shape as a specialized traversal closing over
// its own recursion, one layer out: a new caller reached the walk without crossing the guard
// that owns its invariant.
func checkAuthoredExprBound(expr Expr) error {
	type framed struct {
		node  Expr
		depth int
	}
	stack := []framed{{node: expr}}
	visited := 0
	for len(stack) > 0 {
		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if current.depth > maxExprDepth {
			return fmt.Errorf("expression nests deeper than %d", maxExprDepth)
		}
		visited++
		if visited > maxExprNodes {
			// Counted as the walk proceeds rather than measured up front, because measuring
			// the size of the tree is the very walk that has to be bounded.
			return fmt.Errorf("expression expands to more than %d nodes", maxExprNodes)
		}
		for i := range current.node.Args {
			stack = append(stack, framed{node: current.node.Args[i], depth: current.depth + 1})
		}
	}
	return nil
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
	case ExprField, ExprExists, ExprAllEqual, ExprSum, ExprMin, ExprMax:
		// ExprAllEqual and reductions encode exactly as the other field-carrying kinds:
		// the kind byte is what separates them, which is the scheme the golden vectors pin.
		encoder.string(string(expr.Field))
	case ExprCount:
		// Takes no operands beyond its kind byte.
	case ExprNot, ExprAll, ExprAny, ExprEqual, ExprLess, ExprAdd, ExprAllMembers, ExprAnyMembers:
		encoder.uint64(uint64(len(expr.Args)))
		for i := range expr.Args {
			encodeExpr(encoder, expr.Args[i])
		}
	default:
		// EXHAUSTIVE AND FAIL-CLOSED, and the default arm is the point rather than a
		// formality.
		//
		// The three group kinds got their arms above when OperatorSelectAndAssign made them
		// reachable -- a transformation declaration encodes its authored guard, and a guard
		// is group-scoped -- together with the golden vectors that entry promised. Until
		// then encodeExpr had two entry points, CompileExpression and encodeSelector, and
		// neither could reach a group kind; there are now three.
		//
		// checkExpr and checkOperandShape both refuse an unrecognised kind, so
		// this arm is unreachable today. It exists because adding a kind is the expected
		// change: a new kind with a new operand would be caught by both refusal switches,
		// which error loudly if a case is forgotten, but an encoder whose default merely
		// wrote the argument list would compile, run, and silently omit the new operand from
		// the identity. The one switch that must not be forgotten was the only one that
		// could not complain.
		encoder.fail(fmt.Errorf("expression kind %d has no canonical encoding", expr.Kind))
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
