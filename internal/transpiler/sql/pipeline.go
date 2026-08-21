package sql

import (
	"fmt"
	"strings"

	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

// PipelineOptions configures SQL pipeline generation.
type PipelineOptions struct {
	Dialect Dialect
}

// TranspiledPipeline is the output of compiling a Plan to SQL.
type TranspiledPipeline struct {
	PlanID           semantic.PlanID
	SQL              string
	FinalEntityViews map[string]string
	CheckpointViews  map[string]map[string]string // checkpointKey -> (entityKind -> view/CTE name)
}

// TranspilePlan compiles a complete semantic.Plan into a composable SQL CTE pipeline.
func TranspilePlan(plan semantic.Plan, opts PipelineOptions) (TranspiledPipeline, error) {
	d := opts.Dialect
	if d == nil {
		d = Postgres()
	}

	ctx := TranspileContext{
		Dialect: d,
	}

	var allCTEs []NamedCTE
	currentTables := make(map[string]string) // entity/relation kind -> current CTE name

	// 1. Initial State Staging CTEs
	currentTables["relations"] = "stg_relations"
	allCTEs = append(allCTEs, NamedCTE{
		Name:  "stg_relations",
		Query: `SELECT * FROM "raw_relations"`,
	})

	transformations := plan.Transformations()
	checkpointViews := make(map[string]map[string]string)

	// Collect all entity kinds present across transformations
	seenKinds := make(map[string]bool)
	for _, tx := range transformations {
		decl := tx.Declaration()
		if decl.SelectAssign != nil {
			seenKinds[string(decl.SelectAssign.Selector.Kind)] = true
		}
		if decl.InsertEntity != nil {
			seenKinds[string(decl.InsertEntity.TargetKind)] = true
			if decl.InsertEntity.Selector.Kind != "" {
				seenKinds[string(decl.InsertEntity.Selector.Kind)] = true
			}
		}
		if decl.DeleteEntity != nil {
			seenKinds[string(decl.DeleteEntity.Selector.Kind)] = true
		}
		if decl.RelateEntities != nil {
			seenKinds[string(decl.RelateEntities.FromSelector.Kind)] = true
			seenKinds[string(decl.RelateEntities.ToSelector.Kind)] = true
		}
		if decl.UnrelateEntities != nil {
			seenKinds[string(decl.UnrelateEntities.FromSelector.Kind)] = true
			seenKinds[string(decl.UnrelateEntities.ToSelector.Kind)] = true
		}
		if decl.MergeEntities != nil {
			seenKinds[string(decl.MergeEntities.TargetKind)] = true
			if decl.MergeEntities.Selector.Kind != "" {
				seenKinds[string(decl.MergeEntities.Selector.Kind)] = true
			}
		}
		if decl.SplitEntity != nil {
			seenKinds[string(decl.SplitEntity.TargetKind)] = true
			if decl.SplitEntity.Selector.Kind != "" {
				seenKinds[string(decl.SplitEntity.Selector.Kind)] = true
			}
		}
	}

	for kind := range seenKinds {
		stgName := fmt.Sprintf("stg_entities_%s", kind)
		currentTables[kind] = stgName
		allCTEs = append(allCTEs, NamedCTE{
			Name:  stgName,
			Query: fmt.Sprintf(`SELECT * FROM "raw_entities_%s"`, kind),
		})
	}

	// 2. Transpile each transformation step in topological order
	for i, tx := range transformations {
		decl := tx.Declaration()
		ruleID := decl.ID

		var step TranspiledStep
		var err error

		switch decl.Operator {
		case semantic.OperatorSelectAndAssign:
			kind := string(decl.SelectAssign.Selector.Kind)
			prev := currentTables[kind]
			step, err = TranspileSelectAssign(ctx, i, ruleID, *decl.SelectAssign, prev)

		case semantic.OperatorInsertEntity:
			srcKind := string(decl.InsertEntity.Selector.Kind)
			targetKind := string(decl.InsertEntity.TargetKind)
			prevSrc := currentTables[srcKind]
			prevTgt := currentTables[targetKind]
			if prevTgt == "" {
				prevTgt = fmt.Sprintf("stg_entities_%s", targetKind)
			}
			step, err = TranspileInsertEntity(ctx, i, ruleID, *decl.InsertEntity, prevSrc, prevTgt)

		case semantic.OperatorDeleteEntity:
			kind := string(decl.DeleteEntity.Selector.Kind)
			prev := currentTables[kind]
			step, err = TranspileDeleteEntity(ctx, i, ruleID, *decl.DeleteEntity, prev)

		case semantic.OperatorRelateEntities:
			fromKind := string(decl.RelateEntities.FromSelector.Kind)
			toKind := string(decl.RelateEntities.ToSelector.Kind)
			step, err = TranspileRelateEntities(
				ctx, i, ruleID, *decl.RelateEntities,
				currentTables[fromKind],
				currentTables[toKind],
				currentTables["relations"],
			)

		case semantic.OperatorUnrelateEntities:
			fromKind := string(decl.UnrelateEntities.FromSelector.Kind)
			toKind := string(decl.UnrelateEntities.ToSelector.Kind)
			step, err = TranspileUnrelateEntities(
				ctx, i, ruleID, *decl.UnrelateEntities,
				currentTables[fromKind],
				currentTables[toKind],
				currentTables["relations"],
			)

		case semantic.OperatorMergeEntities:
			srcKind := string(decl.MergeEntities.Selector.Kind)
			targetKind := string(decl.MergeEntities.TargetKind)
			prevSrc := currentTables[srcKind]
			prevTgt := currentTables[targetKind]
			if prevTgt == "" {
				prevTgt = fmt.Sprintf("stg_entities_%s", targetKind)
			}
			step, err = TranspileMergeEntities(
				ctx, i, ruleID, *decl.MergeEntities,
				prevSrc,
				prevTgt,
				currentTables["relations"],
			)

		case semantic.OperatorSplitEntity:
			srcKind := string(decl.SplitEntity.Selector.Kind)
			targetKind := string(decl.SplitEntity.TargetKind)
			prevSrc := currentTables[srcKind]
			prevTgt := currentTables[targetKind]
			if prevTgt == "" {
				prevTgt = fmt.Sprintf("stg_entities_%s", targetKind)
			}
			step, err = TranspileSplitEntity(
				ctx, i, ruleID, *decl.SplitEntity,
				prevSrc,
				prevTgt,
				currentTables["relations"],
			)

		default:
			return TranspiledPipeline{}, fmt.Errorf("unsupported operator %v in rule %s", decl.Operator, ruleID)
		}

		if err != nil {
			return TranspiledPipeline{}, err
		}

		// Append step CTEs
		allCTEs = append(allCTEs, step.CTEs...)

		// Update active current tables
		for k, tbl := range step.OutputTables {
			currentTables[k] = tbl
		}

		// Check if any declared checkpoints coincide with this step
		for _, cp := range plan.Checkpoints() {
			if cp.After == ruleID {
				cpKey := string(cp.Key)
				if checkpointViews[cpKey] == nil {
					checkpointViews[cpKey] = make(map[string]string)
				}
				for k, tbl := range currentTables {
					checkpointViews[cpKey][k] = tbl
				}
			}
		}
	}

	// 3. Assemble final SQL string with WITH clause
	var sqlBuilder strings.Builder
	sqlBuilder.WriteString("WITH\n")
	for idx, cte := range allCTEs {
		sqlBuilder.WriteString(cte.Name)
		sqlBuilder.WriteString(" AS (\n")
		// Indent CTE body
		lines := strings.Split(cte.Query, "\n")
		for _, line := range lines {
			sqlBuilder.WriteString("    ")
			sqlBuilder.WriteString(line)
			sqlBuilder.WriteString("\n")
		}
		if idx < len(allCTEs)-1 {
			sqlBuilder.WriteString("),\n")
		} else {
			sqlBuilder.WriteString(")\n")
		}
	}

	// Default final projection query
	sqlBuilder.WriteString("SELECT 'COMPLETED' AS execution_status;\n")

	return TranspiledPipeline{
		PlanID:           plan.ID(),
		SQL:              sqlBuilder.String(),
		FinalEntityViews: currentTables,
		CheckpointViews:  checkpointViews,
	}, nil
}
