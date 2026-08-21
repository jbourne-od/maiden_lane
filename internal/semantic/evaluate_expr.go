package semantic

import (
	"fmt"
	"strings"
)

// Evaluation of an expression against ONE BOUND ENTITY.
//
// This is the smallest evaluation that means anything, and it is why selection and evaluation
// arrive together. Slice 1 settled that there is no ambient scope, so a Field path names an
// entity kind and no instance; until something binds an instance, no expression denotes a
// value. A selector binds one. Evaluating with no binding at all was the ordering this
// programme rejected, and this is not that: the bound entity IS the referent, supplied by the
// caller and never inferred.
//
// Everything here is total and deterministic. There is no clock, no map iteration, no
// ordering that depends on anything but the expression, and absence is refused rather than
// defaulted -- a missing field yields an error, never a zero.

// evaluated is one expression's result. It is not a Value because an expression may be bool
// and Value has no bool variant.
type evaluated struct {
	kind    ExprType
	value   Value
	boolean bool
}

// evaluateBool evaluates an expression that must be bool.
func evaluateBool(schema Schema, expr Expr, entity Entity) (bool, error) {
	result, err := evaluateExpr(schema, expr, entity)
	if err != nil {
		return false, err
	}
	if result.kind != TypeBool {
		// Unreachable through a compiled selector, which checks the type. Refused rather than
		// coerced so that a future caller reaching this directly cannot get a silent answer.
		return false, fmt.Errorf("expression is %s, not bool", result.kind)
	}
	return result.boolean, nil
}

// evaluateValue evaluates an expression that must yield a Value.
func evaluateValue(schema Schema, expr Expr, entity Entity) (Value, error) {
	result, err := evaluateExpr(schema, expr, entity)
	if err != nil {
		return Value{}, err
	}
	if result.kind == TypeBool {
		return Value{}, fmt.Errorf("expression is bool, not a value")
	}
	if !result.value.Valid() {
		// A zero value with no error is the zero-value-permits shape this codebase forbids.
		return Value{}, fmt.Errorf("expression yielded no value")
	}
	return result.value, nil
}

