package dsl_test

import (
	"testing"

	"github.com/optimaldynamics/maiden-lane/internal/dsl"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

func TestLexerBasicTokens(t *testing.T) {
	input := `
# Test schema and rules
schema {
  entity driver {
    driver_id: string;
    hours: int64;
    status: string;
    depot: string;
    hired_at: timestamp optional;
  }
  relation assigned_truck {
    from: driver;
    to: truck;
  }
}

rule certify_driver {
  select driver
  where driver.hours >= 10 && driver.status == "AVAILABLE"
  set driver.status = "CERTIFIED";
}
`
	l := dsl.NewLexer(input)
	p := dsl.NewParser(l)
	file, err := p.ParseFile()
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}
	if len(file.Declarations) != 2 {
		t.Fatalf("expected 2 declarations, got %d", len(file.Declarations))
	}
}

func TestCompileAllSevenOperators(t *testing.T) {
	dslSource := `
schema {
  entity driver {
    driver_id: string;
    hours: int64;
    status: string;
    depot: string;
  }
  entity truck {
    truck_id: string;
    depot: string;
    status: string;
  }
  entity team {
    depot: string;
    driver_count: int64;
    total_hours: int64;
  }
  entity shift_log {
    shift_type: string;
    hours: int64;
  }
  relation assigned_truck {
    from: driver;
    to: truck;
  }
}

rule select_assign_rule {
  select driver
  where driver.hours > 5
  group_by driver.depot
  having count() >= 1
  set driver.status = "ACTIVE";
}

rule insert_team_rule {
  insert team {
    select driver
    where driver.status == "AVAILABLE"
    group_by driver.depot
    having count() == 2
    discriminator: "TEAM_PAIR";
  } set team.depot = "CHI",
        team.driver_count = count(),
        team.total_hours = sum(driver.hours);
}

rule delete_driver_rule {
  delete driver {
    select driver
    where driver.status == "TERMINATED";
  };
}

rule relate_rule {
  relate driver to truck as assigned_truck {
    from: select driver where driver.status == "AVAILABLE";
    to: select truck where truck.status == "READY";
    guard: driver.depot == truck.depot;
  };
}

rule unrelate_rule {
  unrelate driver from truck as assigned_truck {
    from: select driver;
    to: select truck;
    guard: driver.depot != truck.depot;
  };
}

rule merge_driver_rule (depends_on: ["select_assign_rule"]) {
  merge driver into driver {
    select driver
    group_by driver.depot
    having count() == 2
    discriminator: sum(driver.hours);
    retain_sources: false;
    reanchor_relations: true;
  } set driver.driver_id = "D_MERGED",
        driver.status = "AVAILABLE",
        driver.depot = "MERGED_DEPOT",
        driver.hours = sum(driver.hours);
}

rule split_driver_rule {
  split driver into shift_log {
    select driver
    where driver.status == "AVAILABLE"
    retain_source: false;
    partition "AM" {
      discriminator: "MORNING";
      set shift_log.shift_type = "MORNING",
          shift_log.hours = 5;
    }
    partition "PM" {
      discriminator: "EVENING";
      set shift_log.shift_type = "EVENING",
          shift_log.hours = 5;
    }
  };
}
`

	schemaDecl, rulesetDecl, err := dsl.CompileText(dslSource)
	if err != nil {
		t.Fatalf("dsl.CompileText failed: %v", err)
	}

	if len(schemaDecl.EntityDeclarations()) != 4 {
		t.Fatalf("expected 4 entities, got %d", len(schemaDecl.EntityDeclarations()))
	}
	if len(schemaDecl.RelationDeclarations()) != 1 {
		t.Fatalf("expected 1 relation, got %d", len(schemaDecl.RelationDeclarations()))
	}
	if len(rulesetDecl.Transformations) != 7 {
		t.Fatalf("expected 7 transformations, got %d", len(rulesetDecl.Transformations))
	}

	// Verify compilation through semantic.Compile
	compilation, err := semantic.Compile(semantic.CompileRequest{
		Schema:                   schemaDecl,
		Rules:                    rulesetDecl,
		CompilerSemanticsVersion: "semantics.v1",
	})
	if err != nil {
		t.Fatalf("semantic.Compile failed: %v", err)
	}
	plan, ok := compilation.Plan()
	if !ok {
		failure, _ := compilation.Failure()
		t.Fatalf("plan failed to compile: %v", failure.Diagnostics())
	}

	if len(plan.Transformations()) != 7 {
		t.Fatalf("expected 7 compiled declarations, got %d", len(plan.Transformations()))
	}
}

