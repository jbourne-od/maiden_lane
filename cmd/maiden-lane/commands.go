package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/optimaldynamics/maiden-lane/internal/app"
	"github.com/optimaldynamics/maiden-lane/internal/dsl"
	"github.com/optimaldynamics/maiden-lane/internal/ports"
	"github.com/optimaldynamics/maiden-lane/internal/promotion"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
	"github.com/optimaldynamics/maiden-lane/internal/transpiler/dbt"
	"github.com/optimaldynamics/maiden-lane/internal/transpiler/sql"
)

// JSON Schema representations
type jsonFieldDecl struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Optional bool   `json:"optional,omitempty"`
}

type jsonEntityDecl struct {
	Kind   string          `json:"kind"`
	Fields []jsonFieldDecl `json:"fields"`
}

type jsonRelationDecl struct {
	Kind     string `json:"kind"`
	FromKind string `json:"from_kind"`
	ToKind   string `json:"to_kind"`
}

type jsonSchemaFile struct {
	Entities  []jsonEntityDecl   `json:"entities"`
	Relations []jsonRelationDecl `json:"relations,omitempty"`
}

type jsonEntity struct {
	Kind   string                 `json:"kind"`
	ID     string                 `json:"id"`
	Fields map[string]interface{} `json:"fields"`
}

type jsonRelation struct {
	Kind     string `json:"kind"`
	FromKind string `json:"from_kind"`
	FromID   string `json:"from_id"`
	ToKind   string `json:"to_kind"`
	ToID     string `json:"to_id"`
}

type jsonStateFile struct {
	Schema    jsonSchemaFile `json:"schema"`
	LineageID string         `json:"lineage_id,omitempty"`
	Entities  []jsonEntity   `json:"entities,omitempty"`
	Relations []jsonRelation `json:"relations,omitempty"`
}

type jsonCandidateFile struct {
	PlanJSON    string `json:"plan_json,omitempty"`
	ExecutionID string `json:"execution_id"`
	Backend     string `json:"backend"`
	Version     string `json:"version"`
}

type jsonPolicyFile struct {
	Version           uint64 `json:"version"`
	TenantID          string `json:"tenant_id"`
	CustomerID        string `json:"customer_id"`
	Target            string `json:"target"`
	RequiredProfileID string `json:"required_profile_id"`
}

func parseValueKind(s string) (semantic.ValueKind, error) {
	switch strings.ToLower(s) {
	case "string":
		return semantic.ValueString, nil
	case "atom":
		return semantic.ValueAtom, nil
	case "int64", "int", "integer":
		return semantic.ValueInt64, nil
	case "timestamp":
		return semantic.ValueTimestamp, nil
	case "duration":
		return semantic.ValueDuration, nil
	case "decimal":
		return semantic.ValueDecimal, nil
	case "date":
		return semantic.ValueDate, nil
	default:
		return 0, fmt.Errorf("unknown type %q", s)
	}
}

func formatValue(v semantic.Value) string {
	switch v.Kind() {
	case semantic.ValueString, semantic.ValueAtom:
		s, _ := v.String()
		return s
	case semantic.ValueInt64, semantic.ValueDuration:
		i, _ := v.Int64()
		return fmt.Sprintf("%d", i)
	case semantic.ValueTimestamp:
		t, _ := v.Timestamp()
		return t
	case semantic.ValueDecimal:
		d, _ := v.Decimal()
		return d
	case semantic.ValueDate:
		d, _ := v.Date()
		return d
	default:
		return "<empty>"
	}
}

