package semantic

import (
	"testing"
)

func buildStructuralTestSchema(t *testing.T) Schema {
	t.Helper()
	driverFields := []FieldDeclaration{
		{Name: "driver_id", Kind: ValueString},
		{Name: "status", Kind: ValueString},
		{Name: "depot", Kind: ValueString},
		{Name: "hours", Kind: ValueInt64},
	}
	truckFields := []FieldDeclaration{
		{Name: "truck_id", Kind: ValueString},
		{Name: "depot", Kind: ValueString},
	}
	teamFields := []FieldDeclaration{
		{Name: "depot", Kind: ValueString},
		{Name: "driver_count", Kind: ValueInt64},
		{Name: "total_hours", Kind: ValueInt64},
	}
	logFields := []FieldDeclaration{
		{Name: "shift_type", Kind: ValueString},
		{Name: "hours", Kind: ValueInt64},
	}
	entities := []EntityDeclaration{
		{Kind: "driver", Fields: driverFields},
		{Kind: "truck", Fields: truckFields},
		{Kind: "team", Fields: teamFields},
		{Kind: "shift_log", Fields: logFields},
	}
	relations := []RelationDeclaration{
		{Kind: "assigned_truck", FromKind: "driver", ToKind: "truck"},
		{Kind: "team_truck", FromKind: "team", ToKind: "truck"},
		{Kind: "mentor", FromKind: "driver", ToKind: "driver"},
	}
	schema, err := NewSchema(entities, relations)
	if err != nil {
		t.Fatalf("build test schema: %v", err)
	}
	return schema
}

func allMembersExists(path FieldPath) Expr {
	return Expr{Kind: ExprAllMembers, Args: []Expr{{Kind: ExprExists, Field: path}}}
}

func equalExpr(a, b Expr) Expr {
	return Expr{Kind: ExprEqual, Args: []Expr{a, b}}
}

