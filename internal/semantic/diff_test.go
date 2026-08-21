package semantic

import (
	"testing"
)

func TestDiffStatesIdentical(t *testing.T) {
	schema, err := NewSchema([]EntityDeclaration{
		{
			Kind: "driver",
			Fields: []FieldDeclaration{
				{Name: "driver_id", Kind: ValueString, RequiredAtConstruction: true},
				{Name: "status", Kind: ValueString, RequiredAtConstruction: true},
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("NewSchema: %v", err)
	}

	lineage, _ := NewInputLineageID("test", "root")
	d1ID := SourceEntityID(lineage, "driver", "D1")
	d1Val, _ := NewStringValue("D1")
	statVal, _ := NewStringValue("ACTIVE")

	d1, _ := NewEntity(EntityRef{Kind: "driver", ID: d1ID}, map[FieldName]Value{
		"driver_id": d1Val,
		"status":    statVal,
	})

	state1, _ := NewState(schema, lineage, []Entity{d1}, nil)
	state2, _ := NewState(schema, lineage, []Entity{d1}, nil)

	diff, err := DiffStates(state1, state2)
	if err != nil {
		t.Fatalf("DiffStates: %v", err)
	}

	if !diff.Identical() {
		t.Errorf("expected identical diff, got non-identical")
	}
	if diff.Metrics.IdenticalEntitiesCount != 1 {
		t.Errorf("expected 1 identical entity, got %d", diff.Metrics.IdenticalEntitiesCount)
	}
	if diff.Metrics.CreatedEntitiesCount != 0 || diff.Metrics.DeletedEntitiesCount != 0 || diff.Metrics.ModifiedEntitiesCount != 0 {
		t.Errorf("unexpected entity diff metrics: %+v", diff.Metrics)
	}
}

func TestDiffStatesStructuralChanges(t *testing.T) {
	schema, err := NewSchema([]EntityDeclaration{
		{
			Kind: "driver",
			Fields: []FieldDeclaration{
				{Name: "driver_id", Kind: ValueString, RequiredAtConstruction: true},
				{Name: "status", Kind: ValueString, RequiredAtConstruction: true},
				{Name: "hours", Kind: ValueInt64, RequiredAtConstruction: false},
			},
		},
		{
			Kind: "truck",
			Fields: []FieldDeclaration{
				{Name: "truck_id", Kind: ValueString, RequiredAtConstruction: true},
			},
		},
	}, []RelationDeclaration{
		{
			Kind:     "assigned_truck",
			FromKind: "driver",
			ToKind:   "truck",
		},
	})
	if err != nil {
		t.Fatalf("NewSchema: %v", err)
	}

	lineage, _ := NewInputLineageID("test", "root")
	d1ID := SourceEntityID(lineage, "driver", "D1")
	d2ID := SourceEntityID(lineage, "driver", "D2")
	d3ID := SourceEntityID(lineage, "driver", "D3")
	t1ID := SourceEntityID(lineage, "truck", "T1")

	d1Val, _ := NewStringValue("D1")
	d2Val, _ := NewStringValue("D2")
	d3Val, _ := NewStringValue("D3")
	t1Val, _ := NewStringValue("T1")
	activeVal, _ := NewStringValue("ACTIVE")
	restingVal, _ := NewStringValue("RESTING")

	// Baseline: D1 (ACTIVE, 10h), D2 (ACTIVE), T1
	d1Baseline, _ := NewEntity(EntityRef{Kind: "driver", ID: d1ID}, map[FieldName]Value{
		"driver_id": d1Val,
		"status":    activeVal,
		"hours":     NewInt64Value(10),
	})
	d2Baseline, _ := NewEntity(EntityRef{Kind: "driver", ID: d2ID}, map[FieldName]Value{
		"driver_id": d2Val,
		"status":    activeVal,
	})
	t1Entity, _ := NewEntity(EntityRef{Kind: "truck", ID: t1ID}, map[FieldName]Value{
		"truck_id": t1Val,
	})

	rel1 := Relation{
		Kind: "assigned_truck",
		From: EntityRef{Kind: "driver", ID: d1ID},
		To:   EntityRef{Kind: "truck", ID: t1ID},
	}

	baseline, _ := NewState(schema, lineage, []Entity{d1Baseline, d2Baseline, t1Entity}, []Relation{rel1})

	// Candidate:
	// - D1 modified (status: RESTING, hours: 14)
	// - D2 deleted (not in candidate)
	// - D3 created (ACTIVE)
	// - T1 unchanged
	// - rel1 removed
	d1Candidate, _ := NewEntity(EntityRef{Kind: "driver", ID: d1ID}, map[FieldName]Value{
		"driver_id": d1Val,
		"status":    restingVal,
		"hours":     NewInt64Value(14),
	})
	d3Candidate, _ := NewEntity(EntityRef{Kind: "driver", ID: d3ID}, map[FieldName]Value{
		"driver_id": d3Val,
		"status":    activeVal,
	})

	candidate, _ := NewState(schema, lineage, []Entity{d1Candidate, d3Candidate, t1Entity}, nil)

	diff, err := DiffStates(baseline, candidate)
	if err != nil {
		t.Fatalf("DiffStates: %v", err)
	}

	if diff.Identical() {
		t.Fatalf("expected non-identical diff")
	}

	// Verify metrics
	if diff.Metrics.CreatedEntitiesCount != 1 {
		t.Errorf("expected 1 created entity, got %d", diff.Metrics.CreatedEntitiesCount)
	}
	if diff.Metrics.DeletedEntitiesCount != 1 {
		t.Errorf("expected 1 deleted entity, got %d", diff.Metrics.DeletedEntitiesCount)
	}
	if diff.Metrics.ModifiedEntitiesCount != 1 {
		t.Errorf("expected 1 modified entity, got %d", diff.Metrics.ModifiedEntitiesCount)
	}
	if diff.Metrics.FieldChangesCount != 2 {
		t.Errorf("expected 2 field changes on D1, got %d", diff.Metrics.FieldChangesCount)
	}
	if diff.Metrics.RemovedRelationsCount != 1 {
		t.Errorf("expected 1 removed relation, got %d", diff.Metrics.RemovedRelationsCount)
	}
	if diff.Metrics.AddedRelationsCount != 0 {
		t.Errorf("expected 0 added relations, got %d", diff.Metrics.AddedRelationsCount)
	}
	if diff.Metrics.IdenticalEntitiesCount != 1 { // T1 is identical
		t.Errorf("expected 1 identical entity (T1), got %d", diff.Metrics.IdenticalEntitiesCount)
	}

	// Verify created entity
	if len(diff.CreatedEntities) != 1 || diff.CreatedEntities[0].ID != d3ID {
		t.Errorf("expected CreatedEntities to contain D3, got: %v", diff.CreatedEntities)
	}

	// Verify deleted entity
	if len(diff.DeletedEntities) != 1 || diff.DeletedEntities[0].ID != d2ID {
		t.Errorf("expected DeletedEntities to contain D2, got: %v", diff.DeletedEntities)
	}

	// Verify modified entity
	if len(diff.ModifiedEntities) != 1 || diff.ModifiedEntities[0].Ref.ID != d1ID {
		t.Fatalf("expected ModifiedEntities to contain D1, got: %v", diff.ModifiedEntities)
	}
	mod := diff.ModifiedEntities[0]
	if len(mod.FieldDiffs) != 2 {
		t.Fatalf("expected 2 field diffs on D1, got: %v", mod.FieldDiffs)
	}
	// hours diff
	if mod.FieldDiffs[0].Name != "hours" || mod.FieldDiffs[0].Baseline.Equal(mod.FieldDiffs[0].Candidate) {
		t.Errorf("unexpected hours field diff: %+v", mod.FieldDiffs[0])
	}
	// status diff
	if mod.FieldDiffs[1].Name != "status" || mod.FieldDiffs[1].Baseline.Equal(mod.FieldDiffs[1].Candidate) {
		t.Errorf("unexpected status field diff: %+v", mod.FieldDiffs[1])
	}

	// Verify removed relation
	if len(diff.RemovedRelations) != 1 || diff.RemovedRelations[0] != rel1 {
		t.Errorf("expected RemovedRelations to contain rel1, got: %v", diff.RemovedRelations)
	}
}

func TestDiffStatesAddedRelationsAndCanonicalSorting(t *testing.T) {
	schema, err := NewSchema([]EntityDeclaration{
		{
			Kind: "driver",
			Fields: []FieldDeclaration{
				{Name: "driver_id", Kind: ValueString, RequiredAtConstruction: true},
			},
		},
		{
			Kind: "truck",
			Fields: []FieldDeclaration{
				{Name: "truck_id", Kind: ValueString, RequiredAtConstruction: true},
			},
		},
	}, []RelationDeclaration{
		{Kind: "assigned_truck", FromKind: "driver", ToKind: "truck"},
		{Kind: "backup_truck", FromKind: "driver", ToKind: "truck"},
	})
	if err != nil {
		t.Fatalf("NewSchema: %v", err)
	}

	lineage, _ := NewInputLineageID("test", "root")
	d1ID := SourceEntityID(lineage, "driver", "D1")
	d2ID := SourceEntityID(lineage, "driver", "D2")
	t1ID := SourceEntityID(lineage, "truck", "T1")
	t2ID := SourceEntityID(lineage, "truck", "T2")

	d1Val, _ := NewStringValue("D1")
	d2Val, _ := NewStringValue("D2")
	t1Val, _ := NewStringValue("T1")
	t2Val, _ := NewStringValue("T2")

	d1, _ := NewEntity(EntityRef{Kind: "driver", ID: d1ID}, map[FieldName]Value{"driver_id": d1Val})
	d2, _ := NewEntity(EntityRef{Kind: "driver", ID: d2ID}, map[FieldName]Value{"driver_id": d2Val})
	t1, _ := NewEntity(EntityRef{Kind: "truck", ID: t1ID}, map[FieldName]Value{"truck_id": t1Val})
	t2, _ := NewEntity(EntityRef{Kind: "truck", ID: t2ID}, map[FieldName]Value{"truck_id": t2Val})

	// Baseline has no relations
	baseline, _ := NewState(schema, lineage, []Entity{d1, d2, t1, t2}, nil)

	// Candidate has two added relations in non-canonical insertion order
	relA := Relation{
		Kind: "assigned_truck",
		From: EntityRef{Kind: "driver", ID: d2ID},
		To:   EntityRef{Kind: "truck", ID: t1ID},
	}
	relB := Relation{
		Kind: "assigned_truck",
		From: EntityRef{Kind: "driver", ID: d1ID},
		To:   EntityRef{Kind: "truck", ID: t2ID},
	}

	candidate, _ := NewState(schema, lineage, []Entity{d1, d2, t1, t2}, []Relation{relA, relB})

	diff, err := DiffStates(baseline, candidate)
	if err != nil {
		t.Fatalf("DiffStates: %v", err)
	}

	if diff.Metrics.AddedRelationsCount != 2 {
		t.Fatalf("expected 2 added relations, got %d", diff.Metrics.AddedRelationsCount)
	}
	if len(diff.AddedRelations) != 2 {
		t.Fatalf("expected 2 added relations in slice, got %d", len(diff.AddedRelations))
	}

	// Verify canonical sorting matches compareRelations (d1ID < d2ID)
	if compareRelations(diff.AddedRelations[0], diff.AddedRelations[1]) >= 0 {
		t.Errorf("AddedRelations not canonically sorted: %v", diff.AddedRelations)
	}
}
