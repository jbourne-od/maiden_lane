package semantic

import (
	"bytes"
	"encoding/hex"
	"testing"
)

// Production break caught: validating relation endpoints only against the
// predecessor would reject T1's relation to the team inserted by the same
// atomic patch.
func TestApplyPatchStagesInsertBeforeRelationsAtomically(t *testing.T) {
	before, team, drivers := formTeamPatchFixture(t)
	patch := mustPatch(t,
		InsertOperation(team),
		RelateOperation(memberRelation(team.Ref(), drivers[1].Ref())),
		RelateOperation(memberRelation(team.Ref(), drivers[0].Ref())),
	)

	after, failure := ApplyPatch(before, patch)
	if failure != nil {
		t.Fatalf("ApplyPatch: %s", failure.Code())
	}
	if _, ok := after.Entity(team.Ref()); !ok {
		t.Fatal("inserted team missing")
	}
	if !stateHasRelation(after, memberRelation(team.Ref(), drivers[0].Ref())) ||
		!stateHasRelation(after, memberRelation(team.Ref(), drivers[1].Ref())) {
		t.Fatal("staged member relations missing")
	}
	if _, ok := before.Entity(team.Ref()); ok || len(before.Relations()) != 0 {
		t.Fatal("predecessor changed after accepted patch")
	}
}

// Production break caught: returning the staged candidate after any rejected
// operation would expose a state containing an uncommitted patch prefix.
func TestApplyPatchFailureReturnsByteIdenticalPredecessor(t *testing.T) {
	before, team, drivers := formTeamPatchFixture(t)
	missingDriver := EntityRef{Kind: "driver", ID: EntityID("sha256:4444444444444444444444444444444444444444444444444444444444444444")}

	tests := []struct {
		name  string
		state State
		patch Patch
		code  OperationInvariantCode
	}{
		{
			name:  "first operation",
			state: stateWithTeam(t, before, team, nil),
			patch: mustPatch(t, InsertOperation(team)),
			code:  OperationEntityIdentityCollision,
		},
		{
			name:  "second operation",
			state: before,
			patch: mustPatch(t, InsertOperation(team), RelateOperation(memberRelation(team.Ref(), missingDriver))),
			code:  OperationRelationEndpointMissing,
		},
		{
			name:  "third operation",
			state: before,
			patch: mustPatch(t, InsertOperation(team), RelateOperation(memberRelation(team.Ref(), drivers[0].Ref())), RelateOperation(memberRelation(team.Ref(), missingDriver))),
			code:  OperationRelationEndpointMissing,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			canonical := test.state.CanonicalBytes()
			digest := test.state.Digest()
			returned, failure := ApplyPatch(test.state, test.patch)
			if failure == nil {
				t.Fatal("invalid patch committed")
			}
			if failure.Code() != test.code {
				t.Fatalf("failure code = %q; want %q", failure.Code(), test.code)
			}
			if !bytes.Equal(returned.CanonicalBytes(), canonical) || returned.Digest() != digest {
				t.Fatal("ApplyPatch did not return the byte-identical predecessor")
			}
			if !bytes.Equal(test.state.CanonicalBytes(), canonical) || test.state.Digest() != digest {
				t.Fatal("ApplyPatch mutated the predecessor")
			}
		})
	}
}