func buildStructuralTestInitialState(t *testing.T, schema Schema) State {
	t.Helper()
	lineage := InputLineageID("sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	d1ID := SourceEntityID(lineage, "driver", "D1")
	d2ID := SourceEntityID(lineage, "driver", "D2")
	t1ID := SourceEntityID(lineage, "truck", "T1")

	d1, err := NewEntity(EntityRef{Kind: "driver", ID: d1ID}, map[FieldName]Value{
		"driver_id": mustString(t, "D1"),
		"status":    mustString(t, "AVAILABLE"),
		"depot":     mustString(t, "CHI"),
		"hours":     NewInt64Value(10),
	})
	if err != nil {
		t.Fatalf("new entity d1: %v", err)
	}
	d2, err := NewEntity(EntityRef{Kind: "driver", ID: d2ID}, map[FieldName]Value{
		"driver_id": mustString(t, "D2"),
		"status":    mustString(t, "AVAILABLE"),
		"depot":     mustString(t, "CHI"),
		"hours":     NewInt64Value(15),
	})
	if err != nil {
		t.Fatalf("new entity d2: %v", err)
	}
	trk1, err := NewEntity(EntityRef{Kind: "truck", ID: t1ID}, map[FieldName]Value{
		"truck_id": mustString(t, "T1"),
		"depot":    mustString(t, "CHI"),
	})
	if err != nil {
		t.Fatalf("new entity trk1: %v", err)
	}

	rel := Relation{Kind: "assigned_truck", From: d1.Ref(), To: trk1.Ref()}

	state, err := NewState(schema, lineage, []Entity{d1, d2, trk1}, []Relation{rel})
	if err != nil {
		t.Fatalf("new state: %v", err)
	}
	return state
}

func mustStructuralPlan(t *testing.T, schema Schema, declarations []TransformationDeclaration) Plan {
	t.Helper()
	compilation, err := Compile(CompileRequest{
		Schema:                   schema.Declaration(),
		Rules:                    RulesetDeclaration{Transformations: declarations},
		CompilerSemanticsVersion: "semantics.v1",
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	plan, ok := compilation.Plan()
	if !ok {
		failure, _ := compilation.Failure()
		t.Fatalf("ruleset did not compile: %v", failure.Diagnostics())
	}
	return plan
}

func mustStructuralBinding(t *testing.T, plan Plan, state State) RunBinding {
	t.Helper()
	world, err := NewWorld(nil)
	if err != nil {
		t.Fatalf("NewWorld: %v", err)
	}
	return mustBindRun(t, plan, state, world, mustExecutorIdentityForTests("test", Digest("sha256:"+
		"0000000000000000000000000000000000000000000000000000000000000000")))
}

func mustUndoTransition(t *testing.T, initial State, outcome TransitionOutcome) State {
	t.Helper()
	applyOutcome, err := ApplyPatch(initial, outcome.Patch())
	if err != nil {
		t.Fatalf("ApplyPatch: %v", err)
	}
	receipt, ok := applyOutcome.Receipt()
	if !ok {
		t.Fatalf("ApplyPatch produced no receipt")
	}
	undoOutcome, err := UndoPatch(outcome.State(), outcome.Patch(), receipt)
	if err != nil {
		t.Fatalf("UndoPatch: %v", err)
	}
	if undoOutcome.Failure() != nil {
		t.Fatalf("UndoPatch failure: %v", undoOutcome.Failure().Code())
	}
	return undoOutcome.State()
}

func TestExecuteInsertEntity(t *testing.T) {
	schema := buildStructuralTestSchema(t)
	initial := buildStructuralTestInitialState(t, schema)

	// Insert a team entity for CHI depot drivers
	groupBy := fieldExpr("driver.depot")
	rule := TransformationDeclaration{
		ID:       "form_team.v1",
		Operator: OperatorInsertEntity,
		DeclaredReads: []FieldPath{
			"driver.depot", "driver.hours",
		},
		DeclaredWrites: []FieldPath{
			"team.depot", "team.driver_count", "team.total_hours",
		},
		InsertEntity: &InsertEntityDeclaration{
			Selector: Selector{
				Kind:    "driver",
				GroupBy: &groupBy,
				Members: Cardinality{Kind: CardinalityExactly, Count: 2},
			},
			TargetKind:    "team",
			Discriminator: stringLiteral(t, "CHI"),
			Guard:         equalExpr(countExpr(), intLiteral(2)),
			Assignments: []FieldAssignment{
				{Target: "team.depot", Value: stringLiteral(t, "CHI")},
				{Target: "team.driver_count", Value: countExpr()},
				{Target: "team.total_hours", Value: sumExpr("driver.hours")},
			},
		},
	}

	plan := mustStructuralPlan(t, schema, []TransformationDeclaration{rule})
	binding := mustStructuralBinding(t, plan, initial)

	outcome := mustAcceptedTransition(t, binding, "form_team.v1", initial, Journal{})
	if len(outcome.Journal().Entries()) != 1 {
		t.Fatalf("want 1 journal entry, got %d", len(outcome.Journal().Entries()))
	}

	finalState := outcome.State()
	entities := finalState.Entities()
	if len(entities) != 4 { // 2 drivers, 1 truck, 1 team
		t.Fatalf("want 4 entities, got %d", len(entities))
	}

	// Verify team entity
	var teamEntity *Entity
	for _, e := range entities {
		if e.Ref().Kind == "team" {
			teamEntity = &e
			break
		}
	}
	if teamEntity == nil {
		t.Fatalf("expected team entity to exist in final state")
	}
	assertFieldEquals(t, *teamEntity, "depot", mustString(t, "CHI"))
	assertFieldEquals(t, *teamEntity, "driver_count", NewInt64Value(2))
	assertFieldEquals(t, *teamEntity, "total_hours", NewInt64Value(25))

	// Verify Undo restores exact initial state
	restored := mustUndoTransition(t, initial, outcome)
	if restored.Digest() != initial.Digest() {
		t.Fatalf("restored state digest %s != initial state digest %s", restored.Digest(), initial.Digest())
	}
}

func TestExecuteDeleteEntity(t *testing.T) {
	schema := buildStructuralTestSchema(t)
	initial := buildStructuralTestInitialState(t, schema)

	// Delete driver D2 (which has no relations)
	where := equalExpr(fieldExpr("driver.driver_id"), stringLiteral(t, "D2"))
	rule := TransformationDeclaration{
		ID:       "retire_d2.v1",
		Operator: OperatorDeleteEntity,
		DeclaredReads: []FieldPath{
			"driver.driver_id",
		},
		DeleteEntity: &DeleteEntityDeclaration{
			Selector: Selector{
				Kind:    "driver",
				Where:   &where,
				Members: Cardinality{Kind: CardinalityAny},
			},
			Guard: allMembersExists("driver.driver_id"),
		},
	}

	plan := mustStructuralPlan(t, schema, []TransformationDeclaration{rule})
	binding := mustStructuralBinding(t, plan, initial)

	outcome := mustAcceptedTransition(t, binding, "retire_d2.v1", initial, Journal{})

	finalState := outcome.State()
	if len(finalState.Entities()) != 2 { // D1 and T1 remain
		t.Fatalf("want 2 entities, got %d", len(finalState.Entities()))
	}

	// Verify Undo restores D2
	restored := mustUndoTransition(t, initial, outcome)
	if restored.Digest() != initial.Digest() {
		t.Fatalf("restored state digest %s != initial %s", restored.Digest(), initial.Digest())
	}
}

func TestExecuteRelateAndUnrelateEntities(t *testing.T) {
	schema := buildStructuralTestSchema(t)
	initial := buildStructuralTestInitialState(t, schema)

	// Step 1: Relate D2 to T1
	d2Where := equalExpr(fieldExpr("driver.driver_id"), stringLiteral(t, "D2"))
	t1Where := equalExpr(fieldExpr("truck.truck_id"), stringLiteral(t, "T1"))

	relateRule := TransformationDeclaration{
		ID:       "assign_d2_t1.v1",
		Operator: OperatorRelateEntities,
		DeclaredReads: []FieldPath{
			"driver.driver_id", "driver.depot", "truck.truck_id", "truck.depot",
		},
		RelateEntities: &RelateEntitiesDeclaration{
			RelationKind: "assigned_truck",
			FromSelector: Selector{Kind: "driver", Where: &d2Where, Members: Cardinality{Kind: CardinalityAny}},
			ToSelector:   Selector{Kind: "truck", Where: &t1Where, Members: Cardinality{Kind: CardinalityAny}},
			Guard:        equalExpr(fieldExpr("driver.depot"), fieldExpr("truck.depot")),
		},
	}

	// Step 2: Unrelate D1 from T1
	d1Where := equalExpr(fieldExpr("driver.driver_id"), stringLiteral(t, "D1"))
	unrelateRule := TransformationDeclaration{
		ID:       "unassign_d1_t1.v1",
		Operator: OperatorUnrelateEntities,
		DeclaredReads: []FieldPath{
			"driver.driver_id", "driver.depot", "truck.truck_id", "truck.depot",
		},
		After: []RuleID{"assign_d2_t1.v1"},
		UnrelateEntities: &UnrelateEntitiesDeclaration{
			RelationKind: "assigned_truck",
			FromSelector: Selector{Kind: "driver", Where: &d1Where, Members: Cardinality{Kind: CardinalityAny}},
			ToSelector:   Selector{Kind: "truck", Where: &t1Where, Members: Cardinality{Kind: CardinalityAny}},
			Guard:        equalExpr(fieldExpr("driver.depot"), fieldExpr("truck.depot")),
		},
	}

	plan := mustStructuralPlan(t, schema, []TransformationDeclaration{relateRule, unrelateRule})
	binding := mustStructuralBinding(t, plan, initial)

	// Execute Step 1 (Relate)
	outcome1 := mustAcceptedTransition(t, binding, "assign_d2_t1.v1", initial, Journal{})
	state1 := outcome1.State()
	if len(state1.Relations()) != 2 {
		t.Fatalf("want 2 relations after relate, got %d", len(state1.Relations()))
	}

	// Execute Step 2 (Unrelate)
	outcome2 := mustAcceptedTransition(t, binding, "unassign_d1_t1.v1", state1, outcome1.Journal())
	state2 := outcome2.State()
	if len(state2.Relations()) != 1 {
		t.Fatalf("want 1 relation after unrelate, got %d", len(state2.Relations()))
	}

	// Verify relation is D2 -> T1
	rel := state2.Relations()[0]
	if rel.From.ID != SourceEntityID(initial.InputLineageID(), "driver", "D2") {
		t.Fatalf("expected relation from D2, got %v", rel.From)
	}

	// Undo Step 2
	undo2 := mustUndoTransition(t, state1, outcome2)
	if undo2.Digest() != state1.Digest() {
		t.Fatalf("undo2 digest %s != state1 %s", undo2.Digest(), state1.Digest())
	}

	// Undo Step 1
	undo1 := mustUndoTransition(t, initial, outcome1)
	if undo1.Digest() != initial.Digest() {
		t.Fatalf("undo1 digest %s != initial %s", undo1.Digest(), initial.Digest())
	}
}

func TestExecuteMergeEntitiesWithRelationReanchoring(t *testing.T) {
	schema := buildStructuralTestSchema(t)
	initial := buildStructuralTestInitialState(t, schema)

	// Merge D1 and D2 into a unified master driver with ReanchorRelations = true and RetainSources = false
	groupBy := fieldExpr("driver.depot")
	mergeRule := TransformationDeclaration{
		ID:       "merge_drivers.v1",
		Operator: OperatorMergeEntities,
		DeclaredReads: []FieldPath{
			"driver.depot", "driver.hours",
		},
		DeclaredWrites: []FieldPath{
			"driver.depot", "driver.driver_id", "driver.status", "driver.hours",
		},
		MergeEntities: &MergeEntitiesDeclaration{
			Selector: Selector{
				Kind:    "driver",
				GroupBy: &groupBy,
				Members: Cardinality{Kind: CardinalityExactly, Count: 2},
			},
			TargetKind:        "driver",
			Discriminator:     stringLiteral(t, "CHI"),
			Guard:             equalExpr(countExpr(), intLiteral(2)),
			RetainSources:     false,
			ReanchorRelations: true,
			Assignments: []FieldAssignment{
				{Target: "driver.driver_id", Value: stringLiteral(t, "D_MERGED")},
				{Target: "driver.status", Value: stringLiteral(t, "AVAILABLE")},
				{Target: "driver.depot", Value: stringLiteral(t, "CHI")},
				{Target: "driver.hours", Value: sumExpr("driver.hours")},
			},
		},
	}

	plan := mustStructuralPlan(t, schema, []TransformationDeclaration{mergeRule})
	binding := mustStructuralBinding(t, plan, initial)

	outcome := mustAcceptedTransition(t, binding, "merge_drivers.v1", initial, Journal{})

	state := outcome.State()
	// Initial had 3 entities: D1, D2, T1.
	// D1, D2 were deleted; D_MERGED was inserted; T1 remains.
	// Total entities = 2 (D_MERGED, T1).
	if len(state.Entities()) != 2 {
		t.Fatalf("want 2 entities after merge, got %d", len(state.Entities()))
	}

	// Relations: D1->T1 was un-related, D_MERGED->T1 was related.
	if len(state.Relations()) != 1 {
		t.Fatalf("want 1 reanchored relation, got %d", len(state.Relations()))
	}
	reanchored := state.Relations()[0]
	if reanchored.From.Kind != "driver" || reanchored.To.Kind != "truck" {
		t.Fatalf("expected driver->truck relation, got %v", reanchored)
	}

	// Verify Undo restores D1, D2, D1->T1 relation, and removes D_MERGED
	restored := mustUndoTransition(t, initial, outcome)
	if restored.Digest() != initial.Digest() {
		t.Fatalf("restored state digest %s != initial %s", restored.Digest(), initial.Digest())
	}
}

func TestExecuteSplitEntity(t *testing.T) {
	schema := buildStructuralTestSchema(t)
	initial := buildStructuralTestInitialState(t, schema)

	// Split D2 into two shift_logs (morning: 6 hours, evening: 4 hours) with RetainSource = false
	where := equalExpr(fieldExpr("driver.driver_id"), stringLiteral(t, "D2"))
	splitRule := TransformationDeclaration{
		ID:       "split_d2_shifts.v1",
		Operator: OperatorSplitEntity,
		DeclaredReads: []FieldPath{
			"driver.driver_id",
		},
		DeclaredWrites: []FieldPath{
			"shift_log.hours", "shift_log.shift_type",
		},
		SplitEntity: &SplitEntityDeclaration{
			Selector: Selector{
				Kind:    "driver",
				Where:   &where,
				Members: Cardinality{Kind: CardinalityAny},
			},
			TargetKind:   "shift_log",
			Guard:        allMembersExists("driver.driver_id"),
			RetainSource: false,
			Partitions: []PartitionDeclaration{
				{
					Discriminator: stringLiteral(t, "AM"),
					Assignments: []FieldAssignment{
						{Target: "shift_log.shift_type", Value: stringLiteral(t, "MORNING")},
						{Target: "shift_log.hours", Value: intLiteral(6)},
					},
				},
				{
					Discriminator: stringLiteral(t, "PM"),
					Assignments: []FieldAssignment{
						{Target: "shift_log.shift_type", Value: stringLiteral(t, "EVENING")},
						{Target: "shift_log.hours", Value: intLiteral(4)},
					},
				},
			},
		},
	}

	plan := mustStructuralPlan(t, schema, []TransformationDeclaration{splitRule})
	binding := mustStructuralBinding(t, plan, initial)

	outcome := mustAcceptedTransition(t, binding, "split_d2_shifts.v1", initial, Journal{})

	state := outcome.State()
	// Initial: D1, D2, T1 (3). D2 deleted, 2 shift_logs created. Total = 4.
	if len(state.Entities()) != 4 {
		t.Fatalf("want 4 entities after split, got %d", len(state.Entities()))
	}

	// Verify Undo restores D2, removes the 2 shift_logs
	restored := mustUndoTransition(t, initial, outcome)
	if restored.Digest() != initial.Digest() {
		t.Fatalf("restored state digest %s != initial %s", restored.Digest(), initial.Digest())
	}
}

func TestDeleteEntityRefusesDanglingRelation(t *testing.T) {
	schema := buildStructuralTestSchema(t)
	initial := buildStructuralTestInitialState(t, schema)

	// Attempt to delete D1 (which has relation assigned_truck to T1)
	where := equalExpr(fieldExpr("driver.driver_id"), stringLiteral(t, "D1"))
	rule := TransformationDeclaration{
		ID:       "delete_d1_with_relation.v1",
		Operator: OperatorDeleteEntity,
		DeclaredReads: []FieldPath{
			"driver.driver_id",
		},
		DeleteEntity: &DeleteEntityDeclaration{
			Selector: Selector{
				Kind:    "driver",
				Where:   &where,
				Members: Cardinality{Kind: CardinalityAny},
			},
			Guard: allMembersExists("driver.driver_id"),
		},
	}

	plan := mustStructuralPlan(t, schema, []TransformationDeclaration{rule})
	binding := mustStructuralBinding(t, plan, initial)

	outcome, err := ExecuteTransition(binding, "delete_d1_with_relation.v1", initial, Journal{})
	if err != nil {
		t.Fatalf("unexpected execution error: %v", err)
	}
	failure, ok := outcome.Failure()
	if !ok {
		t.Fatalf("expected rejection for dangling relation")
	}
	if failure.Code() != string(OperationDanglingRelation) {
		t.Fatalf("want rejection code %s, got %s", OperationDanglingRelation, failure.Code())
	}
}

func TestExecuteSameKindRelationWithFromToDisambiguation(t *testing.T) {
	schema := buildStructuralTestSchema(t)
	initial := buildStructuralTestInitialState(t, schema)

	// D2 (hours 15) becomes mentor of D1 (hours 10) in CHI depot
	// Guard: less(to.hours, from.hours) && equal(from.depot, to.depot) && not(equal(from.driver_id, to.driver_id))
	guard := Expr{
		Kind: ExprAll,
		Args: []Expr{
			{Kind: ExprLess, Args: []Expr{
				{Kind: ExprField, Field: "to.hours"},
				{Kind: ExprField, Field: "from.hours"},
			}},
			{Kind: ExprEqual, Args: []Expr{
				{Kind: ExprField, Field: "from.depot"},
				{Kind: ExprField, Field: "to.depot"},
			}},
			{Kind: ExprNot, Args: []Expr{
				{Kind: ExprEqual, Args: []Expr{
					{Kind: ExprField, Field: "from.driver_id"},
					{Kind: ExprField, Field: "to.driver_id"},
				}},
			}},
		},
	}

	rule := TransformationDeclaration{
		ID:       "assign_driver_mentor.v1",
		Operator: OperatorRelateEntities,
		DeclaredReads: []FieldPath{
			"driver.hours", "driver.depot", "driver.driver_id",
		},
		RelateEntities: &RelateEntitiesDeclaration{
			RelationKind: "mentor",
			FromSelector: Selector{Kind: "driver", Members: Cardinality{Kind: CardinalityAny}},
			ToSelector:   Selector{Kind: "driver", Members: Cardinality{Kind: CardinalityAny}},
			Guard:        guard,
		},
	}

	plan := mustStructuralPlan(t, schema, []TransformationDeclaration{rule})
	binding := mustStructuralBinding(t, plan, initial)

	outcome := mustAcceptedTransition(t, binding, "assign_driver_mentor.v1", initial, Journal{})
	finalState := outcome.State()

	// Initial had 1 relation (D1 -> T1). Now we should have 2 relations (D1 -> T1, D2 -> D1 mentor).
	relations := finalState.Relations()
	if len(relations) != 2 {
		t.Fatalf("expected 2 relations, got %d", len(relations))
	}

	d2ID := SourceEntityID(initial.InputLineageID(), "driver", "D2")
	d1ID := SourceEntityID(initial.InputLineageID(), "driver", "D1")
	var foundMentor bool
	for _, r := range relations {
		if r.Kind == "mentor" {
			if r.From.ID != d2ID || r.To.ID != d1ID {
				t.Fatalf("expected mentor relation from D2 to D1, got from %v to %v", r.From, r.To)
			}
			foundMentor = true
		}
	}
	if !foundMentor {
		t.Fatal("mentor relation was not found in final state")
	}

	// Verify Undo restores exact initial state
	restored := mustUndoTransition(t, initial, outcome)
	if restored.Digest() != initial.Digest() {
		t.Fatalf("restored digest %s != initial %s", restored.Digest(), initial.Digest())
	}
}

func TestExecuteSplitEntityWithIncidentRelations(t *testing.T) {
	schema := buildStructuralTestSchema(t)
	initial := buildStructuralTestInitialState(t, schema)

	// D1 has an assigned_truck relation to T1. Splitting D1 with RetainSource = false
	// should clean up the assigned_truck relation and insert split children.
	where := equalExpr(fieldExpr("driver.driver_id"), stringLiteral(t, "D1"))
	splitRule := TransformationDeclaration{
		ID:       "split_d1_with_relations.v1",
		Operator: OperatorSplitEntity,
		DeclaredReads: []FieldPath{
			"driver.driver_id",
		},
		DeclaredWrites: []FieldPath{
			"shift_log.hours", "shift_log.shift_type",
		},
		SplitEntity: &SplitEntityDeclaration{
			Selector: Selector{
				Kind:    "driver",
				Where:   &where,
				Members: Cardinality{Kind: CardinalityAny},
			},
			TargetKind:   "shift_log",
			Guard:        allMembersExists("driver.driver_id"),
			RetainSource: false,
			Partitions: []PartitionDeclaration{
				{
					Discriminator: stringLiteral(t, "AM"),
					Assignments: []FieldAssignment{
						{Target: "shift_log.shift_type", Value: stringLiteral(t, "MORNING")},
						{Target: "shift_log.hours", Value: intLiteral(5)},
					},
				},
			},
		},
	}

	plan := mustStructuralPlan(t, schema, []TransformationDeclaration{splitRule})
	binding := mustStructuralBinding(t, plan, initial)

	outcome := mustAcceptedTransition(t, binding, "split_d1_with_relations.v1", initial, Journal{})
	finalState := outcome.State()

	// D1 deleted, D2 and T1 remain, 1 shift_log inserted -> 3 entities
	if len(finalState.Entities()) != 3 {
		t.Fatalf("want 3 entities, got %d", len(finalState.Entities()))
	}

	// Relation D1 -> T1 should be removed -> 0 relations
	if len(finalState.Relations()) != 0 {
		t.Fatalf("want 0 relations after splitting D1, got %d", len(finalState.Relations()))
	}

	// Verify Undo restores D1, T1, D2, and the relation D1 -> T1
	restored := mustUndoTransition(t, initial, outcome)
	if restored.Digest() != initial.Digest() {
		t.Fatalf("restored digest %s != initial %s", restored.Digest(), initial.Digest())
	}
}

func TestExecuteMergeEntitiesWithDuplicateRelationsAndIntraGroupRelation(t *testing.T) {
	schema := buildStructuralTestSchema(t)
	initialState := buildStructuralTestInitialState(t, schema)

	// Add D2 -> T1 assigned_truck relation and D2 -> D1 mentor relation to state
	d1Ref := EntityRef{Kind: "driver", ID: SourceEntityID(initialState.InputLineageID(), "driver", "D1")}
	d2Ref := EntityRef{Kind: "driver", ID: SourceEntityID(initialState.InputLineageID(), "driver", "D2")}
	t1Ref := EntityRef{Kind: "truck", ID: SourceEntityID(initialState.InputLineageID(), "truck", "T1")}

	initialWithMoreRelations, err := NewState(schema, initialState.InputLineageID(), initialState.Entities(), []Relation{
		{Kind: "assigned_truck", From: d1Ref, To: t1Ref},
		{Kind: "assigned_truck", From: d2Ref, To: t1Ref},
		{Kind: "mentor", From: d2Ref, To: d1Ref},
	})
	if err != nil {
		t.Fatalf("NewState with more relations: %v", err)
	}

	// Merge D1 and D2 into D_MERGED with ReanchorRelations = true
	groupBy := Expr{Kind: ExprField, Field: "driver.depot"}
	mergeRule := TransformationDeclaration{
		ID:       "merge_with_shared_relations.v1",
		Operator: OperatorMergeEntities,
		DeclaredReads: []FieldPath{
			"driver.depot",
		},
		DeclaredWrites: []FieldPath{
			"driver.driver_id", "driver.status", "driver.depot",
		},
		MergeEntities: &MergeEntitiesDeclaration{
			Selector: Selector{
				Kind:    "driver",
				GroupBy: &groupBy,
				Members: Cardinality{Kind: CardinalityExactly, Count: 2},
			},
			TargetKind:        "driver",
			Discriminator:     stringLiteral(t, "CHI"),
			Guard:             equalExpr(countExpr(), intLiteral(2)),
			RetainSources:     false,
			ReanchorRelations: true,
			Assignments: []FieldAssignment{
				{Target: "driver.driver_id", Value: stringLiteral(t, "D_MERGED")},
				{Target: "driver.status", Value: stringLiteral(t, "AVAILABLE")},
				{Target: "driver.depot", Value: stringLiteral(t, "CHI")},
			},
		},
	}

	plan := mustStructuralPlan(t, schema, []TransformationDeclaration{mergeRule})
	binding := mustStructuralBinding(t, plan, initialWithMoreRelations)

	outcome := mustAcceptedTransition(t, binding, "merge_with_shared_relations.v1", initialWithMoreRelations, Journal{})
	finalState := outcome.State()

	// D1, D2 deleted; D_MERGED inserted; T1 remains -> 2 entities
	if len(finalState.Entities()) != 2 {
		t.Fatalf("want 2 entities, got %d", len(finalState.Entities()))
	}

	// Relations: D1->T1 and D2->T1 should deduplicate into exactly 1 D_MERGED->T1 relation.
	// Intra-group relation D2->D1 should be dissolved.
	if len(finalState.Relations()) != 1 {
		t.Fatalf("want exactly 1 reanchored relation, got %d: %v", len(finalState.Relations()), finalState.Relations())
	}
	reanchored := finalState.Relations()[0]
	if reanchored.Kind != "assigned_truck" || reanchored.From.Kind != "driver" || reanchored.To != t1Ref {
		t.Fatalf("unexpected reanchored relation: %v", reanchored)
	}

	// Verify Undo restores exact initial state with all 3 relations
	restored := mustUndoTransition(t, initialWithMoreRelations, outcome)
	if restored.Digest() != initialWithMoreRelations.Digest() {
		t.Fatalf("restored digest %s != initial %s", restored.Digest(), initialWithMoreRelations.Digest())
	}
}

func TestExecuteInsertEntityUngroupedPerMember(t *testing.T) {
	schema := buildStructuralTestSchema(t)
	initial := buildStructuralTestInitialState(t, schema)

	// Insert a shift_log entity for each driver without GroupBy (member-scoped)
	insertRule := TransformationDeclaration{
		ID:       "insert_driver_shifts.v1",
		Operator: OperatorInsertEntity,
		DeclaredReads: []FieldPath{
			"driver.driver_id", "driver.hours",
		},
		DeclaredWrites: []FieldPath{
			"shift_log.hours", "shift_log.shift_type",
		},
		InsertEntity: &InsertEntityDeclaration{
			Selector: Selector{
				Kind:    "driver",
				Members: Cardinality{Kind: CardinalityAny},
			},
			TargetKind:    "shift_log",
			Discriminator: stringLiteral(t, "DAILY"),
			Guard:         allMembersExists("driver.driver_id"),
			Assignments: []FieldAssignment{
				{Target: "shift_log.shift_type", Value: stringLiteral(t, "DAILY")},
				{Target: "shift_log.hours", Value: fieldExpr("driver.hours")},
			},
		},
	}

	plan := mustStructuralPlan(t, schema, []TransformationDeclaration{insertRule})
	binding := mustStructuralBinding(t, plan, initial)

	outcome := mustAcceptedTransition(t, binding, "insert_driver_shifts.v1", initial, Journal{})
	finalState := outcome.State()

	// Initial had 3 entities (D1, D2, T1). 2 shift_logs inserted -> 5 entities.
	if len(finalState.Entities()) != 5 {
		t.Fatalf("want 5 entities, got %d", len(finalState.Entities()))
	}
	shiftCount := 0
	for _, e := range finalState.Entities() {
		if e.Ref().Kind == "shift_log" {
			shiftCount++
		}
	}
	if shiftCount != 2 {
		t.Fatalf("expected 2 shift_log entities, got %d", shiftCount)
	}

	// Verify Undo
	restored := mustUndoTransition(t, initial, outcome)
	if restored.Digest() != initial.Digest() {
		t.Fatalf("restored digest %s != initial %s", restored.Digest(), initial.Digest())
	}
}

func TestExecuteSplitEntityMultipleWithIntraPopulationRelation(t *testing.T) {
	schema := buildStructuralTestSchema(t)
	initialState := buildStructuralTestInitialState(t, schema)

	// Add D2 -> D1 mentor relation
	d1Ref := EntityRef{Kind: "driver", ID: SourceEntityID(initialState.InputLineageID(), "driver", "D1")}
	d2Ref := EntityRef{Kind: "driver", ID: SourceEntityID(initialState.InputLineageID(), "driver", "D2")}

	initialWithMentor, err := NewState(schema, initialState.InputLineageID(), initialState.Entities(), []Relation{
		{Kind: "mentor", From: d2Ref, To: d1Ref},
	})
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}

	// Split both D1 and D2 with RetainSource = false
	splitRule := TransformationDeclaration{
		ID:       "split_both_drivers.v1",
		Operator: OperatorSplitEntity,
		DeclaredReads: []FieldPath{
			"driver.driver_id",
		},
		DeclaredWrites: []FieldPath{
			"shift_log.hours", "shift_log.shift_type",
		},
		SplitEntity: &SplitEntityDeclaration{
			Selector: Selector{
				Kind:    "driver",
				Members: Cardinality{Kind: CardinalityAny},
			},
			TargetKind:   "shift_log",
			Guard:        allMembersExists("driver.driver_id"),
			RetainSource: false,
			Partitions: []PartitionDeclaration{
				{
					Discriminator: stringLiteral(t, "AM"),
					Assignments: []FieldAssignment{
						{Target: "shift_log.shift_type", Value: stringLiteral(t, "MORNING")},
						{Target: "shift_log.hours", Value: intLiteral(5)},
					},
				},
			},
		},
	}

	plan := mustStructuralPlan(t, schema, []TransformationDeclaration{splitRule})
	binding := mustStructuralBinding(t, plan, initialWithMentor)

	outcome := mustAcceptedTransition(t, binding, "split_both_drivers.v1", initialWithMentor, Journal{})
	finalState := outcome.State()

	// D1, D2 deleted, T1 remains, 2 shift_logs created -> 3 entities
	if len(finalState.Entities()) != 3 {
		t.Fatalf("want 3 entities, got %d", len(finalState.Entities()))
	}
	// Relations: D2->D1 should be removed without duplicate unrelate error -> 0 relations
	if len(finalState.Relations()) != 0 {
		t.Fatalf("want 0 relations, got %d", len(finalState.Relations()))
	}

	// Verify Undo
	restored := mustUndoTransition(t, initialWithMentor, outcome)
	if restored.Digest() != initialWithMentor.Digest() {
		t.Fatalf("restored digest %s != initial %s", restored.Digest(), initialWithMentor.Digest())
	}
}

func TestExecuteMergeEntitiesMultipleGroupsCrossGroupRelation(t *testing.T) {
	schema := buildStructuralTestSchema(t)
	initialStateSample := buildStructuralTestInitialState(t, schema)
	lineage := initialStateSample.InputLineageID()

	strVal := func(s string) Value {
		v, _ := NewStringValue(s)
		return v
	}

	dChi1, _ := NewEntity(EntityRef{Kind: "driver", ID: SourceEntityID(lineage, "driver", "D_CHI_1")}, map[FieldName]Value{
		"driver_id": strVal("D_CHI_1"), "status": strVal("ACTIVE"), "depot": strVal("CHI"), "hours": NewInt64Value(10),
	})
	dChi2, _ := NewEntity(EntityRef{Kind: "driver", ID: SourceEntityID(lineage, "driver", "D_CHI_2")}, map[FieldName]Value{
		"driver_id": strVal("D_CHI_2"), "status": strVal("ACTIVE"), "depot": strVal("CHI"), "hours": NewInt64Value(8),
	})
	dNyc1, _ := NewEntity(EntityRef{Kind: "driver", ID: SourceEntityID(lineage, "driver", "D_NYC_1")}, map[FieldName]Value{
		"driver_id": strVal("D_NYC_1"), "status": strVal("ACTIVE"), "depot": strVal("NYC"), "hours": NewInt64Value(12),
	})
	dNyc2, _ := NewEntity(EntityRef{Kind: "driver", ID: SourceEntityID(lineage, "driver", "D_NYC_2")}, map[FieldName]Value{
		"driver_id": strVal("D_NYC_2"), "status": strVal("ACTIVE"), "depot": strVal("NYC"), "hours": NewInt64Value(6),
	})
	truck1, _ := NewEntity(EntityRef{Kind: "truck", ID: SourceEntityID(lineage, "truck", "T1")}, map[FieldName]Value{
		"truck_id": strVal("T1"), "depot": strVal("CHI"),
	})

	// Add cross-group mentor relation D_NYC_1 -> D_CHI_1 and truck relation D_CHI_2 -> T1
	initialState, err := NewState(schema, lineage, []Entity{dChi1, dChi2, dNyc1, dNyc2, truck1}, []Relation{
		{Kind: "mentor", From: dNyc1.Ref(), To: dChi1.Ref()},
		{Kind: "assigned_truck", From: dChi2.Ref(), To: truck1.Ref()},
	})
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}

	// Merge drivers grouped by depot
	groupBy := Expr{Kind: ExprField, Field: "driver.depot"}
	mergeRule := TransformationDeclaration{
		ID:       "merge_by_depot.v1",
		Operator: OperatorMergeEntities,
		DeclaredReads: []FieldPath{
			"driver.depot", "driver.hours",
		},
		DeclaredWrites: []FieldPath{
			"driver.depot", "driver.driver_id", "driver.hours", "driver.status",
		},
		MergeEntities: &MergeEntitiesDeclaration{
			Selector: Selector{
				Kind:    "driver",
				GroupBy: &groupBy,
				Members: Cardinality{Kind: CardinalityExactly, Count: 2},
			},
			TargetKind:        "driver",
			Discriminator:     sumExpr("driver.hours"),
			Guard:             equalExpr(countExpr(), intLiteral(2)),
			RetainSources:     false,
			ReanchorRelations: true,
			Assignments: []FieldAssignment{
				{Target: "driver.driver_id", Value: stringLiteral(t, "D_MERGED")},
				{Target: "driver.status", Value: stringLiteral(t, "AVAILABLE")},
				{Target: "driver.depot", Value: stringLiteral(t, "MERGED_DEPOT")},
				{Target: "driver.hours", Value: sumExpr("driver.hours")},
			},
		},
	}

	plan := mustStructuralPlan(t, schema, []TransformationDeclaration{mergeRule})
	binding := mustStructuralBinding(t, plan, initialState)

	outcome := mustAcceptedTransition(t, binding, "merge_by_depot.v1", initialState, Journal{})
	finalState := outcome.State()

	// 4 drivers merged into 2 (1 for CHI, 1 for NYC) + 1 truck -> 3 entities
	if len(finalState.Entities()) != 3 {
		t.Fatalf("want 3 entities, got %d", len(finalState.Entities()))
	}

	// Relations:
	// 1. Cross-group mentor relation D_NYC_1 -> D_CHI_1 reanchors cleanly to D_NYC_MERGED -> D_CHI_MERGED
	// 2. Assigned truck relation D_CHI_2 -> T1 reanchors cleanly to D_CHI_MERGED -> T1
	// Total relations = 2
	if len(finalState.Relations()) != 2 {
		t.Fatalf("want 2 reanchored relations, got %d: %v", len(finalState.Relations()), finalState.Relations())
	}

	// Verify Undo
	restored := mustUndoTransition(t, initialState, outcome)
	if restored.Digest() != initialState.Digest() {
		t.Fatalf("restored digest %s != initial %s", restored.Digest(), initialState.Digest())
	}
}

func TestExecuteSplitEntityUnevaluableGuardRejectsGroupEvaluableSuffix(t *testing.T) {
	schema := buildStructuralTestSchema(t)
	lineage := InputLineageID("sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")

	// Create driver with missing hours field
	d1ID := SourceEntityID(lineage, "driver", "D1")
	d1, err := NewEntity(EntityRef{Kind: "driver", ID: d1ID}, map[FieldName]Value{
		"driver_id": mustString(t, "D1"),
		"status":    mustString(t, "AVAILABLE"),
		"depot":     mustString(t, "CHI"),
		// hours is absent
	})
	if err != nil {
		t.Fatalf("NewEntity: %v", err)
	}

	state, err := NewState(schema, lineage, []Entity{d1}, nil)
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}

	splitRule := TransformationDeclaration{
		ID:       "split_fault.v1",
		Operator: OperatorSplitEntity,
		DeclaredReads: []FieldPath{
			"driver.hours",
		},
		DeclaredWrites: []FieldPath{
			"shift_log.hours", "shift_log.shift_type",
		},
		SplitEntity: &SplitEntityDeclaration{
			Selector: Selector{
				Kind:    "driver",
				Members: Cardinality{Kind: CardinalityAny},
			},
			TargetKind:   "shift_log",
			Guard:        allMembers(Expr{Kind: ExprLess, Args: []Expr{fieldExpr("driver.hours"), intLiteral(50)}}),
			RetainSource: false,
			Partitions: []PartitionDeclaration{
				{
					Discriminator: stringLiteral(t, "AM"),
					Assignments: []FieldAssignment{
						{Target: "shift_log.shift_type", Value: stringLiteral(t, "MORNING")},
						{Target: "shift_log.hours", Value: intLiteral(5)},
					},
				},
			},
		},
	}

	plan := mustStructuralPlan(t, schema, []TransformationDeclaration{splitRule})
	binding := mustStructuralBinding(t, plan, state)

	outcome, err := ExecuteTransition(binding, "split_fault.v1", state, Journal{})
	if err != nil {
		t.Fatalf("ExecuteTransition: %v", err)
	}
	failure := mustTransitionFailure(t, outcome)
	if failure.Code() != string(SelectionExpressionUnavailable) {
		t.Fatalf("expected refusal code %s, got %s", SelectionExpressionUnavailable, failure.Code())
	}
	results := failure.InvariantResults()
	if len(results) == 0 {
		t.Fatal("expected invariant results in failure report")
	}
	lastResult := results[len(results)-1]
	expectedKey := invariantKey("split_fault.v1", groupEvaluableSuffix)
	if lastResult.DeclarationKey() != expectedKey {
		t.Fatalf("expected invariant key %s, got %s", expectedKey, lastResult.DeclarationKey())
	}
}

