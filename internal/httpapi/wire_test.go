package httpapi

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/optimaldynamics/maiden-lane/internal/fixtures/teamhos"
	openapiv1 "github.com/optimaldynamics/maiden-lane/internal/httpapi/openapiv1"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

// The rule this file exists to protect: JSON is never a canonicalizer.
//
// Translation runs one way, through the kernel's own constructors. The API
// never hashes a DTO, never assembles an identity from parts, and never
// re-derives a digest. If it did, the wire format would become a second source
// of semantic meaning and two encodings of the same facts could disagree about
// what they mean (Inviolate 4).

// Production break caught: if a state built from JSON produced a different
// StateDigest than the same state built directly in Go, then the wire format
// would be deciding identity. Every downstream identity — input, run,
// checkpoint, assessment — is derived from this digest, so a divergence here
// silently forks the entire artifact graph.
func TestStateFromWireMatchesTheKernelConstructedDigest(t *testing.T) {
	inputs, schema := fixtureInputsAndSchema(t)

	direct := inputs.InitialState
	translated, err := stateFromWire(schema, stateInputFor(t, direct))
	if err != nil {
		t.Fatalf("stateFromWire: %v", err)
	}

	if translated.Digest() != direct.Digest() {
		t.Fatalf("wire-built state digest = %s, kernel-built = %s", translated.Digest(), direct.Digest())
	}
	if translated.InputLineageID() != direct.InputLineageID() {
		t.Fatalf("lineage = %s, want %s", translated.InputLineageID(), direct.InputLineageID())
	}
}

// Production break caught: JSON object member order and array order are not
// semantic, so a client that serializes its fields in a different order must
// still produce the identical artifact. If ordering leaked into identity, two
// clients sending the same facts would create two different plans.
func TestWireOrderingDoesNotChangeIdentity(t *testing.T) {
	inputs, schema := fixtureInputsAndSchema(t)
	input := stateInputFor(t, inputs.InitialState)

	first, err := stateFromWire(schema, input)
	if err != nil {
		t.Fatalf("stateFromWire: %v", err)
	}

	reversed := input
	reversed.Entities = append([]openapiv1.EntityInput(nil), input.Entities...)
	for i, j := 0, len(reversed.Entities)-1; i < j; i, j = i+1, j-1 {
		reversed.Entities[i], reversed.Entities[j] = reversed.Entities[j], reversed.Entities[i]
	}

	second, err := stateFromWire(schema, reversed)
	if err != nil {
		t.Fatalf("stateFromWire reversed: %v", err)
	}
	if first.Digest() != second.Digest() {
		t.Fatalf("entity order changed identity: %s vs %s", first.Digest(), second.Digest())
	}
}

// Production break caught: a value whose declared kind disagrees with its
// payload must be rejected rather than coerced. Coercing "10" into an int64
// would let a client's type error become a different, silently valid artifact.
func TestValueTranslationRejectsKindPayloadDisagreement(t *testing.T) {
	tests := []struct {
		name  string
		value openapiv1.Value
	}{
		{"int64 kind carrying a string", openapiv1.Value{Kind: openapiv1.ValueKindInt64, String: ptr("ten")}},
		{"string kind carrying an int", openapiv1.Value{Kind: openapiv1.ValueKindString, Int64: ptrInt(10)}},
		{"atom kind with no payload", openapiv1.Value{Kind: openapiv1.ValueKindAtom}},
		{"two payloads present", openapiv1.Value{Kind: openapiv1.ValueKindString, String: ptr("a"), Int64: ptrInt(1)}},
		{"unknown kind", openapiv1.Value{Kind: openapiv1.ValueKind("float"), String: ptr("1.5")}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := valueFromWire(test.value); err == nil {
				t.Fatal("translation accepted a malformed value")
			}
		})
	}
}

// Production break caught: an entity naming a field the schema does not
// declare must be rejected, not dropped. Silently dropping it would produce a
// valid artifact that omits data the client believed it sent.
func TestUnknownFieldsAreRejectedNotDropped(t *testing.T) {
	inputs, schema := fixtureInputsAndSchema(t)
	input := stateInputFor(t, inputs.InitialState)
	input.Entities[0].Fields["field_that_does_not_exist"] = openapiv1.Value{
		Kind: openapiv1.ValueKindString, String: ptr("x"),
	}

	if _, err := stateFromWire(schema, input); err == nil {
		t.Fatal("translation accepted an undeclared field")
	}
}

