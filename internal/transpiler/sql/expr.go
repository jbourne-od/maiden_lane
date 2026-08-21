package sql

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

// TranspileContext provides scope context for SQL expression transpilation.
type TranspileContext struct {
	// Dialect is the SQL dialect to use.
	Dialect Dialect

	// EntityTableAlias is the table/CTE alias for member entity field access (e.g. "m" or "src").
	EntityTableAlias string

	// GroupTableAlias is the table/CTE alias for group-level reduction access (e.g. "grp").
	GroupTableAlias string

	// FromAlias is the table alias for relation "from" endpoint (e.g. "f").
	FromAlias string

	// ToAlias is the table alias for relation "to" endpoint (e.g. "t").
	ToAlias string

	// IsGroupScope is true when compiling expressions inside a group reduction or group guard.
	IsGroupScope bool
}

// TranspileExpr transpiles a closed semantic.Expr AST node into a dialect-specific SQL expression string.
func TranspileExpr(ctx TranspileContext, expr semantic.Expr) (string, error) {
	if ctx.Dialect == nil {
		ctx.Dialect = Postgres()
	}

	switch expr.Kind {
	case semantic.ExprLiteral:
		if expr.Literal == nil {
			return "NULL", nil
		}
		return transpileLiteral(ctx.Dialect, *expr.Literal)

	case semantic.ExprField:
		return transpileFieldPath(ctx, expr.Field)

	case semantic.ExprExists:
		target, err := transpileFieldPath(ctx, expr.Field)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("(%s IS NOT NULL)", target), nil

	case semantic.ExprNot:
		if len(expr.Args) == 0 {
			return "", fmt.Errorf("not expression requires 1 argument")
		}
		sub, err := TranspileExpr(ctx, expr.Args[0])
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("(NOT %s)", sub), nil

	case semantic.ExprAll:
		if len(expr.Args) == 0 {
			return "TRUE", nil
		}
		parts := make([]string, len(expr.Args))
		for i, a := range expr.Args {
			part, err := TranspileExpr(ctx, a)
			if err != nil {
				return "", err
			}
			parts[i] = part
		}
		return "(" + strings.Join(parts, " AND ") + ")", nil

	case semantic.ExprAny:
		if len(expr.Args) == 0 {
			return "FALSE", nil
		}
		parts := make([]string, len(expr.Args))
		for i, a := range expr.Args {
			part, err := TranspileExpr(ctx, a)
			if err != nil {
				return "", err
			}
			parts[i] = part
		}
		return "(" + strings.Join(parts, " OR ") + ")", nil

	case semantic.ExprEqual:
		if len(expr.Args) < 2 {
			return "", fmt.Errorf("equal expression requires 2 arguments")
		}
		left, err := TranspileExpr(ctx, expr.Args[0])
		if err != nil {
			return "", err
		}
		right, err := TranspileExpr(ctx, expr.Args[1])
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("(%s = %s)", left, right), nil

	case semantic.ExprLess:
		if len(expr.Args) < 2 {
			return "", fmt.Errorf("less expression requires 2 arguments")
		}
		left, err := TranspileExpr(ctx, expr.Args[0])
		if err != nil {
			return "", err
		}
		right, err := TranspileExpr(ctx, expr.Args[1])
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("(%s < %s)", left, right), nil

	case semantic.ExprAdd:
		if len(expr.Args) < 2 {
			return "", fmt.Errorf("add expression requires 2 arguments")
		}
		left, err := TranspileExpr(ctx, expr.Args[0])
		if err != nil {
			return "", err
		}
		right, err := TranspileExpr(ctx, expr.Args[1])
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("(%s + %s)", left, right), nil

	case semantic.ExprSubtract:
		if len(expr.Args) < 2 {
			return "", fmt.Errorf("subtract expression requires 2 arguments")
		}
		left, err := TranspileExpr(ctx, expr.Args[0])
		if err != nil {
			return "", err
		}
		right, err := TranspileExpr(ctx, expr.Args[1])
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("(%s - %s)", left, right), nil

	case semantic.ExprMultiply:
		if len(expr.Args) < 2 {
			return "", fmt.Errorf("multiply expression requires 2 arguments")
		}
		left, err := TranspileExpr(ctx, expr.Args[0])
		if err != nil {
			return "", err
		}
		right, err := TranspileExpr(ctx, expr.Args[1])
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("(%s * %s)", left, right), nil

	case semantic.ExprDivide:
		if len(expr.Args) < 2 {
			return "", fmt.Errorf("divide expression requires 2 arguments")
		}
		left, err := TranspileExpr(ctx, expr.Args[0])
		if err != nil {
			return "", err
		}
		right, err := TranspileExpr(ctx, expr.Args[1])
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("(%s / %s)", left, right), nil

	case semantic.ExprModulo:
		if len(expr.Args) < 2 {
			return "", fmt.Errorf("modulo expression requires 2 arguments")
		}
		left, err := TranspileExpr(ctx, expr.Args[0])
		if err != nil {
			return "", err
		}
		right, err := TranspileExpr(ctx, expr.Args[1])
		if err != nil {
			return "", err
		}
		return ctx.Dialect.Modulo(left, right), nil

	case semantic.ExprAbs:
		if len(expr.Args) == 0 {
			return "", fmt.Errorf("abs expression requires 1 argument")
		}
		arg, err := TranspileExpr(ctx, expr.Args[0])
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("ABS(%s)", arg), nil

	case semantic.ExprClamp:
		if len(expr.Args) != 3 {
			return "", fmt.Errorf("clamp expects 3 arguments, got %d", len(expr.Args))
		}
		val, err := TranspileExpr(ctx, expr.Args[0])
		if err != nil {
			return "", err
		}
		minVal, err := TranspileExpr(ctx, expr.Args[1])
		if err != nil {
			return "", err
		}
		maxVal, err := TranspileExpr(ctx, expr.Args[2])
		if err != nil {
			return "", err
		}
		return ctx.Dialect.Clamp(val, minVal, maxVal), nil

	case semantic.ExprTimestampAdd:
		if len(expr.Args) < 2 {
			return "", fmt.Errorf("timestamp_add expression requires 2 arguments")
		}
		ts, err := TranspileExpr(ctx, expr.Args[0])
		if err != nil {
			return "", err
		}
		dur, err := TranspileExpr(ctx, expr.Args[1])
		if err != nil {
			return "", err
		}
		return ctx.Dialect.TimestampAdd(ts, dur), nil

	case semantic.ExprTimestampDiff:
		if len(expr.Args) < 2 {
			return "", fmt.Errorf("timestamp_diff expression requires 2 arguments")
		}
		ts1, err := TranspileExpr(ctx, expr.Args[0])
		if err != nil {
			return "", err
		}
		ts2, err := TranspileExpr(ctx, expr.Args[1])
		if err != nil {
			return "", err
		}
		return ctx.Dialect.TimestampDiff(ts1, ts2), nil

	case semantic.ExprDateExtract:
		if len(expr.Args) == 0 {
			return "", fmt.Errorf("date_extract expression requires 1 argument")
		}
		ts, err := TranspileExpr(ctx, expr.Args[0])
		if err != nil {
			return "", err
		}
		_, unit := splitFieldPath(expr.Field)
		return ctx.Dialect.DateExtract(unit, ts), nil

	case semantic.ExprConcat:
		parts := make([]string, len(expr.Args))
		for i, a := range expr.Args {
			part, err := TranspileExpr(ctx, a)
			if err != nil {
				return "", err
			}
			parts[i] = part
		}
		return ctx.Dialect.Concat(parts...), nil

	case semantic.ExprSubstring:
		if len(expr.Args) < 2 {
			return "", fmt.Errorf("substring expects at least 2 arguments")
		}
		str, err := TranspileExpr(ctx, expr.Args[0])
		if err != nil {
			return "", err
		}
		offset, err := TranspileExpr(ctx, expr.Args[1])
		if err != nil {
			return "", err
		}
		// DSL offset is 0-based; SQL SUBSTRING offset is 1-based
		sqlStart := fmt.Sprintf("%s + 1", offset)
		var sqlLen string
		if len(expr.Args) >= 3 {
			l, err := TranspileExpr(ctx, expr.Args[2])
			if err != nil {
				return "", err
			}
			sqlLen = l
		}
		return ctx.Dialect.Substring(str, sqlStart, sqlLen), nil

	case semantic.ExprTrim:
		if len(expr.Args) == 0 {
			return "", fmt.Errorf("trim expression requires 1 argument")
		}
		arg, err := TranspileExpr(ctx, expr.Args[0])
		if err != nil {
			return "", err
		}
		return ctx.Dialect.Trim(arg), nil

	case semantic.ExprIf:
		if len(expr.Args) != 3 {
			return "", fmt.Errorf("if expression expects condition, then, else (3 arguments)")
		}
		cond, err := TranspileExpr(ctx, expr.Args[0])
		if err != nil {
			return "", err
		}
		thenExpr, err := TranspileExpr(ctx, expr.Args[1])
		if err != nil {
			return "", err
		}
		elseExpr, err := TranspileExpr(ctx, expr.Args[2])
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("(CASE WHEN %s THEN %s ELSE %s END)", cond, thenExpr, elseExpr), nil

	case semantic.ExprCoalesce:
		if len(expr.Args) == 0 {
			return "NULL", nil
		}
		parts := make([]string, len(expr.Args))
		for i, a := range expr.Args {
			part, err := TranspileExpr(ctx, a)
			if err != nil {
				return "", err
			}
			parts[i] = part
		}
		return "COALESCE(" + strings.Join(parts, ", ") + ")", nil

	case semantic.ExprCount:
		if len(expr.Args) > 0 {
			arg, err := TranspileExpr(ctx, expr.Args[0])
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("COUNT(%s)", arg), nil
		}
		if expr.Field != "" {
			arg, err := transpileFieldPath(ctx, expr.Field)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("COUNT(%s)", arg), nil
		}
		return "COUNT(*)", nil

	case semantic.ExprSum:
		var arg string
		if len(expr.Args) > 0 {
			a, err := TranspileExpr(ctx, expr.Args[0])
			if err != nil {
				return "", err
			}
			arg = a
		} else if expr.Field != "" {
			a, err := transpileFieldPath(ctx, expr.Field)
			if err != nil {
				return "", err
			}
			arg = a
		} else {
			return "", fmt.Errorf("sum expression requires argument or field")
		}
		return fmt.Sprintf("SUM(%s)", arg), nil

	case semantic.ExprMin:
		var arg string
		if len(expr.Args) > 0 {
			a, err := TranspileExpr(ctx, expr.Args[0])
			if err != nil {
				return "", err
			}
			arg = a
		} else if expr.Field != "" {
			a, err := transpileFieldPath(ctx, expr.Field)
			if err != nil {
				return "", err
			}
			arg = a
		} else {
			return "", fmt.Errorf("min expression requires argument or field")
		}
		return fmt.Sprintf("MIN(%s)", arg), nil

	case semantic.ExprMax:
		var arg string
		if len(expr.Args) > 0 {
			a, err := TranspileExpr(ctx, expr.Args[0])
			if err != nil {
				return "", err
			}
			arg = a
		} else if expr.Field != "" {
			a, err := transpileFieldPath(ctx, expr.Field)
			if err != nil {
				return "", err
			}
			arg = a
		} else {
			return "", fmt.Errorf("max expression requires argument or field")
		}
		return fmt.Sprintf("MAX(%s)", arg), nil

	case semantic.ExprAllMembers:
		if len(expr.Args) == 0 {
			return "", fmt.Errorf("all_members expression requires 1 argument")
		}
		arg, err := TranspileExpr(ctx, expr.Args[0])
		if err != nil {
			return "", err
		}
		return ctx.Dialect.BoolAnd(arg), nil

	case semantic.ExprAnyMembers:
		if len(expr.Args) == 0 {
			return "", fmt.Errorf("any_members expression requires 1 argument")
		}
		arg, err := TranspileExpr(ctx, expr.Args[0])
		if err != nil {
			return "", err
		}
		return ctx.Dialect.BoolOr(arg), nil

	case semantic.ExprAllEqual:
		var arg string
		if len(expr.Args) > 0 {
			a, err := TranspileExpr(ctx, expr.Args[0])
			if err != nil {
				return "", err
			}
			arg = a
		} else if expr.Field != "" {
			a, err := transpileFieldPath(ctx, expr.Field)
			if err != nil {
				return "", err
			}
			arg = a
		} else {
			return "", fmt.Errorf("all_equal expression requires argument or field")
		}
		return fmt.Sprintf("(MIN(%s) = MAX(%s))", arg, arg), nil

	default:
		return "", fmt.Errorf("unsupported expression kind: %v", expr.Kind)
	}
}

