package semantic

import (
	"encoding/hex"
	"testing"
)

// expressionSchema declares one entity with a field of every value kind, so a test can vary
// the type dimension without varying anything else.
func expressionSchema(t *testing.T) Schema {
	t.Helper()
	schema, err := NewSchema([]EntityDeclaration{{
		Kind: "driver",
		Fields: []FieldDeclaration{
			{Name: "assignment_key", Kind: ValueString},
			{Name: "hos_anchor", Kind: ValueAtom},
			{Name: "hos_elapsed_hours", Kind: ValueInt64},
			{Name: "hos_driving_hours", Kind: ValueInt64},
		},
	}}, nil)
	if err != nil {
		t.Fatalf("NewSchema: %v", err)
	}
	return schema
}

func intLiteral(value int64) Expr {
	literal := NewInt64Value(value)
	return Expr{Kind: ExprLiteral, Literal: &literal}
}

func stringLiteral(t *testing.T, text string) Expr {
	t.Helper()
	literal, err := NewStringValue(text)
	if err != nil {
		t.Fatalf("NewStringValue: %v", err)
	}
	return Expr{Kind: ExprLiteral, Literal: &literal}
}

func field(path FieldPath) Expr { return Expr{Kind: ExprField, Field: path} }

// Type derivation is total over the vocabulary: every kind either yields a type or refuses,
// and none silently produces the zero value.
func TestCompileExpressionDerivesTypes(t *testing.T) {
	schema := expressionSchema(t)
	for _, test := range []struct {
		name string
		expr Expr
		want ExprType
	}{
		{"string literal", stringLiteral(t, "x"), TypeString},
		{"int literal", intLiteral(3), TypeInt64},
		{"string field", field("driver.assignment_key"), TypeString},
		{"atom field", field("driver.hos_anchor"), TypeAtom},
		{"int field", field("driver.hos_elapsed_hours"), TypeInt64},
		{"exists", Expr{Kind: ExprExists, Field: "driver.hos_anchor"}, TypeBool},
		{"not", Expr{Kind: ExprNot, Args: []Expr{
			{Kind: ExprExists, Field: "driver.hos_anchor"}}}, TypeBool},
		{"all", Expr{Kind: ExprAll, Args: []Expr{
			{Kind: ExprExists, Field: "driver.hos_anchor"}}}, TypeBool},
		{"any", Expr{Kind: ExprAny, Args: []Expr{
			{Kind: ExprExists, Field: "driver.hos_anchor"},
			{Kind: ExprExists, Field: "driver.assignment_key"}}}, TypeBool},
		{"equal on atoms", Expr{Kind: ExprEqual, Args: []Expr{
			field("driver.hos_anchor"), field("driver.hos_anchor")}}, TypeBool},
		{"less on ints", Expr{Kind: ExprLess, Args: []Expr{
			field("driver.hos_driving_hours"), field("driver.hos_elapsed_hours")}}, TypeBool},
		{"add", Expr{Kind: ExprAdd, Args: []Expr{
			field("driver.hos_elapsed_hours"), intLiteral(1)}}, TypeInt64},
	} {
		t.Run(test.name, func(t *testing.T) {
			compiled, err := CompileExpression(schema, testCompilerVersion, test.expr)
			if err != nil {
				t.Fatalf("CompileExpression: %v", err)
			}
			if compiled.Type() != test.want {
				t.Fatalf("type = %s, want %s", compiled.Type(), test.want)
			}
		})
	}
}