func TestEndToEndExecutionFromDSL(t *testing.T) {
	dslSource := `
schema {
  entity driver {
    driver_id: string;
    hours: int64;
    status: string;
    depot: string;
  }
}

rule certify_drivers {
  select driver
  where driver.hours >= 10
  set driver.status = "CERTIFIED";
}
`
	schemaDecl, rulesetDecl, err := dsl.CompileText(dslSource)
	if err != nil {
		t.Fatalf("CompileText: %v", err)
	}

	schema, err := semantic.NewSchema(schemaDecl.EntityDeclarations(), schemaDecl.RelationDeclarations())
	if err != nil {
		t.Fatalf("NewSchema: %v", err)
	}

	compilation, err := semantic.Compile(semantic.CompileRequest{
		Schema:                   schemaDecl,
		Rules:                    rulesetDecl,
		CompilerSemanticsVersion: "semantics.v1",
	})
	if err != nil {
		t.Fatalf("semantic.Compile: %v", err)
	}
	plan, ok := compilation.Plan()
	if !ok {
		failure, _ := compilation.Failure()
		t.Fatalf("compilation failure: %v", failure.Diagnostics())
	}

	lineage, err := semantic.NewInputLineageID("maiden-lane.dsl-test", "lineage-1")
	if err != nil {
		t.Fatalf("NewInputLineageID: %v", err)
	}
	d1ID := semantic.SourceEntityID(lineage, "driver", "D1")
	d2ID := semantic.SourceEntityID(lineage, "driver", "D2")

	sVal, _ := semantic.NewStringValue("AVAILABLE")
	d1Val, _ := semantic.NewStringValue("D1")
	d2Val, _ := semantic.NewStringValue("D2")
	depotVal, _ := semantic.NewStringValue("CHI")

	d1, err := semantic.NewEntity(semantic.EntityRef{Kind: "driver", ID: d1ID}, map[semantic.FieldName]semantic.Value{
		"driver_id": d1Val,
		"hours":     semantic.NewInt64Value(15),
		"status":    sVal,
		"depot":     depotVal,
	})
	if err != nil {
		t.Fatalf("NewEntity d1: %v", err)
	}
	d2, err := semantic.NewEntity(semantic.EntityRef{Kind: "driver", ID: d2ID}, map[semantic.FieldName]semantic.Value{
		"driver_id": d2Val,
		"hours":     semantic.NewInt64Value(5),
		"status":    sVal,
		"depot":     depotVal,
	})
	if err != nil {
		t.Fatalf("NewEntity d2: %v", err)
	}

	initialState, err := semantic.NewState(schema, lineage, []semantic.Entity{d1, d2}, nil)
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}

	world, err := semantic.NewWorld(nil)
	if err != nil {
		t.Fatalf("NewWorld: %v", err)
	}
	exec, err := semantic.NewExecutorIdentity("go", "sha256:0000000000000000000000000000000000000000000000000000000000000001")
	if err != nil {
		t.Fatalf("NewExecutorIdentity: %v", err)
	}
	binding, err := semantic.BindRun(semantic.RunBindingRequest{
		Plan:             plan,
		InitialState:     initialState,
		World:            world,
		ExecutorIdentity: exec,
		Policy:           semantic.ChangesProvenance,
	})
	if err != nil {
		t.Fatalf("BindRun: %v", err)
	}

	outcome, err := semantic.ExecuteTransition(binding, "certify_drivers", initialState, semantic.NewJournal())
	if err != nil {
		t.Fatalf("ExecuteTransition: %v", err)
	}
	if failure, isFail := outcome.Failure(); isFail {
		t.Fatalf("transition failed: %s", failure.Code())
	}

	finalState := outcome.State()
	for _, e := range finalState.Entities() {
		statusVal, _ := e.Field("status")
		hoursVal, _ := e.Field("hours")
		h, _ := hoursVal.Int64()
		s, _ := statusVal.String()
		if h >= 10 && s != "CERTIFIED" {
			t.Fatalf("expected driver with %d hours to be CERTIFIED, got %s", h, s)
		}
		if h < 10 && s != "AVAILABLE" {
			t.Fatalf("expected driver with %d hours to remain AVAILABLE, got %s", h, s)
		}
	}
}