// Production break caught: an entity identity supplied or guessed by a client
// would let two tenants collide, or let a client forge an identity that the
// lineage does not actually produce. Identities must be derived by the kernel.
func TestEntityIdentitiesAreDerivedFromLineageNotSupplied(t *testing.T) {
	inputs, schema := fixtureInputsAndSchema(t)
	input := stateInputFor(t, inputs.InitialState)

	// A different lineage over identical source keys must produce entirely
	// different entity identities, and therefore a different state.
	input.Lineage.RootKey = input.Lineage.RootKey + "-second-load"
	translated, err := stateFromWire(schema, input)
	if err != nil {
		t.Fatalf("stateFromWire: %v", err)
	}
	if translated.Digest() == inputs.InitialState.Digest() {
		t.Fatal("changing the lineage did not change the state identity")
	}
	for _, entity := range translated.Entities() {
		for _, original := range inputs.InitialState.Entities() {
			if entity.Ref().ID == original.Ref().ID {
				t.Fatalf("entity identity %s survived a lineage change", entity.Ref().ID)
			}
		}
	}
}

// Production break caught: a plan projection that rebuilt declarations from a
// stored copy of the request would show what was sent rather than what was
// compiled, which is precisely the difference an operator is looking for.
func TestPlanProjectionRoundTripsThroughTheCompiler(t *testing.T) {
	inputs, schema := fixtureInputsAndSchema(t)
	compilation, err := semantic.Compile(inputs.Compilation)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	plan, ok := compilation.Plan()
	if !ok {
		t.Fatal("fixture did not compile")
	}

	projected, err := planToWire(plan, compilation.Profiles(), schema, inputs.Compilation.CompilerSemanticsVersion, true)
	if err != nil {
		t.Fatalf("planToWire: %v", err)
	}
	if projected.PlanID != openapiv1.Digest(plan.ID()) {
		t.Fatalf("projected planID = %s, want %s", projected.PlanID, plan.ID())
	}
	if projected.Declarations == nil {
		t.Fatal("retrieval projection carries no declarations")
	}

	// Re-translating the projection must compile to the identical plan. If it
	// did not, the projection would be describing a plan that does not exist.
	request, err := compileRequestFromWire(*projected.Declarations)
	if err != nil {
		t.Fatalf("compileRequestFromWire: %v", err)
	}
	recompiled, err := semantic.Compile(request)
	if err != nil {
		t.Fatalf("recompile: %v", err)
	}
	roundTripped, ok := recompiled.Plan()
	if !ok {
		failure, _ := recompiled.Failure()
		t.Fatalf("projected declarations did not compile: %+v", failure.Diagnostics())
	}
	if roundTripped.ID() != plan.ID() {
		t.Fatalf("round-tripped planID = %s, want %s", roundTripped.ID(), plan.ID())
	}
}

// Production break caught: omitting declarations on creation keeps the
// response small, but the omission must be deliberate rather than accidental,
// because the same type serves both operations.
func TestCreationProjectionOmitsDeclarations(t *testing.T) {
	inputs, schema := fixtureInputsAndSchema(t)
	compilation, err := semantic.Compile(inputs.Compilation)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	plan, _ := compilation.Plan()

	projected, err := planToWire(plan, compilation.Profiles(), schema, inputs.Compilation.CompilerSemanticsVersion, false)
	if err != nil {
		t.Fatalf("planToWire: %v", err)
	}
	if projected.Declarations != nil {
		t.Fatal("creation response carries declarations")
	}
	if projected.PlanID == "" || len(projected.Profiles) == 0 {
		t.Fatalf("creation response is missing identities: %+v", projected)
	}
}

// Production break caught: this package must never construct a digest. The
// identities it emits have to be the ones the kernel produced, copied
// verbatim.
func TestPackageNeverConstructsADigest(t *testing.T) {
	// A digest is only ever obtained by projection, so the string "sha256:"
	// must not be built anywhere in this package's non-generated source.
	for _, file := range nonGeneratedSourceFiles(t) {
		if strings.Contains(file.contents, `"sha256:`) {
			t.Errorf("%s constructs a digest literal; identities must be projected from the kernel", file.name)
		}
		if strings.Contains(file.contents, "crypto/sha256") {
			t.Errorf("%s imports a hash implementation; only internal/semantic may hash", file.name)
		}
	}
}

