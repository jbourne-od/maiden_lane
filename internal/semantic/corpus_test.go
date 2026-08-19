package semantic

import (
	"bytes"
	"encoding/hex"
	"slices"
	"testing"
)

// THE PROPERTY THE WHOLE DECISION RESTS ON: a corpus is a set, so assembly order cannot
// affect its identity.
//
// Clause 6 of HLD §14.1 requires a baseline and a candidate to have run over the same
// replay corpus, and the only reason that is checkable is that CorpusID is derived from
// the contents. If order leaked into the identity, two operators assembling the same
// cases would produce different corpora, and the clause would be sensitive to the order
// somebody typed things in rather than to what the corpus contains.
func TestCorpusIdentityIsIndependentOfAssemblyOrder(t *testing.T) {
	cases := corpusCases(t, 4)

	forward, err := NewCorpus(cases)
	if err != nil {
		t.Fatalf("NewCorpus: %v", err)
	}
	reversed := slices.Clone(cases)
	slices.Reverse(reversed)
	backward, err := NewCorpus(reversed)
	if err != nil {
		t.Fatalf("NewCorpus reversed: %v", err)
	}

	if forward.ID() != backward.ID() {
		t.Fatalf("assembly order changed the identity: %s then %s", forward.ID(), backward.ID())
	}
	if !slices.Equal(forward.Digests(), backward.Digests()) {
		t.Fatal("assembly order changed the canonical case order")
	}
	// The canonical bytes, not just the digest, so a collision could not hide a real
	// difference in ordering.
	if string(forward.CanonicalBytes()) != string(backward.CanonicalBytes()) {
		t.Fatal("assembly order changed the canonical bytes")
	}
}

// A different set of cases must be a different corpus. This is the other half of the
// same property: adding a case produces a different identity, so a comparison over the
// enlarged corpus is visibly a different comparison rather than the old one with better
// numbers.
func TestADifferentSetOfCasesIsADifferentCorpus(t *testing.T) {
	cases := corpusCases(t, 4)

	whole, err := NewCorpus(cases)
	if err != nil {
		t.Fatalf("NewCorpus: %v", err)
	}
	fewer, err := NewCorpus(cases[:3])
	if err != nil {
		t.Fatalf("NewCorpus subset: %v", err)
	}
	if whole.ID() == fewer.ID() {
		t.Fatal("dropping a case did not change the corpus identity")
	}
	if whole.Len() != 4 || fewer.Len() != 3 {
		t.Fatalf("lengths = %d and %d, want 4 and 3", whole.Len(), fewer.Len())
	}
}

// A duplicate case is refused rather than deduplicated.
//
// Silently collapsing it would answer a different question than the one asked while
// looking like it answered this one: the caller believes this corpus has more distinct
// cases than it does, and a comparison reported over "twelve cases" that ran eleven is
// a false statement about coverage.
func TestADuplicateCaseIsRefusedRatherThanDeduplicated(t *testing.T) {
	cases := corpusCases(t, 3)
	withDuplicate := append(slices.Clone(cases), cases[1])

	if _, err := NewCorpus(withDuplicate); err == nil {
		t.Fatal("NewCorpus accepted a duplicate case")
	}
}

// An empty corpus is refused, unlike an empty world.
//
// The asymmetry is deliberate and worth stating: an empty world is a real statement —
// nothing was pinned — while a comparison over no cases establishes nothing about a
// candidate. Admitting one would let clause 6 be satisfied by being vacuously true,
// which is the most dangerous way for a gate to pass.
func TestAnEmptyCorpusIsRefused(t *testing.T) {
	if _, err := NewCorpus(nil); err == nil {
		t.Fatal("NewCorpus accepted no cases")
	}
	if _, err := NewCorpus([]State{}); err == nil {
		t.Fatal("NewCorpus accepted an empty slice")
	}

	// The contrast, asserted so the asymmetry is anchored rather than described.
	if _, err := NewWorld(nil); err != nil {
		t.Fatalf("an empty world must remain a real artifact: %v", err)
	}
}

// A zero-valued State cannot contribute a case. Its digest would enter an identity while
// committing to nothing, and this is the only point at which that can be caught.
func TestAnUnverifiableCaseIsRefused(t *testing.T) {
	valid := corpusCases(t, 2)
	if _, err := NewCorpus(append(slices.Clone(valid), State{})); err == nil {
		t.Fatal("NewCorpus accepted a zero-valued state as a case")
	}
}