func TestFormatRoundTrip(t *testing.T) {
	dslSource := `schema {
  entity driver {
    driver_id: string;
    hours: int64;
    status: string;
  }
}

rule certify_drivers {
  select driver
  where !(driver.hours < 10)
  set driver.status = "CERTIFIED";
}

checkpoint drivers_certified after certify_drivers;

profile driver_readiness for entity driver {
  require driver.status present as REQ_STATUS;
  require driver.hours present as REQ_HOURS;
}
`
	formatted, err := dsl.FormatSource(dslSource)
	if err != nil {
		t.Fatalf("FormatSource failed: %v", err)
	}

	reformatted, err := dsl.FormatSource(formatted)
	if err != nil {
		t.Fatalf("re-FormatSource failed: %v", err)
	}

	if formatted != reformatted {
		t.Fatalf("formatter is not idempotent:\nFirst:\n%s\nSecond:\n%s", formatted, reformatted)
	}
}

func TestCompileCheckpointsAndProfiles(t *testing.T) {
	dslSource := `
schema {
  entity driver {
    driver_id: string;
    status: string;
    hours: int64;
  }
}

rule certify_drivers {
  select driver
  where !(driver.hours < 10)
  set driver.status = "CERTIFIED";
}

checkpoint post_certification after certify_drivers;

profile driver_certified for entity driver {
  require driver.status present as STATUS_REQUIRED;
  require driver.hours present as HOURS_REQUIRED;
}
`
	req, err := dsl.CompileRequestFromText(dslSource)
	if err != nil {
		t.Fatalf("CompileRequestFromText failed: %v", err)
	}

	if len(req.Rules.Checkpoints) != 1 {
		t.Fatalf("expected 1 checkpoint, got %d", len(req.Rules.Checkpoints))
	}
	if req.Rules.Checkpoints[0].Key != "post_certification" || req.Rules.Checkpoints[0].After != "certify_drivers" {
		t.Fatalf("unexpected checkpoint: %+v", req.Rules.Checkpoints[0])
	}

	if len(req.Profiles) != 1 {
		t.Fatalf("expected 1 profile, got %d", len(req.Profiles))
	}
	if req.Profiles[0].Key != "driver_certified" || len(req.Profiles[0].Requirements) != 2 {
		t.Fatalf("unexpected profile: %+v", req.Profiles[0])
	}

	compilation, err := semantic.Compile(req)
	if err != nil {
		t.Fatalf("semantic.Compile failed: %v", err)
	}
	plan, ok := compilation.Plan()
	if !ok {
		failure, _ := compilation.Failure()
		t.Fatalf("plan failed to compile: %v", failure.Diagnostics())
	}
	if len(plan.Checkpoints()) != 1 {
		t.Fatalf("expected 1 plan checkpoint, got %d", len(plan.Checkpoints()))
	}
}

func TestLexerLiteralsAndEscapes(t *testing.T) {
	dslSource := `
# Comments are skipped
/* Multi-line comment
   spanning multiple lines */
"hello\n\t\"world\""
12345
-42
12.34
:ACTIVE
true
false
null
`
	l := dsl.NewLexer(dslSource)
	toks := []dsl.TokenType{
		dsl.TokenString,
		dsl.TokenInt,
		dsl.TokenMinus,
		dsl.TokenInt,
		dsl.TokenDecimal,
		dsl.TokenAtom,
		dsl.TokenTrue,
		dsl.TokenFalse,
		dsl.TokenNull,
		dsl.TokenEOF,
	}

	for _, want := range toks {
		tok := l.NextToken()
		if tok.Type != want {
			t.Fatalf("expected token %v, got %v (%q) at %s", want, tok.Type, tok.Literal, tok.Pos)
		}
	}
}

func TestParserSyntaxErrorReporting(t *testing.T) {
	dslSource := `
schema {
  entity driver {
    driver_id: ;
  }
}
`
	_, _, err := dsl.CompileText(dslSource)
	if err == nil {
		t.Fatal("expected syntax error, got nil")
	}
}

