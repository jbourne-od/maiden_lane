// Package sql implements deterministic transpilation of Maiden Lane canonical
// transformation plans and expressions into SQL pipelines and CTE DAGs.
//
// In accordance with Inviolate 1 and AGENTS.md §3.1, transformation semantics belong
// exclusively to Maiden Lane's closed semantic model (internal/semantic). SQL is an
// execution backend for that meaning. The transpiler consumes the canonical Plan and
// generates deterministic, dialect-aware SQL without inventing new semantics.
package sql

import (
	"fmt"
	"strings"
)

// Dialect abstracts SQL syntax differences across database engines.
type Dialect interface {
	// Name returns the dialect identifier.
	Name() string

	// QuoteIdentifier quotes an SQL table, CTE, or column identifier.
	QuoteIdentifier(name string) string

	// QuoteString escapes and quotes an SQL string literal.
	QuoteString(val string) string

	// Concat formats string concatenation for the given expressions.
	Concat(args ...string) string

	// Substring formats substring extraction (1-based offset).
	Substring(str, start, length string) string

	// Trim formats whitespace trimming.
	Trim(str string) string

	// TimestampAdd adds integer seconds (duration) to a timestamp.
	TimestampAdd(ts, durationSeconds string) string

	// TimestampDiff computes the difference in seconds between two timestamps (ts1 - ts2).
	TimestampDiff(ts1, ts2 string) string

	// DateExtract extracts a date/time part (year, month, day, hour, minute, second, day_of_week) from a timestamp.
	DateExtract(unit string, ts string) string

	// Clamp bounds a value between min and max.
	Clamp(val, min, max string) string

	// Modulo formats integer modulo.
	Modulo(left, right string) string

	// BoolAnd formats a boolean aggregation for group-level all().
	BoolAnd(expr string) string

	// BoolOr formats a boolean aggregation for group-level any().
	BoolOr(expr string) string

	// CastBoolean casts an expression to a boolean.
	CastBoolean(expr string) string

	// DigestSHA256 formats an expression that computes a hex SHA-256 digest with 'sha256:' prefix.
	DigestSHA256(expr string) string

	// SyntheticEntityID formats an expression that computes a canonical content-addressed
	// synthetic entity identity matching semantic.SyntheticEntityID.
	SyntheticEntityID(targetKind string, ruleID string, lineageExpr string, progenitorExpr string, discriminatorExpr string) string
}

// PostgreSQL dialect implementation.
type postgresDialect struct{}

// Postgres returns a Dialect configured for PostgreSQL and DuckDB.
func Postgres() Dialect {
	return postgresDialect{}
}

func (p postgresDialect) Name() string { return "postgres" }

func (p postgresDialect) QuoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func (p postgresDialect) QuoteString(val string) string {
	return `'` + strings.ReplaceAll(val, `'`, `''`) + `'`
}

func (p postgresDialect) Concat(args ...string) string {
	if len(args) == 0 {
		return "''"
	}
	if len(args) == 1 {
		return args[0]
	}
	return "CONCAT(" + strings.Join(args, ", ") + ")"
}

func (p postgresDialect) Substring(str, start, length string) string {
	if length != "" {
		return fmt.Sprintf("SUBSTRING(%s FROM (%s) FOR (%s))", str, start, length)
	}
	return fmt.Sprintf("SUBSTRING(%s FROM (%s))", str, start)
}

func (p postgresDialect) Trim(str string) string {
	return fmt.Sprintf("TRIM(%s)", str)
}

func (p postgresDialect) TimestampAdd(ts, durationSeconds string) string {
	return fmt.Sprintf("(%s + ((%s) * INTERVAL '1 second'))", ts, durationSeconds)
}

func (p postgresDialect) TimestampDiff(ts1, ts2 string) string {
	return fmt.Sprintf("FLOOR(EXTRACT(EPOCH FROM ((%s) - (%s))))::BIGINT", ts1, ts2)
}

func (p postgresDialect) DateExtract(unit string, ts string) string {
	switch strings.ToLower(unit) {
	case "day_of_week", "dow":
		return fmt.Sprintf("EXTRACT(DOW FROM %s)::BIGINT", ts)
	default:
		return fmt.Sprintf("EXTRACT(%s FROM %s)::BIGINT", strings.ToUpper(unit), ts)
	}
}

func (p postgresDialect) Clamp(val, min, max string) string {
	return fmt.Sprintf("LEAST(GREATEST(%s, %s), %s)", val, min, max)
}

func (p postgresDialect) Modulo(left, right string) string {
	return fmt.Sprintf("((%s) %% (%s))", left, right)
}

func (p postgresDialect) BoolAnd(expr string) string {
	return fmt.Sprintf("BOOL_AND(%s)", expr)
}

func (p postgresDialect) BoolOr(expr string) string {
	return fmt.Sprintf("BOOL_OR(%s)", expr)
}

func (p postgresDialect) CastBoolean(expr string) string {
	return fmt.Sprintf("(%s)::BOOLEAN", expr)
}

func (p postgresDialect) DigestSHA256(expr string) string {
	return fmt.Sprintf("('sha256:' || ENCODE(DIGEST(%s, 'sha256'), 'hex'))", expr)
}

func (p postgresDialect) SyntheticEntityID(targetKind string, ruleID string, lineageExpr string, progenitorExpr string, discriminatorExpr string) string {
	return fmt.Sprintf(`('sha256:' || ENCODE(DIGEST(
    '\x000000000000001f'::bytea
    || convert_to('maiden-lane.synthetic-entity.v1', 'UTF8')
    || decode(SUBSTRING(COALESCE(%s, 'sha256:0000000000000000000000000000000000000000000000000000000000000000') FROM 8), 'hex')
    || decode(LPAD(TO_HEX(OCTET_LENGTH(%s)), 16, '0'), 'hex')
    || convert_to(%s, 'UTF8')
    || decode(LPAD(TO_HEX(OCTET_LENGTH(%s)), 16, '0'), 'hex')
    || convert_to(%s, 'UTF8')
    || (%s)
    || '\x01'::bytea
    || decode(LPAD(TO_HEX(OCTET_LENGTH((%s)::text)), 16, '0'), 'hex')
    || convert_to((%s)::text, 'UTF8'),
    'sha256'
), 'hex'))`,
		lineageExpr,
		p.QuoteString(targetKind), p.QuoteString(targetKind),
		p.QuoteString(ruleID), p.QuoteString(ruleID),
		progenitorExpr,
		discriminatorExpr, discriminatorExpr,
	)
}

// ANSISQL returns a standard ANSI SQL dialect.
func ANSISQL() Dialect {
	return ansiDialect{}
}

type ansiDialect struct {
	postgresDialect
}

func (a ansiDialect) Name() string { return "ansi" }
