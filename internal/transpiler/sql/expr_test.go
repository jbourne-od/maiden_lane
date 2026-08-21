package sql

import (
	"strings"
	"testing"

	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

func strLit(s string) semantic.Expr {
	v, _ := semantic.NewStringValue(s)
	return semantic.Expr{Kind: semantic.ExprLiteral, Literal: &v}
}

func intLit(i int64) semantic.Expr {
	v := semantic.NewInt64Value(i)
	return semantic.Expr{Kind: semantic.ExprLiteral, Literal: &v}
}

func atomLit(a string) semantic.Expr {
	v, _ := semantic.NewAtomValue(a)
	return semantic.Expr{Kind: semantic.ExprLiteral, Literal: &v}
}

func fieldExpr(path semantic.FieldPath) semantic.Expr {
	return semantic.Expr{Kind: semantic.ExprField, Field: path}
}

func TestTranspileExpressions(t *testing.T) {
	d := Postgres()
	ctx := TranspileContext{
		Dialect:          d,
		EntityTableAlias: "m",
	}

	tests := []struct {
		name    string
		expr    semantic.Expr
		wantSub string
	}{
		{
			name:    "literal string",
			expr:    strLit("hello"),
			wantSub: "'hello'",
		},
		{
			name:    "literal int64",
			expr:    intLit(42),
			wantSub: "42",
		},
		{
			name:    "field access",
			expr:    fieldExpr("driver.hours"),
			wantSub: `m."hours"`,
		},
		{
			name:    "exists",
			expr:    semantic.Expr{Kind: semantic.ExprExists, Field: "driver.depot"},
			wantSub: `(m."depot" IS NOT NULL)`,
		},
		{
			name: "equal and less",
			expr: semantic.Expr{
				Kind: semantic.ExprEqual,
				Args: []semantic.Expr{
					fieldExpr("driver.hours"),
					intLit(10),
				},
			},
			wantSub: `(m."hours" = 10)`,
		},
		{
			name: "arithmetic add and multiply",
			expr: semantic.Expr{
				Kind: semantic.ExprMultiply,
				Args: []semantic.Expr{
					{
						Kind: semantic.ExprAdd,
						Args: []semantic.Expr{
							fieldExpr("driver.hours"),
							intLit(2),
						},
					},
					intLit(3),
				},
			},
			wantSub: `((m."hours" + 2) * 3)`,
		},
		{
			name: "abs and clamp",
			expr: semantic.Expr{
				Kind: semantic.ExprClamp,
				Args: []semantic.Expr{
					{
						Kind: semantic.ExprAbs,
						Args: []semantic.Expr{fieldExpr("driver.hours")},
					},
					intLit(0),
					intLit(60),
				},
			},
			wantSub: `LEAST(GREATEST(ABS(m."hours"), 0), 60)`,
		},
		{
			name: "concat and substring",
			expr: semantic.Expr{
				Kind: semantic.ExprConcat,
				Args: []semantic.Expr{
					{
						Kind: semantic.ExprSubstring,
						Args: []semantic.Expr{
							fieldExpr("driver.name"),
							intLit(0),
							intLit(3),
						},
					},
					strLit("_suffix"),
				},
			},
			wantSub: `CONCAT(SUBSTRING(m."name" FROM (0 + 1) FOR (3)), '_suffix')`,
		},
		{
			name: "if conditional",
			expr: semantic.Expr{
				Kind: semantic.ExprIf,
				Args: []semantic.Expr{
					{
						Kind: semantic.ExprEqual,
						Args: []semantic.Expr{
							fieldExpr("driver.status"),
							atomLit("ACTIVE"),
						},
					},
					intLit(1),
					intLit(0),
				},
			},
			wantSub: `(CASE WHEN (m."status" = 'ACTIVE') THEN 1 ELSE 0 END)`,
		},
		{
			name: "coalesce",
			expr: semantic.Expr{
				Kind: semantic.ExprCoalesce,
				Args: []semantic.Expr{
					fieldExpr("driver.preferred_hours"),
					fieldExpr("driver.default_hours"),
					intLit(40),
				},
			},
			wantSub: `COALESCE(m."preferred_hours", m."default_hours", 40)`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := TranspileExpr(ctx, tc.expr)
			if err != nil {
				t.Fatalf("TranspileExpr error: %v", err)
			}
			if !strings.Contains(got, tc.wantSub) {
				t.Errorf("got %q, want substring %q", got, tc.wantSub)
			}
		})
	}
}

func TestTranspileRelationGuardExpressions(t *testing.T) {
	d := Postgres()
	ctx := TranspileContext{
		Dialect:   d,
		FromAlias: "f",
		ToAlias:   "t",
	}

	expr := semantic.Expr{
		Kind: semantic.ExprEqual,
		Args: []semantic.Expr{
			fieldExpr("from.depot"),
			fieldExpr("to.origin_depot"),
		},
	}

	got, err := TranspileExpr(ctx, expr)
	if err != nil {
		t.Fatalf("TranspileExpr error: %v", err)
	}
	want := `(f."depot" = t."origin_depot")`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