func TestAutoDerivedReadsAndWrites(t *testing.T) {
	dslSource := `
schema {
  entity driver {
    driver_id: string;
    hours: int64;
    status: string;
    region: string;
  }
}

rule update_driver {
  select driver
  where driver.hours > 8 && driver.region == "MIDWEST"
  set driver.status = "OVERTIME";
}
`
	_, rulesetDecl, err := dsl.CompileText(dslSource)
	if err != nil {
		t.Fatalf("CompileText failed: %v", err)
	}

	rule := rulesetDecl.Transformations[0]
	if len(rule.DeclaredReads) != 2 {
		t.Fatalf("expected 2 auto-derived reads, got %d: %v", len(rule.DeclaredReads), rule.DeclaredReads)
	}
	if len(rule.DeclaredWrites) != 1 || rule.DeclaredWrites[0] != "driver.status" {
		t.Fatalf("expected 1 auto-derived write to driver.status, got: %v", rule.DeclaredWrites)
	}
}

func TestExtractAndDateDiffExpressions(t *testing.T) {
	dslSource := `
schema {
  entity driver {
    driver_id: string;
    hired_at: timestamp;
    hire_year: int64 optional;
    shift_start: timestamp;
    shift_end: timestamp;
    shift_duration: duration optional;
  }
}

rule process_driver_temporal {
  select driver
  where driver.driver_id == "D1"
  set driver.hire_year = extract("year", driver.hired_at),
      driver.shift_duration = date_diff(driver.shift_end, driver.shift_start);
}
`
	req, err := dsl.CompileRequestFromText(dslSource)
	if err != nil {
		t.Fatalf("CompileRequestFromText failed: %v", err)
	}

	compilation, err := semantic.Compile(req)
	if err != nil {
		t.Fatalf("semantic.Compile failed: %v", err)
	}
	plan, ok := compilation.Plan()
	if !ok {
		failure, _ := compilation.Failure()
		t.Fatalf("plan compilation failed: %v", failure.Diagnostics())
	}

	schema, _ := semantic.NewSchema(req.Schema.EntityDeclarations(), nil)
	lineage, _ := semantic.NewInputLineageID("test", "root")
	d1ID := semantic.SourceEntityID(lineage, "driver", "D1")

	hiredAt, _ := semantic.NewTimestampValue("2024-01-01T00:00:00Z")
	shiftStart, _ := semantic.NewTimestampValue("2024-01-01T08:00:00Z")
	shiftEnd, _ := semantic.NewTimestampValue("2024-01-01T16:00:00Z") // (8 hours = 28800s)

	d1Val, _ := semantic.NewStringValue("D1")
	d1, _ := semantic.NewEntity(semantic.EntityRef{Kind: "driver", ID: d1ID}, map[semantic.FieldName]semantic.Value{
		"driver_id":   d1Val,
		"hired_at":    hiredAt,
		"shift_start": shiftStart,
		"shift_end":   shiftEnd,
	})
	initialState, _ := semantic.NewState(schema, lineage, []semantic.Entity{d1}, nil)
	world, _ := semantic.NewWorld(nil)
	exec, _ := semantic.NewExecutorIdentity("go", "sha256:0000000000000000000000000000000000000000000000000000000000000001")
	binding, _ := semantic.BindRun(semantic.RunBindingRequest{
		Plan:             plan,
		InitialState:     initialState,
		World:            world,
		ExecutorIdentity: exec,
		Policy:           semantic.ChangesProvenance,
	})

	outcome, err := semantic.ExecuteTransition(binding, "process_driver_temporal", initialState, semantic.NewJournal())
	if err != nil {
		t.Fatalf("ExecuteTransition: %v", err)
	}
	if fail, isFail := outcome.Failure(); isFail {
		t.Fatalf("transition failed: %v", fail.Code())
	}

	finalDriver := outcome.State().Entities()[0]
	yearVal, _ := finalDriver.Field("hire_year")
	y, _ := yearVal.Int64()
	if y != 2024 {
		t.Fatalf("expected hire_year 2024, got %d", y)
	}

	durVal, _ := finalDriver.Field("shift_duration")
	durSec, _ := durVal.Duration()
	if durSec != 28800 {
		t.Fatalf("expected shift_duration 28800 seconds, got %d", durSec)
	}
}