// evaluateExpr walks one expression against the bound entity.
func evaluateExpr(schema Schema, expr Expr, entity Entity) (evaluated, error) {
	// TOTALITY, enforced rather than claimed. The header says this function is total, and an
	// earlier version was not: Expr{Kind: ExprNot} indexed Args[0] and panicked, as did every
	// binary kind, while the sibling arms all guarded exactly this class of un-compiled input.
	// checkOperandShape is the compiler's own check, reused here so the two cannot disagree
	// about what a kind's operands are.
	if err := checkOperandShape(expr); err != nil {
		return evaluated{}, err
	}
	switch expr.Kind {
	case ExprLiteral:
		if expr.Literal == nil {
			return evaluated{}, fmt.Errorf("literal carries no value")
		}
		// literalType, which is the compiler's ExprLiteral mapping: Valid() and THEN
		// valueKindType. An earlier fix collapsed both call sites onto valueKindType alone,
		// which switches on the KIND and never consults Valid() -- so a Value with a
		// recognised kind and invalid content (a string holding invalid UTF-8) was refused by
		// the compiler and accepted here, and equal compared kind and text and answered TRUE.
		// That is the fourth instance of one shape, sitting inside the fix for the second:
		// two mappings that disagree about refusal, collapsed onto the wrong one.
		kind, err := literalType(*expr.Literal)
		if err != nil {
			return evaluated{}, fmt.Errorf("literal: %w", err)
		}
		return evaluated{kind: kind, value: *expr.Literal}, nil

	case ExprField:
		value, declared, present, err := boundField(schema, expr.Field, entity)
		if err != nil {
			return evaluated{}, err
		}
		if !present {
			// ABSENCE IS REFUSED, NOT DEFAULTED. Yielding a zero here would make
			// less(driver.hours, 10) true for an entity with no hours at all, which is a
			// claim about the world nobody made. An author who means "present and below"
			// writes all(exists(f), less(f, 10)), which works because all short circuits --
			// see ExprAll. The refusal keeps that distinction the author's to make; the
			// short circuit keeps it expressible.
			return evaluated{}, fmt.Errorf("field %q is absent", expr.Field)
		}
		// ONE DERIVATION, from the DECLARATION, which is what checkExpr uses. An earlier
		// version derived this type from the stored value's kind, so the compiler typed a
		// field from schema.fieldKind and the evaluator typed it from value.Kind() -- two
		// derivations of one fact from two references, which is the shape that produced four
		// of this branch's wrong-answer defects. What kept them equal was validateEntityFields,
		// and the comment here cited it as refusing invalid values, which it does not: it
		// checks kind agreement and required-field presence, not Valid().
		kind, err := valueKindType(declared)
		if err != nil {
			return evaluated{}, fmt.Errorf("field %q: %w", expr.Field, err)
		}
		// And the stored value is required to agree with the declaration rather than assumed
		// to. NewState enforces this; a caller reaching the evaluator with an Entity that did
		// not pass through it would otherwise get a value tagged with a type it is not.
		if value.Kind() != declared {
			return evaluated{}, fmt.Errorf(
				"field %q holds a value of kind %d, declared %d", expr.Field, value.Kind(), declared)
		}
		return evaluated{kind: kind, value: value}, nil

	case ExprExists:
		_, _, present, err := boundField(schema, expr.Field, entity)
		if err != nil {
			return evaluated{}, err
		}
		return evaluated{kind: TypeBool, boolean: present}, nil

	case ExprNot:
		operand, err := evaluateBool(schema, expr.Args[0], entity)
		if err != nil {
			return evaluated{}, err
		}
		return evaluated{kind: TypeBool, boolean: !operand}, nil

	case ExprAll:
		// SHORT CIRCUITS, and an earlier version deliberately did not. That version argued
		// determinism: an absent field should be refused wherever it appears rather than
		// depending on the order the author wrote the conjuncts in.
		//
		// The argument was wrong twice over. Short-circuit evaluation is deterministic --
		// it depends on authored order, which is content this kernel already treats as
		// semantic and preserves in the canonical bytes, not on anything ambient. And
		// without it, absence-tolerant predicates are UNWRITABLE: absence is refused, so
		// all(exists(f), less(f, 10)) evaluates less() on the absent field and errors, and
		// so does any(not(exists(f)), less(f, 10)). One sparse entity then fails the whole
		// selection. Real inputs are sparse, so a language that cannot say "present and
		// below" is not a language for them.
		for i := range expr.Args {
			operand, err := evaluateBool(schema, expr.Args[i], entity)
			if err != nil {
				return evaluated{}, err
			}
			if !operand {
				return evaluated{kind: TypeBool, boolean: false}, nil
			}
		}
		return evaluated{kind: TypeBool, boolean: true}, nil

	case ExprAny:
		for i := range expr.Args {
			operand, err := evaluateBool(schema, expr.Args[i], entity)
			if err != nil {
				return evaluated{}, err
			}
			if operand {
				return evaluated{kind: TypeBool, boolean: true}, nil
			}
		}
		return evaluated{kind: TypeBool, boolean: false}, nil

	case ExprEqual:
		left, right, err := evaluatePair(schema, expr, entity)
		if err != nil {
			return evaluated{}, err
		}
		if left.kind != right.kind {
			return evaluated{}, fmt.Errorf("equal compares %s with %s", left.kind, right.kind)
		}
		if left.kind == TypeBool {
			return evaluated{kind: TypeBool, boolean: left.boolean == right.boolean}, nil
		}
		return evaluated{kind: TypeBool, boolean: left.value.Equal(right.value)}, nil

	case ExprLess:
		left, right, err := evaluatePair(schema, expr, entity)
		if err != nil {
			return evaluated{}, err
		}
		if left.kind != right.kind {
			return evaluated{}, fmt.Errorf("less compares %s with %s", left.kind, right.kind)
		}
		switch left.kind {
		case TypeInt64, TypeDuration, TypeTimestamp, TypeDate:
			return evaluated{kind: TypeBool, boolean: left.value.integer < right.value.integer}, nil
		case TypeDecimal:
			d1, _ := parseDecimal(left.value.text)
			d2, _ := parseDecimal(right.value.text)
			return evaluated{kind: TypeBool, boolean: d1.Less(d2)}, nil
		default:
			return evaluated{}, fmt.Errorf("less not supported on %s", left.kind)
		}

	case ExprAdd:
		left, right, err := evaluatePair(schema, expr, entity)
		if err != nil {
			return evaluated{}, err
		}
		if left.kind == TypeInt64 && right.kind == TypeInt64 {
			sum, err := addInt64(left.value.integer, right.value.integer)
			if err != nil {
				return evaluated{}, err
			}
			return evaluated{kind: TypeInt64, value: NewInt64Value(sum)}, nil
		}
		if left.kind == TypeDecimal && right.kind == TypeDecimal {
			d1, _ := parseDecimal(left.value.text)
			d2, _ := parseDecimal(right.value.text)
			res, err := d1.Add(d2)
			if err != nil {
				return evaluated{}, err
			}
			val, _ := NewDecimalValue(res.String())
			return evaluated{kind: TypeDecimal, value: val}, nil
		}
		if left.kind == TypeDuration && right.kind == TypeDuration {
			sum, err := addInt64(left.value.integer, right.value.integer)
			if err != nil {
				return evaluated{}, err
			}
			return evaluated{kind: TypeDuration, value: NewDurationValue(sum)}, nil
		}
		if left.kind == TypeTimestamp && right.kind == TypeDuration {
			ts, _ := parseTimestamp(left.value.text)
			res, err := ts.AddDuration(right.value.integer)
			if err != nil {
				return evaluated{}, err
			}
			val, _ := NewTimestampValue(res.String())
			return evaluated{kind: TypeTimestamp, value: val}, nil
		}
		if left.kind == TypeDuration && right.kind == TypeTimestamp {
			ts, _ := parseTimestamp(right.value.text)
			res, err := ts.AddDuration(left.value.integer)
			if err != nil {
				return evaluated{}, err
			}
			val, _ := NewTimestampValue(res.String())
			return evaluated{kind: TypeTimestamp, value: val}, nil
		}
		return evaluated{}, fmt.Errorf("add unsupported on %s and %s", left.kind, right.kind)

	case ExprSubtract:
		left, right, err := evaluatePair(schema, expr, entity)
		if err != nil {
			return evaluated{}, err
		}
		if left.kind == TypeInt64 && right.kind == TypeInt64 {
			diff, err := subInt64(left.value.integer, right.value.integer)
			if err != nil {
				return evaluated{}, err
			}
			return evaluated{kind: TypeInt64, value: NewInt64Value(diff)}, nil
		}
		if left.kind == TypeDecimal && right.kind == TypeDecimal {
			d1, _ := parseDecimal(left.value.text)
			d2, _ := parseDecimal(right.value.text)
			res, err := d1.Sub(d2)
			if err != nil {
				return evaluated{}, err
			}
			val, _ := NewDecimalValue(res.String())
			return evaluated{kind: TypeDecimal, value: val}, nil
		}
		if left.kind == TypeDuration && right.kind == TypeDuration {
			diff, err := subInt64(left.value.integer, right.value.integer)
			if err != nil {
				return evaluated{}, err
			}
			return evaluated{kind: TypeDuration, value: NewDurationValue(diff)}, nil
		}
		if left.kind == TypeTimestamp && right.kind == TypeTimestamp {
			ts1, _ := parseTimestamp(left.value.text)
			ts2, _ := parseTimestamp(right.value.text)
			diff, err := ts1.Diff(ts2)
			if err != nil {
				return evaluated{}, err
			}
			return evaluated{kind: TypeDuration, value: NewDurationValue(diff)}, nil
		}
		if left.kind == TypeTimestamp && right.kind == TypeDuration {
			ts, _ := parseTimestamp(left.value.text)
			dur := right.value.integer
			if dur == -9223372036854775808 {
				return evaluated{}, fmt.Errorf("duration %d overflows negation", dur)
			}
			res, err := ts.AddDuration(-dur)
			if err != nil {
				return evaluated{}, err
			}
			val, _ := NewTimestampValue(res.String())
			return evaluated{kind: TypeTimestamp, value: val}, nil
		}
		return evaluated{}, fmt.Errorf("subtract unsupported on %s and %s", left.kind, right.kind)

	case ExprMultiply:
		left, right, err := evaluatePair(schema, expr, entity)
		if err != nil {
			return evaluated{}, err
		}
		if left.kind == TypeInt64 && right.kind == TypeInt64 {
			prod, err := mulInt64(left.value.integer, right.value.integer)
			if err != nil {
				return evaluated{}, err
			}
			return evaluated{kind: TypeInt64, value: NewInt64Value(prod)}, nil
		}
		if left.kind == TypeDecimal && right.kind == TypeDecimal {
			d1, _ := parseDecimal(left.value.text)
			d2, _ := parseDecimal(right.value.text)
			res, err := d1.Mul(d2)
			if err != nil {
				return evaluated{}, err
			}
			val, _ := NewDecimalValue(res.String())
			return evaluated{kind: TypeDecimal, value: val}, nil
		}
		if left.kind == TypeDuration && right.kind == TypeInt64 {
			prod, err := mulInt64(left.value.integer, right.value.integer)
			if err != nil {
				return evaluated{}, err
			}
			return evaluated{kind: TypeDuration, value: NewDurationValue(prod)}, nil
		}
		if left.kind == TypeInt64 && right.kind == TypeDuration {
			prod, err := mulInt64(left.value.integer, right.value.integer)
			if err != nil {
				return evaluated{}, err
			}
			return evaluated{kind: TypeDuration, value: NewDurationValue(prod)}, nil
		}
		return evaluated{}, fmt.Errorf("multiply unsupported on %s and %s", left.kind, right.kind)

	case ExprDivide:
		left, right, err := evaluatePair(schema, expr, entity)
		if err != nil {
			return evaluated{}, err
		}
		if left.kind == TypeInt64 && right.kind == TypeInt64 {
			if right.value.integer == 0 {
				return evaluated{}, fmt.Errorf("division by zero")
			}
			if left.value.integer == -9223372036854775808 && right.value.integer == -1 {
				return evaluated{}, fmt.Errorf("division overflows int64")
			}
			return evaluated{kind: TypeInt64, value: NewInt64Value(left.value.integer / right.value.integer)}, nil
		}
		if left.kind == TypeDecimal && right.kind == TypeDecimal {
			d1, _ := parseDecimal(left.value.text)
			d2, _ := parseDecimal(right.value.text)
			res, err := d1.Div(d2)
			if err != nil {
				return evaluated{}, err
			}
			val, _ := NewDecimalValue(res.String())
			return evaluated{kind: TypeDecimal, value: val}, nil
		}
		return evaluated{}, fmt.Errorf("divide unsupported on %s and %s", left.kind, right.kind)

	case ExprModulo:
		left, right, err := evaluatePair(schema, expr, entity)
		if err != nil {
			return evaluated{}, err
		}
		if left.kind == TypeInt64 && right.kind == TypeInt64 {
			if right.value.integer == 0 {
				return evaluated{}, fmt.Errorf("modulo by zero")
			}
			if left.value.integer == -9223372036854775808 && right.value.integer == -1 {
				return evaluated{}, fmt.Errorf("modulo overflows int64")
			}
			return evaluated{kind: TypeInt64, value: NewInt64Value(left.value.integer % right.value.integer)}, nil
		}
		return evaluated{}, fmt.Errorf("modulo requires int64")

	case ExprAbs:
		arg, err := evaluateExpr(schema, expr.Args[0], entity)
		if err != nil {
			return evaluated{}, err
		}
		switch arg.kind {
		case TypeInt64:
			if arg.value.integer == -9223372036854775808 {
				return evaluated{}, fmt.Errorf("abs overflows int64")
			}
			v := arg.value.integer
			if v < 0 {
				v = -v
			}
			return evaluated{kind: TypeInt64, value: NewInt64Value(v)}, nil
		case TypeDuration:
			if arg.value.integer == -9223372036854775808 {
				return evaluated{}, fmt.Errorf("abs overflows duration")
			}
			v := arg.value.integer
			if v < 0 {
				v = -v
			}
			return evaluated{kind: TypeDuration, value: NewDurationValue(v)}, nil
		case TypeDecimal:
			d, _ := parseDecimal(arg.value.text)
			res := d.Abs()
			val, _ := NewDecimalValue(res.String())
			return evaluated{kind: TypeDecimal, value: val}, nil
		default:
			return evaluated{}, fmt.Errorf("abs unsupported on %s", arg.kind)
		}

	case ExprClamp:
		val, err := evaluateExpr(schema, expr.Args[0], entity)
		if err != nil {
			return evaluated{}, err
		}
		minVal, err := evaluateExpr(schema, expr.Args[1], entity)
		if err != nil {
			return evaluated{}, err
		}
		maxVal, err := evaluateExpr(schema, expr.Args[2], entity)
		if err != nil {
			return evaluated{}, err
		}
		if val.kind != minVal.kind || val.kind != maxVal.kind {
			return evaluated{}, fmt.Errorf("clamp types mismatch: %s, %s, %s", val.kind, minVal.kind, maxVal.kind)
		}
		switch val.kind {
		case TypeInt64, TypeDuration, TypeTimestamp, TypeDate:
			if maxVal.value.integer < minVal.value.integer {
				return evaluated{}, fmt.Errorf("clamp min %d is greater than max %d", minVal.value.integer, maxVal.value.integer)
			}
			clamped := val.value.integer
			if clamped < minVal.value.integer {
				return minVal, nil
			}
			if clamped > maxVal.value.integer {
				return maxVal, nil
			}
			return val, nil
		case TypeDecimal:
			dVal, _ := parseDecimal(val.value.text)
			dMin, _ := parseDecimal(minVal.value.text)
			dMax, _ := parseDecimal(maxVal.value.text)
			res, err := dVal.Clamp(dMin, dMax)
			if err != nil {
				return evaluated{}, err
			}
			v, _ := NewDecimalValue(res.String())
			return evaluated{kind: TypeDecimal, value: v}, nil
		default:
			return evaluated{}, fmt.Errorf("clamp unsupported on %s", val.kind)
		}

	case ExprTimestampAdd:
		left, right, err := evaluatePair(schema, expr, entity)
		if err != nil {
			return evaluated{}, err
		}
		if left.kind == TypeTimestamp && right.kind == TypeDuration {
			ts, _ := parseTimestamp(left.value.text)
			res, err := ts.AddDuration(right.value.integer)
			if err != nil {
				return evaluated{}, err
			}
			v, _ := NewTimestampValue(res.String())
			return evaluated{kind: TypeTimestamp, value: v}, nil
		}
		if left.kind == TypeDuration && right.kind == TypeTimestamp {
			ts, _ := parseTimestamp(right.value.text)
			res, err := ts.AddDuration(left.value.integer)
			if err != nil {
				return evaluated{}, err
			}
			v, _ := NewTimestampValue(res.String())
			return evaluated{kind: TypeTimestamp, value: v}, nil
		}
		return evaluated{}, fmt.Errorf("timestamp_add requires timestamp and duration")

	case ExprTimestampDiff:
		left, right, err := evaluatePair(schema, expr, entity)
		if err != nil {
			return evaluated{}, err
		}
		if left.kind == TypeTimestamp && right.kind == TypeTimestamp {
			ts1, _ := parseTimestamp(left.value.text)
			ts2, _ := parseTimestamp(right.value.text)
			diff, err := ts1.Diff(ts2)
			if err != nil {
				return evaluated{}, err
			}
			return evaluated{kind: TypeDuration, value: NewDurationValue(diff)}, nil
		}
		return evaluated{}, fmt.Errorf("timestamp_diff requires timestamps")

	case ExprDateExtract:
		arg, err := evaluateExpr(schema, expr.Args[0], entity)
		if err != nil {
			return evaluated{}, err
		}
		unit := string(expr.Field)
		if arg.kind == TypeTimestamp {
			ts, _ := parseTimestamp(arg.value.text)
			extracted, err := ts.DateExtract(unit)
			if err != nil {
				return evaluated{}, err
			}
			return evaluated{kind: TypeInt64, value: NewInt64Value(extracted)}, nil
		}
		if arg.kind == TypeDate {
			d, _ := parseDate(arg.value.text)
			extracted, err := d.DateExtract(unit)
			if err != nil {
				return evaluated{}, err
			}
			return evaluated{kind: TypeInt64, value: NewInt64Value(extracted)}, nil
		}
		return evaluated{}, fmt.Errorf("date_extract requires timestamp or date")

	case ExprConcat:
		var buf strings.Builder
		for _, arg := range expr.Args {
			res, err := evaluateExpr(schema, arg, entity)
			if err != nil {
				return evaluated{}, err
			}
			if res.kind != TypeString {
				return evaluated{}, fmt.Errorf("concat requires string, got %s", res.kind)
			}
			buf.WriteString(res.value.text)
		}
		val, err := NewStringValue(buf.String())
		if err != nil {
			return evaluated{}, err
		}
		return evaluated{kind: TypeString, value: val}, nil

	case ExprSubstring:
		strRes, err := evaluateExpr(schema, expr.Args[0], entity)
		if err != nil {
			return evaluated{}, err
		}
		startRes, err := evaluateExpr(schema, expr.Args[1], entity)
		if err != nil {
			return evaluated{}, err
		}
		lenRes, err := evaluateExpr(schema, expr.Args[2], entity)
		if err != nil {
			return evaluated{}, err
		}
		if strRes.kind != TypeString || startRes.kind != TypeInt64 || lenRes.kind != TypeInt64 {
			return evaluated{}, fmt.Errorf("substring requires (string, int64, int64)")
		}
		start := startRes.value.integer
		length := lenRes.value.integer
		if start < 0 || length < 0 {
			return evaluated{}, fmt.Errorf("substring start and length must be non-negative")
		}
		runes := []rune(strRes.value.text)
		runeCount := int64(len(runes))
		if start >= runeCount {
			val, _ := NewStringValue("")
			return evaluated{kind: TypeString, value: val}, nil
		}
		end := runeCount
		if length < runeCount-start {
			end = start + length
		}
		val, _ := NewStringValue(string(runes[start:end]))
		return evaluated{kind: TypeString, value: val}, nil

	case ExprTrim:
		strRes, err := evaluateExpr(schema, expr.Args[0], entity)
		if err != nil {
			return evaluated{}, err
		}
		if strRes.kind != TypeString {
			return evaluated{}, fmt.Errorf("trim requires string, got %s", strRes.kind)
		}
		val, _ := NewStringValue(strings.TrimSpace(strRes.value.text))
		return evaluated{kind: TypeString, value: val}, nil

	case ExprIf:
		cond, err := evaluateBool(schema, expr.Args[0], entity)
		if err != nil {
			return evaluated{}, err
		}
		if cond {
			return evaluateExpr(schema, expr.Args[1], entity)
		}
		return evaluateExpr(schema, expr.Args[2], entity)

	case ExprCoalesce:
		var lastErr error
		for _, arg := range expr.Args {
			res, err := evaluateExpr(schema, arg, entity)
			if err == nil {
				return res, nil
			}
			lastErr = err
		}
		return evaluated{}, fmt.Errorf("coalesce failed: %w", lastErr)

	default:
		return evaluated{}, fmt.Errorf("unknown expression kind %d", expr.Kind)
	}
}

