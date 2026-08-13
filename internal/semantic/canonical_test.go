package semantic

import (
	"bytes"
	"encoding/hex"
	"testing"
)

const (
	goldenLineageID InputLineageID = "sha256:b091c8c3b981aa6579316a3a14ee11b44abb6c1ee1a1358f43df6b4a285f7ff5"
	goldenSourceID  EntityID       = "sha256:1b6eee7d46de4fea4b2e092dda6bd25b4db31a3bb680b6b40bb9dad358ce98a9"
)

// Production break caught: changing the v1 lineage-root tag, field order, or
// length encoding would silently rename every source and synthetic descendant.
func TestInputLineageCanonicalGoldenVector(t *testing.T) {
	const wantHex = "000000000000001b6d616964656e2d6c616e652e6c696e656167652d726f6f742e7631000000000000001d6d616964656e2d6c616e652e73616e6974697a65642d6669787475726500000000000000107465616d2d686f732d7465616d2d6162"
	const wantID = goldenLineageID

	gotBytes, err := lineageRootCanonicalBytes("maiden-lane.sanitized-fixture", "team-hos-team-ab")
	if err != nil {
		t.Fatalf("lineageRootCanonicalBytes: %v", err)
	}
	if got := hex.EncodeToString(gotBytes); got != wantHex {
		t.Fatalf("canonical lineage hex\n got: %s\nwant: %s", got, wantHex)
	}
	gotID, err := NewInputLineageID("maiden-lane.sanitized-fixture", "team-hos-team-ab")
	if err != nil {
		t.Fatalf("NewInputLineageID: %v", err)
	}
	if gotID != wantID {
		t.Fatalf("InputLineageID = %q; want %q", gotID, wantID)
	}
}

// Production break caught: omitting lineage, kind, or exact source-key bytes
// from the v1 source tuple would collide distinct source identities.
func TestSourceEntityCanonicalGoldenVector(t *testing.T) {
	const wantHex = "000000000000001f6d616964656e2d6c616e652e736f757263652d656e746974792d69642e7631b091c8c3b981aa6579316a3a14ee11b44abb6c1ee1a1358f43df6b4a285f7ff50000000000000006647269766572000000000000000141"
	const wantID = goldenSourceID

	gotBytes, err := sourceEntityIDCanonicalBytes(goldenLineageID, "driver", "A")
	if err != nil {
		t.Fatalf("sourceEntityIDCanonicalBytes: %v", err)
	}
	if got := hex.EncodeToString(gotBytes); got != wantHex {
		t.Fatalf("canonical source ID tuple hex\n got: %s\nwant: %s", got, wantHex)
	}
	if got := SourceEntityID(goldenLineageID, "driver", "A"); got != wantID {
		t.Fatalf("SourceEntityID = %q; want %q", got, wantID)
	}
}

// Production break caught: normalizing a canonical source key would merge
// byte-distinct UTF-8 keys within one lineage.
func TestSourceEntityIDPreservesExactUTF8SourceKey(t *testing.T) {
	composed := SourceEntityID(goldenLineageID, "driver", "caf\u00e9")
	decomposed := SourceEntityID(goldenLineageID, "driver", "cafe\u0301")
	if composed == "" || decomposed == "" {
		t.Fatal("valid UTF-8 source key produced an empty identity")
	}
	if composed == decomposed {
		t.Fatal("byte-distinct UTF-8 source keys produced the same identity")
	}
}

// Production break caught: accepting an empty or malformed lineage-root field
// would create an identity outside the validated canonical domain.
func TestNewInputLineageIDRejectsInvalidDeclaration(t *testing.T) {
	tests := []struct {
		name      string
		namespace string
		rootKey   string
	}{
		{name: "empty namespace", rootKey: "root"},
		{name: "empty root key", namespace: "namespace"},
		{name: "invalid namespace UTF-8", namespace: string([]byte{0xff}), rootKey: "root"},
		{name: "invalid root-key UTF-8", namespace: "namespace", rootKey: string([]byte{0xff})},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewInputLineageID(test.namespace, test.rootKey); err == nil {
				t.Fatal("NewInputLineageID accepted invalid declaration")
			}
		})
	}
}