func parseSchemaBytes(sBytes []byte) (semantic.SchemaDeclaration, error) {
	trimmed := bytes.TrimSpace(sBytes)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		// Parse JSON Schema
		var js jsonSchemaFile
		if err := json.Unmarshal(trimmed, &js); err != nil {
			return semantic.SchemaDeclaration{}, fmt.Errorf("unmarshal json schema: %w", err)
		}
		var entities []semantic.EntityDeclaration
		for _, ed := range js.Entities {
			var fields []semantic.FieldDeclaration
			for _, fd := range ed.Fields {
				vk, err := parseValueKind(fd.Type)
				if err != nil {
					return semantic.SchemaDeclaration{}, err
				}
				fields = append(fields, semantic.FieldDeclaration{
					Name:                   semantic.FieldName(fd.Name),
					Kind:                   vk,
					RequiredAtConstruction: !fd.Optional,
				})
			}
			entities = append(entities, semantic.EntityDeclaration{
				Kind:   semantic.EntityKind(ed.Kind),
				Fields: fields,
			})
		}
		var relations []semantic.RelationDeclaration
		for _, rd := range js.Relations {
			relations = append(relations, semantic.RelationDeclaration{
				Kind:     semantic.RelationKind(rd.Kind),
				FromKind: semantic.EntityKind(rd.FromKind),
				ToKind:   semantic.EntityKind(rd.ToKind),
			})
		}
		schema, err := semantic.NewSchema(entities, relations)
		if err != nil {
			return semantic.SchemaDeclaration{}, fmt.Errorf("validate json schema: %w", err)
		}
		return schema.Declaration(), nil
	}

	// Parse DSL Schema
	schemaReq, err := dsl.CompileRequestFromText(string(sBytes))
	if err != nil {
		return semantic.SchemaDeclaration{}, fmt.Errorf("parse dsl schema: %w", err)
	}
	return schemaReq.Schema, nil
}

func parseStateFile(path string) (semantic.State, error) {
	sBytes, err := os.ReadFile(path)
	if err != nil {
		return semantic.State{}, fmt.Errorf("read state file %s: %w", path, err)
	}

	trimmed := bytes.TrimSpace(sBytes)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		var js jsonStateFile
		if err := json.Unmarshal(trimmed, &js); err != nil {
			return semantic.State{}, fmt.Errorf("unmarshal json state: %w", err)
		}

		schemaDeclBytes, err := json.Marshal(js.Schema)
		if err != nil {
			return semantic.State{}, err
		}
		schemaDecl, err := parseSchemaBytes(schemaDeclBytes)
		if err != nil {
			return semantic.State{}, err
		}

		schema, err := semantic.NewSchema(schemaDecl.EntityDeclarations(), schemaDecl.RelationDeclarations())
		if err != nil {
			return semantic.State{}, err
		}

		lineageID := semantic.InputLineageID(js.LineageID)
		if lineageID == "" {
			lineageID = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
		}

		var entities []semantic.Entity
		for _, e := range js.Entities {
			fields := make(map[semantic.FieldName]semantic.Value)
			for k, v := range e.Fields {
				fName := semantic.FieldName(k)
				switch val := v.(type) {
				case string:
					sv, _ := semantic.NewStringValue(val)
					fields[fName] = sv
				case float64:
					fields[fName] = semantic.NewInt64Value(int64(val))
				case bool:
					if val {
						av, _ := semantic.NewAtomValue("true")
						fields[fName] = av
					} else {
						av, _ := semantic.NewAtomValue("false")
						fields[fName] = av
					}
				}
			}
			ent, err := semantic.NewEntity(semantic.EntityRef{
				Kind: semantic.EntityKind(e.Kind),
				ID:   semantic.EntityID(e.ID),
			}, fields)
			if err != nil {
				return semantic.State{}, fmt.Errorf("construct entity %s: %w", e.ID, err)
			}
			entities = append(entities, ent)
		}

		var relations []semantic.Relation
		for _, r := range js.Relations {
			relations = append(relations, semantic.Relation{
				Kind: semantic.RelationKind(r.Kind),
				From: semantic.EntityRef{Kind: semantic.EntityKind(r.FromKind), ID: semantic.EntityID(r.FromID)},
				To:   semantic.EntityRef{Kind: semantic.EntityKind(r.ToKind), ID: semantic.EntityID(r.ToID)},
			})
		}

		return semantic.NewState(schema, lineageID, entities, relations)
	}

	// If .ml file, parse schema and construct empty state
	schemaDecl, err := parseSchemaBytes(sBytes)
	if err != nil {
		return semantic.State{}, err
	}
	schema, err := semantic.NewSchema(schemaDecl.EntityDeclarations(), schemaDecl.RelationDeclarations())
	if err != nil {
		return semantic.State{}, err
	}
	lineageID := semantic.InputLineageID("sha256:0000000000000000000000000000000000000000000000000000000000000000")
	return semantic.NewState(schema, lineageID, nil, nil)
}

