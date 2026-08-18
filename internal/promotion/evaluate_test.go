package promotion

import (
	"bytes"
	"testing"

	"github.com/optimaldynamics/maiden-lane/internal/fixtures/teamhos"
	"github.com/optimaldynamics/maiden-lane/internal/ports"
)

// A candidate carrying everything the two implemented clauses need must have
// exactly those two evaluated, and must still refuse: seven clauses this build
// cannot answer remain, and a nine-clause gate that authorized on two would be a
// nine-clause gate in name only.
func TestTheImplementedClausesPassAndTheGateStillRefuses(t *testing.T) {
	decision := Evaluate(samplePolicy(), wholeCandidate(t))
	if decision.Authorized() {
		t.Fatal("publication was authorized while seven clauses are unanswerable")
	}

	byClause := clauseIndex(decision)
	for _, clause := range []Clause{ClauseProtectedInvariants, ClauseDigestConsistency} {
		if got := byClause[clause].Verdict(); got != Pass {
			t.Fatalf("clause %v = %v, want Pass", clause, got)
		}
		if got := byClause[clause].Unevaluated(); got != UnevaluatedNotApplicable {
			t.Fatalf("passing clause %v carried reason %v", clause, got)
		}
	}
	for _, clause := range []Clause{
		ClauseStaticValidation, ClauseSealedWithProvenance, ClauseReadyAssessment,
		ClausePinnedIdentities, ClauseComparisonCorpus, ClauseNoMetricRegression,
		ClauseCertifiedBackend,
	} {
		result := byClause[clause]
		if result.Verdict() != NotEvaluated {
			t.Fatalf("clause %v = %v, want NotEvaluated", clause, result.Verdict())
		}
		// UnsupportedByBuild rather than InformationAbsent: no candidate satisfies
		// these and no extra evidence would help, so an operator must be told to
		// wait for engineering rather than sent looking for inputs.
		if result.Unevaluated() != UnsupportedByBuild {
			t.Fatalf("clause %v reason = %v, want UnsupportedByBuild", clause, result.Unevaluated())
		}
	}
	if got := len(decision.Refusals()); got != 7 {
		t.Fatalf("refusals = %d, want the 7 unsupported clauses", got)
	}
}

// The user's recorded edge case, and the reason the two clauses are not one: a
// witness that does not reproduce the artifact's committed digest makes the record
// definitely inconsistent, while saying nothing whatsoever about what the
// invariants did.
//
// Collapsing these into a single Fail would report "protected invariants failed"
// for a checkpoint whose protected invariants provably passed, because an artifact
// whose invariants failed cannot be sealed at all.
func TestAMismatchedWitnessFailsConsistencyWithoutCondemningTheInvariants(t *testing.T) {
	candidate := wholeCandidate(t)
	tampered := bytes.Clone(candidate.RetainedInvariantWitness)
	tampered[len(tampered)-1] ^= 0xff
	candidate.RetainedInvariantWitness = tampered

	byClause := clauseIndex(Evaluate(samplePolicy(), candidate))

	consistency := byClause[ClauseDigestConsistency]
	if consistency.Verdict() != Fail {
		t.Fatalf("digest consistency = %v, want Fail", consistency.Verdict())
	}
	invariants := byClause[ClauseProtectedInvariants]
	if invariants.Verdict() != NotEvaluated {
		t.Fatalf("protected invariants = %v, want NotEvaluated: unattributable "+
			"evidence does not say the invariants failed", invariants.Verdict())
	}
	if invariants.Unevaluated() != InformationAbsent {
		t.Fatalf("protected invariants reason = %v, want InformationAbsent",
			invariants.Unevaluated())
	}
}

// Evidence transplanted from another checkpoint of the same run must not establish
// anything. This is the case that separates verifying a witness against the
// artifact that committed to it from merely holding some well-formed witness.
func TestATransplantedWitnessEstablishesNothing(t *testing.T) {
	sealed := sealTeamHOS(t)
	candidate := Candidate{
		Checkpoint: sealed[0].artifact,
		Assessment: sealed[0].assessments[0],
		// A genuine, kernel-produced witness — for the wrong checkpoint.
		RetainedInvariantWitness: sealed[1].artifact.InvariantResultCanonicalBytes(),
	}

	byClause := clauseIndex(Evaluate(samplePolicy(), candidate))
	if got := byClause[ClauseDigestConsistency].Verdict(); got != Fail {
		t.Fatalf("digest consistency = %v, want Fail", got)
	}
	if got := byClause[ClauseProtectedInvariants].Verdict(); got != NotEvaluated {
		t.Fatalf("protected invariants = %v, want NotEvaluated", got)
	}
}