func addInt64(a, b int64) (int64, error) {
	sum := a + b
	if (b > 0 && sum < a) || (b < 0 && sum > a) {
		return 0, fmt.Errorf("add overflows int64")
	}
	return sum, nil
}

func subInt64(a, b int64) (int64, error) {
	diff := a - b
	if (b > 0 && diff > a) || (b < 0 && diff < a) {
		return 0, fmt.Errorf("subtract overflows int64")
	}
	return diff, nil
}

func mulInt64(a, b int64) (int64, error) {
	if a == 0 || b == 0 {
		return 0, nil
	}
	prod := a * b
	if prod/b != a || (a == -1 && b == -9223372036854775808) {
		return 0, fmt.Errorf("multiply overflows int64")
	}
	return prod, nil
}

// evaluatePair evaluates both operands of a binary node.
func evaluatePair(schema Schema, expr Expr, entity Entity) (evaluated, evaluated, error) {
	left, err := evaluateExpr(schema, expr.Args[0], entity)
	if err != nil {
		return evaluated{}, evaluated{}, err
	}
	right, err := evaluateExpr(schema, expr.Args[1], entity)
	if err != nil {
		return evaluated{}, evaluated{}, err
	}
	return left, right, nil
}

// boundField reads a field from the bound entity, refusing a path that names another kind.
//
// A compiled selector has already established that every path names the selected kind, so
// this cannot fire through that route. It is checked anyway because the evaluator is reachable
// without one and must not read a field off an entity the path does not describe.
func boundField(
	schema Schema, path FieldPath, entity Entity,
) (value Value, declared ValueKind, present bool, err error) {
	kind, name := splitFieldPath(path)
	if kind == "" || name == "" {
		return Value{}, 0, false, fmt.Errorf("malformed field path %q", path)
	}
	// DECLAREDNESS, re-established rather than relied on. Without this, exists() over a path
	// no schema declares answered false rather than refusing -- absence of a DECLARATION
	// collapsed into absence of a VALUE, so not(exists(driver.typo)) was true for every
	// entity while the compiler refused the identical node. The kind half of this guard was
	// added a round earlier; this is the other half of the same sentence.
	declaredKind, isDeclared := schema.fieldKind(path)
	if !isDeclared {
		return Value{}, 0, false, fmt.Errorf("field %q is not declared by this schema", path)
	}
	if kind != entity.Ref().Kind {
		return Value{}, 0, false, fmt.Errorf(
			"path %q does not name the bound entity kind %q", path, entity.Ref().Kind)
	}
	stored, found := entity.Field(name)
	return stored, declaredKind, found, nil
}