// A corpus and a world together name exactly the inputs a side must run over. This is
// the operation the corpus exists for, and it is what makes two sides naming the same
// corpus and world provably running over the same inputs.
func TestACorpusAndAWorldNameEveryCaseInput(t *testing.T) {
	corpus, err := NewCorpus(corpusCases(t, 3))
	if err != nil {
		t.Fatalf("NewCorpus: %v", err)
	}
	world, err := NewWorld(nil)
	if err != nil {
		t.Fatalf("NewWorld: %v", err)
	}

	identities, err := corpus.InputIdentities(world)
	if err != nil {
		t.Fatalf("InputIdentities: %v", err)
	}
	if len(identities) != corpus.Len() {
		t.Fatalf("identities = %d, want %d", len(identities), corpus.Len())
	}
	for i, identity := range identities {
		if identity == "" {
			t.Fatalf("case %d has no input identity", i)
		}
		if i > 0 && identity == identities[i-1] {
			t.Fatalf("cases %d and %d share an input identity", i-1, i)
		}
	}

	// Each identity must be the one the rest of the system derives for that case, or a
	// corpus would name inputs no execution could ever match. Asserted against
	// BindRun's own derivation rather than against a recomputation of the same formula.
	first, ok := corpus.Case(0)
	if !ok {
		t.Fatal("the corpus has no first case")
	}
	binding := bindingForState(t, first, world)
	if binding.InputID() != identities[0] {
		t.Fatalf("the corpus derives input %s while binding derives %s",
			identities[0], binding.InputID())
	}

	// A different world must produce different inputs over the same corpus, which is
	// why §14.2 pins the world separately rather than folding it into the corpus.
	reference, err := NewWorldReference(WorldReferenceSnapshot, Digest("sha256:"+corpusRepeat("a", 64)))
	if err != nil {
		t.Fatalf("NewWorldReference: %v", err)
	}
	otherWorld, err := NewWorld([]WorldReference{reference})
	if err != nil {
		t.Fatalf("NewWorld: %v", err)
	}
	otherIdentities, err := corpus.InputIdentities(otherWorld)
	if err != nil {
		t.Fatalf("InputIdentities: %v", err)
	}
	if slices.Equal(identities, otherIdentities) {
		t.Fatal("changing the world left every case input unchanged")
	}
}

// The accessors must hand out copies. A caller able to write through one could alter the
// cases an identity was derived from, inside one process.
func TestCorpusAccessorsReturnCopies(t *testing.T) {
	corpus, err := NewCorpus(corpusCases(t, 3))
	if err != nil {
		t.Fatalf("NewCorpus: %v", err)
	}
	before := corpus.ID()

	digests := corpus.Digests()
	for i := range digests {
		digests[i] = "sha256:" + StateDigest(corpusRepeat("f", 64))
	}
	canonical := corpus.CanonicalBytes()
	for i := range canonical {
		canonical[i] ^= 0xff
	}

	if corpus.ID() != before {
		t.Fatal("writing through an accessor changed the corpus identity")
	}
	if slices.Equal(corpus.Digests(), digests) {
		t.Fatal("Digests returned the corpus's own slice")
	}
	if string(corpus.CanonicalBytes()) == string(canonical) {
		t.Fatal("CanonicalBytes returned the corpus's own buffer")
	}
}

// An index outside the corpus reports absence rather than panicking, because a caller
// iterating a corpus it did not build should not be able to crash the process.
func TestCaseReportsAnOutOfRangeIndex(t *testing.T) {
	corpus, err := NewCorpus(corpusCases(t, 2))
	if err != nil {
		t.Fatalf("NewCorpus: %v", err)
	}
	for _, index := range []int{-1, 2, 99} {
		if _, ok := corpus.Case(index); ok {
			t.Fatalf("index %d reported a case", index)
		}
	}
	if _, ok := corpus.Case(0); !ok {
		t.Fatal("index 0 reported no case")
	}
}

// ── fixture ─────────────────────────────────────────────────────────────────

