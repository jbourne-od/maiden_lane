package semantic

import (
	"bytes"
	"encoding/hex"
	"slices"
	"testing"
)

// THE PROPERTY §14.2 EXISTS TO PROTECT: correspondence is declared, never inferred from
// names.
//
// Two plans here both declare `team_formed.v1`, and the candidate renames the second
// checkpoint. Name matching would pair the first two automatically and would be right —
// which is precisely why it is dangerous. Two plans may legitimately name the same
// semantics differently, and may name DIFFERENT semantics the same; a comparison built on
// name matching would be right most of the time, and the times it was wrong would be
// indistinguishable from the times it was right.
//
// So an identically named checkpoint in two plans must NOT correspond until somebody says
// it does.
func TestCorrespondenceIsDeclaredRatherThanInferredFromNames(t *testing.T) {
	baseline, candidate := comparisonPlans(t)

	// A policy mapping only the renamed pair. `team_formed.v1` exists under that name in
	// both plans and is deliberately left unmapped.
	policy, err := NewComparisonPolicy(baseline, candidate, []CheckpointPair{
		{Baseline: "team_hos_aggregated.v1", Candidate: "team_hos_reconciled.v2"},
	})
	if err != nil {
		t.Fatalf("NewComparisonPolicy: %v", err)
	}

	sharedBaseline := mustCheckpointIdentity(t, baseline.ID(), "team_formed.v1")
	sharedCandidate := mustCheckpointIdentity(t, candidate.ID(), "team_formed.v1")
	if sharedBaseline == sharedCandidate {
		t.Fatal("the fixture is wrong: the two plans must be distinct, so an identically " +
			"named checkpoint must still have different identities")
	}
	if policy.Corresponds(sharedBaseline, sharedCandidate) {
		t.Fatal("two identically named checkpoints corresponded without being declared")
	}
	if _, found := policy.CandidateFor(sharedBaseline); found {
		t.Fatal("an undeclared baseline checkpoint resolved to a candidate")
	}

	// The declared pair does correspond, so the refusal above is about declaration
	// rather than about the policy failing to work.
	declaredBaseline := mustCheckpointIdentity(t, baseline.ID(), "team_hos_aggregated.v1")
	declaredCandidate := mustCheckpointIdentity(t, candidate.ID(), "team_hos_reconciled.v2")
	if !policy.Corresponds(declaredBaseline, declaredCandidate) {
		t.Fatal("the declared correspondence was not honoured")
	}
}

// FAILING CLOSED, which §14.2 requires: a comparison whose two sides the policy does not
// declare to correspond cannot be constructed at all.
//
// It is refused at construction rather than at evaluation deliberately. Checking later
// would make the refusal a behaviour somebody has to remember, and would let an
// unidentifiable comparison be handed to downstream code that might not.
func TestAComparisonWithoutADeclaredCorrespondenceCannotBeBuilt(t *testing.T) {
	baseline, candidate := comparisonPlans(t)
	policy, err := NewComparisonPolicy(baseline, candidate, []CheckpointPair{
		{Baseline: "team_hos_aggregated.v1", Candidate: "team_hos_reconciled.v2"},
	})
	if err != nil {
		t.Fatalf("NewComparisonPolicy: %v", err)
	}
	request := comparisonRequest(t, baseline, candidate, policy)

	t.Run("the declared pair builds", func(t *testing.T) {
		comparison, err := NewComparison(request)
		if err != nil {
			t.Fatalf("NewComparison: %v", err)
		}
		if comparison.ID() == "" {
			t.Fatal("a valid comparison has no identity")
		}
	})

	t.Run("an undeclared pair is refused", func(t *testing.T) {
		undeclared := request
		undeclared.Baseline = mustCheckpointIdentity(t, baseline.ID(), "team_formed.v1")
		undeclared.Candidate = mustCheckpointIdentity(t, candidate.ID(), "team_formed.v1")
		if _, err := NewComparison(undeclared); err == nil {
			t.Fatal("a comparison was built for checkpoints the policy never mapped")
		}
	})

	t.Run("a candidate the policy did not pair with this baseline is refused", func(t *testing.T) {
		// The baseline IS mapped, just not to this candidate. Checking only that the
		// baseline resolves to something would accept this.
		crossed := request
		crossed.Candidate = mustCheckpointIdentity(t, candidate.ID(), "team_formed.v1")
		if _, err := NewComparison(crossed); err == nil {
			t.Fatal("a comparison was built against a candidate the policy paired elsewhere")
		}
	})
}