func TestExecuteRelateEntitiesSelectorDataFaultRejectsSelectorEvaluableSuffix(t *testing.T) {
	schema := buildStructuralTestSchema(t)
	lineage := InputLineageID("sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")

	// D1 has missing hours field
	d1, err := NewEntity(EntityRef{Kind: "driver", ID: SourceEntityID(lineage, "driver", "D1")}, map[FieldName]Value{
		"driver_id": mustString(t, "D1"),
		"status":    mustString(t, "AVAILABLE"),
		"depot":     mustString(t, "CHI"),
		// hours is absent
	})
	if err != nil {
		t.Fatalf("NewEntity d1: %v", err)
	}
	trk1, err := NewEntity(EntityRef{Kind: "truck", ID: SourceEntityID(lineage, "truck", "T1")}, map[FieldName]Value{
		"truck_id": mustString(t, "T1"),
		"depot":    mustString(t, "CHI"),
	})
	if err != nil {
		t.Fatalf("NewEntity trk1: %v", err)
	}

	state, err := NewState(schema, lineage, []Entity{d1, trk1}, nil)
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}

	// FromSelector filters on driver.hours > 5
	where := Expr{Kind: ExprLess, Args: []Expr{intLiteral(5), fieldExpr("driver.hours")}}
	relateRule := TransformationDeclaration{
		ID:       "relate_selector_fault.v1",
		Operator: OperatorRelateEntities,
		DeclaredReads: []FieldPath{
			"driver.hours",
		},
		DeclaredWrites: []FieldPath{},
		RelateEntities: &RelateEntitiesDeclaration{
			RelationKind: "assigned_truck",
			FromSelector: Selector{Kind: "driver", Where: &where, Members: Cardinality{Kind: CardinalityAny}},
			ToSelector:   Selector{Kind: "truck", Members: Cardinality{Kind: CardinalityAny}},
			Guard:        Expr{Kind: ExprEqual, Args: []Expr{intLiteral(1), intLiteral(1)}},
		},
	}

	plan := mustStructuralPlan(t, schema, []TransformationDeclaration{relateRule})
	binding := mustStructuralBinding(t, plan, state)

	outcome, err := ExecuteTransition(binding, "relate_selector_fault.v1", state, Journal{})
	if err != nil {
		t.Fatalf("ExecuteTransition: %v", err)
	}
	failure := mustTransitionFailure(t, outcome)
	if failure.Code() != string(SelectionExpressionUnavailable) {
		t.Fatalf("expected refusal code %s, got %s", SelectionExpressionUnavailable, failure.Code())
	}
	results := failure.InvariantResults()
	if len(results) == 0 {
		t.Fatal("expected invariant results in failure report")
	}
	lastResult := results[len(results)-1]
	expectedKey := invariantKey("relate_selector_fault.v1", selectorEvaluableSuffix)
	if lastResult.DeclarationKey() != expectedKey {
		t.Fatalf("expected invariant key %s, got %s", expectedKey, lastResult.DeclarationKey())
	}
}