// Production break caught: collapsing distinct operation failures into a
// catch-all would make deterministic rejection unauditable at the patch
// boundary.
func TestApplyPatchReturnsExactClosedOperationCodes(t *testing.T) {
	before, team, drivers := formTeamPatchFixture(t)
	withTeam := stateWithTeam(t, before, team, nil)
	relation := memberRelation(team.Ref(), drivers[0].Ref())
	withRelation := stateWithTeam(t, before, team, []Relation{relation})
	missingTeam := EntityRef{Kind: "team", ID: EntityID("sha256:5555555555555555555555555555555555555555555555555555555555555555")}

	tests := []struct {
		name  string
		state State
		patch Patch
		code  OperationInvariantCode
	}{
		{
			name:  "identity collision",
			state: withTeam,
			patch: mustPatch(t, InsertOperation(team)),
			code:  OperationEntityIdentityCollision,
		},
		{
			name:  "missing update target",
			state: before,
			patch: mustPatch(t, UpdateOperation(team.Ref(), []FieldUpdate{{
				Name: "aggregation_anchor", Before: AbsentField(), After: mustAtom(t, "T0"),
			}})),
			code: OperationUpdateTargetNotFound,
		},
		{
			name:  "update before image mismatch",
			state: withTeam,
			patch: mustPatch(t, UpdateOperation(team.Ref(), []FieldUpdate{{
				Name: "assignment_key", Before: PresentField(mustString(t, "wrong")), After: mustString(t, "Y"),
			}})),
			code: OperationBeforeImageMismatch,
		},
		{
			name:  "relation already present",
			state: withRelation,
			patch: mustPatch(t, RelateOperation(relation)),
			code:  OperationRelationAlreadyPresent,
		},
		{
			name:  "relation endpoint missing",
			state: before,
			patch: mustPatch(t, RelateOperation(memberRelation(missingTeam, drivers[0].Ref()))),
			code:  OperationRelationEndpointMissing,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			returned, failure := ApplyPatch(test.state, test.patch)
			if failure == nil {
				t.Fatal("invalid patch committed")
			}
			if failure.Code() != test.code {
				t.Fatalf("failure code = %q; want %q", failure.Code(), test.code)
			}
			if !bytes.Equal(returned.CanonicalBytes(), test.state.CanonicalBytes()) {
				t.Fatal("rejected operation changed predecessor")
			}
		})
	}
}

// Production break caught: retaining proposal order would make equivalent T1
// patches hash differently and could evaluate a relation before its staged
// insert dependency.
func TestPatchCanonicalOrderUsesOperationRankThenTypedKey(t *testing.T) {
	_, team, drivers := formTeamPatchFixture(t)
	changes := []FieldUpdate{{
		Name: "assignment_key", Before: PresentField(mustString(t, "X")), After: mustString(t, "Y"),
	}}
	forward := mustPatch(t,
		UpdateOperation(drivers[0].Ref(), changes),
		RelateOperation(memberRelation(team.Ref(), drivers[1].Ref())),
		InsertOperation(team),
		RelateOperation(memberRelation(team.Ref(), drivers[0].Ref())),
	)
	reverse := mustPatch(t,
		RelateOperation(memberRelation(team.Ref(), drivers[0].Ref())),
		InsertOperation(team),
		RelateOperation(memberRelation(team.Ref(), drivers[1].Ref())),
		UpdateOperation(drivers[0].Ref(), changes),
	)

	if !bytes.Equal(forward.CanonicalBytes(), reverse.CanonicalBytes()) || forward.Digest() != reverse.Digest() {
		t.Fatal("proposal order changed canonical patch identity")
	}
	operations := forward.Operations()
	if len(operations) != 4 {
		t.Fatalf("operation count = %d; want 4", len(operations))
	}
	wantKinds := []OperationKind{OperationInsert, OperationRelate, OperationRelate, OperationUpdate}
	for index, want := range wantKinds {
		if operations[index].Kind() != want {
			t.Fatalf("operation %d kind = %d; want %d", index, operations[index].Kind(), want)
		}
	}
	firstRelation, ok := operations[1].Relate()
	if !ok || firstRelation.Relation().To != drivers[0].Ref() {
		t.Fatalf("first relation = %+v; want driver A endpoint", firstRelation.Relation())
	}
}