func ptr(s string) *string { return &s }

func ptrInt(v int64) *int64 { return &v }

func fixtureInputsAndSchema(t *testing.T) (teamhos.Inputs, semantic.Schema) {
	t.Helper()
	inputs, err := teamhos.New(teamhos.Passing)
	if err != nil {
		t.Fatalf("teamhos.New: %v", err)
	}
	schema, err := semantic.NewSchema(
		inputs.Compilation.Schema.EntityDeclarations(),
		inputs.Compilation.Schema.RelationDeclarations(),
	)
	if err != nil {
		t.Fatalf("NewSchema: %v", err)
	}
	return inputs, schema
}

// stateInputFor rebuilds the wire form of a kernel state so translation can be
// compared against the artifact the kernel itself produced.
func stateInputFor(t *testing.T, state semantic.State) openapiv1.StateInput {
	t.Helper()
	lineage := state.InputLineageID()
	entities := make([]openapiv1.EntityInput, 0, len(state.Entities()))
	for _, entity := range state.Entities() {
		key, ok := sourceKeyFor(lineage, entity.Ref())
		if !ok {
			t.Fatalf("entity %v does not correspond to a known source key", entity.Ref())
		}
		fields := map[string]openapiv1.Value{}
		for name, value := range entity.Fields() {
			fields[string(name)] = valueToWire(value)
		}
		entities = append(entities, openapiv1.EntityInput{
			Kind:               string(entity.Ref().Kind),
			CanonicalSourceKey: key,
			Fields:             fields,
		})
	}
	namespace, rootKey := "maiden-lane.sanitized-fixture", "team-hos-team-ab"
	return openapiv1.StateInput{
		Lineage:  openapiv1.InputLineage{Namespace: namespace, RootKey: rootKey},
		Entities: entities,
	}
}

// sourceKeyFor recovers which ratified source key produced an entity identity.
func sourceKeyFor(lineage semantic.InputLineageID, ref semantic.EntityRef) (string, bool) {
	for _, key := range []string{"A", "B"} {
		if semantic.SourceEntityID(lineage, ref.Kind, key) == ref.ID {
			return key, true
		}
	}
	return "", false
}

// Production break caught: ignoring an unknown JSON member would silently drop
// a misspelled field, so the run would proceed over inputs the client did not
// intend and produce a valid artifact that is quietly wrong. Rejecting is the
// ratified choice (owner decision, 2026-08-16).
func TestStrictDecodingRejectsUnknownMembers(t *testing.T) {
	tests := []struct {
		name string
		body string
		want error
	}{
		{"unknown member", `{"namespace":"a","rootKey":"b","surprise":1}`, errTranslation},
		{"misspelled member", `{"namespace":"a","root_key":"b"}`, errTranslation},
		{"trailing document", `{"namespace":"a","rootKey":"b"}{"namespace":"c","rootKey":"d"}`, errTranslation},
		{"malformed json", `{"namespace":`, errTranslation},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/v1/plans", strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")

			var target openapiv1.InputLineage
			err := decodeJSON(request, &target)
			if !errors.Is(err, test.want) {
				t.Fatalf("err = %v, want %v", err, test.want)
			}
		})
	}
}

// Production break caught: a well-formed document must still decode, or strict
// decoding would reject every request rather than only malformed ones.
func TestStrictDecodingAcceptsExactDocuments(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/v1/plans",
		strings.NewReader(`{"namespace":"maiden-lane.sanitized-fixture","rootKey":"team-hos-team-ab"}`))
	request.Header.Set("Content-Type", "application/json")

	var target openapiv1.InputLineage
	if err := decodeJSON(request, &target); err != nil {
		t.Fatalf("decodeJSON: %v", err)
	}
	if target.Namespace != "maiden-lane.sanitized-fixture" || target.RootKey != "team-hos-team-ab" {
		t.Fatalf("decoded = %+v", target)
	}
}