func TestExecuteRelateEntitiesGuardDataFaultRejectsGroupEvaluableSuffix(t *testing.T) {
	schema := buildStructuralTestSchema(t)
	lineage := InputLineageID("sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")

	// D1 has missing hours field
	d1, err := NewEntity(EntityRef{Kind: "driver", ID: SourceEntityID(lineage, "driver", "D1")}, map[FieldName]Value{
		"driver_id": mustString(t, "D1"),
		"status":    mustString(t, "AVAILABLE"),
		"depot":     mustString(t, "CHI"),
		// hours is absent
	})
	if err != nil {
		t.Fatalf("NewEntity d1: %v", err)
	}
	trk1, err := NewEntity(EntityRef{Kind: "truck", ID: SourceEntityID(lineage, "truck", "T1")}, map[FieldName]Value{
		"truck_id": mustString(t, "T1"),
		"depot":    mustString(t, "CHI"),
	})
	if err != nil {
		t.Fatalf("NewEntity trk1: %v", err)
	}

	state, err := NewState(schema, lineage, []Entity{d1, trk1}, nil)
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}

	// Guard tests driver.hours > 5
	relateRule := TransformationDeclaration{
		ID:       "relate_guard_fault.v1",
		Operator: OperatorRelateEntities,
		DeclaredReads: []FieldPath{
			"driver.hours",
		},
		DeclaredWrites: []FieldPath{},
		RelateEntities: &RelateEntitiesDeclaration{
			RelationKind: "assigned_truck",
			FromSelector: Selector{Kind: "driver", Members: Cardinality{Kind: CardinalityAny}},
			ToSelector:   Selector{Kind: "truck", Members: Cardinality{Kind: CardinalityAny}},
			Guard:        Expr{Kind: ExprLess, Args: []Expr{intLiteral(5), fieldExpr("driver.hours")}},
		},
	}

	plan := mustStructuralPlan(t, schema, []TransformationDeclaration{relateRule})
	binding := mustStructuralBinding(t, plan, state)

	outcome, err := ExecuteTransition(binding, "relate_guard_fault.v1", state, Journal{})
	if err != nil {
		t.Fatalf("ExecuteTransition: %v", err)
	}
	failure := mustTransitionFailure(t, outcome)
	if failure.Code() != string(SelectionExpressionUnavailable) {
		t.Fatalf("expected refusal code %s, got %s", SelectionExpressionUnavailable, failure.Code())
	}
	results := failure.InvariantResults()
	if len(results) == 0 {
		t.Fatal("expected invariant results in failure report")
	}
	lastResult := results[len(results)-1]
	expectedKey := invariantKey("relate_guard_fault.v1", groupEvaluableSuffix)
	if lastResult.DeclarationKey() != expectedKey {
		t.Fatalf("expected invariant key %s, got %s", expectedKey, lastResult.DeclarationKey())
	}
	// Verify entity attribution contains D1 and T1
	if len(failure.Entities()) != 2 {
		t.Fatalf("expected 2 entity references in failure report, got %d: %v", len(failure.Entities()), failure.Entities())
	}
}