// Production break caught: including mutable observation content in source
// identity would make stable source references drift across record snapshots.
func TestObservationChangePreservesLineageAndSourceEntityID(t *testing.T) {
	lineage, err := NewInputLineageID("namespace", "continuing-record")
	if err != nil {
		t.Fatalf("NewInputLineageID: %v", err)
	}
	a := SourceEntityID(lineage, "driver", "A")
	if a != SourceEntityID(lineage, "driver", "A") {
		t.Fatal("source ID drifted for identical lineage tuple")
	}
	otherLineage, err := NewInputLineageID("namespace", "different-record")
	if err != nil {
		t.Fatalf("NewInputLineageID(other): %v", err)
	}
	if a == SourceEntityID(otherLineage, "driver", "A") {
		t.Fatal("source identity omitted input lineage")
	}
}

// Production break caught: treating the empty world as missing or unversioned
// would make its identity ambiguous with absent input.
func TestWorldCanonicalEmptyGoldenVector(t *testing.T) {
	const wantHex = "00000000000000146d616964656e2d6c616e652e776f726c642e76310000000000000000"
	const wantID WorldID = "sha256:b12ff2816c22117ecc377d715fc4b509f5eb4f6420e4a0a1375d4cd9e9596057"

	world, err := NewWorld(nil)
	if err != nil {
		t.Fatalf("NewWorld: %v", err)
	}
	if got := hex.EncodeToString(world.CanonicalBytes()); got != wantHex {
		t.Fatalf("canonical empty world hex\n got: %s\nwant: %s", got, wantHex)
	}
	if got := world.ID(); got != wantID {
		t.Fatalf("WorldID = %q; want %q", got, wantID)
	}
	if got := world.References(); len(got) != 0 {
		t.Fatalf("empty world returned %d references", len(got))
	}
}

// Production break caught: preserving caller order for a world set would make
// semantically identical pinned worlds hash differently.
func TestWorldCanonicalBytesIgnoreReferenceInsertionOrder(t *testing.T) {
	a := mustWorldReference(t, WorldReferenceConfiguration, Digest(testDriverBID))
	b := mustWorldReference(t, WorldReferenceSnapshot, Digest(testDriverAID))

	first, err := NewWorld([]WorldReference{a, b})
	if err != nil {
		t.Fatalf("NewWorld(first): %v", err)
	}
	second, err := NewWorld([]WorldReference{b, a})
	if err != nil {
		t.Fatalf("NewWorld(second): %v", err)
	}
	if !bytes.Equal(first.CanonicalBytes(), second.CanonicalBytes()) || first.ID() != second.ID() {
		t.Fatal("world insertion order changed canonical identity")
	}
}

// Production break caught: retaining a caller world slice or returning live
// references/canonical bytes would mutate an already-pinned world identity.
func TestWorldDefensivelyCopiesConstructorInputsAndGetterResults(t *testing.T) {
	reference := mustWorldReference(t, WorldReferenceSnapshot, Digest(testDriverAID))
	input := []WorldReference{reference}
	world, err := NewWorld(input)
	if err != nil {
		t.Fatalf("NewWorld: %v", err)
	}
	wantCanonical := world.CanonicalBytes()
	wantID := world.ID()

	input[0] = WorldReference{}
	references := world.References()
	references[0] = WorldReference{}
	canonical := world.CanonicalBytes()
	canonical[0] ^= 0xff

	stored := world.References()
	if len(stored) != 1 || stored[0] != reference {
		t.Fatalf("stored world references mutated: %+v", stored)
	}
	if !bytes.Equal(wantCanonical, world.CanonicalBytes()) || world.ID() != wantID {
		t.Fatal("world identity changed through caller-owned input or getter result")
	}
}

// Production break caught: accepting duplicate world set members would make
// multiplicity an undeclared part of pinned-world meaning.
func TestNewWorldRejectsDuplicateReference(t *testing.T) {
	reference := mustWorldReference(t, WorldReferenceSnapshot, Digest(testDriverAID))
	if _, err := NewWorld([]WorldReference{reference, reference}); err == nil {
		t.Fatal("NewWorld accepted duplicate reference")
	}
}