// runCompile handles the `maiden-lane compile <ruleset.ml>` subcommand.
func runCompile(_ context.Context, args []string, stdout, stderr io.Writer) error {
	var schemaPath string
	var outPath string
	var rulePath string

	for i := 0; i < len(args); i++ {
		if args[i] == "--schema" || args[i] == "-schema" {
			if i+1 < len(args) {
				schemaPath = args[i+1]
				i++
			}
		} else if strings.HasPrefix(args[i], "--schema=") {
			schemaPath = strings.TrimPrefix(args[i], "--schema=")
		} else if strings.HasPrefix(args[i], "-schema=") {
			schemaPath = strings.TrimPrefix(args[i], "-schema=")
		} else if args[i] == "--out" || args[i] == "-out" {
			if i+1 < len(args) {
				outPath = args[i+1]
				i++
			}
		} else if strings.HasPrefix(args[i], "--out=") {
			outPath = strings.TrimPrefix(args[i], "--out=")
		} else if strings.HasPrefix(args[i], "-out=") {
			outPath = strings.TrimPrefix(args[i], "-out=")
		} else if rulePath == "" && !strings.HasPrefix(args[i], "-") {
			rulePath = args[i]
		}
	}

	if rulePath == "" {
		return errors.New("compile requires a ruleset file argument (.ml)")
	}

	ruleBytes, err := os.ReadFile(rulePath)
	if err != nil {
		return fmt.Errorf("read ruleset file: %w", err)
	}

	compileReq, err := dsl.CompileRequestFromText(string(ruleBytes))
	if err != nil {
		return fmt.Errorf("dsl parse/lower error: %w", err)
	}

	if schemaPath != "" {
		sBytes, err := os.ReadFile(schemaPath)
		if err != nil {
			return fmt.Errorf("read schema file: %w", err)
		}
		schemaDecl, err := parseSchemaBytes(sBytes)
		if err != nil {
			return fmt.Errorf("parse schema: %w", err)
		}
		compileReq.Schema = schemaDecl
	}

	if len(compileReq.Schema.EntityDeclarations()) == 0 {
		return errors.New("compile requires a schema declaration (specify 'schema { ... }' in .ml or use --schema flag)")
	}

	compilation, err := semantic.Compile(compileReq)
	if err != nil {
		return fmt.Errorf("semantic compile error: %w", err)
	}

	plan, ok := compilation.Plan()
	if !ok {
		fail, _ := compilation.Failure()
		var diagMsg string
		for _, d := range fail.Diagnostics() {
			diagMsg += fmt.Sprintf(" [%s: subject=%s, detail=%s]", d.Code(), d.Subject(), d.Detail())
		}
		return fmt.Errorf("compilation rejected by compiler:%s", diagMsg)
	}

	fmt.Fprintf(stdout, "Plan compiled successfully!\n")
	fmt.Fprintf(stdout, "Plan ID:          %s\n", plan.ID())
	fmt.Fprintf(stdout, "Ruleset Digest:   %s\n", plan.RulesetDigest())
	fmt.Fprintf(stdout, "Schema Digest:    %s\n", plan.SchemaDigest())
	fmt.Fprintf(stdout, "Transformations:  %d\n", len(plan.Transformations()))
	fmt.Fprintf(stdout, "Checkpoints:      %d\n", len(plan.Checkpoints()))

	if outPath != "" {
		if err := os.WriteFile(outPath, plan.CanonicalBytes(), 0600); err != nil {
			return fmt.Errorf("write plan output: %w", err)
		}
		fmt.Fprintf(stdout, "Wrote canonical plan bytes to %s\n", outPath)
	}

	return nil
}

