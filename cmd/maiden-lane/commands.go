package main

import (
	"context"
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

// runCompile handles the `maiden-lane compile <ruleset.ml>` subcommand.
func runCompile(_ context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("compile", flag.ContinueOnError)
	fs.SetOutput(stderr)
	schemaFlag := fs.String("schema", "", "Path to schema source file (.ml or .json)")
	outFlag := fs.String("out", "", "Output path for compiled plan canonical JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if fs.NArg() < 1 {
		return errors.New("compile requires a ruleset file argument (.ml)")
	}
	rulePath := fs.Arg(0)

	ruleBytes, err := os.ReadFile(rulePath)
	if err != nil {
		return fmt.Errorf("read ruleset file: %w", err)
	}

	compileReq, err := dsl.CompileRequestFromText(string(ruleBytes))
	if err != nil {
		return fmt.Errorf("dsl parse/lower error: %w", err)
	}

	if *schemaFlag != "" {
		sBytes, err := os.ReadFile(*schemaFlag)
		if err != nil {
			return fmt.Errorf("read schema file: %w", err)
		}
		schemaReq, err := dsl.CompileRequestFromText(string(sBytes))
		if err != nil {
			return fmt.Errorf("parse schema file: %w", err)
		}
		compileReq.Schema = schemaReq.Schema
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

	if *outFlag != "" {
		if err := os.WriteFile(*outFlag, plan.CanonicalBytes(), 0600); err != nil {
			return fmt.Errorf("write plan output: %w", err)
		}
		fmt.Fprintf(stdout, "Wrote canonical plan bytes to %s\n", *outFlag)
	}

	return nil
}

// runExecute handles the `maiden-lane run <ruleset.ml>` subcommand.
func runExecute(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	var backend string = "go"
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
		} else if rulePath == "" && !strings.HasPrefix(args[i], "-") {
			rulePath = args[i]
		}
	}

	if rulePath == "" {
		return errors.New("run requires a ruleset file argument (.ml)")
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

	compilation, err := semantic.Compile(compileReq)
	if err != nil {
		return fmt.Errorf("compile: %w", err)
	}
	plan, ok := compilation.Plan()
	if !ok {
		fail, _ := compilation.Failure()
		return fmt.Errorf("compilation failed: %v", fail.Diagnostics())
	}

	schema, err := semantic.NewSchema(compileReq.Schema.EntityDeclarations(), compileReq.Schema.RelationDeclarations())
	if err != nil {
		return fmt.Errorf("schema validation: %w", err)
	}

	if backend == "sql" {
		pipeline, err := sql.TranspilePlan(plan, sql.PipelineOptions{
			Dialect: sql.Postgres(),
			Schema:  &schema,
		})
		if err != nil {
			return fmt.Errorf("sql transpile error: %w", err)
		}
		fmt.Fprintf(stdout, "%s\n", pipeline.SQL)
		return nil
	}

	// Construct empty initial state conforming to schema
	lineageID := semantic.InputLineageID("sha256:0000000000000000000000000000000000000000000000000000000000000000")
	initState, err := semantic.NewState(schema, lineageID, nil, nil)
	if err != nil {
		return fmt.Errorf("initial state: %w", err)
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

// runDiff handles `maiden-lane diff <schema.ml>`.
func runDiff(_ context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) < 1 {
		return errors.New("diff requires a schema file (.ml)")
	}

	ruleBytes, err := os.ReadFile(args[0])
	if err != nil {
		return fmt.Errorf("read schema file: %w", err)
	}

	compileReq, err := dsl.CompileRequestFromText(string(ruleBytes))
	if err != nil {
		return fmt.Errorf("parse schema: %w", err)
	}

	schema, err := semantic.NewSchema(compileReq.Schema.EntityDeclarations(), compileReq.Schema.RelationDeclarations())
	if err != nil {
		return fmt.Errorf("new schema: %w", err)
	}

	lineageID := semantic.InputLineageID("sha256:0000000000000000000000000000000000000000000000000000000000000000")
	stateA, err := semantic.NewState(schema, lineageID, nil, nil)
	if err != nil {
		return err
	}
	stateB, err := semantic.NewState(schema, lineageID, nil, nil)
	if err != nil {
		return err
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
	return nil
}

// runGate handles `maiden-lane gate`.
func runGate(_ context.Context, _ []string, stdout, stderr io.Writer) error {
	targetPolicy := ports.TargetPolicy{
		Version:           1,
		TenantID:          "tenant-prod",
		CustomerID:        "customer-1",
		Target:            "production",
		RequiredProfileID: "cm.v1",
	}

	candidate := promotion.Candidate{}
	decision := promotion.Evaluate(targetPolicy, candidate)

	fmt.Fprintf(stdout, "9-Clause Promotion Gate Evaluation:\n")
	fmt.Fprintf(stdout, "Authorized: %v\n", decision.Authorized())
	fmt.Fprintf(stdout, "Clauses:\n")
	for _, c := range decision.Clauses() {
		fmt.Fprintf(stdout, "  - %-25s: %s\n", c.Clause(), c.Verdict())
	}

	return nil
}