// Production break caught: preserving authored update-field order would make
// equivalent exact images acquire different canonical bytes and digests.
func TestPatchCanonicalOrderSortsUpdateFields(t *testing.T) {
	_, team, _ := formTeamPatchFixture(t)
	anchor := FieldUpdate{Name: "aggregation_anchor", Before: AbsentField(), After: mustAtom(t, "T0")}
	elapsed := FieldUpdate{Name: "elapsed_duration_hours", Before: AbsentField(), After: NewInt64Value(10)}
	first := mustPatch(t, UpdateOperation(team.Ref(), []FieldUpdate{elapsed, anchor}))
	second := mustPatch(t, UpdateOperation(team.Ref(), []FieldUpdate{anchor, elapsed}))
	if !bytes.Equal(first.CanonicalBytes(), second.CanonicalBytes()) || first.Digest() != second.Digest() {
		t.Fatal("update field order changed canonical patch identity")
	}
}

// Production break caught: retaining caller slices or exposing internal
// operation payloads would let a patch drift away from its cached digest.
func TestPatchDefensivelyCopiesInputsAndGetterResults(t *testing.T) {
	_, team, _ := formTeamPatchFixture(t)
	changes := []FieldUpdate{{Name: "aggregation_anchor", Before: AbsentField(), After: mustAtom(t, "T0")}}
	operations := []Operation{UpdateOperation(team.Ref(), changes)}
	patch, err := NewPatch(operations)
	if err != nil {
		t.Fatalf("NewPatch: %v", err)
	}
	wantCanonical := patch.CanonicalBytes()
	wantDigest := patch.Digest()

	changes[0].Name = "input-change"
	operations[0].update.fields[0].Name = "operation-input-change"
	operations[0] = Operation{}
	got := patch.Operations()
	got[0].update.fields[0].Name = "getter-change"
	got[0] = Operation{}
	canonical := patch.CanonicalBytes()
	canonical[0] ^= 0xff

	if !bytes.Equal(patch.CanonicalBytes(), wantCanonical) || patch.Digest() != wantDigest {
		t.Fatal("patch identity changed through caller-owned input or getter result")
	}
	stored := patch.Operations()
	update, ok := stored[0].Update()
	if !ok || update.Fields()[0].Name != "aggregation_anchor" {
		t.Fatalf("stored operation mutated: %+v", stored[0])
	}
}

// Production break caught: changing the patch tag, complete payload layout,
// absent-field marker, operation rank, or typed key would drift accepted
// structural provenance.
func TestPatchCanonicalGoldenVectors(t *testing.T) {
	const (
		wantT1Hex                = "00000000000000146d616964656e2d6c616e652e70617463682e763100000000000000030100000000000000047465616d33333333333333333333333333333333333333333333333333333333333333330000000000000001000000000000000e61737369676e6d656e745f6b6579010000000000000001580200000000000000066d656d62657200000000000000047465616d3333333333333333333333333333333333333333333333333333333333333333000000000000000664726976657211111111111111111111111111111111111111111111111111111111111111110200000000000000066d656d62657200000000000000047465616d333333333333333333333333333333333333333333333333333333333333333300000000000000066472697665722222222222222222222222222222222222222222222222222222222222222222"
		wantT1Digest PatchDigest = "sha256:0c9f251213f646dfc407dcd9595fd489bdd4ac6f477dc593f5588208698de2d2"
		wantT2Hex                = "00000000000000146d616964656e2d6c616e652e70617463682e763100000000000000010300000000000000047465616d3333333333333333333333333333333333333333333333333333333333333333000000000000000300000000000000126167677265676174696f6e5f616e63686f72000200000000000000025430000000000000001664726976696e675f6475726174696f6e5f686f757273000300000000000000080000000000000016656c61707365645f6475726174696f6e5f686f7572730003000000000000000a"
		wantT2Digest PatchDigest = "sha256:758fc7285b6639c085fda4822faeece462b5931a64180806acade3cdd8e1e818"
	)

	_, team, drivers := formTeamPatchFixture(t)
	t1 := mustPatch(t,
		RelateOperation(memberRelation(team.Ref(), drivers[1].Ref())),
		RelateOperation(memberRelation(team.Ref(), drivers[0].Ref())),
		InsertOperation(team),
	)
	t2 := mustPatch(t, UpdateOperation(team.Ref(), []FieldUpdate{
		{Name: "elapsed_duration_hours", Before: AbsentField(), After: NewInt64Value(10)},
		{Name: "aggregation_anchor", Before: AbsentField(), After: mustAtom(t, "T0")},
		{Name: "driving_duration_hours", Before: AbsentField(), After: NewInt64Value(8)},
	}))

	assertPatchVector(t, "T1", t1, wantT1Hex, wantT1Digest)
	assertPatchVector(t, "T2", t2, wantT2Hex, wantT2Digest)
}