// runExecute handles the `maiden-lane run <ruleset.ml>` subcommand.
func runExecute(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	var backend string = "go"
	var statePath string
	var rulePath string

	for i := 0; i < len(args); i++ {
		if args[i] == "--backend" || args[i] == "-backend" {
			if i+1 < len(args) {
				backend = args[i+1]
				i++
			}
		} else if strings.HasPrefix(args[i], "--backend=") {
			backend = strings.TrimPrefix(args[i], "--backend=")
		} else if strings.HasPrefix(args[i], "-backend=") {
			backend = strings.TrimPrefix(args[i], "-backend=")
		} else if args[i] == "--state" || args[i] == "-state" {
			if i+1 < len(args) {
				statePath = args[i+1]
				i++
			}
		} else if strings.HasPrefix(args[i], "--state=") {
			statePath = strings.TrimPrefix(args[i], "--state=")
		} else if strings.HasPrefix(args[i], "-state=") {
			statePath = strings.TrimPrefix(args[i], "-state=")
		} else if rulePath == "" && !strings.HasPrefix(args[i], "-") {
			rulePath = args[i]
		}
	}

	if rulePath == "" {
		return errors.New("run requires a ruleset file argument (.ml)")
	}

	if backend != "go" && backend != "reference" {
		return fmt.Errorf("unsupported execution backend %q (only 'go' is supported for local execution; use 'maiden-lane transpile sql' to generate SQL CTE pipelines)", backend)
	}

	ruleBytes, err := os.ReadFile(rulePath)
	if err != nil {
		return fmt.Errorf("read ruleset file: %w", err)
	}

	compileReq, err := dsl.CompileRequestFromText(string(ruleBytes))
	if err != nil {
		return fmt.Errorf("dsl parse/lower error: %w", err)
	}

	if len(compileReq.Schema.EntityDeclarations()) == 0 {
		return errors.New("run requires a schema declaration (specify 'schema { ... }' in .ml or provide schema)")
	}

	schema, err := semantic.NewSchema(compileReq.Schema.EntityDeclarations(), compileReq.Schema.RelationDeclarations())
	if err != nil {
		return fmt.Errorf("schema validation: %w", err)
	}

	var initState semantic.State
	if statePath != "" {
		initState, err = parseStateFile(statePath)
		if err != nil {
			return fmt.Errorf("load state file: %w", err)
		}
	} else {
		lineageID := semantic.InputLineageID("sha256:0000000000000000000000000000000000000000000000000000000000000000")
		initState, err = semantic.NewState(schema, lineageID, nil, nil)
		if err != nil {
			return fmt.Errorf("initial state: %w", err)
		}
	}

	world, err := semantic.NewWorld(nil)
	if err != nil {
		return fmt.Errorf("world: %w", err)
	}

	execIdentity, err := semantic.NewExecutorIdentity("go-reference", "sha256:0000000000000000000000000000000000000000000000000000000000000000")
	if err != nil {
		return fmt.Errorf("executor identity: %w", err)
	}

	appReq := app.Request{
		Compilation:      compileReq,
		InitialState:     initState,
		World:            world,
		ExecutorIdentity: execIdentity,
		Policy:           semantic.ChangesProvenance,
	}

	outcome, err := app.Run(ctx, appReq, nil)
	if err != nil {
		return fmt.Errorf("execution error: %w", err)
	}

	runID, _ := outcome.SemanticRunID()
	execID, _ := outcome.ExecutionID()

	fmt.Fprintf(stdout, "Execution completed successfully!\n")
	fmt.Fprintf(stdout, "Semantic Run ID:   %s\n", runID)
	fmt.Fprintf(stdout, "Execution ID:      %s\n", execID)
	fmt.Fprintf(stdout, "Checkpoints:       %d\n", len(outcome.Checkpoints()))
	fmt.Fprintf(stdout, "Assessments:       %d\n", len(outcome.Assessments()))

	for _, cp := range outcome.Checkpoints() {
		fmt.Fprintf(stdout, "  - Checkpoint %s: ID=%s, StateDigest=%s\n",
			cp.Checkpoint().Key, cp.ID(), cp.StateDigest())
	}

	return nil
}