// Production break caught: accepting an unknown world-reference union tag
// would extend the closed v1 semantic vocabulary accidentally.
func TestNewWorldReferenceRejectsUnknownKind(t *testing.T) {
	if _, err := NewWorldReference(0, Digest(testDriverAID)); err == nil {
		t.Fatal("NewWorldReference accepted unknown kind")
	}
}

// Production break caught: accepting a malformed referenced digest would make
// raw-digest canonical encoding ambiguous or impossible.
func TestNewWorldReferenceRejectsMalformedDigest(t *testing.T) {
	if _, err := NewWorldReference(WorldReferenceSnapshot, "sha256:ABC"); err == nil {
		t.Fatal("NewWorldReference accepted malformed digest")
	}
}

// Production break caught: schema authoring order would change every state
// identity even when the closed declarations have identical meaning.
func TestSchemaCanonicalBytesIgnoreDeclarationOrder(t *testing.T) {
	first := schemaWithDeclarationOrder(t, false)
	second := schemaWithDeclarationOrder(t, true)
	if !bytes.Equal(first.CanonicalBytes(), second.CanonicalBytes()) || first.Digest() != second.Digest() {
		t.Fatal("schema declaration order changed canonical identity")
	}
}

// Production break caught: changing the v1 schema tag, field table, or marker
// encoding would silently change the schema committed by every state.
func TestSchemaCanonicalGoldenVector(t *testing.T) {
	const wantHex = "00000000000000156d616964656e2d6c616e652e736368656d612e7631000000000000000100000000000000066472697665720000000000000001000000000000000e61737369676e6d656e745f6b657901000000000000000000"
	const wantDigest SchemaDigest = "sha256:6e4e0742bdf4246159f97b7f87a351e0ac54f6144787a52d4d063bcfb637273e"

	schema, err := NewSchema([]EntityDeclaration{{
		Kind: "driver",
		Fields: []FieldDeclaration{{
			Name: "assignment_key", Kind: ValueString, RequiredAtConstruction: false,
		}},
	}}, nil)
	if err != nil {
		t.Fatalf("NewSchema: %v", err)
	}
	if got := hex.EncodeToString(schema.CanonicalBytes()); got != wantHex {
		t.Fatalf("canonical schema hex\n got: %s\nwant: %s", got, wantHex)
	}
	if got := schema.Digest(); got != wantDigest {
		t.Fatalf("SchemaDigest = %q; want %q", got, wantDigest)
	}
}

// Production break caught: iterating caller maps or entity slices directly
// would make semantically identical states acquire different identities.
func TestStateCanonicalBytesIgnoreMapAndEntityInsertionOrder(t *testing.T) {
	a := stateWithOrder(t, []string{"A", "B"}, []string{"assignment_key", "hos_anchor"})
	b := stateWithOrder(t, []string{"B", "A"}, []string{"hos_anchor", "assignment_key"})
	if !bytes.Equal(a.CanonicalBytes(), b.CanonicalBytes()) || a.Digest() != b.Digest() {
		t.Fatal("semantic order changed state identity")
	}
}

// Production break caught: preserving relation insertion order would make the
// graph's content identity depend on input traversal order.
func TestStateCanonicalBytesIgnoreRelationInsertionOrder(t *testing.T) {
	team := mustEntity(t, "team", testTeamID, map[FieldName]Value{
		"assignment_key": mustString(t, "X"),
	})
	driverA := mustEntity(t, "driver", testDriverAID, nil)
	driverB := mustEntity(t, "driver", testDriverBID, nil)
	a := Relation{Kind: "member", From: team.Ref(), To: driverA.Ref()}
	b := Relation{Kind: "member", From: team.Ref(), To: driverB.Ref()}

	first, err := NewState(fixtureSchemaForStateTests(t), testLineageID, []Entity{team, driverA, driverB}, []Relation{a, b})
	if err != nil {
		t.Fatalf("NewState(first): %v", err)
	}
	second, err := NewState(fixtureSchemaForStateTests(t), testLineageID, []Entity{driverB, team, driverA}, []Relation{b, a})
	if err != nil {
		t.Fatalf("NewState(second): %v", err)
	}
	if !bytes.Equal(first.CanonicalBytes(), second.CanonicalBytes()) || first.Digest() != second.Digest() {
		t.Fatal("relation insertion order changed state identity")
	}
}

