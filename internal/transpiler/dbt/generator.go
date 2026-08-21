// Package dbt generates clean, declarative, inspectable dbt project artifacts
// from canonical Maiden Lane transformation plans.
//
// In accordance with AGENTS.md §3.1 and Inviolate 1, dbt models are execution representations
// of Maiden Lane's closed semantic model, consuming the canonical Plan and preserving
// exact lineage, dependency order, and checkpoint boundaries.
package dbt

import (
	"fmt"
	"slices"
	"strings"

	"github.com/optimaldynamics/maiden-lane/internal/semantic"
	"github.com/optimaldynamics/maiden-lane/internal/transpiler/sql"
)

// ProjectFile represents one generated file within a dbt project.
type ProjectFile struct {
	Path    string // Relative path within the dbt project, e.g. "models/staging/stg_driver.sql"
	Content string
}

// Project represents a complete generated dbt project file bundle.
type Project struct {
	Name             string
	Files            []ProjectFile
	CheckpointModels map[string]string // checkpointKey -> modelName
}

// Options configures dbt project generation.
type Options struct {
	ProjectName string
	ProfileName string
	Dialect     sql.Dialect
	Schema      *semantic.Schema
}

// GenerateProject generates a complete, deterministic dbt project bundle from a compiled Plan.
func GenerateProject(plan semantic.Plan, opts Options) (Project, error) {
	if opts.ProjectName == "" {
		opts.ProjectName = "maiden_lane_pipeline"
	}
	if opts.ProfileName == "" {
		opts.ProfileName = "default"
	}
	if opts.Dialect == nil {
		opts.Dialect = sql.Postgres()
	}

	var files []ProjectFile

	// 1. dbt_project.yml
	dbtProjectYML := fmt.Sprintf(`name: '%s'
version: '1.0.0'
config-version: 2
profile: '%s'

model-paths: ["models"]
macro-paths: ["macros"]

models:
  %s:
    staging:
      +materialized: view
    transformations:
      +materialized: table
    checkpoints:
      +materialized: view
`, opts.ProjectName, opts.ProfileName, opts.ProjectName)

	files = append(files, ProjectFile{
		Path:    "dbt_project.yml",
		Content: dbtProjectYML,
	})

	// 2. Build entity fields map from schema or plan
	entityFields := make(map[string][]string)
	if opts.Schema != nil {
		for _, ed := range opts.Schema.Declaration().EntityDeclarations() {
			k := string(ed.Kind)
			for _, fd := range ed.Fields {
				entityFields[k] = append(entityFields[k], string(fd.Name))
			}
		}
	}

	transformations := plan.Transformations()
	for _, tx := range transformations {
		for _, read := range tx.ReadSet() {
			k, f := splitFieldPath(read)
			if k != "" && f != "" && !slices.Contains(entityFields[k], f) {
				entityFields[k] = append(entityFields[k], f)
			}
		}
		for _, write := range tx.WriteSet() {
			k, f := splitFieldPath(write)
			if k != "" && f != "" && !slices.Contains(entityFields[k], f) {
				entityFields[k] = append(entityFields[k], f)
			}
		}
	}

	// 3. Discover all entity kinds across transformations
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

	currentModels := make(map[string]string) // entity/relation kind -> current model name

	// Staging Models
	for kind := range seenKinds {
		modelName := fmt.Sprintf("stg_entities_%s", kind)
		currentModels[kind] = modelName
		stgSQL := fmt.Sprintf(`{{ config(materialized='view') }}

SELECT * FROM {{ source('maiden_lane', 'raw_entities_%s') }}
`, kind)
		files = append(files, ProjectFile{
			Path:    fmt.Sprintf("models/staging/%s.sql", modelName),
			Content: stgSQL,
		})
	}

	// Staging for relations
	currentModels["relations"] = "stg_relations"
	files = append(files, ProjectFile{
		Path: "models/staging/stg_relations.sql",
		Content: `{{ config(materialized='view') }}

SELECT * FROM {{ source('maiden_lane', 'raw_relations') }}
`,
	})

	// 4. Transformation Models
	ctx := sql.TranspileContext{
		Dialect:      opts.Dialect,
		EntityFields: entityFields,
	}

	checkpointModels := make(map[string]string)

	for i, tx := range transformations {
		decl := tx.Declaration()
		ruleID := decl.ID
		sanitizedRule := sanitizeID(string(ruleID))

		var step sql.TranspiledStep
		var err error

		switch decl.Operator {
		case semantic.OperatorSelectAndAssign:
			kind := string(decl.SelectAssign.Selector.Kind)
			prev := currentModels[kind]
			step, err = sql.TranspileSelectAssign(ctx, i, ruleID, *decl.SelectAssign, fmt.Sprintf("{{ ref('%s') }}", prev))

		case semantic.OperatorInsertEntity:
			srcKind := string(decl.InsertEntity.Selector.Kind)
			targetKind := string(decl.InsertEntity.TargetKind)
			prevSrc := currentModels[srcKind]
			prevTgt := currentModels[targetKind]
			if prevTgt == "" {
				prevTgt = fmt.Sprintf("stg_entities_%s", targetKind)
			}
			step, err = sql.TranspileInsertEntity(
				ctx, i, ruleID, *decl.InsertEntity,
				fmt.Sprintf("{{ ref('%s') }}", prevSrc),
				fmt.Sprintf("{{ ref('%s') }}", prevTgt),
			)

		case semantic.OperatorDeleteEntity:
			kind := string(decl.DeleteEntity.Selector.Kind)
			prev := currentModels[kind]
			step, err = sql.TranspileDeleteEntity(ctx, i, ruleID, *decl.DeleteEntity, fmt.Sprintf("{{ ref('%s') }}", prev))

		case semantic.OperatorRelateEntities:
			fromKind := string(decl.RelateEntities.FromSelector.Kind)
			toKind := string(decl.RelateEntities.ToSelector.Kind)
			step, err = sql.TranspileRelateEntities(
				ctx, i, ruleID, *decl.RelateEntities,
				fmt.Sprintf("{{ ref('%s') }}", currentModels[fromKind]),
				fmt.Sprintf("{{ ref('%s') }}", currentModels[toKind]),
				fmt.Sprintf("{{ ref('%s') }}", currentModels["relations"]),
			)

		case semantic.OperatorUnrelateEntities:
			fromKind := string(decl.UnrelateEntities.FromSelector.Kind)
			toKind := string(decl.UnrelateEntities.ToSelector.Kind)
			step, err = sql.TranspileUnrelateEntities(
				ctx, i, ruleID, *decl.UnrelateEntities,
				fmt.Sprintf("{{ ref('%s') }}", currentModels[fromKind]),
				fmt.Sprintf("{{ ref('%s') }}", currentModels[toKind]),
				fmt.Sprintf("{{ ref('%s') }}", currentModels["relations"]),
			)

		case semantic.OperatorMergeEntities:
			srcKind := string(decl.MergeEntities.Selector.Kind)
			targetKind := string(decl.MergeEntities.TargetKind)
			prevSrc := currentModels[srcKind]
			prevTgt := currentModels[targetKind]
			if prevTgt == "" {
				prevTgt = fmt.Sprintf("stg_entities_%s", targetKind)
			}
			step, err = sql.TranspileMergeEntities(
				ctx, i, ruleID, *decl.MergeEntities,
				fmt.Sprintf("{{ ref('%s') }}", prevSrc),
				fmt.Sprintf("{{ ref('%s') }}", prevTgt),
				fmt.Sprintf("{{ ref('%s') }}", currentModels["relations"]),
			)

		case semantic.OperatorSplitEntity:
			srcKind := string(decl.SplitEntity.Selector.Kind)
			targetKind := string(decl.SplitEntity.TargetKind)
			prevSrc := currentModels[srcKind]
			prevTgt := currentModels[targetKind]
			if prevTgt == "" {
				prevTgt = fmt.Sprintf("stg_entities_%s", targetKind)
			}
			step, err = sql.TranspileSplitEntity(
				ctx, i, ruleID, *decl.SplitEntity,
				fmt.Sprintf("{{ ref('%s') }}", prevSrc),
				fmt.Sprintf("{{ ref('%s') }}", prevTgt),
				fmt.Sprintf("{{ ref('%s') }}", currentModels["relations"]),
			)

		default:
			return Project{}, fmt.Errorf("unsupported operator %v in rule %s", decl.Operator, ruleID)
		}

		if err != nil {
			return Project{}, err
		}

		// Generate a distinct dbt model for EACH modified table in OutputTables
		for tableKind, outCTE := range step.OutputTables {
			modelName := fmt.Sprintf("tx_%02d_%s_%s", i, sanitizedRule, tableKind)
			var modelSQL strings.Builder
			modelSQL.WriteString("{{ config(materialized='table') }}\n\nWITH\n")
			for idx, cte := range step.CTEs {
				modelSQL.WriteString(cte.Name)
				modelSQL.WriteString(" AS (\n")
				lines := strings.Split(cte.Query, "\n")
				for _, line := range lines {
					modelSQL.WriteString("    ")
					modelSQL.WriteString(line)
					modelSQL.WriteString("\n")
				}
				if idx < len(step.CTEs)-1 {
					modelSQL.WriteString("),\n")
				} else {
					modelSQL.WriteString(")\n\n")
				}
			}

			fmt.Fprintf(&modelSQL, "SELECT * FROM %s\n", outCTE)

			files = append(files, ProjectFile{
				Path:    fmt.Sprintf("models/transformations/%s.sql", modelName),
				Content: modelSQL.String(),
			})

			// Update active current model for this specific table kind
			currentModels[tableKind] = modelName
		}

		// Check if any declared checkpoints coincide with this step
		for _, cp := range plan.Checkpoints() {
			if cp.After == ruleID {
				cpKey := string(cp.Key)
				// Target model for primary entity or target kind
				if targetModel := currentModels[step.TargetKind]; targetModel != "" {
					checkpointModels[cpKey] = targetModel
				}
			}
		}
	}

	// 5. Checkpoint Models
	for _, cp := range plan.Checkpoints() {
		cpKey := string(cp.Key)
		sanitizedKey := sanitizeID(cpKey)
		targetModel := checkpointModels[cpKey]
		if targetModel != "" {
			chkModelName := fmt.Sprintf("chk_%s", sanitizedKey)
			chkSQL := fmt.Sprintf(`{{ config(materialized='view') }}

SELECT * FROM {{ ref('%s') }}
`, targetModel)
			files = append(files, ProjectFile{
				Path:    fmt.Sprintf("models/checkpoints/%s.sql", chkModelName),
				Content: chkSQL,
			})
		}
	}

	return Project{
		Name:             opts.ProjectName,
		Files:            files,
		CheckpointModels: checkpointModels,
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

func splitFieldPath(path semantic.FieldPath) (string, string) {
	val := string(path)
	idx := strings.IndexByte(val, '.')
	if idx < 0 {
		return "", val
	}
	return val[:idx], val[idx+1:]
}