// runTranspile handles `maiden-lane transpile <sql|dbt> <ruleset.ml>`.
func runTranspile(_ context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) < 2 {
		return errors.New("transpile requires target format ('sql' or 'dbt') and ruleset file (.ml)")
	}
	targetFormat := args[0]
	rulePath := args[1]

	ruleBytes, err := os.ReadFile(rulePath)
	if err != nil {
		return fmt.Errorf("read ruleset file: %w", err)
	}

	compileReq, err := dsl.CompileRequestFromText(string(ruleBytes))
	if err != nil {
		return fmt.Errorf("dsl parse/lower error: %w", err)
	}

	if len(compileReq.Schema.EntityDeclarations()) == 0 {
		return errors.New("transpile requires a schema declaration (specify 'schema { ... }' in .ml or provide schema)")
	}

	compilation, err := semantic.Compile(compileReq)
	if err != nil {
		return fmt.Errorf("compile: %w", err)
	}
	plan, ok := compilation.Plan()
	if !ok {
		fail, _ := compilation.Failure()
		return fmt.Errorf("plan compilation failed: %v", fail.Diagnostics())
	}

	schema, err := semantic.NewSchema(compileReq.Schema.EntityDeclarations(), compileReq.Schema.RelationDeclarations())
	if err != nil {
		return fmt.Errorf("schema validation: %w", err)
	}

	switch targetFormat {
	case "sql":
		pipeline, err := sql.TranspilePlan(plan, sql.PipelineOptions{
			Dialect: sql.Postgres(),
			Schema:  &schema,
		})
		if err != nil {
			return fmt.Errorf("transpile sql: %w", err)
		}
		fmt.Fprintf(stdout, "%s\n", pipeline.SQL)
		return nil

	case "dbt":
		fs := flag.NewFlagSet("transpile dbt", flag.ContinueOnError)
		fs.SetOutput(stderr)
		outDir := fs.String("out", "dbt_project", "Output directory for dbt project")
		if err := fs.Parse(args[2:]); err != nil {
			return err
		}

		project, err := dbt.GenerateProject(plan, dbt.Options{
			ProjectName: "maiden_lane_pipeline",
			ProfileName: "default",
			Schema:      &schema,
		})
		if err != nil {
			return fmt.Errorf("generate dbt project: %w", err)
		}

		for _, file := range project.Files {
			fullPath := filepath.Join(*outDir, file.Path)
			if err := os.MkdirAll(filepath.Dir(fullPath), 0750); err != nil {
				return fmt.Errorf("create dir %s: %w", filepath.Dir(fullPath), err)
			}
			if err := os.WriteFile(fullPath, []byte(file.Content), 0600); err != nil {
				return fmt.Errorf("write file %s: %w", fullPath, err)
			}
		}
		fmt.Fprintf(stdout, "Generated dbt project '%s' (%d files) in %s\n", project.Name, len(project.Files), *outDir)
		return nil

	default:
		return fmt.Errorf("unsupported transpile format %q (use 'sql' or 'dbt')", targetFormat)
	}
}