// Production break caught: changing the state v1 tag, digest representation,
// entity/field table, typed value, or zero relation count would drift replay.
func TestStateCanonicalGoldenVector(t *testing.T) {
	const wantHex = "00000000000000146d616964656e2d6c616e652e73746174652e7631daa9e09c5060542711a13d06bb64a6fe6f84b93664e1698245e5a3e06eab628db091c8c3b981aa6579316a3a14ee11b44abb6c1ee1a1358f43df6b4a285f7ff5000000000000000100000000000000066472697665721b6eee7d46de4fea4b2e092dda6bd25b4db31a3bb680b6b40bb9dad358ce98a90000000000000004000000000000000e61737369676e6d656e745f6b657901000000000000000158000000000000000a686f735f616e63686f7202000000000000000254300000000000000011686f735f64726976696e675f686f7572730300000000000000080000000000000011686f735f656c61707365645f686f75727303000000000000000a0000000000000000"
	const wantDigest StateDigest = "sha256:4c31dd60b8afdf3e496bbfeb2a61b7dcc1a574919d3b2f7f27d773963d20bfd7"

	schema := fixtureSchemaForStateTests(t)
	entity := mustEntity(t, "driver", goldenSourceID, map[FieldName]Value{
		"assignment_key":    mustString(t, "X"),
		"hos_anchor":        mustAtom(t, "T0"),
		"hos_driving_hours": NewInt64Value(8),
		"hos_elapsed_hours": NewInt64Value(10),
	})
	state, err := NewState(schema, goldenLineageID, []Entity{entity}, nil)
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	if got := hex.EncodeToString(state.CanonicalBytes()); got != wantHex {
		t.Fatalf("canonical state hex\n got: %s\nwant: %s", got, wantHex)
	}
	if got := state.Digest(); got != wantDigest {
		t.Fatalf("StateDigest = %q; want %q", got, wantDigest)
	}
}

func mustWorldReference(t *testing.T, kind WorldReferenceKind, digest Digest) WorldReference {
	t.Helper()
	reference, err := NewWorldReference(kind, digest)
	if err != nil {
		t.Fatalf("NewWorldReference: %v", err)
	}
	return reference
}

func schemaWithDeclarationOrder(t *testing.T, reverse bool) Schema {
	t.Helper()
	drivers := EntityDeclaration{
		Kind: "driver",
		Fields: []FieldDeclaration{
			{Name: "assignment_key", Kind: ValueString},
			{Name: "hos_anchor", Kind: ValueAtom},
		},
	}
	team := EntityDeclaration{
		Kind: "team",
		Fields: []FieldDeclaration{
			{Name: "assignment_key", Kind: ValueString, RequiredAtConstruction: true},
		},
	}
	entities := []EntityDeclaration{drivers, team}
	if reverse {
		entities = []EntityDeclaration{team, EntityDeclaration{
			Kind: "driver",
			Fields: []FieldDeclaration{
				{Name: "hos_anchor", Kind: ValueAtom},
				{Name: "assignment_key", Kind: ValueString},
			},
		}}
	}
	schema, err := NewSchema(entities, []RelationDeclaration{{Kind: "member", FromKind: "team", ToKind: "driver"}})
	if err != nil {
		t.Fatalf("NewSchema: %v", err)
	}
	return schema
}

func stateWithOrder(t *testing.T, entityOrder, fieldOrder []string) State {
	t.Helper()
	entities := make([]Entity, 0, len(entityOrder))
	for _, sourceKey := range entityOrder {
		fields := make(map[FieldName]Value, len(fieldOrder))
		for _, fieldName := range fieldOrder {
			switch fieldName {
			case "assignment_key":
				fields[FieldName(fieldName)] = mustString(t, "X")
			case "hos_anchor":
				fields[FieldName(fieldName)] = mustAtom(t, "T0")
			default:
				t.Fatalf("unknown test field %q", fieldName)
			}
		}
		var id EntityID
		switch sourceKey {
		case "A":
			id = testDriverAID
		case "B":
			id = testDriverBID
		default:
			t.Fatalf("unknown test entity %q", sourceKey)
		}
		entities = append(entities, mustEntity(t, "driver", id, fields))
	}
	state, err := NewState(fixtureSchemaForStateTests(t), testLineageID, entities, nil)
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	return state
}