func transpileLiteral(d Dialect, val semantic.Value) (string, error) {
	switch val.Kind() {
	case semantic.ValueString, semantic.ValueAtom:
		s, _ := val.String()
		return d.QuoteString(s), nil
	case semantic.ValueInt64:
		i, _ := val.Int64()
		return strconv.FormatInt(i, 10), nil
	case semantic.ValueTimestamp:
		ts, _ := val.Timestamp()
		return d.QuoteString(ts), nil
	case semantic.ValueDuration:
		dur, _ := val.Duration()
		return strconv.FormatInt(dur, 10), nil
	case semantic.ValueDecimal:
		dec, _ := val.Decimal()
		return dec, nil
	case semantic.ValueDate:
		dt, _ := val.Date()
		return d.QuoteString(dt), nil
	default:
		return "", fmt.Errorf("unsupported value kind: %v", val.Kind())
	}
}

func transpileFieldPath(ctx TranspileContext, path semantic.FieldPath) (string, error) {
	entityKind, fieldName := splitFieldPath(path)
	colName := ctx.Dialect.QuoteIdentifier(fieldName)

	switch {
	case entityKind == "from" && ctx.FromAlias != "":
		return fmt.Sprintf("%s.%s", ctx.FromAlias, colName), nil
	case entityKind == "to" && ctx.ToAlias != "":
		return fmt.Sprintf("%s.%s", ctx.ToAlias, colName), nil
	case ctx.EntityTableAlias != "":
		return fmt.Sprintf("%s.%s", ctx.EntityTableAlias, colName), nil
	default:
		return colName, nil
	}
}

func splitFieldPath(path semantic.FieldPath) (string, string) {
	val := string(path)
	idx := strings.IndexByte(val, '.')
	if idx < 0 {
		return "", val
	}
	return val[:idx], val[idx+1:]
}