// A correspondence must be one-to-one in both directions. Ambiguity is worse than
// absence: a comparison cannot fail closed on an ambiguity it silently resolved by
// taking the first match.
func TestACorrespondenceMustBeUnambiguousInBothDirections(t *testing.T) {
	baseline, candidate := comparisonPlans(t)

	t.Run("one baseline to two candidates", func(t *testing.T) {
		_, err := NewComparisonPolicy(baseline, candidate, []CheckpointPair{
			{Baseline: "team_hos_aggregated.v1", Candidate: "team_hos_reconciled.v2"},
			{Baseline: "team_hos_aggregated.v1", Candidate: "team_formed.v1"},
		})
		if err == nil {
			t.Fatal("a policy mapped one baseline checkpoint to two candidates")
		}
	})

	t.Run("two baselines to one candidate", func(t *testing.T) {
		_, err := NewComparisonPolicy(baseline, candidate, []CheckpointPair{
			{Baseline: "team_hos_aggregated.v1", Candidate: "team_hos_reconciled.v2"},
			{Baseline: "team_formed.v1", Candidate: "team_hos_reconciled.v2"},
		})
		if err == nil {
			t.Fatal("a policy mapped two baseline checkpoints to one candidate")
		}
	})
}

// A correspondence cannot name a checkpoint its plan does not declare, so it cannot
// outlive the declarations it describes.
func TestACorrespondenceCannotNameAnUndeclaredCheckpoint(t *testing.T) {
	baseline, candidate := comparisonPlans(t)

	for _, test := range []struct {
		name string
		pair CheckpointPair
	}{
		{"an unknown baseline checkpoint",
			CheckpointPair{Baseline: "nothing.v1", Candidate: "team_hos_reconciled.v2"}},
		{"an unknown candidate checkpoint",
			CheckpointPair{Baseline: "team_hos_aggregated.v1", Candidate: "nothing.v1"}},
		// The renamed checkpoint exists in the candidate and NOT in the baseline, which
		// is the realistic version of this mistake: the author swapped the two columns.
		{"the columns swapped",
			CheckpointPair{Baseline: "team_hos_reconciled.v2", Candidate: "team_hos_aggregated.v1"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewComparisonPolicy(baseline, candidate, []CheckpointPair{test.pair}); err == nil {
				t.Fatal("a policy named a checkpoint its plan does not declare")
			}
		})
	}

	t.Run("a policy mapping nothing", func(t *testing.T) {
		// A contract that corresponds nothing can never supply the correspondence it will
		// be asked for, so it is a refusal deferred rather than avoided.
		if _, err := NewComparisonPolicy(baseline, candidate, nil); err == nil {
			t.Fatal("a policy mapping no checkpoints was accepted")
		}
	})
}