// runDiff handles `maiden-lane diff <state_a.json> <state_b.json>`.
func runDiff(_ context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) < 2 {
		return errors.New("diff requires two state file arguments (<baseline_state.json> <candidate_state.json>)")
	}

	stateA, err := parseStateFile(args[0])
	if err != nil {
		return fmt.Errorf("baseline state: %w", err)
	}
	stateB, err := parseStateFile(args[1])
	if err != nil {
		return fmt.Errorf("candidate state: %w", err)
	}

	diff, err := semantic.DiffStates(stateA, stateB)
	if err != nil {
		return fmt.Errorf("diff states: %w", err)
	}

	fmt.Fprintf(stdout, "Semantic State Diff:\n")
	fmt.Fprintf(stdout, "Identical: %v\n", diff.Identical())
	fmt.Fprintf(stdout, "Modified Entities: %d\n", len(diff.ModifiedEntities))
	fmt.Fprintf(stdout, "Created Entities:  %d\n", len(diff.CreatedEntities))
	fmt.Fprintf(stdout, "Deleted Entities:  %d\n", len(diff.DeletedEntities))

	for _, m := range diff.ModifiedEntities {
		fmt.Fprintf(stdout, "  - Entity %s (%s):\n", m.Ref.ID, m.Ref.Kind)
		for _, f := range m.FieldDiffs {
			fmt.Fprintf(stdout, "      %s: baseline=%s, candidate=%s\n", f.Name, formatValue(f.Baseline), formatValue(f.Candidate))
		}
	}
	return nil
}

// runGate handles `maiden-lane gate [--policy policy.json] [--candidate candidate.json]`.
func runGate(_ context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("gate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	policyFlag := fs.String("policy", "", "Path to policy JSON file")
	candidateFlag := fs.String("candidate", "", "Path to candidate JSON file")
	if err := fs.Parse(args); err != nil {
		return err
	}

	targetPolicy := ports.TargetPolicy{
		Version:           1,
		TenantID:          "default-tenant",
		CustomerID:        "default-customer",
		Target:            "production",
		RequiredProfileID: "cm.v1",
	}

	if *policyFlag != "" {
		pBytes, err := os.ReadFile(*policyFlag)
		if err != nil {
			return fmt.Errorf("read policy file: %w", err)
		}
		var pf jsonPolicyFile
		if err := json.Unmarshal(pBytes, &pf); err != nil {
			return fmt.Errorf("unmarshal policy: %w", err)
		}
		targetPolicy = ports.TargetPolicy{
			Version:           ports.PolicyVersion(pf.Version),
			TenantID:          ports.TenantID(pf.TenantID),
			CustomerID:        ports.CustomerID(pf.CustomerID),
			Target:            ports.TargetKey(pf.Target),
			RequiredProfileID: semantic.ProfileID(pf.RequiredProfileID),
		}
	}

	candidate := promotion.Candidate{}
	if *candidateFlag != "" {
		cBytes, err := os.ReadFile(*candidateFlag)
		if err != nil {
			return fmt.Errorf("read candidate file: %w", err)
		}
		var cf jsonCandidateFile
		if err := json.Unmarshal(cBytes, &cf); err != nil {
			return fmt.Errorf("unmarshal candidate: %w", err)
		}
		if cf.Backend != "" && cf.Version != "" {
			exec, err := semantic.NewExecutorIdentity(cf.Backend, semantic.Digest(cf.Version))
			if err != nil {
				return fmt.Errorf("executor identity: %w", err)
			}
			candidate.Executor = exec
		}
		if cf.ExecutionID != "" {
			candidate.ExecutionID = semantic.ExecutionID(cf.ExecutionID)
		}
	}

	decision := promotion.Evaluate(targetPolicy, candidate)

	fmt.Fprintf(stdout, "9-Clause Promotion Gate Evaluation:\n")
	fmt.Fprintf(stdout, "Authorized: %v\n", decision.Authorized())
	fmt.Fprintf(stdout, "Clauses:\n")
	for _, c := range decision.Clauses() {
		fmt.Fprintf(stdout, "  - %-25s: %s\n", c.Clause(), c.Verdict())
	}

	if !decision.Authorized() {
		return fmt.Errorf("promotion gate refused: candidate not authorized (refusals: %v)", decision.Refusals())
	}

	return nil
}
