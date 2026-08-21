package sql

import (
	"fmt"
	"slices"
	"strings"

	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

// TranspiledStep contains the SQL fragments generated for one transformation step.
type TranspiledStep struct {
	RuleID       semantic.RuleID
	StepIndex    int
	Operator     semantic.OperatorKind
	TargetKind   string
	CTEs         []NamedCTE
	OutputTables map[string]string // entity/relation kind -> CTE/Table name
}

// NamedCTE is a single Common Table Expression with a name and SQL query body.
type NamedCTE struct {
	Name  string
	Query string
}

// TranspileSelectAssign generates CTEs for a SelectAndAssign transformation.
func TranspileSelectAssign(
	ctx TranspileContext,
	stepIndex int,
	ruleID semantic.RuleID,
	decl semantic.SelectAssignDeclaration,
	prevEntityTable string,
) (TranspiledStep, error) {
	d := ctx.Dialect
	stepName := fmt.Sprintf("step_%d_%s", stepIndex, sanitizeID(string(ruleID)))
	entityKind := string(decl.Selector.Kind)

	var ctes []NamedCTE

	// 1. Selection & filtering CTE
	selectCTEName := fmt.Sprintf("%s_selected", stepName)
	var whereClause string
	if decl.Selector.Where != nil {
		wCtx := ctx
		wCtx.EntityTableAlias = "m"
		wSql, err := TranspileExpr(wCtx, *decl.Selector.Where)
		if err != nil {
			return TranspiledStep{}, fmt.Errorf("transpile where in rule %s: %w", ruleID, err)
		}
		whereClause = fmt.Sprintf("WHERE (%s) AND m.\"is_active\" = TRUE", wSql)
	} else {
		whereClause = `WHERE m."is_active" = TRUE`
	}

	selectQuery := fmt.Sprintf("SELECT m.* FROM %s m %s", prevEntityTable, whereClause)
	ctes = append(ctes, NamedCTE{Name: selectCTEName, Query: selectQuery})

	// 2. Grouping & Reductions CTE with window functions
	qualifiedCTEName := fmt.Sprintf("%s_qualified", stepName)
	guardCtx := ctx
	guardCtx.EntityTableAlias = "s"
	guardCtx.IsGroupScope = true

	var groupKeySQL string
	if decl.Selector.GroupBy != nil {
		gCtx := ctx
		gCtx.EntityTableAlias = "s"
		gk, err := TranspileExpr(gCtx, *decl.Selector.GroupBy)
		if err != nil {
			return TranspiledStep{}, fmt.Errorf("transpile group by in rule %s: %w", ruleID, err)
		}
		groupKeySQL = gk
		guardCtx.GroupPartitionClause = fmt.Sprintf("PARTITION BY (%s)", gk)
	} else {
		groupKeySQL = "s.\"id\""
		guardCtx.GroupPartitionClause = "PARTITION BY (s.\"id\")"
	}

	guardExpr, err := TranspileExpr(guardCtx, decl.Guard)
	if err != nil {
		return TranspiledStep{}, fmt.Errorf("transpile guard in rule %s: %w", ruleID, err)
	}

	guardQuery := fmt.Sprintf(`SELECT s.*, 
    (%s) AS _ml_group_key,
    (%s) AS _ml_guard_passed
FROM %s s`, groupKeySQL, guardExpr, selectCTEName)
	ctes = append(ctes, NamedCTE{Name: qualifiedCTEName, Query: guardQuery})

	// 3. Project updated entity table via LEFT JOIN with prevEntityTable to preserve ALL schema columns
	outputCTEName := fmt.Sprintf("%s_output_%s", stepName, entityKind)

	// Determine fields to project
	var fields []string
	if ctx.EntityFields != nil && len(ctx.EntityFields[entityKind]) > 0 {
		fields = slices.Clone(ctx.EntityFields[entityKind])
	} else {
		// Discover from assignments
		for _, assign := range decl.Assignments {
			_, fieldName := splitFieldPath(assign.Target)
			if !slices.Contains(fields, fieldName) {
				fields = append(fields, fieldName)
			}
		}
	}
	slices.Sort(fields)

	assignedMap := make(map[string]semantic.Expr)
	for _, assign := range decl.Assignments {
		_, fieldName := splitFieldPath(assign.Target)
		assignedMap[fieldName] = assign.Value
	}

	var projectionSQL strings.Builder
	for _, f := range fields {
		col := d.QuoteIdentifier(f)
		if valExpr, ok := assignedMap[f]; ok {
			aCtx := ctx
			aCtx.EntityTableAlias = "q"
			if decl.Selector.GroupBy != nil {
				aCtx.GroupPartitionClause = `PARTITION BY (q."_ml_group_key")`
			} else {
				aCtx.GroupPartitionClause = `PARTITION BY (q."id")`
			}
			valSQL, err := TranspileExpr(aCtx, valExpr)
			if err != nil {
				return TranspiledStep{}, fmt.Errorf("transpile assignment %s in rule %s: %w", f, ruleID, err)
			}
			fmt.Fprintf(&projectionSQL, ",\n    CASE WHEN q.\"_ml_guard_passed\" = TRUE THEN (%s) ELSE m.%s END AS %s", valSQL, col, col)
		} else {
			fmt.Fprintf(&projectionSQL, ",\n    m.%s AS %s", col, col)
		}
	}

	outputQuery := fmt.Sprintf(`SELECT 
    m."id",
    m."lineage_id",
    m."is_active"%s,
    CASE WHEN q."_ml_guard_passed" = TRUE THEN %s ELSE m."updated_by_rule" END AS "updated_by_rule"
FROM %s m
LEFT JOIN %s q ON m."id" = q."id"`,
		projectionSQL.String(),
		d.QuoteString(string(ruleID)),
		prevEntityTable,
		qualifiedCTEName,
	)
	ctes = append(ctes, NamedCTE{Name: outputCTEName, Query: outputQuery})

	return TranspiledStep{
		RuleID:     ruleID,
		StepIndex:  stepIndex,
		Operator:   semantic.OperatorSelectAndAssign,
		TargetKind: entityKind,
		CTEs:       ctes,
		OutputTables: map[string]string{
			entityKind: outputCTEName,
		},
	}, nil
}

// TranspileInsertEntity generates CTEs for an InsertEntity transformation.
func TranspileInsertEntity(
	ctx TranspileContext,
	stepIndex int,
	ruleID semantic.RuleID,
	decl semantic.InsertEntityDeclaration,
	prevSourceTable string,
	prevTargetTable string,
) (TranspiledStep, error) {
	d := ctx.Dialect
	stepName := fmt.Sprintf("step_%d_%s", stepIndex, sanitizeID(string(ruleID)))
	targetKind := string(decl.TargetKind)

	var ctes []NamedCTE

	// 1. Selection & Guard CTE
	selectCTEName := fmt.Sprintf("%s_selected", stepName)
	var whereClause string
	if decl.Selector.Where != nil {
		wCtx := ctx
		wCtx.EntityTableAlias = "s"
		wSql, err := TranspileExpr(wCtx, *decl.Selector.Where)
		if err != nil {
			return TranspiledStep{}, err
		}
		whereClause = fmt.Sprintf("WHERE (%s) AND s.\"is_active\" = TRUE", wSql)
	} else {
		whereClause = `WHERE s."is_active" = TRUE`
	}

	guardCtx := ctx
	guardCtx.EntityTableAlias = "s"
	discCtx := ctx
	discCtx.EntityTableAlias = "s"

	var selectQuery string
	var newEntitiesAssignmentsSQL strings.Builder
	var progenitorExpr string

	if decl.Selector.GroupBy != nil {
		gCtx := ctx
		gCtx.EntityTableAlias = "s"
		gk, err := TranspileExpr(gCtx, *decl.Selector.GroupBy)
		if err != nil {
			return TranspiledStep{}, fmt.Errorf("transpile group by in rule %s: %w", ruleID, err)
		}
		partitionClause := fmt.Sprintf("PARTITION BY (%s)", gk)
		guardCtx.GroupPartitionClause = partitionClause
		guardCtx.IsGroupScope = true
		discCtx.GroupPartitionClause = partitionClause
		discCtx.IsGroupScope = true

		discExpr, err := TranspileExpr(discCtx, decl.Discriminator)
		if err != nil {
			return TranspiledStep{}, fmt.Errorf("transpile discriminator in rule %s: %w", ruleID, err)
		}

		guardExpr, err := TranspileExpr(guardCtx, decl.Guard)
		if err != nil {
			return TranspiledStep{}, fmt.Errorf("transpile guard in rule %s: %w", ruleID, err)
		}

		// Compute all windowed group assignments in the selection CTE so that aggregates
		// evaluate over all group members before row_num filtering.
		assignCtx := ctx
		assignCtx.EntityTableAlias = "s"
		assignCtx.IsGroupScope = true
		assignCtx.GroupPartitionClause = partitionClause

		var windowedAssignmentsSQL strings.Builder
		for _, assign := range decl.Assignments {
			_, fieldName := splitFieldPath(assign.Target)
			col := d.QuoteIdentifier(fieldName)
			valExpr, err := TranspileExpr(assignCtx, assign.Value)
			if err != nil {
				return TranspiledStep{}, fmt.Errorf("transpile assignment %s in rule %s: %w", assign.Target, ruleID, err)
			}
			assignCol := fmt.Sprintf("_ml_assign_%s", sanitizeID(fieldName))
			fmt.Fprintf(&windowedAssignmentsSQL, ",\n    (%s) AS %s", valExpr, d.QuoteIdentifier(assignCol))
			fmt.Fprintf(&newEntitiesAssignmentsSQL, ",\n    src.%s AS %s", d.QuoteIdentifier(assignCol), col)
		}

		selectQuery = fmt.Sprintf(`SELECT s.*, 
    (%s) AS _ml_group_key,
    (%s) AS _ml_discriminator,
    (%s) AS _ml_guard_passed,
    ROW_NUMBER() OVER (PARTITION BY (%s) ORDER BY s."id") AS _ml_row_num,
    COUNT(*) OVER (PARTITION BY (%s)) AS _ml_progenitor_count,
    STRING_AGG(SUBSTRING(s."id" FROM 8), '' ORDER BY s."id") OVER (PARTITION BY (%s)) AS _ml_progenitor_hex%s
FROM %s s %s`, gk, discExpr, guardExpr, gk, gk, gk, windowedAssignmentsSQL.String(), prevSourceTable, whereClause)

		progenitorExpr = `decode(LPAD(TO_HEX(src."_ml_progenitor_count"), 16, '0'), 'hex') || decode(src."_ml_progenitor_hex", 'hex')`

	} else {
		guardCtx.GroupPartitionClause = "PARTITION BY (s.\"id\")"
		discCtx.GroupPartitionClause = "PARTITION BY (s.\"id\")"

		discExpr, err := TranspileExpr(discCtx, decl.Discriminator)
		if err != nil {
			return TranspiledStep{}, fmt.Errorf("transpile discriminator in rule %s: %w", ruleID, err)
		}

		guardExpr, err := TranspileExpr(guardCtx, decl.Guard)
		if err != nil {
			return TranspiledStep{}, fmt.Errorf("transpile guard in rule %s: %w", ruleID, err)
		}

		selectQuery = fmt.Sprintf(`SELECT s.*, 
    (%s) AS _ml_discriminator,
    (%s) AS _ml_guard_passed
FROM %s s %s`, discExpr, guardExpr, prevSourceTable, whereClause)

		aCtx := ctx
		aCtx.EntityTableAlias = "src"
		for _, assign := range decl.Assignments {
			_, fieldName := splitFieldPath(assign.Target)
			col := d.QuoteIdentifier(fieldName)

			valExpr, err := TranspileExpr(aCtx, assign.Value)
			if err != nil {
				return TranspiledStep{}, fmt.Errorf("transpile assignment %s in rule %s: %w", assign.Target, ruleID, err)
			}
			fmt.Fprintf(&newEntitiesAssignmentsSQL, ",\n    (%s) AS %s", valExpr, col)
		}

		progenitorExpr = `'\x0000000000000001'::bytea || decode(SUBSTRING(src."id" FROM 8), 'hex')`
	}

	ctes = append(ctes, NamedCTE{Name: selectCTEName, Query: selectQuery})

	// 2. Synthesize new entities with canonical content-addressed identity
	newEntitiesCTEName := fmt.Sprintf("%s_new_entities", stepName)
	idHashExpr := d.SyntheticEntityID(targetKind, string(ruleID), `src."lineage_id"`, progenitorExpr, `src."_ml_discriminator"`)

	var whereFilter string
	if decl.Selector.GroupBy != nil {
		whereFilter = `WHERE src."_ml_guard_passed" = TRUE AND src."_ml_row_num" = 1`
	} else {
		whereFilter = `WHERE src."_ml_guard_passed" = TRUE`
	}

	newEntitiesQuery := fmt.Sprintf(`SELECT 
    %s AS "id",
    src."lineage_id" AS "lineage_id",
    TRUE AS "is_active"%s,
    %s AS "updated_by_rule"
FROM %s src
%s`, idHashExpr, newEntitiesAssignmentsSQL.String(), d.QuoteString(string(ruleID)), selectCTEName, whereFilter)
	ctes = append(ctes, NamedCTE{Name: newEntitiesCTEName, Query: newEntitiesQuery})

	// 3. Union with previous target entity table
	outputCTEName := fmt.Sprintf("%s_output_%s", stepName, targetKind)
	outputQuery := fmt.Sprintf(`SELECT t.* FROM %s t
UNION ALL
SELECT n.* FROM %s n`, prevTargetTable, newEntitiesCTEName)
	ctes = append(ctes, NamedCTE{Name: outputCTEName, Query: outputQuery})

	return TranspiledStep{
		RuleID:     ruleID,
		StepIndex:  stepIndex,
		Operator:   semantic.OperatorInsertEntity,
		TargetKind: targetKind,
		CTEs:       ctes,
		OutputTables: map[string]string{
			targetKind: outputCTEName,
		},
	}, nil
}

// TranspileDeleteEntity generates CTEs for a DeleteEntity transformation.
func TranspileDeleteEntity(
	ctx TranspileContext,
	stepIndex int,
	ruleID semantic.RuleID,
	decl semantic.DeleteEntityDeclaration,
	prevEntityTable string,
) (TranspiledStep, error) {
	d := ctx.Dialect
	stepName := fmt.Sprintf("step_%d_%s", stepIndex, sanitizeID(string(ruleID)))
	entityKind := string(decl.Selector.Kind)

	var ctes []NamedCTE

	// Selection & Guard CTE for rows to delete
	delCTEName := fmt.Sprintf("%s_to_delete", stepName)
	var whereClause string
	if decl.Selector.Where != nil {
		wCtx := ctx
		wCtx.EntityTableAlias = "m"
		wSql, err := TranspileExpr(wCtx, *decl.Selector.Where)
		if err != nil {
			return TranspiledStep{}, err
		}
		whereClause = fmt.Sprintf("WHERE (%s) AND m.\"is_active\" = TRUE", wSql)
	} else {
		whereClause = `WHERE m."is_active" = TRUE`
	}

	gCtx := ctx
	gCtx.EntityTableAlias = "m"
	guardExpr, err := TranspileExpr(gCtx, decl.Guard)
	if err != nil {
		return TranspiledStep{}, err
	}

	delQuery := fmt.Sprintf(`SELECT m."id" FROM %s m %s AND (%s) = TRUE`, prevEntityTable, whereClause, guardExpr)
	ctes = append(ctes, NamedCTE{Name: delCTEName, Query: delQuery})

	// Output table marking deleted entities inactive (or omitting them)
	outputCTEName := fmt.Sprintf("%s_output_%s", stepName, entityKind)
	outputQuery := fmt.Sprintf(`SELECT m.* FROM %s m WHERE m."id" NOT IN (SELECT d."id" FROM %s d)`, prevEntityTable, delCTEName)
	ctes = append(ctes, NamedCTE{Name: outputCTEName, Query: outputQuery})

	_ = d
	return TranspiledStep{
		RuleID:     ruleID,
		StepIndex:  stepIndex,
		Operator:   semantic.OperatorDeleteEntity,
		TargetKind: entityKind,
		CTEs:       ctes,
		OutputTables: map[string]string{
			entityKind: outputCTEName,
		},
	}, nil
}

// TranspileRelateEntities generates CTEs for a RelateEntities transformation.
func TranspileRelateEntities(
	ctx TranspileContext,
	stepIndex int,
	ruleID semantic.RuleID,
	decl semantic.RelateEntitiesDeclaration,
	prevFromTable string,
	prevToTable string,
	prevRelationsTable string,
) (TranspiledStep, error) {
	d := ctx.Dialect
	stepName := fmt.Sprintf("step_%d_%s", stepIndex, sanitizeID(string(ruleID)))
	relKind := string(decl.RelationKind)
	fromKind := string(decl.FromSelector.Kind)
	toKind := string(decl.ToSelector.Kind)

	var ctes []NamedCTE

	// 1. Candidate Cartesian product with FromSelector and ToSelector filters
	candCTEName := fmt.Sprintf("%s_candidates", stepName)
	var fromWhere, toWhere string
	if decl.FromSelector.Where != nil {
		fCtx := ctx
		fCtx.EntityTableAlias = "f"
		fCtx.FromAlias = "f"
		fCtx.FromKind = fromKind
		fSql, err := TranspileExpr(fCtx, *decl.FromSelector.Where)
		if err != nil {
			return TranspiledStep{}, err
		}
		fromWhere = fmt.Sprintf("AND (%s)", fSql)
	}
	if decl.ToSelector.Where != nil {
		tCtx := ctx
		tCtx.EntityTableAlias = "t"
		tCtx.ToAlias = "t"
		tCtx.ToKind = toKind
		tSql, err := TranspileExpr(tCtx, *decl.ToSelector.Where)
		if err != nil {
			return TranspiledStep{}, err
		}
		toWhere = fmt.Sprintf("AND (%s)", tSql)
	}

	guardCtx := ctx
	guardCtx.FromAlias = "f"
	guardCtx.ToAlias = "t"
	guardCtx.FromKind = fromKind
	guardCtx.ToKind = toKind
	guardExpr, err := TranspileExpr(guardCtx, decl.Guard)
	if err != nil {
		return TranspiledStep{}, fmt.Errorf("transpile relation guard in rule %s: %w", ruleID, err)
	}

	candQuery := fmt.Sprintf(`SELECT 
    %s AS "kind",
    %s AS "from_kind",
    f."id" AS "from_id",
    %s AS "to_kind",
    t."id" AS "to_id",
    TRUE AS "is_active",
    %s AS "updated_by_rule"
FROM %s f
CROSS JOIN %s t
WHERE f."is_active" = TRUE %s
  AND t."is_active" = TRUE %s
  AND (%s) = TRUE`,
		d.QuoteString(relKind),
		d.QuoteString(fromKind),
		d.QuoteString(toKind),
		d.QuoteString(string(ruleID)),
		prevFromTable,
		prevToTable,
		fromWhere,
		toWhere,
		guardExpr,
	)
	ctes = append(ctes, NamedCTE{Name: candCTEName, Query: candQuery})

	// 2. Output relations table
	outputCTEName := fmt.Sprintf("%s_output_relations", stepName)
	outputQuery := fmt.Sprintf(`SELECT r.* FROM %s r
UNION ALL
SELECT c.* FROM %s c`, prevRelationsTable, candCTEName)
	ctes = append(ctes, NamedCTE{Name: outputCTEName, Query: outputQuery})

	return TranspiledStep{
		RuleID:     ruleID,
		StepIndex:  stepIndex,
		Operator:   semantic.OperatorRelateEntities,
		TargetKind: "relations",
		CTEs:       ctes,
		OutputTables: map[string]string{
			"relations": outputCTEName,
		},
	}, nil
}

// TranspileUnrelateEntities generates CTEs for an UnrelateEntities transformation.
func TranspileUnrelateEntities(
	ctx TranspileContext,
	stepIndex int,
	ruleID semantic.RuleID,
	decl semantic.UnrelateEntitiesDeclaration,
	prevFromTable string,
	prevToTable string,
	prevRelationsTable string,
) (TranspiledStep, error) {
	d := ctx.Dialect
	stepName := fmt.Sprintf("step_%d_%s", stepIndex, sanitizeID(string(ruleID)))
	relKind := string(decl.RelationKind)
	fromKind := string(decl.FromSelector.Kind)
	toKind := string(decl.ToSelector.Kind)

	var ctes []NamedCTE

	// 1. Identify relations to remove
	unrelCTEName := fmt.Sprintf("%s_to_unrelate", stepName)
	guardCtx := ctx
	guardCtx.FromAlias = "f"
	guardCtx.ToAlias = "t"
	guardCtx.FromKind = fromKind
	guardCtx.ToKind = toKind
	guardExpr, err := TranspileExpr(guardCtx, decl.Guard)
	if err != nil {
		return TranspiledStep{}, fmt.Errorf("transpile unrelate guard in rule %s: %w", ruleID, err)
	}

	unrelQuery := fmt.Sprintf(`SELECT r."from_id", r."to_id"
FROM %s r
JOIN %s f ON r."from_id" = f."id" AND r."from_kind" = %s
JOIN %s t ON r."to_id" = t."id" AND r."to_kind" = %s
WHERE r."kind" = %s
  AND r."is_active" = TRUE
  AND (%s) = TRUE`,
		prevRelationsTable,
		prevFromTable, d.QuoteString(fromKind),
		prevToTable, d.QuoteString(toKind),
		d.QuoteString(relKind),
		guardExpr,
	)
	ctes = append(ctes, NamedCTE{Name: unrelCTEName, Query: unrelQuery})

	// 2. Output relations excluding removed pairs
	outputCTEName := fmt.Sprintf("%s_output_relations", stepName)
	outputQuery := fmt.Sprintf(`SELECT r.* FROM %s r
WHERE NOT EXISTS (
    SELECT 1 FROM %s u
    WHERE r."from_id" = u."from_id" AND r."to_id" = u."to_id" AND r."kind" = %s
)`, prevRelationsTable, unrelCTEName, d.QuoteString(relKind))
	ctes = append(ctes, NamedCTE{Name: outputCTEName, Query: outputQuery})

	return TranspiledStep{
		RuleID:     ruleID,
		StepIndex:  stepIndex,
		Operator:   semantic.OperatorUnrelateEntities,
		TargetKind: "relations",
		CTEs:       ctes,
		OutputTables: map[string]string{
			"relations": outputCTEName,
		},
	}, nil
}

// TranspileMergeEntities generates CTEs for a MergeEntities transformation.
func TranspileMergeEntities(
	ctx TranspileContext,
	stepIndex int,
	ruleID semantic.RuleID,
	decl semantic.MergeEntitiesDeclaration,
	prevSourceTable string,
	prevTargetTable string,
	prevRelationsTable string,
) (TranspiledStep, error) {
	d := ctx.Dialect
	stepName := fmt.Sprintf("step_%d_%s", stepIndex, sanitizeID(string(ruleID)))
	sourceKind := string(decl.Selector.Kind)
	targetKind := string(decl.TargetKind)

	var ctes []NamedCTE

	// 1. Group & select source entities
	selectCTEName := fmt.Sprintf("%s_selected", stepName)
	var whereClause string
	if decl.Selector.Where != nil {
		wCtx := ctx
		wCtx.EntityTableAlias = "s"
		wSql, err := TranspileExpr(wCtx, *decl.Selector.Where)
		if err != nil {
			return TranspiledStep{}, err
		}
		whereClause = fmt.Sprintf("WHERE (%s) AND s.\"is_active\" = TRUE", wSql)
	} else {
		whereClause = `WHERE s."is_active" = TRUE`
	}

	gCtx := ctx
	gCtx.EntityTableAlias = "s"
	var groupKeyExpr string
	if decl.Selector.GroupBy != nil {
		gk, err := TranspileExpr(gCtx, *decl.Selector.GroupBy)
		if err != nil {
			return TranspiledStep{}, err
		}
		groupKeyExpr = gk
	} else {
		groupKeyExpr = "s.\"id\""
	}

	guardCtx := ctx
	guardCtx.EntityTableAlias = "s"
	guardCtx.IsGroupScope = true
	guardCtx.GroupPartitionClause = fmt.Sprintf("PARTITION BY (%s)", groupKeyExpr)
	guardExpr, err := TranspileExpr(guardCtx, decl.Guard)
	if err != nil {
		return TranspiledStep{}, err
	}

	discCtx := ctx
	discCtx.EntityTableAlias = "s"
	discExpr, err := TranspileExpr(discCtx, decl.Discriminator)
	if err != nil {
		return TranspiledStep{}, err
	}

	selectQuery := fmt.Sprintf(`SELECT s.*, 
    (%s) AS _ml_group_key,
    (%s) AS _ml_discriminator,
    (%s) AS _ml_guard_passed
FROM %s s %s`, groupKeyExpr, discExpr, guardExpr, prevSourceTable, whereClause)
	ctes = append(ctes, NamedCTE{Name: selectCTEName, Query: selectQuery})

	// 2. Synthesize merged entities (grouped by _ml_group_key)
	mergedCTEName := fmt.Sprintf("%s_merged_entities", stepName)
	var assignmentsSQL strings.Builder
	for _, assign := range decl.Assignments {
		_, fieldName := splitFieldPath(assign.Target)
		col := d.QuoteIdentifier(fieldName)

		aCtx := ctx
		aCtx.EntityTableAlias = "grp"
		aCtx.IsGroupScope = true
		aCtx.IsAggregateGroupBy = true
		valExpr, err := TranspileExpr(aCtx, assign.Value)
		if err != nil {
			return TranspiledStep{}, err
		}
		fmt.Fprintf(&assignmentsSQL, ",\n    (%s) AS %s", valExpr, col)
	}

	progenitorExpr := `decode(LPAD(TO_HEX(COUNT(*)), 16, '0'), 'hex') || decode(STRING_AGG(SUBSTRING(grp."id" FROM 8), '' ORDER BY grp."id"), 'hex')`
	idHashExpr := d.SyntheticEntityID(targetKind, string(ruleID), `MAX(grp."lineage_id")`, progenitorExpr, `MAX(grp."_ml_discriminator"::text)`)

	mergedQuery := fmt.Sprintf(`SELECT 
    %s AS "id",
    MAX(grp."lineage_id") AS "lineage_id",
    TRUE AS "is_active"%s,
    %s AS "updated_by_rule"
FROM %s grp
WHERE grp."_ml_guard_passed" = TRUE
GROUP BY grp."_ml_group_key"`, idHashExpr, assignmentsSQL.String(), d.QuoteString(string(ruleID)), selectCTEName)
	ctes = append(ctes, NamedCTE{Name: mergedCTEName, Query: mergedQuery})

	// 3. Target entity output
	outTargetCTEName := fmt.Sprintf("%s_output_%s", stepName, targetKind)
	outputTables := make(map[string]string)

	if sourceKind == targetKind {
		if !decl.RetainSources {
			outTargetQuery := fmt.Sprintf(`SELECT t.* FROM %s t
WHERE t."id" NOT IN (SELECT sel."id" FROM %s sel WHERE sel."_ml_guard_passed" = TRUE)
UNION ALL
SELECT m.* FROM %s m`, prevSourceTable, selectCTEName, mergedCTEName)
			ctes = append(ctes, NamedCTE{Name: outTargetCTEName, Query: outTargetQuery})
		} else {
			outTargetQuery := fmt.Sprintf(`SELECT t.* FROM %s t
UNION ALL
SELECT m.* FROM %s m`, prevTargetTable, mergedCTEName)
			ctes = append(ctes, NamedCTE{Name: outTargetCTEName, Query: outTargetQuery})
		}
		outputTables[targetKind] = outTargetCTEName
	} else {
		outTargetQuery := fmt.Sprintf(`SELECT t.* FROM %s t
UNION ALL
SELECT m.* FROM %s m`, prevTargetTable, mergedCTEName)
		ctes = append(ctes, NamedCTE{Name: outTargetCTEName, Query: outTargetQuery})
		outputTables[targetKind] = outTargetCTEName

		if !decl.RetainSources {
			outSourceCTEName := fmt.Sprintf("%s_output_%s", stepName, sourceKind)
			outSourceQuery := fmt.Sprintf(`SELECT s.* FROM %s s
WHERE s."id" NOT IN (SELECT sel."id" FROM %s sel WHERE sel."_ml_guard_passed" = TRUE)`, prevSourceTable, selectCTEName)
			ctes = append(ctes, NamedCTE{Name: outSourceCTEName, Query: outSourceQuery})
			outputTables[sourceKind] = outSourceCTEName
		}
	}

	return TranspiledStep{
		RuleID:       ruleID,
		StepIndex:    stepIndex,
		Operator:     semantic.OperatorMergeEntities,
		TargetKind:   targetKind,
		CTEs:         ctes,
		OutputTables: outputTables,
	}, nil
}

// TranspileSplitEntity generates CTEs for a SplitEntity transformation.
func TranspileSplitEntity(
	ctx TranspileContext,
	stepIndex int,
	ruleID semantic.RuleID,
	decl semantic.SplitEntityDeclaration,
	prevSourceTable string,
	prevTargetTable string,
	prevRelationsTable string,
) (TranspiledStep, error) {
	d := ctx.Dialect
	stepName := fmt.Sprintf("step_%d_%s", stepIndex, sanitizeID(string(ruleID)))
	sourceKind := string(decl.Selector.Kind)
	targetKind := string(decl.TargetKind)

	var ctes []NamedCTE

	// 1. Select source entities
	selectCTEName := fmt.Sprintf("%s_selected", stepName)
	var whereClause string
	if decl.Selector.Where != nil {
		wCtx := ctx
		wCtx.EntityTableAlias = "s"
		wSql, err := TranspileExpr(wCtx, *decl.Selector.Where)
		if err != nil {
			return TranspiledStep{}, err
		}
		whereClause = fmt.Sprintf("WHERE (%s) AND s.\"is_active\" = TRUE", wSql)
	} else {
		whereClause = `WHERE s."is_active" = TRUE`
	}

	guardCtx := ctx
	guardCtx.EntityTableAlias = "s"
	guardExpr, err := TranspileExpr(guardCtx, decl.Guard)
	if err != nil {
		return TranspiledStep{}, err
	}

	selectQuery := fmt.Sprintf(`SELECT s.*, (%s) AS _ml_guard_passed
FROM %s s %s`, guardExpr, prevSourceTable, whereClause)
	ctes = append(ctes, NamedCTE{Name: selectCTEName, Query: selectQuery})

	// 2. Synthesize child entities for each partition
	var partitionUnionQueries []string
	for pIdx, part := range decl.Partitions {
		discCtx := ctx
		discCtx.EntityTableAlias = "src"
		discExpr, err := TranspileExpr(discCtx, part.Discriminator)
		if err != nil {
			return TranspiledStep{}, err
		}

		var assignmentsSQL strings.Builder
		for _, assign := range part.Assignments {
			_, fieldName := splitFieldPath(assign.Target)
			col := d.QuoteIdentifier(fieldName)

			aCtx := ctx
			aCtx.EntityTableAlias = "src"
			valExpr, err := TranspileExpr(aCtx, assign.Value)
			if err != nil {
				return TranspiledStep{}, err
			}
			fmt.Fprintf(&assignmentsSQL, ",\n    (%s) AS %s", valExpr, col)
		}

		progenitorExpr := `'\x0000000000000001'::bytea || decode(SUBSTRING(src."id" FROM 8), 'hex')`
		idHashExpr := d.SyntheticEntityID(targetKind, string(ruleID), `src."lineage_id"`, progenitorExpr, discExpr)

		pQuery := fmt.Sprintf(`SELECT 
    %s AS "id",
    src."lineage_id" AS "lineage_id",
    TRUE AS "is_active"%s,
    %s AS "updated_by_rule"
FROM %s src
WHERE src."_ml_guard_passed" = TRUE`, idHashExpr, assignmentsSQL.String(), d.QuoteString(string(ruleID)), selectCTEName)
		partitionUnionQueries = append(partitionUnionQueries, pQuery)
		_ = pIdx
	}

	splitChildrenCTEName := fmt.Sprintf("%s_split_children", stepName)
	splitChildrenQuery := strings.Join(partitionUnionQueries, "\nUNION ALL\n")
	ctes = append(ctes, NamedCTE{Name: splitChildrenCTEName, Query: splitChildrenQuery})

	// 3. Target entity output
	outTargetCTEName := fmt.Sprintf("%s_output_%s", stepName, targetKind)
	outputTables := make(map[string]string)

	if sourceKind == targetKind {
		if !decl.RetainSource {
			outTargetQuery := fmt.Sprintf(`SELECT t.* FROM %s t
WHERE t."id" NOT IN (SELECT sel."id" FROM %s sel WHERE sel."_ml_guard_passed" = TRUE)
UNION ALL
SELECT c.* FROM %s c`, prevSourceTable, selectCTEName, splitChildrenCTEName)
			ctes = append(ctes, NamedCTE{Name: outTargetCTEName, Query: outTargetQuery})
		} else {
			outTargetQuery := fmt.Sprintf(`SELECT t.* FROM %s t
UNION ALL
SELECT c.* FROM %s c`, prevTargetTable, splitChildrenCTEName)
			ctes = append(ctes, NamedCTE{Name: outTargetCTEName, Query: outTargetQuery})
		}
		outputTables[targetKind] = outTargetCTEName
	} else {
		outTargetQuery := fmt.Sprintf(`SELECT t.* FROM %s t
UNION ALL
SELECT m.* FROM %s m`, prevTargetTable, splitChildrenCTEName)
		ctes = append(ctes, NamedCTE{Name: outTargetCTEName, Query: outTargetQuery})
		outputTables[targetKind] = outTargetCTEName

		if !decl.RetainSource {
			outSourceCTEName := fmt.Sprintf("%s_output_%s", stepName, sourceKind)
			outSourceQuery := fmt.Sprintf(`SELECT s.* FROM %s s
WHERE s."id" NOT IN (SELECT sel."id" FROM %s sel WHERE sel."_ml_guard_passed" = TRUE)`, prevSourceTable, selectCTEName)
			ctes = append(ctes, NamedCTE{Name: outSourceCTEName, Query: outSourceQuery})
			outputTables[sourceKind] = outSourceCTEName
		}
	}

	return TranspiledStep{
		RuleID:       ruleID,
		StepIndex:    stepIndex,
		Operator:     semantic.OperatorSplitEntity,
		TargetKind:   targetKind,
		CTEs:         ctes,
		OutputTables: outputTables,
	}, nil
}

func sanitizeID(id string) string {
	var sb strings.Builder
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			sb.WriteRune(r)
		} else {
			sb.WriteRune('_')
		}
	}
	return sb.String()
}