// A policy is a set of correspondences, so the order they were authored in must not
// become part of its identity.
func TestPolicyIdentityIsIndependentOfAuthoringOrder(t *testing.T) {
	baseline, candidate := comparisonPlans(t)
	pairs := []CheckpointPair{
		{Baseline: "team_hos_aggregated.v1", Candidate: "team_hos_reconciled.v2"},
		{Baseline: "team_formed.v1", Candidate: "team_formed.v1"},
	}

	forward, err := NewComparisonPolicy(baseline, candidate, pairs)
	if err != nil {
		t.Fatalf("NewComparisonPolicy: %v", err)
	}
	reversed := slices.Clone(pairs)
	slices.Reverse(reversed)
	backward, err := NewComparisonPolicy(baseline, candidate, reversed)
	if err != nil {
		t.Fatalf("NewComparisonPolicy reversed: %v", err)
	}

	if forward.ID() != backward.ID() {
		t.Fatalf("authoring order changed the policy identity: %s then %s",
			forward.ID(), backward.ID())
	}
	if string(forward.CanonicalBytes()) != string(backward.CanonicalBytes()) {
		t.Fatal("authoring order changed the canonical bytes")
	}
}

// PRODUCTION BREAK CAUGHT BY MUTATION TESTING, and the test that should have caught it
// passed for the wrong reason.
//
// A policy's identity must depend on its CONTENT, not merely on how many rows it has.
// The identity test below varies the policy by supplying a narrower one, which has a
// different number of correspondences — so it still passed when the encoder dropped the
// correspondence digests and kept only the count. Two policies over the same plans with
// the same number of rows and DIFFERENT pairings would then share an identity, which is
// exactly the "reinterpret a comparison under a different correspondence after the fact"
// failure the identity exists to prevent.
//
// The two policies here are both valid, both one-to-one, both two rows, and mean
// completely different things.
func TestPolicyIdentityDependsOnTheCorrespondencesNotTheirCount(t *testing.T) {
	baseline, candidate := comparisonPlans(t)

	straight, err := NewComparisonPolicy(baseline, candidate, []CheckpointPair{
		{Baseline: "team_hos_aggregated.v1", Candidate: "team_hos_reconciled.v2"},
		{Baseline: "team_formed.v1", Candidate: "team_formed.v1"},
	})
	if err != nil {
		t.Fatalf("NewComparisonPolicy: %v", err)
	}
	crossed, err := NewComparisonPolicy(baseline, candidate, []CheckpointPair{
		{Baseline: "team_hos_aggregated.v1", Candidate: "team_formed.v1"},
		{Baseline: "team_formed.v1", Candidate: "team_hos_reconciled.v2"},
	})
	if err != nil {
		t.Fatalf("NewComparisonPolicy crossed: %v", err)
	}

	if len(straight.Correspondences()) != len(crossed.Correspondences()) {
		t.Fatal("the fixture is wrong: both policies must have the same number of rows, " +
			"or this asserts nothing the count does not already distinguish")
	}
	if straight.ID() == crossed.ID() {
		t.Fatal("two policies pairing the same checkpoints differently share one identity, " +
			"so a comparison could be reinterpreted under a correspondence nobody declared")
	}

	// And they really do declare different things, so the differing identity is about
	// meaning rather than about incidental encoding.
	aggregated := mustCheckpointIdentity(t, baseline.ID(), "team_hos_aggregated.v1")
	reconciled := mustCheckpointIdentity(t, candidate.ID(), "team_hos_reconciled.v2")
	if !straight.Corresponds(aggregated, reconciled) {
		t.Fatal("the straight policy does not declare the pairing it was built with")
	}
	if crossed.Corresponds(aggregated, reconciled) {
		t.Fatal("the crossed policy declares a pairing it was not built with")
	}
}