// Production break caught: undoing in forward order would try to remove the
// inserted team while accepted member relations still reference it, and an
// inexact update inverse would not reproduce the canonical predecessor.
func TestUndoPatchReproducesCanonicalPredecessorForGeneratedLawfulPatches(t *testing.T) {
	for sample := range 64 {
		assignmentBefore := mustString(t, "assignment-before-"+string(rune('A'+sample%26)))
		assignmentAfter := mustString(t, "assignment-after-"+string(rune('A'+sample%26)))
		driver := mustEntity(t, "driver", testDriverAID, map[FieldName]Value{"assignment_key": assignmentBefore})
		team := mustEntity(t, "team", testTeamID, map[FieldName]Value{"assignment_key": assignmentBefore})
		predecessor, err := NewState(fixtureSchemaForStateTests(t), testLineageID, []Entity{driver}, nil)
		if err != nil {
			t.Fatalf("sample %d NewState: %v", sample, err)
		}
		patch := mustPatch(t,
			UpdateOperation(driver.Ref(), []FieldUpdate{{Name: "assignment_key", Before: PresentField(assignmentBefore), After: assignmentAfter}}),
			RelateOperation(memberRelation(team.Ref(), driver.Ref())),
			InsertOperation(team),
		)

		after, failure := ApplyPatch(predecessor, patch)
		if failure != nil {
			t.Fatalf("sample %d ApplyPatch: %s", sample, failure.Code())
		}
		reproduced, failure := UndoPatch(after, patch)
		if failure != nil {
			t.Fatalf("sample %d UndoPatch: %s", sample, failure.Code())
		}
		if !bytes.Equal(reproduced.CanonicalBytes(), predecessor.CanonicalBytes()) || reproduced.Digest() != predecessor.Digest() {
			t.Fatalf("sample %d inverse did not reproduce canonical predecessor", sample)
		}
	}
}

// Production break caught: forgetting the explicit absent update image would
// restore zero values instead of removing the three fields added by T2.
func TestUndoPatchRestoresAbsentUpdateBeforeImages(t *testing.T) {
	before, team, _ := formTeamPatchFixture(t)
	predecessor := stateWithTeam(t, before, team, nil)
	patch := mustPatch(t, UpdateOperation(team.Ref(), []FieldUpdate{
		{Name: "aggregation_anchor", Before: AbsentField(), After: mustAtom(t, "T0")},
		{Name: "elapsed_duration_hours", Before: AbsentField(), After: NewInt64Value(10)},
		{Name: "driving_duration_hours", Before: AbsentField(), After: NewInt64Value(8)},
	}))
	after, failure := ApplyPatch(predecessor, patch)
	if failure != nil {
		t.Fatalf("ApplyPatch: %s", failure.Code())
	}
	reproduced, failure := UndoPatch(after, patch)
	if failure != nil {
		t.Fatalf("UndoPatch: %s", failure.Code())
	}
	if !bytes.Equal(reproduced.CanonicalBytes(), predecessor.CanonicalBytes()) {
		t.Fatal("inverse retained a field whose before-image was absent")
	}
}