// Documented limitation, not a passing assertion of desired behavior:
// encoding/json matches member names case-insensitively, so DisallowUnknownFields
// does not reject `rootkey` for `rootKey`. The member still maps to the field
// the client intended, so no data is dropped and no artifact is silently wrong,
// which is the risk strict decoding exists to prevent. What it does mean is
// that this server accepts casings the published contract does not declare, so
// a client must not depend on them: a conforming implementation, or a client
// generated from api/openapi.yaml, will use the declared casing only.
//
// Recorded here so the gap is known rather than discovered later. Closing it
// would require decoding into raw members and comparing keys exactly, which is
// not worth the reflection cost for a mismatch that preserves intent.
func TestStrictDecodingIsCaseInsensitiveForKnownMembers(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/v1/plans",
		strings.NewReader(`{"namespace":"a","rootkey":"b"}`))
	request.Header.Set("Content-Type", "application/json")

	var target openapiv1.InputLineage
	if err := decodeJSON(request, &target); err != nil {
		t.Fatalf("decodeJSON: %v", err)
	}
	if target.RootKey != "b" {
		t.Fatalf("RootKey = %q; the member did not map to the intended field", target.RootKey)
	}
}

// Production break caught: a non-JSON body must answer 415 rather than 400, so
// a client can tell "wrong format" from "wrong content".
func TestNonJSONBodiesAreDistinguishedFromInvalidOnes(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/v1/plans", strings.NewReader(`namespace=a`))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	var target openapiv1.InputLineage
	if err := decodeJSON(request, &target); !errors.Is(err, errUnsupportedMediaType) {
		t.Fatalf("err = %v, want errUnsupportedMediaType", err)
	}
}

// Production break caught: an absent world must become a real, versioned empty
// world rather than a zero value. The empty world is a genuine artifact that
// participates in run identity, so treating "no references" as "no world"
// would change what the run is pinned to.
func TestAbsentWorldBecomesTheVersionedEmptyWorld(t *testing.T) {
	expected, err := semantic.NewWorld(nil)
	if err != nil {
		t.Fatalf("NewWorld: %v", err)
	}

	for _, name := range []string{"nil input", "nil references"} {
		var input *openapiv1.WorldInput
		if name == "nil references" {
			input = &openapiv1.WorldInput{}
		}
		world, err := worldFromWire(input)
		if err != nil {
			t.Fatalf("%s: worldFromWire: %v", name, err)
		}
		if world.ID() != expected.ID() {
			t.Errorf("%s: worldID = %s, want %s", name, world.ID(), expected.ID())
		}
		if world.ID() == "" {
			t.Errorf("%s: empty world has no identity", name)
		}
	}
}

// Production break caught: a pinned reference must change the world identity,
// or replay would not actually be pinned to it.
func TestPinnedWorldReferenceChangesWorldIdentity(t *testing.T) {
	empty, err := worldFromWire(nil)
	if err != nil {
		t.Fatalf("worldFromWire: %v", err)
	}
	references := []openapiv1.WorldReference{{
		Kind:          openapiv1.WorldReferenceKindConfiguration,
		ContentDigest: "sha256:1111111111111111111111111111111111111111111111111111111111111111",
	}}
	pinned, err := worldFromWire(&openapiv1.WorldInput{References: &references})
	if err != nil {
		t.Fatalf("worldFromWire: %v", err)
	}
	if pinned.ID() == empty.ID() {
		t.Fatal("pinning a reference did not change the world identity")
	}
}

// Production break caught: an unratified world reference kind or a malformed
// digest must be refused rather than coerced into a valid-looking world.
func TestWorldTranslationRejectsUnratifiedInput(t *testing.T) {
	tests := []struct {
		name      string
		reference openapiv1.WorldReference
	}{
		{"unknown kind", openapiv1.WorldReference{
			Kind:          openapiv1.WorldReferenceKind("catalog"),
			ContentDigest: "sha256:1111111111111111111111111111111111111111111111111111111111111111",
		}},
		{"malformed digest", openapiv1.WorldReference{
			Kind:          openapiv1.WorldReferenceKindSnapshot,
			ContentDigest: "not-a-digest",
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			references := []openapiv1.WorldReference{test.reference}
			if _, err := worldFromWire(&openapiv1.WorldInput{References: &references}); err == nil {
				t.Fatal("translation accepted an unratified world reference")
			}
		})
	}
}