// EVERY INPUT §14.2 NAMES MUST PARTICIPATE IN THE IDENTITY. An input that did not would
// let two materially different comparisons share one identity, so a result recorded
// against it would answer a question nobody can reconstruct.
func TestEveryComparisonInputParticipatesInItsIdentity(t *testing.T) {
	baseline, candidate := comparisonPlans(t)
	policy, err := NewComparisonPolicy(baseline, candidate, []CheckpointPair{
		{Baseline: "team_hos_aggregated.v1", Candidate: "team_hos_reconciled.v2"},
		{Baseline: "team_formed.v1", Candidate: "team_formed.v1"},
	})
	if err != nil {
		t.Fatalf("NewComparisonPolicy: %v", err)
	}
	base := comparisonRequest(t, baseline, candidate, policy)
	original, err := NewComparison(base)
	if err != nil {
		t.Fatalf("NewComparison: %v", err)
	}

	// A narrower policy over the same plans: it still declares the pair under test, so
	// the comparison stays constructible while the policy identity differs.
	narrower, err := NewComparisonPolicy(baseline, candidate, []CheckpointPair{
		{Baseline: "team_hos_aggregated.v1", Candidate: "team_hos_reconciled.v2"},
	})
	if err != nil {
		t.Fatalf("NewComparisonPolicy narrower: %v", err)
	}

	for _, test := range []struct {
		name   string
		mutate func(*ComparisonRequest)
	}{
		{"baseline checkpoint", func(r *ComparisonRequest) {
			r.Baseline = mustCheckpointIdentity(t, baseline.ID(), "team_formed.v1")
			r.Candidate = mustCheckpointIdentity(t, candidate.ID(), "team_formed.v1")
		}},
		{"profile", func(r *ComparisonRequest) { r.Profile = ProfileID(comparisonDigest("other-profile")) }},
		{"world", func(r *ComparisonRequest) { r.World = WorldID(comparisonDigest("other-world")) }},
		{"corpus", func(r *ComparisonRequest) { r.Corpus = CorpusID(comparisonDigest("other-corpus")) }},
		{"comparison policy", func(r *ComparisonRequest) { r.Policy = narrower }},
	} {
		t.Run(test.name, func(t *testing.T) {
			altered := base
			test.mutate(&altered)
			changed, err := NewComparison(altered)
			if err != nil {
				t.Fatalf("NewComparison: %v", err)
			}
			if changed.ID() == original.ID() {
				t.Fatalf("changing the %s left the comparison identity unchanged, so two "+
					"different questions share one identity", test.name)
			}
		})
	}

	// And the same question asked twice is the same question. This is the other half of
	// the identity's meaning: a comparison names the question, so re-evidencing it later
	// must not produce a different identity.
	again, err := NewComparison(base)
	if err != nil {
		t.Fatalf("NewComparison: %v", err)
	}
	if again.ID() != original.ID() {
		t.Fatal("the same comparison asked twice produced two identities")
	}
}

// Every input is required, because each one is what makes the comparison meaningful:
// without a profile it compares readiness against nothing, without a world or corpus it
// compares over an unstated set of inputs, and without a policy it asserts a
// correspondence nobody declared.
func TestAComparisonRequiresEveryInput(t *testing.T) {
	baseline, candidate := comparisonPlans(t)
	policy, err := NewComparisonPolicy(baseline, candidate, []CheckpointPair{
		{Baseline: "team_hos_aggregated.v1", Candidate: "team_hos_reconciled.v2"},
	})
	if err != nil {
		t.Fatalf("NewComparisonPolicy: %v", err)
	}
	base := comparisonRequest(t, baseline, candidate, policy)

	for _, test := range []struct {
		name  string
		blank func(*ComparisonRequest)
	}{
		{"baseline", func(r *ComparisonRequest) { r.Baseline = "" }},
		{"candidate", func(r *ComparisonRequest) { r.Candidate = "" }},
		{"profile", func(r *ComparisonRequest) { r.Profile = "" }},
		{"world", func(r *ComparisonRequest) { r.World = "" }},
		{"corpus", func(r *ComparisonRequest) { r.Corpus = "" }},
		{"policy", func(r *ComparisonRequest) { r.Policy = ComparisonPolicy{} }},
	} {
		t.Run("no "+test.name, func(t *testing.T) {
			incomplete := base
			test.blank(&incomplete)
			if _, err := NewComparison(incomplete); err == nil {
				t.Fatalf("a comparison was built with no %s", test.name)
			}
		})
	}
}