// Production break caught: inverse application that trusts the patch without
// checking its accepted after-image could erase or overwrite a later state.
func TestUndoPatchAfterImageMismatchReturnsCurrentStateUnchanged(t *testing.T) {
	before, team, _ := formTeamPatchFixture(t)
	predecessor := stateWithTeam(t, before, team, nil)
	patch := mustPatch(t, UpdateOperation(team.Ref(), []FieldUpdate{{
		Name: "aggregation_anchor", Before: AbsentField(), After: mustAtom(t, "T0"),
	}}))
	after, failure := ApplyPatch(predecessor, patch)
	if failure != nil {
		t.Fatalf("ApplyPatch: %s", failure.Code())
	}
	tamperedTeam, ok := after.Entity(team.Ref())
	if !ok {
		t.Fatal("team missing after patch")
	}
	tamperedFields := tamperedTeam.Fields()
	tamperedFields["aggregation_anchor"] = mustAtom(t, "T1")
	tamperedTeam = mustEntity(t, "team", testTeamID, tamperedFields)
	tampered := replaceEntityInState(t, after, tamperedTeam)

	returned, failure := UndoPatch(tampered, patch)
	if failure == nil {
		t.Fatal("UndoPatch accepted changed after-image")
	}
	if failure.Code() != OperationBeforeImageMismatch {
		t.Fatalf("failure code = %q; want %q", failure.Code(), OperationBeforeImageMismatch)
	}
	if !bytes.Equal(returned.CanonicalBytes(), tampered.CanonicalBytes()) || returned.Digest() != tampered.Digest() {
		t.Fatal("rejected inverse changed current state")
	}
}

func formTeamPatchFixture(t *testing.T) (State, Entity, [2]Entity) {
	t.Helper()
	drivers := [2]Entity{
		mustEntity(t, "driver", testDriverAID, map[FieldName]Value{"assignment_key": mustString(t, "X")}),
		mustEntity(t, "driver", testDriverBID, map[FieldName]Value{"assignment_key": mustString(t, "X")}),
	}
	team := mustEntity(t, "team", testTeamID, map[FieldName]Value{"assignment_key": mustString(t, "X")})
	state, err := NewState(fixtureSchemaForStateTests(t), testLineageID, drivers[:], nil)
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	return state, team, drivers
}

func stateWithTeam(t *testing.T, before State, team Entity, relations []Relation) State {
	t.Helper()
	entities := before.Entities()
	entities = append(entities, team)
	state, err := NewState(before.Schema(), before.InputLineageID(), entities, relations)
	if err != nil {
		t.Fatalf("NewState with team: %v", err)
	}
	return state
}

func memberRelation(team, driver EntityRef) Relation {
	return Relation{Kind: "member", From: team, To: driver}
}

func stateHasRelation(state State, wanted Relation) bool {
	for _, relation := range state.Relations() {
		if relation == wanted {
			return true
		}
	}
	return false
}

func mustPatch(t *testing.T, operations ...Operation) Patch {
	t.Helper()
	patch, err := NewPatch(operations)
	if err != nil {
		t.Fatalf("NewPatch: %v", err)
	}
	return patch
}

func assertPatchVector(t *testing.T, name string, patch Patch, wantHex string, wantDigest PatchDigest) {
	t.Helper()
	if got := hex.EncodeToString(patch.CanonicalBytes()); got != wantHex {
		t.Fatalf("%s canonical patch hex\n got: %s\nwant: %s", name, got, wantHex)
	}
	if got := patch.Digest(); got != wantDigest {
		t.Fatalf("%s PatchDigest = %q; want %q", name, got, wantDigest)
	}
}

func replaceEntityInState(t *testing.T, state State, replacement Entity) State {
	t.Helper()
	entities := state.Entities()
	found := false
	for index := range entities {
		if entities[index].Ref() == replacement.Ref() {
			entities[index] = replacement
			found = true
			break
		}
	}
	if !found {
		t.Fatal("replacement target missing")
	}
	rebuilt, err := NewState(state.Schema(), state.InputLineageID(), entities, state.Relations())
	if err != nil {
		t.Fatalf("NewState replacement: %v", err)
	}
	return rebuilt
}