// An assessment of a different checkpoint must not travel alongside this one. If
// it could, the readiness clause would later read a verdict about the wrong
// artifact, and a checkpoint could be published on another's readiness.
func TestAnAssessmentBoundToAnotherCheckpointFailsConsistency(t *testing.T) {
	sealed := sealTeamHOS(t)
	candidate := Candidate{
		Checkpoint:               sealed[0].artifact,
		Assessment:               sealed[1].assessments[0],
		RetainedInvariantWitness: sealed[0].artifact.InvariantResultCanonicalBytes(),
	}

	byClause := clauseIndex(Evaluate(samplePolicy(), candidate))
	if got := byClause[ClauseDigestConsistency].Verdict(); got != Fail {
		t.Fatalf("digest consistency = %v, want Fail", got)
	}
	// The invariant leg of that same clause is intact, so the invariants clause is
	// unaffected: a misbound assessment says nothing about the invariant evidence.
	if got := byClause[ClauseProtectedInvariants].Verdict(); got != Pass {
		t.Fatalf("protected invariants = %v, want Pass", got)
	}
}

// Absent evidence must be unevaluated rather than failed. Failing would say
// something adverse about a candidate nobody finished describing, and an operator
// would go looking for a defect instead of for the missing input.
//
// Each clause is stated per case rather than asserted jointly, because they do not
// move together and flattening them hides the distinction the two clauses exist
// for: a candidate with a valid witness but no assessment has genuinely
// established that its protected invariants passed, and has established nothing
// about whether its digests agree.
func TestAbsentEvidenceIsUnevaluatedRatherThanFailed(t *testing.T) {
	whole := wholeCandidate(t)
	for _, test := range []struct {
		name        string
		candidate   Candidate
		invariants  Verdict
		consistency Verdict
	}{
		{"no witness", Candidate{Checkpoint: whole.Checkpoint, Assessment: whole.Assessment},
			NotEvaluated, NotEvaluated},
		{"empty witness", Candidate{Checkpoint: whole.Checkpoint, Assessment: whole.Assessment,
			RetainedInvariantWitness: []byte{}}, NotEvaluated, NotEvaluated},
		// The witness is intact here, so the invariants clause is genuinely
		// established. Only the assessment leg of consistency is missing.
		{"no assessment", Candidate{Checkpoint: whole.Checkpoint,
			RetainedInvariantWitness: whole.RetainedInvariantWitness}, Pass, NotEvaluated},
		{"no checkpoint", Candidate{Assessment: whole.Assessment,
			RetainedInvariantWitness: whole.RetainedInvariantWitness}, NotEvaluated, NotEvaluated},
		{"nothing at all", Candidate{}, NotEvaluated, NotEvaluated},
	} {
		t.Run(test.name, func(t *testing.T) {
			byClause := clauseIndex(Evaluate(samplePolicy(), test.candidate))
			for _, expected := range []struct {
				clause  Clause
				verdict Verdict
			}{
				{ClauseProtectedInvariants, test.invariants},
				{ClauseDigestConsistency, test.consistency},
			} {
				result := byClause[expected.clause]
				if result.Verdict() != expected.verdict {
					t.Fatalf("clause %v = %v, want %v", expected.clause,
						result.Verdict(), expected.verdict)
				}
				// Nothing here is adverse about the candidate, so nothing may Fail.
				if result.Verdict() == Fail {
					t.Fatalf("clause %v failed on absent evidence", expected.clause)
				}
				if result.Verdict() == NotEvaluated && result.Unevaluated() != InformationAbsent {
					t.Fatalf("clause %v reason = %v, want InformationAbsent",
						expected.clause, result.Unevaluated())
				}
			}
		})
	}
}

// Production break caught by construction: an absent witness must not be treated
// as the digest of empty input. With no checkpoint at all, the artifact's
// invariant-result digest is the zero value, and a verifier that hashed empty
// input would find them equal and pass the clause on a candidate that carries
// nothing.
func TestAnEmptyWitnessAgainstAnEmptyCheckpointDoesNotPass(t *testing.T) {
	byClause := clauseIndex(Evaluate(samplePolicy(), Candidate{RetainedInvariantWitness: []byte{}}))
	if got := byClause[ClauseProtectedInvariants].Verdict(); got == Pass {
		t.Fatal("an empty witness satisfied the invariants clause against a zero artifact")
	}
}

// A policy that does not name a destination and a required profile is not a
// policy, and no clause may report a result under one -- including the two this
// build can answer from the candidate alone.
//
// This matters more as clauses land. Once all nine are wired, a forgotten policy
// lookup returning the zero value, or a half-written control-plane row, must not
// become an authorization; and it must not become one silently, which is why the
// version supplied is still recorded.
func TestAnUnestablishedPolicyEstablishesNothingEvenForACompleteCandidate(t *testing.T) {
	complete := samplePolicy()
	for _, test := range []struct {
		name   string
		policy ports.TargetPolicy
	}{
		{"zero value", ports.TargetPolicy{}},
		{"version zero", withVersion(complete, 0)},
		{"no tenant", withoutTenant(complete)},
		{"no customer", withoutCustomer(complete)},
		{"no target", withoutTarget(complete)},
		{"no required profile", withoutProfile(complete)},
	} {
		t.Run(test.name, func(t *testing.T) {
			decision := Evaluate(test.policy, wholeCandidate(t))
			if decision.Authorized() {
				t.Fatal("a candidate was authorized under an unestablished policy")
			}
			if got := decision.PolicyVersion(); got != test.policy.Version {
				t.Fatalf("policy version = %d, want the %d it was judged under",
					got, test.policy.Version)
			}
			for _, result := range decision.Clauses() {
				if result.Verdict() != NotEvaluated {
					t.Fatalf("clause %v = %v under an unestablished policy, want NotEvaluated",
						result.Clause(), result.Verdict())
				}
				if result.Unevaluated() != InformationAbsent {
					t.Fatalf("clause %v reason = %v, want InformationAbsent: the missing "+
						"information is the policy", result.Clause(), result.Unevaluated())
				}
			}
		})
	}
}