// The checkpoint identities a correspondence is built from must be the ones the rest of
// the system derives, or a policy would map checkpoints no sealed artifact ever names.
//
// Asserted against Seal's own derivation rather than against a recomputation of the same
// formula, which would only prove that two copies of one mistake agree.
func TestPolicyCheckpointIdentitiesAgreeWithSealing(t *testing.T) {
	binding, c1, _ := checkpointExecutionFixture(t, testGoExecutor)
	artifact := mustSealedCheckpoint(t, SealRequest{
		Binding:          binding,
		Checkpoint:       "team_formed.v1",
		State:            c1.State(),
		Journal:          c1.Journal(),
		InvariantResults: c1.InvariantResults(),
	})

	plan := binding.Plan()
	derived := mustCheckpointIdentity(t, plan.ID(), "team_formed.v1")
	if derived != artifact.CheckpointID() {
		t.Fatalf("the policy derives checkpoint %s while sealing derives %s",
			derived, artifact.CheckpointID())
	}
}

// The accessors must hand out copies, so a caller cannot alter the correspondence an
// identity was derived from.
func TestComparisonPolicyAccessorsReturnCopies(t *testing.T) {
	baseline, candidate := comparisonPlans(t)
	policy, err := NewComparisonPolicy(baseline, candidate, []CheckpointPair{
		{Baseline: "team_hos_aggregated.v1", Candidate: "team_hos_reconciled.v2"},
		{Baseline: "team_formed.v1", Candidate: "team_formed.v1"},
	})
	if err != nil {
		t.Fatalf("NewComparisonPolicy: %v", err)
	}
	before := policy.ID()

	correspondences := policy.Correspondences()
	for i := range correspondences {
		correspondences[i] = CheckpointCorrespondence{}
	}
	canonical := policy.CanonicalBytes()
	for i := range canonical {
		canonical[i] ^= 0xff
	}

	if policy.ID() != before {
		t.Fatal("writing through an accessor changed the policy identity")
	}
	if len(policy.Correspondences()) != 2 || policy.Correspondences()[0] == (CheckpointCorrespondence{}) {
		t.Fatal("Correspondences returned the policy's own slice")
	}
	if string(policy.CanonicalBytes()) == string(canonical) {
		t.Fatal("CanonicalBytes returned the policy's own buffer")
	}
}

// ── fixture ─────────────────────────────────────────────────────────────────

// comparisonPlans compiles two genuinely different plans that share one checkpoint name
// and differ in another, which is the shape correspondence exists for: a checkpoint that
// was renamed, beside one that was not.
func comparisonPlans(t *testing.T) (Plan, Plan) {
	t.Helper()
	baseline := mustCompiledPlan(t, compileFixtureRequest(t, false))

	renamed := compileFixtureRequest(t, false)
	checkpoints := slices.Clone(renamed.Rules.Checkpoints)
	found := false
	for i := range checkpoints {
		if checkpoints[i].Key == "team_hos_aggregated.v1" {
			checkpoints[i].Key = "team_hos_reconciled.v2"
			found = true
		}
	}
	if !found {
		t.Fatal("the fixture no longer declares team_hos_aggregated.v1")
	}
	renamed.Rules.Checkpoints = checkpoints
	candidate := mustCompiledPlan(t, renamed)

	if baseline.ID() == candidate.ID() {
		t.Fatal("the fixture produced one plan twice; correspondence needs two")
	}
	return baseline, candidate
}

func mustCompiledPlan(t *testing.T, request CompileRequest) Plan {
	t.Helper()
	compilation, err := Compile(request)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if failure, refused := compilation.Failure(); refused {
		t.Fatalf("the fixture did not compile: %v", failure)
	}
	plan, ok := compilation.Plan()
	if !ok {
		t.Fatal("compilation produced neither plan nor failure")
	}
	return plan
}

