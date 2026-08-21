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

	outcome, err := ApplyPatch(before, patch)
	if err != nil {
		t.Fatalf("ApplyPatch: %v", err)
	}
	after, failure := outcome.State(), outcome.Failure()
	if failure != nil {
		t.Fatalf("ApplyPatch: %s", failure.Code())
	}
	receipt, ok := outcome.Receipt()
	if !ok {
		t.Fatal("accepted patch returned no receipt")
	}
	if receipt.PatchDigest() != patch.Digest() || receipt.PredecessorStateDigest() != before.Digest() || receipt.ResultStateDigest() != after.Digest() {
		t.Fatalf("receipt links = (%s, %s, %s); want (%s, %s, %s)", receipt.PatchDigest(), receipt.PredecessorStateDigest(), receipt.ResultStateDigest(), patch.Digest(), before.Digest(), after.Digest())
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
			outcome, err := ApplyPatch(test.state, test.patch)
			if err != nil {
				t.Fatalf("ApplyPatch: %v", err)
			}
			returned, failure := outcome.State(), outcome.Failure()
			if failure == nil {
				t.Fatal("invalid patch committed")
			}
			if _, ok := outcome.Receipt(); ok {
				t.Fatal("rejected patch returned an accepted receipt")
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
			outcome, err := ApplyPatch(test.state, test.patch)
			if err != nil {
				t.Fatalf("ApplyPatch: %v", err)
			}
			returned, failure := outcome.State(), outcome.Failure()
			if failure == nil {
				t.Fatal("invalid patch committed")
			}
			if _, ok := outcome.Receipt(); ok {
				t.Fatal("rejected operation returned an accepted receipt")
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

// Production break caught: deferring operation/schema compatibility to final
// state reconstruction could panic or misclassify malformed patch input as a
// protected operation failure.
func TestNewPatchRejectsSchemaInvalidOperations(t *testing.T) {
	schema := fixtureSchemaForStateTests(t)
	team := mustEntity(t, "team", testTeamID, map[FieldName]Value{"assignment_key": mustString(t, "X")})
	driver := mustEntity(t, "driver", testDriverAID, nil)

	tests := []struct {
		name      string
		operation Operation
	}{
		{
			name: "insert field type",
			operation: InsertOperation(mustEntity(t, "team", testTeamID, map[FieldName]Value{
				"assignment_key": mustAtom(t, "X"),
			})),
		},
		{
			name:      "unknown relation kind",
			operation: RelateOperation(Relation{Kind: "unknown", From: team.Ref(), To: driver.Ref()}),
		},
		{
			name:      "relation endpoint kinds",
			operation: RelateOperation(Relation{Kind: "member", From: driver.Ref(), To: team.Ref()}),
		},
		{
			name: "unknown update field",
			operation: UpdateOperation(team.Ref(), []FieldUpdate{{
				Name: "unknown", Before: AbsentField(), After: mustString(t, "X"),
			}}),
		},
		{
			name: "update after-image type",
			operation: UpdateOperation(team.Ref(), []FieldUpdate{{
				Name: "elapsed_duration_hours", Before: AbsentField(), After: mustString(t, "ten"),
			}}),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewPatch(schema, []Operation{test.operation}); err == nil {
				t.Fatal("NewPatch accepted schema-invalid operation")
			}
		})
	}
}

// Production break caught: applying a valid patch to a state under another
// schema could reach final reconstruction and panic after staging changes.
func TestApplyPatchSchemaMismatchReturnsErrorAndPredecessor(t *testing.T) {
	before, team, _ := formTeamPatchFixture(t)
	patch := mustPatch(t, InsertOperation(team))
	alternate := fixtureSchemaWithExtraTeamField(t)
	predecessor, err := NewState(alternate, before.InputLineageID(), before.Entities(), nil)
	if err != nil {
		t.Fatalf("NewState(alternate): %v", err)
	}

	outcome, err := ApplyPatch(predecessor, patch)
	if err == nil {
		t.Fatal("ApplyPatch accepted patch/state schema mismatch")
	}
	if outcome.Failure() != nil {
		t.Fatalf("schema mismatch returned protected failure %q", outcome.Failure().Code())
	}
	if _, ok := outcome.Receipt(); ok {
		t.Fatal("schema mismatch returned accepted receipt")
	}
	if !bytes.Equal(outcome.State().CanonicalBytes(), predecessor.CanonicalBytes()) || outcome.State().Digest() != predecessor.Digest() {
		t.Fatal("schema mismatch did not return exact predecessor")
	}
}

// Production break caught: mutating the staged field map before checking every
// field image could leak an earlier update when a later before-image rejects.
func TestApplyPatchLaterUpdateBeforeImageMismatchLeavesEveryFieldUnchanged(t *testing.T) {
	before, team, _ := formTeamPatchFixture(t)
	predecessor := stateWithTeam(t, before, team, nil)
	patch := mustPatch(t, UpdateOperation(team.Ref(), []FieldUpdate{
		{Name: "aggregation_anchor", Before: AbsentField(), After: mustAtom(t, "T0")},
		{Name: "assignment_key", Before: PresentField(mustString(t, "wrong")), After: mustString(t, "Y")},
	}))

	outcome, err := ApplyPatch(predecessor, patch)
	if err != nil {
		t.Fatalf("ApplyPatch: %v", err)
	}
	if failure := outcome.Failure(); failure == nil || failure.Code() != OperationBeforeImageMismatch {
		t.Fatalf("failure = %#v; want %q", failure, OperationBeforeImageMismatch)
	}
	if _, ok := outcome.Receipt(); ok {
		t.Fatal("rejected multi-field update returned receipt")
	}
	if !bytes.Equal(outcome.State().CanonicalBytes(), predecessor.CanonicalBytes()) || outcome.State().Digest() != predecessor.Digest() {
		t.Fatal("later before-image failure leaked an earlier field update")
	}
	stored, ok := outcome.State().Entity(team.Ref())
	if !ok {
		t.Fatal("team missing from returned predecessor")
	}
	if _, present := stored.Field("aggregation_anchor"); present {
		t.Fatal("rejected earlier field update became visible")
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
	patch, err := NewPatch(fixtureSchemaForStateTests(t), operations)
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
		wantT1Hex                = "00000000000000146d616964656e2d6c616e652e70617463682e7631daa9e09c5060542711a13d06bb64a6fe6f84b93664e1698245e5a3e06eab628d00000000000000030100000000000000047465616d33333333333333333333333333333333333333333333333333333333333333330000000000000001000000000000000e61737369676e6d656e745f6b6579010000000000000001580200000000000000066d656d62657200000000000000047465616d3333333333333333333333333333333333333333333333333333333333333333000000000000000664726976657211111111111111111111111111111111111111111111111111111111111111110200000000000000066d656d62657200000000000000047465616d333333333333333333333333333333333333333333333333333333333333333300000000000000066472697665722222222222222222222222222222222222222222222222222222222222222222"
		wantT1Digest PatchDigest = "sha256:7a5562086adb2fb8aa1eb83cf98145def294f57997ce78023287bca70989afa5"
		wantT2Hex                = "00000000000000146d616964656e2d6c616e652e70617463682e7631daa9e09c5060542711a13d06bb64a6fe6f84b93664e1698245e5a3e06eab628d00000000000000010300000000000000047465616d3333333333333333333333333333333333333333333333333333333333333333000000000000000300000000000000126167677265676174696f6e5f616e63686f72000200000000000000025430000000000000001664726976696e675f6475726174696f6e5f686f757273000300000000000000080000000000000016656c61707365645f6475726174696f6e5f686f7572730003000000000000000a"
		wantT2Digest PatchDigest = "sha256:0a69a112e4d4b10d8902c3f9336146b614fad91b3e8a989912eb3e47263c01f2"
		wantT3Hex                = "00000000000000146d616964656e2d6c616e652e70617463682e7631daa9e09c5060542711a13d06bb64a6fe6f84b93664e1698245e5a3e06eab628d00000000000000020400000000000000066d656d62657200000000000000047465616d3333333333333333333333333333333333333333333333333333333333333333000000000000000664726976657211111111111111111111111111111111111111111111111111111111111111110500000000000000047465616d33333333333333333333333333333333333333333333333333333333333333330000000000000001000000000000000e61737369676e6d656e745f6b657901000000000000000158"
		wantT3Digest PatchDigest = "sha256:b881bd8bbc4f76b06c1c9d47b4ddddbbc72529ecb68f86fa35788f675f03022b"
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
	t3 := mustPatch(t,
		UnrelateOperation(memberRelation(team.Ref(), drivers[0].Ref())),
		DeleteOperation(team),
	)

	assertPatchVector(t, "T1", t1, wantT1Hex, wantT1Digest)
	assertPatchVector(t, "T2", t2, wantT2Hex, wantT2Digest)
	assertPatchVector(t, "T3", t3, wantT3Hex, wantT3Digest)
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

		applyOutcome, err := ApplyPatch(predecessor, patch)
		if err != nil {
			t.Fatalf("sample %d ApplyPatch: %v", sample, err)
		}
		after, failure := applyOutcome.State(), applyOutcome.Failure()
		if failure != nil {
			t.Fatalf("sample %d ApplyPatch: %s", sample, failure.Code())
		}
		receipt := mustAcceptedReceipt(t, applyOutcome)
		undoOutcome, err := UndoPatch(after, patch, receipt)
		if err != nil {
			t.Fatalf("sample %d UndoPatch: %v", sample, err)
		}
		reproduced, failure := undoOutcome.State(), undoOutcome.Failure()
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
	applyOutcome, err := ApplyPatch(predecessor, patch)
	if err != nil {
		t.Fatalf("ApplyPatch: %v", err)
	}
	after, failure := applyOutcome.State(), applyOutcome.Failure()
	if failure != nil {
		t.Fatalf("ApplyPatch: %s", failure.Code())
	}
	undoOutcome, err := UndoPatch(after, patch, mustAcceptedReceipt(t, applyOutcome))
	if err != nil {
		t.Fatalf("UndoPatch: %v", err)
	}
	reproduced, failure := undoOutcome.State(), undoOutcome.Failure()
	if failure != nil {
		t.Fatalf("UndoPatch: %s", failure.Code())
	}
	if !bytes.Equal(reproduced.CanonicalBytes(), predecessor.CanonicalBytes()) {
		t.Fatal("inverse retained a field whose before-image was absent")
	}
}

// Production break caught: inverse application that trusts the patch without
// checking its accepted after-image could erase or overwrite a later state.
func TestUndoPatchCurrentStateReceiptMismatchReturnsCurrentStateUnchanged(t *testing.T) {
	before, team, _ := formTeamPatchFixture(t)
	predecessor := stateWithTeam(t, before, team, nil)
	patch := mustPatch(t, UpdateOperation(team.Ref(), []FieldUpdate{{
		Name: "aggregation_anchor", Before: AbsentField(), After: mustAtom(t, "T0"),
	}}))
	applyOutcome, err := ApplyPatch(predecessor, patch)
	if err != nil {
		t.Fatalf("ApplyPatch: %v", err)
	}
	after, failure := applyOutcome.State(), applyOutcome.Failure()
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

	undoOutcome, err := UndoPatch(tampered, patch, mustAcceptedReceipt(t, applyOutcome))
	if err == nil {
		t.Fatal("UndoPatch accepted current state that does not match receipt result")
	}
	if failure := undoOutcome.Failure(); failure != nil {
		t.Fatalf("receipt-link error returned inaccurate protected code %q", failure.Code())
	}
	returned := undoOutcome.State()
	if !bytes.Equal(returned.CanonicalBytes(), tampered.CanonicalBytes()) || returned.Digest() != tampered.Digest() {
		t.Fatal("rejected inverse changed current state")
	}
}

// Production break caught: accepting a patch as sufficient undo authority
// would delete an independently existing identical entity that this patch was
// never proven to have inserted.
func TestUndoPatchRequiresAcceptedReceiptBeforeRemovingIdenticalEntity(t *testing.T) {
	before, team, _ := formTeamPatchFixture(t)
	patch := mustPatch(t, InsertOperation(team))
	independent := stateWithTeam(t, before, team, nil)

	outcome, err := UndoPatch(independent, patch, AcceptedPatchReceipt{})
	if err == nil {
		t.Fatal("UndoPatch removed entity without accepted-application evidence")
	}
	if outcome.Failure() != nil {
		t.Fatalf("missing receipt returned protected failure %q", outcome.Failure().Code())
	}
	if !bytes.Equal(outcome.State().CanonicalBytes(), independent.CanonicalBytes()) || outcome.State().Digest() != independent.Digest() {
		t.Fatal("missing receipt changed independent current state")
	}
	if _, ok := outcome.State().Entity(team.Ref()); !ok {
		t.Fatal("missing receipt deleted independently existing team")
	}
}

// Production break caught: a receipt accepted for one patch must not authorize
// destructive inverse application of another patch.
func TestUndoPatchRejectsMismatchedPatchReceipt(t *testing.T) {
	before, team, drivers := formTeamPatchFixture(t)
	acceptedPatch := mustPatch(t, InsertOperation(team))
	applyOutcome, err := ApplyPatch(before, acceptedPatch)
	if err != nil || applyOutcome.Failure() != nil {
		t.Fatalf("ApplyPatch = (%#v, %v)", applyOutcome.Failure(), err)
	}
	otherPatch := mustPatch(t,
		InsertOperation(team),
		RelateOperation(memberRelation(team.Ref(), drivers[0].Ref())),
	)

	outcome, err := UndoPatch(applyOutcome.State(), otherPatch, mustAcceptedReceipt(t, applyOutcome))
	if err == nil {
		t.Fatal("UndoPatch accepted receipt for another patch")
	}
	if outcome.Failure() != nil {
		t.Fatalf("receipt mismatch returned protected failure %q", outcome.Failure().Code())
	}
	if !bytes.Equal(outcome.State().CanonicalBytes(), applyOutcome.State().CanonicalBytes()) || outcome.State().Digest() != applyOutcome.State().Digest() {
		t.Fatal("mismatched receipt changed current state")
	}
}

func TestApplyAndUndoStructuralDeleteAndUnrelate(t *testing.T) {
	before, team, drivers := formTeamPatchFixture(t)
	rel := memberRelation(team.Ref(), drivers[0].Ref())
	initialState := stateWithTeam(t, before, team, []Relation{rel})

	// Patch: Unrelate the team from driver, then Delete the team
	patch := mustPatch(t,
		UnrelateOperation(rel),
		DeleteOperation(team),
	)

	// Apply
	applyOutcome, err := ApplyPatch(initialState, patch)
	if err != nil {
		t.Fatalf("ApplyPatch: %v", err)
	}
	if applyOutcome.Failure() != nil {
		t.Fatalf("ApplyPatch failure: %s", applyOutcome.Failure().Code())
	}
	appliedState := applyOutcome.State()
	receipt, ok := applyOutcome.Receipt()
	if !ok {
		t.Fatal("accepted patch returned no receipt")
	}

	// Verify team is deleted and relation is removed
	if _, exists := appliedState.Entity(team.Ref()); exists {
		t.Fatal("deleted team still exists in applied state")
	}
	if stateHasRelation(appliedState, rel) {
		t.Fatal("unrelated relation still exists in applied state")
	}

	// Undo
	undoOutcome, err := UndoPatch(appliedState, patch, receipt)
	if err != nil {
		t.Fatalf("UndoPatch: %v", err)
	}
	if undoOutcome.Failure() != nil {
		t.Fatalf("UndoPatch failure: %s", undoOutcome.Failure().Code())
	}
	undoneState := undoOutcome.State()

	if !bytes.Equal(undoneState.CanonicalBytes(), initialState.CanonicalBytes()) || undoneState.Digest() != initialState.Digest() {
		t.Fatal("UndoPatch did not reconstruct exact byte-identical initial state")
	}
}

func TestApplyStructuralPatchInvariants(t *testing.T) {
	before, team, drivers := formTeamPatchFixture(t)
	rel := memberRelation(team.Ref(), drivers[0].Ref())
	stateWithRel := stateWithTeam(t, before, team, []Relation{rel})

	t.Run("delete target not found", func(t *testing.T) {
		missingTeam := mustEntity(t, "team", EntityID("sha256:9999999999999999999999999999999999999999999999999999999999999999"), map[FieldName]Value{"assignment_key": mustString(t, "X")})
		patch := mustPatch(t, DeleteOperation(missingTeam))
		outcome, err := ApplyPatch(before, patch)
		if err != nil {
			t.Fatalf("ApplyPatch: %v", err)
		}
		if outcome.Failure() == nil || outcome.Failure().Code() != OperationDeleteTargetNotFound {
			t.Fatalf("expected OperationDeleteTargetNotFound, got %v", outcome.Failure())
		}
	})

	t.Run("delete before image mismatch", func(t *testing.T) {
		modifiedTeam := mustEntity(t, "team", testTeamID, map[FieldName]Value{"assignment_key": mustString(t, "OTHER")})
		patch := mustPatch(t, DeleteOperation(modifiedTeam))
		outcome, err := ApplyPatch(stateWithRel, patch)
		if err != nil {
			t.Fatalf("ApplyPatch: %v", err)
		}
		if outcome.Failure() == nil || outcome.Failure().Code() != OperationBeforeImageMismatch {
			t.Fatalf("expected OperationBeforeImageMismatch, got %v", outcome.Failure())
		}
	})

	t.Run("delete leaves dangling relation", func(t *testing.T) {
		// Deleting team without unrelating first leaves dangling relation
		patch := mustPatch(t, DeleteOperation(team))
		outcome, err := ApplyPatch(stateWithRel, patch)
		if err != nil {
			t.Fatalf("ApplyPatch: %v", err)
		}
		if outcome.Failure() == nil || outcome.Failure().Code() != OperationDanglingRelation {
			t.Fatalf("expected OperationDanglingRelation, got %v", outcome.Failure())
		}
	})

	t.Run("unrelate relation not found", func(t *testing.T) {
		missingRel := memberRelation(team.Ref(), drivers[1].Ref())
		patch := mustPatch(t, UnrelateOperation(missingRel))
		outcome, err := ApplyPatch(stateWithRel, patch)
		if err != nil {
			t.Fatalf("ApplyPatch: %v", err)
		}
		if outcome.Failure() == nil || outcome.Failure().Code() != OperationRelationNotFound {
			t.Fatalf("expected OperationRelationNotFound, got %v", outcome.Failure())
		}
	})
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
	patch, err := NewPatch(fixtureSchemaForStateTests(t), operations)
	if err != nil {
		t.Fatalf("NewPatch: %v", err)
	}
	return patch
}

func mustAcceptedReceipt(t *testing.T, outcome PatchOutcome) AcceptedPatchReceipt {
	t.Helper()
	receipt, ok := outcome.Receipt()
	if !ok {
		t.Fatal("accepted patch returned no receipt")
	}
	return receipt
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

func fixtureSchemaWithExtraTeamField(t *testing.T) Schema {
	t.Helper()
	base := fixtureSchemaForStateTests(t).Declaration()
	entities := base.EntityDeclarations()
	for index := range entities {
		if entities[index].Kind == "team" {
			entities[index].Fields = append(entities[index].Fields, FieldDeclaration{Name: "extra", Kind: ValueString})
		}
	}
	schema, err := NewSchema(entities, base.RelationDeclarations())
	if err != nil {
		t.Fatalf("NewSchema(extra field): %v", err)
	}
	return schema
}