// Production break caught: every one of these is a well-formed-looking expression that means
// nothing, and each must be refused at compile time rather than surfacing as a runtime
// surprise in a later slice.
func TestCompileExpressionRefusals(t *testing.T) {
	schema := expressionSchema(t)
	deep := field("driver.hos_elapsed_hours")
	for range maxExprDepth + 2 {
		deep = Expr{Kind: ExprAdd, Args: []Expr{deep, intLiteral(1)}}
	}

	for _, test := range []struct {
		name string
		expr Expr
	}{
		{"zero value", Expr{}},
		{"unknown kind", Expr{Kind: ExprKind(200), Args: []Expr{intLiteral(1)}}},
		{"undeclared field", field("driver.nope")},
		{"undeclared entity", field("truck.assignment_key")},
		{"malformed path", field("assignment_key")},
		// FEATURE BOUNDARY, not a parsing detail. A two-segment path is how relation
		// traversal would sneak in before lookup and grouping own it: "driver.team.name"
		// reads as "follow the relation, then take a field". Refusing it here is what keeps
		// the deferral of lookup real rather than nominal, because a language that already
		// traverses has not deferred anything.
		{"relation traversal smuggled into a path", field("driver.team.name")},
		{"trailing dot", field("driver.")},
		{"leading dot", field(".assignment_key")},
		{"exists on undeclared field", Expr{Kind: ExprExists, Field: "driver.nope"}},
		{"literal with no value", Expr{Kind: ExprLiteral, Literal: &Value{}}},
		{"empty all", Expr{Kind: ExprAll}},
		{"empty any", Expr{Kind: ExprAny}},
		{"not with two arguments", Expr{Kind: ExprNot, Args: []Expr{
			{Kind: ExprExists, Field: "driver.hos_anchor"},
			{Kind: ExprExists, Field: "driver.hos_anchor"}}}},
		{"equal with one argument", Expr{Kind: ExprEqual, Args: []Expr{intLiteral(1)}}},
		{"not on a non-bool", Expr{Kind: ExprNot, Args: []Expr{intLiteral(1)}}},
		{"all over a non-bool", Expr{Kind: ExprAll, Args: []Expr{intLiteral(1)}}},
		// HLD §9.1: type-incompatible comparisons are a static validation failure.
		{"equal across types", Expr{Kind: ExprEqual, Args: []Expr{
			field("driver.assignment_key"), field("driver.hos_elapsed_hours")}}},
		// Ordering atoms asserts a meaning they do not carry.
		{"less on atoms", Expr{Kind: ExprLess, Args: []Expr{
			field("driver.hos_anchor"), field("driver.hos_anchor")}}},
		// Ordering strings needs a collation this kernel does not define.
		{"less on strings", Expr{Kind: ExprLess, Args: []Expr{
			field("driver.assignment_key"), stringLiteral(t, "x")}}},
		{"add on strings", Expr{Kind: ExprAdd, Args: []Expr{
			field("driver.assignment_key"), stringLiteral(t, "x")}}},
		{"deeper than the bound", deep},
	} {
		t.Run(test.name, func(t *testing.T) {
			compiled, err := CompileExpression(schema, testCompilerVersion, test.expr)
			if err == nil {
				t.Fatalf("compiled an expression that should be refused, type=%s",
					compiled.Type())
			}
			if compiled.Type() != TypeInvalid {
				t.Fatalf("a refused expression reported type %s", compiled.Type())
			}
		})
	}
}