func comparisonRequest(t *testing.T, baseline, candidate Plan, policy ComparisonPolicy) ComparisonRequest {
	t.Helper()
	corpus, err := NewCorpus(corpusCases(t, 2))
	if err != nil {
		t.Fatalf("NewCorpus: %v", err)
	}
	world, err := NewWorld(nil)
	if err != nil {
		t.Fatalf("NewWorld: %v", err)
	}
	return ComparisonRequest{
		Baseline:  mustCheckpointIdentity(t, baseline.ID(), "team_hos_aggregated.v1"),
		Candidate: mustCheckpointIdentity(t, candidate.ID(), "team_hos_reconciled.v2"),
		Profile:   ProfileID(comparisonDigest("cm-profile")),
		World:     world.ID(),
		Corpus:    corpus.ID(),
		Policy:    policy,
	}
}

func mustCheckpointIdentity(t *testing.T, plan PlanID, key CheckpointKey) CheckpointID {
	t.Helper()
	identity, err := checkpointIdentity(plan, key)
	if err != nil {
		t.Fatalf("checkpointIdentity: %v", err)
	}
	return identity
}

func comparisonDigest(label string) string {
	sum := make([]byte, 0, 64)
	for i := 0; i < 64; i++ {
		sum = append(sum, "0123456789abcdef"[(int(label[i%len(label)])+i)%16])
	}
	return "sha256:" + string(sum)
}

// ── golden canonical vectors ────────────────────────────────────────────────

// The behavioural tests above cannot establish everything the canonical tuples claim, and
// these two exist because of exactly that limit rather than as belt-and-braces.
//
// Two omissions are invisible to any valid fixture:
//
//   - Baseline and Candidate cannot be varied independently. The one-to-one correspondence
//     invariant means Candidate is functionally determined by Baseline for a fixed policy,
//     so no valid comparison can change one without the other. A behavioural test can only
//     establish that AT LEAST ONE of the pair participates, never that each does — and
//     deleting either digest alone leaves those tests green. Verified, not assumed.
//   - The policy's plan identities are individually unprovable, because every
//     correspondence's CheckpointID already commits to its plan, so removing the explicit
//     plan digests changes no observable behaviour.
//
// Where the valid state space cannot distinguish an omission, the representation is
// pinned directly. Canonical formats are one of the few places brittleness is the point:
// deliberately changing a v1 tuple should force somebody to edit a conspicuous constant
// and thereby admit they are renaming every artifact that tuple identifies.

// goldenComparisonDigest builds a recognizable fixed digest, so a vector's hex can be read
// by eye and a transposition is visible rather than merely unequal.
func goldenComparisonDigest(character string) string {
	repeated := make([]byte, 0, 64)
	for i := 0; i < 64; i++ {
		repeated = append(repeated, character[0])
	}
	return "sha256:" + string(repeated)
}

// Production break caught: dropping either plan identity, reordering the pair, or dropping
// the correspondence content would let two policies that describe different comparisons
// share one identity.
func TestComparisonPolicyCanonicalGoldenVector(t *testing.T) {
	const wantHex = "00000000000000206d616964656e2d6c616e652e636f6d70617269736f6e2d706f6c6963792e76311111111111111111111111111111111111111111111111111111111111111111222222222222222222222222222222222222222222222222222222222222222200000000000000023333333333333333333333333333333333333333333333333333333333333333444444444444444444444444444444444444444444444444444444444444444455555555555555555555555555555555555555555555555555555555555555556666666666666666666666666666666666666666666666666666666666666666"
	const wantID ComparisonPolicyID = "sha256:d17106e21a39a7780539f5cf8e78a7ccfa5faeea17f01f4cce30f8e0b1feda6b"

	gotBytes, err := comparisonPolicyCanonicalBytes(
		PlanID(goldenComparisonDigest("1")),
		PlanID(goldenComparisonDigest("2")),
		[]CheckpointCorrespondence{
			{
				baseline:  CheckpointID(goldenComparisonDigest("3")),
				candidate: CheckpointID(goldenComparisonDigest("4")),
			},
			{
				baseline:  CheckpointID(goldenComparisonDigest("5")),
				candidate: CheckpointID(goldenComparisonDigest("6")),
			},
		})
	if err != nil {
		t.Fatalf("comparisonPolicyCanonicalBytes: %v", err)
	}
	if got := hex.EncodeToString(gotBytes); got != wantHex {
		t.Fatalf("canonical comparison-policy hex\n got: %s\nwant: %s", got, wantHex)
	}
	if got := ComparisonPolicyID(canonicalDigest(gotBytes)); got != wantID {
		t.Fatalf("ComparisonPolicyID = %q; want %q", got, wantID)
	}
}