// corpusCases builds distinct replay cases by varying one field, so every case is a real
// kernel-validated state with its own digest.
//
// Real states rather than fabricated digests, because that is what NewCorpus accepts and
// the reason it does: a corpus assembled from invented digests would carry an identity
// over content nobody has.
func corpusCases(t *testing.T, count int) []State {
	t.Helper()
	schema := compileFixtureSchema(t, false)
	lineage, err := NewInputLineageID("maiden-lane.sanitized-fixture", "team-hos-team-ab")
	if err != nil {
		t.Fatalf("NewInputLineageID: %v", err)
	}

	cases := make([]State, 0, count)
	for i := 0; i < count; i++ {
		// The assignment key distinguishes the cases, which is enough to give each a
		// distinct state digest without needing a distinct schema or lineage.
		key := mustString(t, "case-"+string(rune('a'+i)))
		entities := []Entity{
			mustEntity(t, "driver", SourceEntityID(lineage, "driver", "A"),
				map[FieldName]Value{"assignment_key": key}),
			mustEntity(t, "driver", SourceEntityID(lineage, "driver", "B"),
				map[FieldName]Value{"assignment_key": key}),
		}
		state, err := NewState(schema, lineage, entities, nil)
		if err != nil {
			t.Fatalf("NewState case %d: %v", i, err)
		}
		cases = append(cases, state)
	}
	return cases
}

// bindingForState binds a run over one case, so a corpus's derived input identity can be
// checked against the one the rest of the system derives rather than against a
// recomputation of the same formula in the test.
func bindingForState(t *testing.T, state State, world World) RunBinding {
	t.Helper()
	compilation, err := Compile(compileFixtureRequest(t, false))
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	plan, ok := compilation.Plan()
	if !ok {
		t.Fatal("the fixture did not compile")
	}
	return mustBindRun(t, plan, state, world, testGoExecutor)
}

func corpusRepeat(character string, count int) string {
	out := make([]byte, 0, count)
	for i := 0; i < count; i++ {
		out = append(out, character[0])
	}
	return string(out)
}

// PRODUCTION BREAK CAUGHT BY OWNER REVIEW: the constructor refuses an empty corpus so
// clause 6 cannot be satisfied by being vacuously true, and the zero value walked
// straight back into that state through the one method that asserts what inputs exist.
// It iterated nothing and reported success, so "this corpus names zero inputs" came back
// with a nil error from a corpus NewCorpus would never have produced.
//
// The other accessors are intentionally not covered here. ID(), Len(), and Case() only
// describe an empty value; this is the method that makes a claim.
func TestAnUnconstructedCorpusNamesNoInputs(t *testing.T) {
	world, err := NewWorld(nil)
	if err != nil {
		t.Fatalf("NewWorld: %v", err)
	}

	var zero Corpus
	identities, err := zero.InputIdentities(world)
	if err == nil {
		t.Fatalf("a corpus the constructor would refuse named %d inputs successfully",
			len(identities))
	}
	if identities != nil {
		t.Fatal("a refused corpus returned identities alongside its error")
	}

	// Harmless zero-value description, asserted so a later change that made these error
	// is a deliberate decision rather than a side effect of this one.
	if zero.ID() != "" || zero.Len() != 0 {
		t.Fatalf("the zero corpus describes itself as %q with %d cases", zero.ID(), zero.Len())
	}
	if _, ok := zero.Case(0); ok {
		t.Fatal("the zero corpus reported a case")
	}

	// A constructed corpus still works, or the guard has broken the operation it guards.
	corpus, err := NewCorpus(corpusCases(t, 2))
	if err != nil {
		t.Fatalf("NewCorpus: %v", err)
	}
	if _, err := corpus.InputIdentities(world); err != nil {
		t.Fatalf("a real corpus was refused: %v", err)
	}
}

// A corpus whose cases do not share one schema could never be replayed under any plan,
// because BindRun refuses a state whose schema digest is not the plan's. Refusing at
// assembly means that is discovered when the corpus is built rather than partway through
// executing it.
//
// Found while working corpus persistence: the storage record needs one schema, and the
// reason it can have one is that a corpus with several is unusable.
func TestACorpusRequiresOneSchemaAcrossEveryCase(t *testing.T) {
	cases := corpusCases(t, 2)
	other := corpusCasesUnderWiderSchema(t, 1)

	if cases[0].Schema().Digest() == other[0].Schema().Digest() {
		t.Fatal("the fixture is wrong: the two schemas must differ")
	}
	if _, err := NewCorpus(append(slices.Clone(cases), other...)); err == nil {
		t.Fatal("a corpus accepted cases under two different schemas")
	}

	// A single-schema corpus reports the schema every case shares, which is what
	// comparability will compare against both plans.
	corpus, err := NewCorpus(cases)
	if err != nil {
		t.Fatalf("NewCorpus: %v", err)
	}
	if corpus.SchemaDigest() != cases[0].Schema().Digest() {
		t.Fatalf("schema digest = %s, want the cases' %s",
			corpus.SchemaDigest(), cases[0].Schema().Digest())
	}

	// The schema's ABSENCE from CorpusID is asserted by the golden vector below, not
	// here. An earlier version of this test compared SchemaDigest() to the corpus ID and
	// claimed to establish the exclusion; it established nothing, because CorpusID is a
	// hash either way. Adding the schema digest to the canonical tuple left every corpus
	// test green — verified.
}