// Production break caught by mutation, and the mutation is the only thing that could have
// caught it: the traversal-smuggling case above passes for the WRONG REASON against an
// ordinary schema.
//
// Removing splitFieldPath's second-dot rejection leaves "driver.team.name" splitting into
// entity "driver" and field "team.name", which then fails schema lookup because no such field
// is declared. The refusal survives, so the test stays green while the guard it names is gone.
//
// This schema closes that hole by declaring a field whose NAME CONTAINS A DOT, which
// validSemanticName permits — it requires only a non-empty valid UTF-8 string. With that
// field present, only splitFieldPath stands between an authored path and relation-shaped
// addressing, so the test pins the guard rather than the fixture's silence.
//
// It also documents a latent defect that predates this slice: a field named "team.name" is
// declarable and, because every path with two dots is refused, addressable by nothing. It is
// recorded in the programme index rather than fixed here, since tightening validSemanticName
// would change which schemas compile and therefore which identities exist.
func TestFieldPathCannotAddressThroughARelation(t *testing.T) {
	schema, err := NewSchema([]EntityDeclaration{{
		Kind: "driver",
		Fields: []FieldDeclaration{
			{Name: "assignment_key", Kind: ValueString},
			// Declarable today. If splitFieldPath stops refusing two dots, "driver.team.name"
			// resolves to this and traversal-shaped paths become expressible.
			{Name: "team.name", Kind: ValueString},
		},
	}}, nil)
	if err != nil {
		t.Fatalf("NewSchema refused a dotted field name; validSemanticName has been "+
			"tightened and this test needs rewriting: %v", err)
	}

	if _, err := CompileExpression(schema, testCompilerVersion, field("driver.team.name")); err == nil {
		t.Fatal("a two-segment path resolved, so an expression can address through a " +
			"relation before lookup and grouping own that feature")
	}
	// The control: the same schema's ordinary field still resolves, so the refusal above is
	// about the path shape and not about this schema being unusable.
	if _, err := CompileExpression(schema, testCompilerVersion, field("driver.assignment_key")); err != nil {
		t.Fatalf("the control field did not compile: %v", err)
	}
}

// THE ENCODING MUST BE INJECTIVE OVER AUTHORED CONTENT, and the fat-struct shape is what
// threatens it. The encoder writes only the operands a kind uses, so a node carrying an
// ignored operand would encode identically to one without it: two materially different
// authored expressions would share one identity, and the ruleset digest would stop
// committing to what the author wrote.
//
// Refusing the node is what keeps that from being expressible at all.
func TestCompileExpressionRefusesOperandsAKindIgnores(t *testing.T) {
	schema := expressionSchema(t)
	literal := NewInt64Value(1)

	for _, test := range []struct {
		name string
		expr Expr
	}{
		{"literal on an operator", Expr{Kind: ExprNot, Literal: &literal, Args: []Expr{
			{Kind: ExprExists, Field: "driver.hos_anchor"}}}},
		{"field on an operator", Expr{Kind: ExprNot, Field: "driver.hos_anchor", Args: []Expr{
			{Kind: ExprExists, Field: "driver.hos_anchor"}}}},
		{"field on a literal", Expr{Kind: ExprLiteral, Literal: &literal,
			Field: "driver.hos_anchor"}},
		{"literal on a field", Expr{Kind: ExprField, Field: "driver.hos_anchor",
			Literal: &literal}},
		{"arguments on a field", Expr{Kind: ExprField, Field: "driver.hos_anchor",
			Args: []Expr{intLiteral(1)}}},
		{"arguments on a literal", Expr{Kind: ExprLiteral, Literal: &literal,
			Args: []Expr{intLiteral(1)}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := CompileExpression(schema, testCompilerVersion, test.expr); err == nil {
				t.Fatal("compiled a node carrying an operand its kind ignores")
			}
		})
	}
}

// Authored operand order is PRESERVED, not canonicalized, and the choice is deliberate.
//
// Canonicalizing would mean the encoder deciding which operators commute, which is a semantic
// claim an encoder has no business making: int64 addition is commutative, but a later kind
// added to this union may not be, and the encoder would have to be taught the difference or
// silently get it wrong. It would also mean the bytes no longer reflect what the author wrote,
// so a ruleset digest would stop committing to authored content — the same property the
// operand-shape refusal above exists to protect.
//
// The cost is accepted and worth stating: add(a, 1) and add(1, a) are semantically identical
// and get different identities, so two authors writing the same rule differently produce
// different PlanIDs. That is the correct trade here, because identity is over authored
// content and a normalizer is a semantic component that would need its own specification.
func TestCompileExpressionDistinguishesArgumentOrder(t *testing.T) {
	schema := expressionSchema(t)
	forward, err := CompileExpression(schema, testCompilerVersion, Expr{Kind: ExprAdd, Args: []Expr{
		field("driver.hos_elapsed_hours"), intLiteral(1)}})
	if err != nil {
		t.Fatalf("CompileExpression: %v", err)
	}
	reversed, err := CompileExpression(schema, testCompilerVersion, Expr{Kind: ExprAdd, Args: []Expr{
		intLiteral(1), field("driver.hos_elapsed_hours")}})
	if err != nil {
		t.Fatalf("CompileExpression: %v", err)
	}
	if string(forward.CanonicalBytes()) == string(reversed.CanonicalBytes()) {
		t.Fatal("swapping arguments produced identical canonical bytes")
	}
}