// Production break caught: executor identity affects only ExecutionID, but it
// must still be validated here, because an invalid one would otherwise surface
// as a machinery failure deep inside binding rather than as invalid input.
func TestExecutorIdentityTranslation(t *testing.T) {
	const version = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
	expected, err := semantic.NewExecutorIdentity("go", version)
	if err != nil {
		t.Fatalf("NewExecutorIdentity: %v", err)
	}
	got, err := executorIdentityFromWire(openapiv1.ExecutorIdentity{Backend: "go", Version: version})
	if err != nil {
		t.Fatalf("executorIdentityFromWire: %v", err)
	}
	if got != expected {
		t.Fatalf("identity = %+v, want %+v", got, expected)
	}

	for _, test := range []struct {
		name     string
		identity openapiv1.ExecutorIdentity
	}{
		{"empty backend", openapiv1.ExecutorIdentity{Backend: "", Version: version}},
		{"uppercase backend", openapiv1.ExecutorIdentity{Backend: "Go", Version: version}},
		{"malformed version", openapiv1.ExecutorIdentity{Backend: "go", Version: "v1.2.3"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := executorIdentityFromWire(test.identity); err == nil {
				t.Fatal("translation accepted an invalid executor identity")
			}
		})
	}
}

// Production break caught: the provenance policy is part of run identity, so
// an unrecognized token must be refused rather than defaulted to changes.v1.
func TestProvenancePolicyTranslationIsClosed(t *testing.T) {
	policy, err := provenancePolicyFromWire(openapiv1.CreateExecutionRequestProvenancePolicyChangesV1)
	if err != nil {
		t.Fatalf("provenancePolicyFromWire: %v", err)
	}
	if policy != semantic.ChangesProvenance {
		t.Fatalf("policy = %v, want ChangesProvenance", policy)
	}
	if _, err := provenancePolicyFromWire("changes.v2"); err == nil {
		t.Fatal("translation accepted an unratified provenance policy")
	}
}

// The projection refuses a declaration this contract version cannot express.
//
// Production break caught: the switch in transformationToWire had no default, so a compiled
// declaration carrying an operator the wire enum lacks projected to a TransformationDeclaration
// whose Operator was the ZERO string -- outside the closed enum -- and whose payload was
// absent entirely. A client reading that response would see a well-formed rule that the server
// does not hold, and neither the enum's own validator nor any test would have said a word.
//
// OperatorSelectAndAssign WAS that operator, and the contract has since gained it, so this no
// longer has a real inexpressible payload to reach for. The default arm is still what stands
// between the next kernel operator and a fabricated response, so it is exercised directly by
// a declaration carrying no payload the projection recognises.
func TestTransformationProjectionRefusesADeclarationTheContractCannotExpress(t *testing.T) {
	projected, err := transformationToWire(semantic.TransformationDeclaration{
		ID:            "unmapped.v1",
		Operator:      semantic.OperatorKind(99),
		DeclaredReads: []semantic.FieldPath{"driver.depot"},
	})
	if err == nil {
		t.Fatalf("projected an inexpressible declaration as %+v", projected)
	}
	if projected.Operator != "" || projected.SelectAssign != nil {
		t.Fatalf("refused projection still returned content: %+v", projected)
	}

	// And every operator the contract DOES express still projects, or this test would pass
	// against a projection that had simply stopped working.
	groupBy := semantic.Expr{Kind: semantic.ExprField, Field: "driver.depot"}
	for _, expressible := range []semantic.TransformationDeclaration{
		{ID: "certify_depot.v1", Operator: semantic.OperatorSelectAndAssign,
			SelectAssign: &semantic.SelectAssignDeclaration{
				Selector: semantic.Selector{Kind: "driver", GroupBy: &groupBy,
					Members: semantic.Cardinality{Kind: semantic.CardinalityAtLeast, Count: 1}},
				Guard: semantic.Expr{Kind: semantic.ExprAllEqual, Field: "driver.depot"},
				Assignments: []semantic.FieldAssignment{{Target: "driver.status",
					Value: semantic.Expr{Kind: semantic.ExprField, Field: "driver.depot"}}},
			}},
	} {
		if _, err := transformationToWire(expressible); err != nil {
			t.Fatalf("%s: %v", expressible.ID, err)
		}
	}
}