// Production break caught: dropping or transposing any of §14.2's six inputs would let two
// materially different comparison questions share one identity, so a result recorded
// against it would answer a question nobody can reconstruct.
//
// The six digests appear in the hex as aa…, bb…, cc…, dd…, ee…, ff… in exactly the order
// §14.2 states them: baseline, candidate, profile, world, corpus, policy.
func TestComparisonCanonicalGoldenVector(t *testing.T) {
	const wantHex = "000000000000001c6d616964656e2d6c616e652e636f6d70617269736f6e2d69642e7631aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaabbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	const wantID ComparisonID = "sha256:57e371d43bb6deaff2b93814057303bbfcb9f46919f900069b11148a12c71f57"

	gotBytes, err := comparisonCanonicalBytes(
		CheckpointID(goldenComparisonDigest("a")),
		CheckpointID(goldenComparisonDigest("b")),
		ProfileID(goldenComparisonDigest("c")),
		WorldID(goldenComparisonDigest("d")),
		CorpusID(goldenComparisonDigest("e")),
		ComparisonPolicyID(goldenComparisonDigest("f")))
	if err != nil {
		t.Fatalf("comparisonCanonicalBytes: %v", err)
	}
	if got := hex.EncodeToString(gotBytes); got != wantHex {
		t.Fatalf("canonical comparison hex\n got: %s\nwant: %s", got, wantHex)
	}
	if got := ComparisonID(canonicalDigest(gotBytes)); got != wantID {
		t.Fatalf("ComparisonID = %q; want %q", got, wantID)
	}
}

// The constructors must produce the bytes the golden vectors pin. Without this the vectors
// would freeze two helpers nothing calls, and the encoders could drift away from them
// while every test stayed green.
func TestTheConstructorsProduceTheCanonicalVectorBytes(t *testing.T) {
	baseline, candidate := comparisonPlans(t)
	policy, err := NewComparisonPolicy(baseline, candidate, []CheckpointPair{
		{Baseline: "team_hos_aggregated.v1", Candidate: "team_hos_reconciled.v2"},
		{Baseline: "team_formed.v1", Candidate: "team_formed.v1"},
	})
	if err != nil {
		t.Fatalf("NewComparisonPolicy: %v", err)
	}
	wantPolicy, err := comparisonPolicyCanonicalBytes(
		baseline.ID(), candidate.ID(), policy.Correspondences())
	if err != nil {
		t.Fatalf("comparisonPolicyCanonicalBytes: %v", err)
	}
	if !bytes.Equal(policy.CanonicalBytes(), wantPolicy) {
		t.Fatal("NewComparisonPolicy does not encode through the canonical helper")
	}

	request := comparisonRequest(t, baseline, candidate, policy)
	comparison, err := NewComparison(request)
	if err != nil {
		t.Fatalf("NewComparison: %v", err)
	}
	wantComparison, err := comparisonCanonicalBytes(
		request.Baseline, request.Candidate, request.Profile,
		request.World, request.Corpus, policy.ID())
	if err != nil {
		t.Fatalf("comparisonCanonicalBytes: %v", err)
	}
	if !bytes.Equal(comparison.CanonicalBytes(), wantComparison) {
		t.Fatal("NewComparison does not encode through the canonical helper")
	}
}
