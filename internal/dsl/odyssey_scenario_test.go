package dsl_test

import (
	"testing"

	"github.com/optimaldynamics/maiden-lane/internal/dsl"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

func TestOdysseyMapperCompletePipeline(t *testing.T) {
	dslSource := `
schema {
  entity driver {
    driver_id: string;
    driving_hours: int64;
    onduty_hours: int64;
    total_duty_hours: int64 optional;
    hos_status: string;
    depot: string;
  }
  entity truck {
    truck_id: string;
    depot: string;
    status: string;
  }
  entity team {
    team_id: string;
    depot: string;
    driver_count: int64;
    total_hours: int64;
    status: string;
  }
  entity shift_log {
    log_id: string;
    shift_name: string;
    hours: int64;
  }
  relation assigned_truck {
    from: team;
    to: truck;
  }
}

# Rule 1: HOS Duty Hours Calculation
rule calculate_hos {
  select driver
  where driver.hos_status == "PENDING"
  group_by driver.driver_id
  having count() >= 1
  set driver.total_duty_hours = (driver.driving_hours + driver.onduty_hours),
      driver.hos_status = if((driver.driving_hours + driver.onduty_hours) <= 14, "LEGAL", "VIOLATION");
}

checkpoint hos_calculated after calculate_hos;

# Rule 2: Form 2-driver teams at each depot for LEGAL drivers
rule form_teams (depends_on: ["calculate_hos"]) {
  insert team {
    select driver
    where driver.hos_status == "LEGAL"
    group_by driver.depot
    having count() == 2
    discriminator: "TEAM_PAIR";
  } set team.team_id = "TEAM_CHI_1",
        team.depot = "CHI",
        team.driver_count = count(),
        team.total_hours = sum(driver.total_duty_hours),
        team.status = "READY";
}

checkpoint teams_formed after form_teams;

# Rule 3: Relate ready teams to available trucks at matching depot
rule assign_trucks (depends_on: ["form_teams"]) {
  relate team to truck as assigned_truck {
    from: select team where team.status == "READY";
    to: select truck where truck.status == "AVAILABLE";
    guard: team.depot == truck.depot;
  };
}

checkpoint trucks_assigned after assign_trucks;

# Rule 4: Split driver into morning and evening shift logs
rule split_driver_shifts (depends_on: ["calculate_hos"]) {
  split driver into shift_log {
    select driver
    where driver.driver_id == "D1"
    retain_source: true;
    partition "MORNING_SHIFT" {
      discriminator: "AM";
      set shift_log.log_id = "LOG_D1_AM",
          shift_log.shift_name = "MORNING",
          shift_log.hours = 6;
    }
    partition "EVENING_SHIFT" {
      discriminator: "PM";
      set shift_log.log_id = "LOG_D1_PM",
          shift_log.shift_name = "EVENING",
          shift_log.hours = 4;
    }
  };
}

profile driver_hos_profile for entity driver {
  require driver.hos_status present as STATUS_REQUIRED;
  require driver.total_duty_hours present as DUTY_HOURS_REQUIRED;
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
		t.Fatalf("compilation failure: %v", failure.Diagnostics())
	}

	schema, err := semantic.NewSchema(req.Schema.EntityDeclarations(), req.Schema.RelationDeclarations())
	if err != nil {
		t.Fatalf("NewSchema: %v", err)
	}

	lineage, err := semantic.NewInputLineageID("maiden-lane.odyssey-scenario", "root-1")
	if err != nil {
		t.Fatalf("NewInputLineageID: %v", err)
	}

	// Create initial drivers: D1 (6h driving + 4h onduty = 10h -> LEGAL), D2 (5h driving + 3h onduty = 8h -> LEGAL)
	// D3 (10h driving + 6h onduty = 16h -> VIOLATION)
	d1ID := semantic.SourceEntityID(lineage, "driver", "D1")
	d2ID := semantic.SourceEntityID(lineage, "driver", "D2")
	d3ID := semantic.SourceEntityID(lineage, "driver", "D3")
	t1ID := semantic.SourceEntityID(lineage, "truck", "T1")

	pendingVal, _ := semantic.NewStringValue("PENDING")
	d1Val, _ := semantic.NewStringValue("D1")
	d2Val, _ := semantic.NewStringValue("D2")
	d3Val, _ := semantic.NewStringValue("D3")
	t1Val, _ := semantic.NewStringValue("T1")
	chiVal, _ := semantic.NewStringValue("CHI")
	availVal, _ := semantic.NewStringValue("AVAILABLE")

	d1, _ := semantic.NewEntity(semantic.EntityRef{Kind: "driver", ID: d1ID}, map[semantic.FieldName]semantic.Value{
		"driver_id":     d1Val,
		"driving_hours": semantic.NewInt64Value(6),
		"onduty_hours":  semantic.NewInt64Value(4),
		"hos_status":    pendingVal,
		"depot":         chiVal,
	})
	d2, _ := semantic.NewEntity(semantic.EntityRef{Kind: "driver", ID: d2ID}, map[semantic.FieldName]semantic.Value{
		"driver_id":     d2Val,
		"driving_hours": semantic.NewInt64Value(5),
		"onduty_hours":  semantic.NewInt64Value(3),
		"hos_status":    pendingVal,
		"depot":         chiVal,
	})
	d3, _ := semantic.NewEntity(semantic.EntityRef{Kind: "driver", ID: d3ID}, map[semantic.FieldName]semantic.Value{
		"driver_id":     d3Val,
		"driving_hours": semantic.NewInt64Value(10),
		"onduty_hours":  semantic.NewInt64Value(6),
		"hos_status":    pendingVal,
		"depot":         chiVal,
	})
	trk1, _ := semantic.NewEntity(semantic.EntityRef{Kind: "truck", ID: t1ID}, map[semantic.FieldName]semantic.Value{
		"truck_id": t1Val,
		"depot":    chiVal,
		"status":   availVal,
	})

	initialState, err := semantic.NewState(schema, lineage, []semantic.Entity{d1, d2, d3, trk1}, nil)
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

	journal := semantic.NewJournal()

	// Step 1: Execute calculate_hos
	step1, err := semantic.ExecuteTransition(binding, "calculate_hos", initialState, journal)
	if err != nil {
		t.Fatalf("Step 1 ExecuteTransition: %v", err)
	}
	if fail, isFail := step1.Failure(); isFail {
		t.Fatalf("Step 1 failed: %v", fail.Code())
	}
	journal = step1.Journal()
	state1 := step1.State()

	for _, e := range state1.Entities() {
		if e.Ref().Kind != "driver" {
			continue
		}
		statusVal, _ := e.Field("hos_status")
		totVal, _ := e.Field("total_duty_hours")
		s, _ := statusVal.String()
		tot, _ := totVal.Int64()
		idVal, _ := e.Field("driver_id")
		did, _ := idVal.String()

		if did == "D1" && (tot != 10 || s != "LEGAL") {
			t.Fatalf("D1: want 10h LEGAL, got %d %s", tot, s)
		}
		if did == "D2" && (tot != 8 || s != "LEGAL") {
			t.Fatalf("D2: want 8h LEGAL, got %d %s", tot, s)
		}
		if did == "D3" && (tot != 16 || s != "VIOLATION") {
			t.Fatalf("D3: want 16h VIOLATION, got %d %s", tot, s)
		}
	}

	// Step 2: Execute form_teams
	step2, err := semantic.ExecuteTransition(binding, "form_teams", state1, journal)
	if err != nil {
		t.Fatalf("Step 2 ExecuteTransition: %v", err)
	}
	if fail, isFail := step2.Failure(); isFail {
		t.Fatalf("Step 2 failed: %v", fail.Code())
	}
	journal = step2.Journal()
	state2 := step2.State()

	var teamEntity *semantic.Entity
	for _, e := range state2.Entities() {
		if e.Ref().Kind == "team" {
			teamEntity = &e
			break
		}
	}
	if teamEntity == nil {
		t.Fatalf("expected team entity created in state 2")
	}
	teamDriversVal, _ := teamEntity.Field("driver_count")
	cnt, _ := teamDriversVal.Int64()
	if cnt != 2 {
		t.Fatalf("expected team driver_count 2, got %d", cnt)
	}

	// Step 3: Execute assign_trucks
	step3, err := semantic.ExecuteTransition(binding, "assign_trucks", state2, journal)
	if err != nil {
		t.Fatalf("Step 3 ExecuteTransition: %v", err)
	}
	if fail, isFail := step3.Failure(); isFail {
		t.Fatalf("Step 3 failed: %v", fail.Code())
	}
	journal = step3.Journal()
	state3 := step3.State()

	if len(state3.Relations()) != 1 {
		t.Fatalf("expected 1 assigned_truck relation, got %d", len(state3.Relations()))
	}
	rel := state3.Relations()[0]
	if rel.Kind != "assigned_truck" || rel.From.Kind != "team" || rel.To.Kind != "truck" {
		t.Fatalf("unexpected relation: %+v", rel)
	}

	// Step 4: Execute split_driver_shifts
	step4, err := semantic.ExecuteTransition(binding, "split_driver_shifts", state3, journal)
	if err != nil {
		t.Fatalf("Step 4 ExecuteTransition: %v", err)
	}
	if fail, isFail := step4.Failure(); isFail {
		t.Fatalf("Step 4 failed: %v", fail.Code())
	}
	state4 := step4.State()

	shiftLogCount := 0
	for _, e := range state4.Entities() {
		if e.Ref().Kind == "shift_log" {
			shiftLogCount++
		}
	}
	if shiftLogCount != 2 {
		t.Fatalf("expected 2 shift_log entities created, got %d", shiftLogCount)
	}
}