// corpusCasesUnderWiderSchema builds cases under a different schema, so a mixed-schema
// corpus can be attempted.
func corpusCasesUnderWiderSchema(t *testing.T, count int) []State {
	t.Helper()
	schema, err := NewSchema(
		[]EntityDeclaration{{Kind: "driver", Fields: []FieldDeclaration{
			{Name: "assignment_key", Kind: ValueString},
		}}}, nil)
	if err != nil {
		t.Fatalf("NewSchema: %v", err)
	}
	lineage, err := NewInputLineageID("maiden-lane.sanitized-fixture", "team-hos-team-ab")
	if err != nil {
		t.Fatalf("NewInputLineageID: %v", err)
	}

	cases := make([]State, 0, count)
	for i := 0; i < count; i++ {
		state, err := NewState(schema, lineage, []Entity{
			mustEntity(t, "driver", SourceEntityID(lineage, "driver", "A"),
				map[FieldName]Value{"assignment_key": mustString(t, "wider-"+string(rune('a'+i)))}),
		}, nil)
		if err != nil {
			t.Fatalf("NewState: %v", err)
		}
		cases = append(cases, state)
	}
	return cases
}

// ── golden canonical vector ─────────────────────────────────────────────────

// Production break caught: the v1 corpus tuple must contain the tag, the case count, and
// the ordered case digests, and NOTHING else.
//
// This is the only way that exclusion can be established. A behavioural test cannot
// observe a redundant canonical input here — adding the schema digest to the tuple
// changes CorpusID, but CorpusID is a hash either way, so every comparison a test could
// make still holds. Verified: the redundancy left every corpus test green, which is why
// the assertion that used to claim this was deleted rather than repaired.
//
// CorpusID is a persisted protocol identity, so freezing its tuple is worth the
// brittleness. Adding the schema, the world, an insertion index, or any other
// well-meaning semantic barnacle now breaks loudly, and deliberately changing v1 forces
// somebody to edit a conspicuous constant and thereby admit they are renaming every
// corpus that identity names.
func TestCorpusCanonicalGoldenVector(t *testing.T) {
	const wantHex = "000000000000001c6d616964656e2d6c616e652e7265706c61792d636f727075732e76310000000000000003111111111111111111111111111111111111111111111111111111111111111122222222222222222222222222222222222222222222222222222222222222223333333333333333333333333333333333333333333333333333333333333333"
	const wantID CorpusID = "sha256:b50f67fa7dc67eec0d2b9dd36ccde5d67e59f929e16ef82c036a521625e2b02d"

	digest := func(character string) StateDigest {
		repeated := make([]byte, 0, 64)
		for i := 0; i < 64; i++ {
			repeated = append(repeated, character[0])
		}
		return StateDigest("sha256:" + string(repeated))
	}

	gotBytes, err := corpusCanonicalBytes([]StateDigest{digest("1"), digest("2"), digest("3")})
	if err != nil {
		t.Fatalf("corpusCanonicalBytes: %v", err)
	}
	if got := hex.EncodeToString(gotBytes); got != wantHex {
		t.Fatalf("canonical corpus hex\n got: %s\nwant: %s", got, wantHex)
	}
	if got := CorpusID(canonicalDigest(gotBytes)); got != wantID {
		t.Fatalf("CorpusID = %q; want %q", got, wantID)
	}
}

// The constructor must produce the bytes the vector pins. Without this the vector would
// freeze a helper nothing calls, and NewCorpus could drift away from it while every test
// stayed green — the same failure mode one level up.
func TestNewCorpusProducesTheCanonicalVectorBytes(t *testing.T) {
	corpus, err := NewCorpus(corpusCases(t, 3))
	if err != nil {
		t.Fatalf("NewCorpus: %v", err)
	}
	want, err := corpusCanonicalBytes(corpus.Digests())
	if err != nil {
		t.Fatalf("corpusCanonicalBytes: %v", err)
	}
	if !bytes.Equal(corpus.CanonicalBytes(), want) {
		t.Fatal("NewCorpus does not encode through the canonical helper")
	}
}