func TestExecuteRelateEntitiesUnsatisfiedGuardAttribution(t *testing.T) {
	schema := buildStructuralTestSchema(t)
	initial := buildStructuralTestInitialState(t, schema)

	// Guard evaluates false (driver.hours > 1000)
	relateRule := TransformationDeclaration{
		ID:       "relate_guard_unsatisfied.v1",
		Operator: OperatorRelateEntities,
		DeclaredReads: []FieldPath{
			"driver.hours",
		},
		DeclaredWrites: []FieldPath{},
		RelateEntities: &RelateEntitiesDeclaration{
			RelationKind: "assigned_truck",
			FromSelector: Selector{Kind: "driver", Members: Cardinality{Kind: CardinalityAny}},
			ToSelector:   Selector{Kind: "truck", Members: Cardinality{Kind: CardinalityAny}},
			Guard:        Expr{Kind: ExprLess, Args: []Expr{intLiteral(1000), fieldExpr("driver.hours")}},
		},
	}

	plan := mustStructuralPlan(t, schema, []TransformationDeclaration{relateRule})
	binding := mustStructuralBinding(t, plan, initial)

	outcome, err := ExecuteTransition(binding, "relate_guard_unsatisfied.v1", initial, Journal{})
	if err != nil {
		t.Fatalf("ExecuteTransition: %v", err)
	}
	failure := mustTransitionFailure(t, outcome)
	if failure.Code() != string(SelectionGuardUnsatisfied) {
		t.Fatalf("expected refusal code %s, got %s", SelectionGuardUnsatisfied, failure.Code())
	}
	results := failure.InvariantResults()
	if len(results) == 0 {
		t.Fatal("expected invariant results in failure report")
	}
	lastResult := results[len(results)-1]
	expectedKey := invariantKey("relate_guard_unsatisfied.v1", guardSuffix)
	if lastResult.DeclarationKey() != expectedKey {
		t.Fatalf("expected invariant key %s, got %s", expectedKey, lastResult.DeclarationKey())
	}
	// Initial state has 2 drivers and 1 truck -> candidate refs must contain all 3 participating entities
	if len(failure.Entities()) != 3 {
		t.Fatalf("expected 3 candidate entity references in failure report, got %d: %v", len(failure.Entities()), failure.Entities())
	}
}