// A compiled expression shares nothing with the declaration it was built from, and nothing
// with a second caller. Copying the struct would copy the Args slice header and the Literal
// pointer.
func TestCompiledExpressionSharesNothing(t *testing.T) {
	schema := expressionSchema(t)
	// THREE LEVELS DEEP WITH A LIVE LITERAL POINTER, deliberately. An earlier version of this
	// test was two levels with scalar leaves and replaced whole Expr values, so it could
	// observe neither of cloneExpr's two guards: removing the Literal pointer copy or the
	// recursion left the whole suite green.
	literal := NewInt64Value(1)
	authored := Expr{Kind: ExprLess, Args: []Expr{
		field("driver.hos_driving_hours"),
		{Kind: ExprAdd, Args: []Expr{
			field("driver.hos_elapsed_hours"),
			{Kind: ExprLiteral, Literal: &literal},
		}},
	}}
	compiled, err := CompileExpression(schema, testCompilerVersion, authored)
	if err != nil {
		t.Fatalf("CompileExpression: %v", err)
	}
	before := string(compiled.CanonicalBytes())

	// Write through the pointer the caller still holds, mutate a grandchild in place, and
	// scribble on both returned copies.
	literal = NewInt64Value(99)
	authored.Args[1].Args[0] = field("driver.assignment_key")
	returned := compiled.Expression()
	returned.Args[1].Args[1].Literal = nil
	scribble := compiled.CanonicalBytes()
	scribble[0] ^= 0xff

	if string(compiled.CanonicalBytes()) != before {
		t.Fatal("mutating the declaration or a returned copy changed the compiled bytes")
	}
	recovered := compiled.Expression()
	if recovered.Args[1].Args[0].Field != "driver.hos_elapsed_hours" {
		t.Fatal("the compiled expression aliases a grandchild of the authored declaration")
	}
	held := recovered.Args[1].Args[1].Literal
	if held == nil {
		t.Fatal("a caller nilled the compiled expression's literal through a returned copy")
	}
	if got, _ := held.Int64(); got != 1 {
		t.Fatalf("the compiled literal followed the caller's pointer to %d, want 1", got)
	}
}

// Compiling the same authored expression twice yields the same bytes, which is what makes an
// expression's contribution to a ruleset identity stable.
func TestCompileExpressionIsDeterministic(t *testing.T) {
	schema := expressionSchema(t)
	authored := Expr{Kind: ExprAll, Args: []Expr{
		{Kind: ExprExists, Field: "driver.hos_anchor"},
		{Kind: ExprLess, Args: []Expr{
			field("driver.hos_driving_hours"), field("driver.hos_elapsed_hours")}},
	}}
	first, err := CompileExpression(schema, testCompilerVersion, authored)
	if err != nil {
		t.Fatalf("CompileExpression: %v", err)
	}
	second, err := CompileExpression(schema, testCompilerVersion, authored)
	if err != nil {
		t.Fatalf("CompileExpression: %v", err)
	}
	if string(first.CanonicalBytes()) != string(second.CanonicalBytes()) {
		t.Fatal("two compilations of one expression produced different bytes")
	}
}

// ── golden canonical vectors ────────────────────────────────────────────────