// Production break caught by construction: Evaluate dispositions every clause
// explicitly, so a clause added to requiredClauses without a decision here is a
// test failure rather than a refusal whose stated reason is a lie. Absent from the
// map it would collapse to InformationAbsent, telling an operator to supply
// evidence for something no code reads.
func TestEvaluateDispositionsEveryRequiredClause(t *testing.T) {
	byClause := clauseIndex(Evaluate(samplePolicy(), Candidate{}))
	for _, clause := range requiredClauses {
		result, present := byClause[clause]
		if !present {
			t.Fatalf("clause %v has no result", clause)
		}
		// Only the two implemented clauses may report InformationAbsent, and only
		// because their evidence is genuinely absent from this empty candidate.
		if result.Unevaluated() == InformationAbsent &&
			clause != ClauseProtectedInvariants && clause != ClauseDigestConsistency {
			t.Fatalf("clause %v reports InformationAbsent, so Evaluate does not "+
				"disposition it and an operator is sent looking for evidence "+
				"nothing reads", clause)
		}
	}
}

// The decision must carry the version it was judged under, so a refusal recorded
// today remains explainable after the target's policy advances.
func TestTheDecisionRecordsThePolicyVersionItWasJudgedUnder(t *testing.T) {
	policy := samplePolicy()
	if got := Evaluate(policy, wholeCandidate(t)).PolicyVersion(); got != policy.Version {
		t.Fatalf("policy version = %d, want %d", got, policy.Version)
	}
}

// Evaluation must neither read the caller's witness after the fact nor write to it.
//
// The first direction is what makes a decision a record: nothing is authorized
// lazily, so a decision cannot change meaning after it is taken. The second is the
// aliasing trap this codebase has hit before -- a slice parameter is a window into
// the caller's buffer, and normalizing bytes in place would corrupt the evidence
// the caller still holds for its own later verification.
func TestEvaluationNeitherRereadsNorRewritesTheCallersWitness(t *testing.T) {
	candidate := wholeCandidate(t)
	before := bytes.Clone(candidate.RetainedInvariantWitness)

	decision := Evaluate(samplePolicy(), candidate)

	if !bytes.Equal(candidate.RetainedInvariantWitness, before) {
		t.Fatal("evaluation wrote through the caller's witness")
	}
	for i := range candidate.RetainedInvariantWitness {
		candidate.RetainedInvariantWitness[i] ^= 0xff
	}
	if got := clauseIndex(decision)[ClauseProtectedInvariants].Verdict(); got != Pass {
		t.Fatalf("a recorded decision changed to %v when the caller's witness was "+
			"mutated afterwards", got)
	}
}

// The theorem the protected-invariants clause rests on, asserted against the kernel
// rather than described in a comment: a run whose protected invariant fails seals
// nothing at that boundary, so no artifact exists that would need this clause to
// report Fail.
//
// HOS_ANCHOR_MISMATCH is an InvariantCode, and InvariantCode is the closed
// protected vocabulary, so the AnchorMismatch golden variant is a genuine protected
// invariant failure rather than some other kind of rejection. The checkpoint before
// the failure still seals, and its own prefix's invariants did pass, so it passes
// this clause -- which is why the clause is scoped to a checkpoint prefix rather
// than to a run.
func TestAProtectedInvariantFailureSealsNothingToJudge(t *testing.T) {
	sealed, rejected := sealTeamHOSVariant(t, teamhos.AnchorMismatch)
	if !rejected {
		t.Fatal("the anchor-mismatch variant completed, so it no longer exercises a " +
			"protected invariant failure")
	}
	passing := sealTeamHOS(t)
	if len(sealed) >= len(passing) {
		t.Fatalf("the rejected run sealed %d checkpoints and the passing run sealed %d: "+
			"a protected invariant failure must leave its boundary unsealed",
			len(sealed), len(passing))
	}
	if len(sealed) == 0 {
		t.Fatal("the rejection preceded every checkpoint, so this asserts nothing about " +
			"a prefix that sealed before a later failure")
	}

	// The prefix that sealed before the failure is judged on its own invariants.
	byClause := clauseIndex(Evaluate(samplePolicy(), Candidate{
		Checkpoint:               sealed[0].artifact,
		Assessment:               sealed[0].assessments[0],
		RetainedInvariantWitness: sealed[0].artifact.InvariantResultCanonicalBytes(),
	}))
	if got := byClause[ClauseProtectedInvariants].Verdict(); got != Pass {
		t.Fatalf("the prefix sealed before the failure = %v, want Pass: the clause is "+
			"scoped to the checkpoint prefix, not to the whole run", got)
	}
}