func TestRelateWithFromToAliasesReadsDerivation(t *testing.T) {
	dslSource := `
schema {
  entity driver {
    driver_id: string;
    depot: string;
  }
  relation team_mate {
    from: driver;
    to: driver;
  }
}

rule relate_drivers {
  relate driver to driver as team_mate {
    from: select driver;
    to: select driver;
    guard: from.depot == to.depot;
  };
}
`
	req, err := dsl.CompileRequestFromText(dslSource)
	if err != nil {
		t.Fatalf("CompileRequestFromText failed: %v", err)
	}

	compilation, err := semantic.Compile(req)
	if err != nil {
		t.Fatalf("semantic.Compile failed: %v", err)
	}
	if _, ok := compilation.Plan(); !ok {
		failure, _ := compilation.Failure()
		t.Fatalf("plan compilation failed: %v", failure.Diagnostics())
	}
}

func TestNegativeDecimalAndDurationLiterals(t *testing.T) {
	dslSource := `
schema {
  entity account {
    account_id: string;
    balance: decimal;
    penalty: duration;
  }
}

rule update_account {
  select account
  where account.account_id == "A1"
  set account.balance = -12.34,
      account.penalty = -dur("2h");
}
`
	req, err := dsl.CompileRequestFromText(dslSource)
	if err != nil {
		t.Fatalf("CompileRequestFromText failed: %v", err)
	}

	compilation, err := semantic.Compile(req)
	if err != nil {
		t.Fatalf("semantic.Compile failed: %v", err)
	}
	plan, ok := compilation.Plan()
	if !ok {
		failure, _ := compilation.Failure()
		t.Fatalf("plan compilation failed: %v", failure.Diagnostics())
	}

	schema, _ := semantic.NewSchema(req.Schema.EntityDeclarations(), nil)
	lineage, _ := semantic.NewInputLineageID("test", "root")
	a1ID := semantic.SourceEntityID(lineage, "account", "A1")
	initBal, _ := semantic.NewDecimalValue("100.00")
	initPen := semantic.NewDurationValue(0)

	a1Val, _ := semantic.NewStringValue("A1")
	a1, _ := semantic.NewEntity(semantic.EntityRef{Kind: "account", ID: a1ID}, map[semantic.FieldName]semantic.Value{
		"account_id": a1Val,
		"balance":    initBal,
		"penalty":    initPen,
	})
	initialState, _ := semantic.NewState(schema, lineage, []semantic.Entity{a1}, nil)
	world, _ := semantic.NewWorld(nil)
	exec, _ := semantic.NewExecutorIdentity("go", "sha256:0000000000000000000000000000000000000000000000000000000000000001")
	binding, _ := semantic.BindRun(semantic.RunBindingRequest{
		Plan:             plan,
		InitialState:     initialState,
		World:            world,
		ExecutorIdentity: exec,
		Policy:           semantic.ChangesProvenance,
	})

	outcome, err := semantic.ExecuteTransition(binding, "update_account", initialState, semantic.NewJournal())
	if err != nil {
		t.Fatalf("ExecuteTransition: %v", err)
	}
	if fail, isFail := outcome.Failure(); isFail {
		t.Fatalf("transition failed: %v", fail.Code())
	}

	finalAcc := outcome.State().Entities()[0]
	balVal, _ := finalAcc.Field("balance")
	balStr, _ := balVal.Decimal()
	if balStr != "-12.34" {
		t.Fatalf("expected balance -12.34, got %s", balStr)
	}

	penVal, _ := finalAcc.Field("penalty")
	penSec, _ := penVal.Duration()
	if penSec != -7200 {
		t.Fatalf("expected penalty -7200s, got %d", penSec)
	}
}

func TestBuiltinFunctionsLowering(t *testing.T) {
	dslSource := `
schema {
  entity payload {
    id: string;
    raw_str: string;
    trimmed: string optional;
    sub: string optional;
    clamped: int64 optional;
    val_abs: int64 optional;
  }
}

rule process_payload {
  select payload
  where payload.id == "P1"
  set payload.trimmed = trim(payload.raw_str),
      payload.sub = substring(payload.raw_str, 1, 3),
      payload.clamped = clamp(15, 0, 10),
      payload.val_abs = abs(-42);
}
`
	req, err := dsl.CompileRequestFromText(dslSource)
	if err != nil {
		t.Fatalf("CompileRequestFromText failed: %v", err)
	}

	compilation, err := semantic.Compile(req)
	if err != nil {
		t.Fatalf("semantic.Compile failed: %v", err)
	}
	if _, ok := compilation.Plan(); !ok {
		failure, _ := compilation.Failure()
		t.Fatalf("plan compilation failed: %v", failure.Diagnostics())
	}
}