// These exist because of a limit no behavioural test can cross, not as belt-and-braces.
//
// The kind byte is the case. Every valid expression's meaning is determined by its kind, so
// no fixture can hold the meaning fixed and vary "is the kind byte written". Delete the byte
// from the encoder and every behavioural test above still passes — type derivation is
// unaffected, argument order still distinguishes, determinism still holds — while two
// different kinds sharing an operand shape silently collide into one identity.
//
// The same applies to the domain tag, which no behavioural test observes at all.
//
// Canonical formats are one of the few places brittleness is the point: changing a v1
// encoding should force somebody to edit a conspicuous constant and thereby admit they are
// renaming every artifact that encoding identifies.

// Production break caught: dropping the kind byte lets ExprExists and ExprIsNull -- identical
// in operand shape and both yielding bool -- encode identically and share one identity.
func TestExpressionCanonicalGoldenVectors(t *testing.T) {
	schema := expressionSchema(t)
	for _, test := range []struct {
		name    string
		expr    Expr
		wantHex string
	}{
		{
			name: "exists",
			expr: Expr{Kind: ExprExists, Field: "driver.hos_anchor"},
			wantHex: "00000000000000196d616964656e2d6c616e652e65787072657373696f6e2e763100000000000000216d616964656e2d6c616e652e636f6d70696c65722d73656d616e746963732e7631" +
				"b9ceee82852bb8e562a9b4b9866659833af65e8e11ada88d370be23cc83fd56d" +
				"0300000000000000116472697665722e686f735f616e63686f72",
		},
		{
			// Byte-for-byte identical to the vector above except for the kind byte: field and
			// exists take the same operand and no arguments. If that byte ever stops being
			// written these two collide, and no behavioural test can see it — type derivation
			// does not touch the encoding, so both would still report their own types while
			// sharing one identity.
			name: "field, differing from exists only in the kind byte",
			expr: field("driver.hos_anchor"),
			wantHex: "00000000000000196d616964656e2d6c616e652e65787072657373696f6e2e763100000000000000216d616964656e2d6c616e652e636f6d70696c65722d73656d616e746963732e7631" +
				"b9ceee82852bb8e562a9b4b9866659833af65e8e11ada88d370be23cc83fd56d" +
				"0200000000000000116472697665722e686f735f616e63686f72",
		},
		{
			name: "int literal",
			expr: intLiteral(11),
			wantHex: "00000000000000196d616964656e2d6c616e652e65787072657373696f6e2e763100000000000000216d616964656e2d6c616e652e636f6d70696c65722d73656d616e746963732e7631" +
				"b9ceee82852bb8e562a9b4b9866659833af65e8e11ada88d370be23cc83fd56d" +
				"0103000000000000000b",
		},
		{
			name: "nested tree",
			expr: Expr{Kind: ExprAll, Args: []Expr{
				{Kind: ExprExists, Field: "driver.hos_anchor"},
				{Kind: ExprLess, Args: []Expr{
					field("driver.hos_driving_hours"),
					{Kind: ExprAdd, Args: []Expr{
						field("driver.hos_elapsed_hours"), intLiteral(1)}},
				}},
			}},
			wantHex: "00000000000000196d616964656e2d6c616e652e65787072657373696f6e2e763100000000000000216d616964656e2d6c616e652e636f6d70696c65722d73656d616e746963732e7631" +
				"b9ceee82852bb8e562a9b4b9866659833af65e8e11ada88d370be23cc83fd56d" +
				"0500000000000000020300000000000000116472697665722e686f735f616e63686f72" +
				"0800000000000000020200000000000000186472697665722e686f735f64726976696e675f686f757273" +
				"0900000000000000020200000000000000186472697665722e686f735f656c61707365645f686f757273" +
				"01030000000000000001",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			compiled, err := CompileExpression(schema, testCompilerVersion, test.expr)
			if err != nil {
				t.Fatalf("CompileExpression: %v", err)
			}
			if got := hex.EncodeToString(compiled.CanonicalBytes()); got != test.wantHex {
				t.Fatalf("canonical bytes =\n%s\nwant\n%s", got, test.wantHex)
			}
		})
	}
}
