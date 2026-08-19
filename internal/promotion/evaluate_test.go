package promotion

import (
	"bytes"
	"slices"
	"testing"

	"github.com/optimaldynamics/maiden-lane/internal/fixtures/teamhos"
	"github.com/optimaldynamics/maiden-lane/internal/ports"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

// A candidate carrying everything the six implemented clauses need must have exactly
// those six evaluated, and must still refuse: three clauses this build cannot answer
// remain, and a nine-clause gate that authorized on six would be a nine-clause gate in
// name only.
func TestTheImplementedClausesPassAndTheGateStillRefuses(t *testing.T) {
	sealed := sealTeamHOS(t)
	candidate := candidateFrom(sealed[0])
	// Comparison evidence for the checkpoint being promoted, so clause 6 answers about
	// this candidate rather than about its own missing evidence.
	candidate.Comparison = comparisonEvidenceFor(t, sealed[0])
	// The second checkpoint is assessed ready under the CM profile; the first is only
	// ready under CM too, so either serves. Bind the policy to the profile the
	// assessment actually used, or the readiness clause answers about its own missing
	// evidence rather than about this candidate.
	policy := policyRequiring(sealed[0].assessments[0].ProfileID())

	decision := Evaluate(policy, candidate)
	if decision.Authorized() {
		t.Fatal("publication was authorized while three clauses are unanswerable")
	}

	byClause := clauseIndex(decision)
	for _, clause := range implementedClauses {
		result := byClause[clause]
		if result.Verdict() != Pass {
			t.Fatalf("clause %v = %v/%v, want Pass", clause, result.Verdict(), result.Unevaluated())
		}
		if result.Unevaluated() != UnevaluatedNotApplicable {
			t.Fatalf("passing clause %v carried reason %v", clause, result.Unevaluated())
		}
	}
	for _, clause := range unsupportedClauses {
		result := byClause[clause]
		if result.Verdict() != NotEvaluated {
			t.Fatalf("clause %v = %v, want NotEvaluated", clause, result.Verdict())
		}
		// UnsupportedByBuild rather than InformationAbsent: no candidate satisfies
		// these and no extra evidence would help, so an operator must be told to wait
		// for engineering rather than sent looking for inputs.
		if result.Unevaluated() != UnsupportedByBuild {
			t.Fatalf("clause %v reason = %v, want UnsupportedByBuild", clause, result.Unevaluated())
		}
	}
	if got := len(decision.Refusals()); got != len(unsupportedClauses) {
		t.Fatalf("refusals = %d, want the %d unsupported clauses", got, len(unsupportedClauses))
	}
}

// implementedClauses and unsupportedClauses are the two halves of §14.1 in this build.
// Together they must be every required clause, which a test below asserts: a clause
// that fell out of both lists would stop being covered without any test failing.
var implementedClauses = []Clause{
	ClauseStaticValidation,
	ClauseSealedWithProvenance,
	ClauseProtectedInvariants,
	ClauseReadyAssessment,
	ClausePinnedIdentities,
	ClauseComparisonCorpus,
	ClauseDigestConsistency,
}

// The two that name a concept this codebase does not have: a protected-metric regression
// policy, and executor certification against a reference implementation. Each is a
// programme, not a task. The replay corpus was the third and is now implemented.
var unsupportedClauses = []Clause{
	ClauseNoMetricRegression,
	ClauseCertifiedBackend,
}

// Production break caught by construction: if a clause were dropped from both lists,
// every test above would still pass while nothing asserted anything about it.
func TestTheTwoClauseListsCoverEveryRequiredClause(t *testing.T) {
	covered := map[Clause]int{}
	for _, clause := range implementedClauses {
		covered[clause]++
	}
	for _, clause := range unsupportedClauses {
		covered[clause]++
	}
	for _, clause := range requiredClauses {
		switch covered[clause] {
		case 0:
			t.Errorf("clause %v is in neither list, so no test covers it", clause)
		case 1:
		default:
			t.Errorf("clause %v is in both lists", clause)
		}
	}
	if got := len(implementedClauses) + len(unsupportedClauses); got != len(requiredClauses) {
		t.Fatalf("the lists hold %d clauses, want %d", got, len(requiredClauses))
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
		// Only an implemented clause may report InformationAbsent, and only because its
		// evidence is genuinely absent from this empty candidate. An unsupported clause
		// reporting it would send an operator looking for evidence nothing reads.
		if result.Unevaluated() == InformationAbsent && !slices.Contains(implementedClauses, clause) {
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

// ── the four clauses wired in this slice ────────────────────────────────────

// Static validation is established by a compiled plan existing, not by re-running
// validation. But the plan must be the one the checkpoint was sealed under: a plan
// supplied that is not that one is a definite disagreement about which program produced
// this checkpoint, which is adverse rather than merely unknown.
func TestStaticValidationRequiresThePlanTheCheckpointWasSealedUnder(t *testing.T) {
	sealed := sealTeamHOS(t)
	policy := policyRequiring(sealed[0].assessments[0].ProfileID())

	t.Run("no plan is unevaluated", func(t *testing.T) {
		candidate := candidateFrom(sealed[0])
		candidate.Plan = semantic.Plan{}
		result := clauseIndex(Evaluate(policy, candidate))[ClauseStaticValidation]
		if result.Verdict() != NotEvaluated || result.Unevaluated() != InformationAbsent {
			t.Fatalf("= %v/%v, want NotEvaluated/InformationAbsent",
				result.Verdict(), result.Unevaluated())
		}
	})

	t.Run("another program's plan fails", func(t *testing.T) {
		candidate := candidateFrom(sealed[0])
		candidate.Plan = otherPlan(t)
		if candidate.Plan.ID() == candidate.Checkpoint.PlanID() {
			t.Fatal("the fixture is wrong: this must be a different plan")
		}
		result := clauseIndex(Evaluate(policy, candidate))[ClauseStaticValidation]
		if result.Verdict() != Fail {
			t.Fatalf("= %v, want Fail: the plan is not the one this checkpoint was "+
				"sealed under, which is adverse rather than unknown", result.Verdict())
		}
	})
}

// The readiness clause has three distinct answers and conflating any two would
// authorize or condemn wrongly. This is the clause where the three-valued verdict
// vocabulary earns its existence.
func TestReadyAssessmentDistinguishesWrongProfileFromNotReady(t *testing.T) {
	sealed := sealTeamHOS(t)

	// The first checkpoint is ready under CM and needs_input under the optimizer, which
	// is the natural pair for this test: the same artifact, two real answers.
	var ready, needsInput semantic.Assessment
	for _, assessment := range sealed[0].assessments {
		switch assessment.Verdict() {
		case semantic.Ready:
			ready = assessment
		case semantic.NeedsInput:
			needsInput = assessment
		}
	}
	if ready.ID() == "" || needsInput.ID() == "" {
		t.Fatal("the fixture must supply both a ready and a needs_input assessment")
	}

	t.Run("ready under the required profile passes", func(t *testing.T) {
		candidate := candidateFrom(sealed[0])
		candidate.Assessment = ready
		result := clauseIndex(Evaluate(policyRequiring(ready.ProfileID()), candidate))[ClauseReadyAssessment]
		if result.Verdict() != Pass {
			t.Fatalf("= %v/%v, want Pass", result.Verdict(), result.Unevaluated())
		}
	})

	t.Run("needs_input under the required profile fails", func(t *testing.T) {
		candidate := candidateFrom(sealed[0])
		candidate.Assessment = needsInput
		result := clauseIndex(Evaluate(policyRequiring(needsInput.ProfileID()), candidate))[ClauseReadyAssessment]
		// A real, adverse answer about this candidate: it was assessed under the profile
		// the target requires and found incomplete.
		if result.Verdict() != Fail {
			t.Fatalf("= %v, want Fail", result.Verdict())
		}
	})

	t.Run("ready under a different profile is unevaluated, not passed", func(t *testing.T) {
		candidate := candidateFrom(sealed[0])
		candidate.Assessment = ready
		// The target requires the profile this checkpoint is NOT ready under.
		result := clauseIndex(Evaluate(policyRequiring(needsInput.ProfileID()), candidate))[ClauseReadyAssessment]
		if result.Verdict() == Pass {
			t.Fatal("a ready verdict under another profile satisfied the clause, so the " +
				"gate would authorize on a question nobody asked")
		}
		if result.Verdict() != NotEvaluated || result.Unevaluated() != InformationAbsent {
			t.Fatalf("= %v/%v, want NotEvaluated/InformationAbsent: nothing is known "+
				"about the required profile", result.Verdict(), result.Unevaluated())
		}
	})

	t.Run("an assessment of another checkpoint is unevaluated", func(t *testing.T) {
		candidate := candidateFrom(sealed[0])
		candidate.Assessment = sealed[1].assessments[0]
		result := clauseIndex(Evaluate(
			policyRequiring(sealed[1].assessments[0].ProfileID()), candidate))[ClauseReadyAssessment]
		if result.Verdict() == Pass {
			t.Fatal("another checkpoint's assessment satisfied this checkpoint's clause")
		}
	})

	t.Run("a policy binding no profile is unevaluated", func(t *testing.T) {
		// Evaluate refuses an unestablished policy before any clause runs, so the guard
		// inside this clause is unreachable through it. Called directly, for the same
		// reason app.advancePointer is: a guard that cannot be reached today becomes
		// load-bearing the moment the policy shape changes, and an empty required
		// profile compared against an assessment's would match nothing while looking
		// like a comparison.
		//
		// An earlier version of this subtest went through Evaluate and therefore
		// asserted the short-circuit rather than the guard.
		policy := policyRequiring(ready.ProfileID())
		policy.RequiredProfileID = ""
		result := readyAssessment(policy, candidateFrom(sealed[0]))
		if result.Verdict() != NotEvaluated || result.Unevaluated() != InformationAbsent {
			t.Fatalf("= %v/%v, want NotEvaluated/InformationAbsent",
				result.Verdict(), result.Unevaluated())
		}
	})
}

// The pinned-identity clause names ten identities. Only the two a caller supplies --
// the plan's three parts and the execution identity -- can be absent from an otherwise
// well-formed candidate, because the other seven come from artifacts that cannot be
// constructed without them. So this covers those, plus the cross-links, which are the
// failure a list of non-empty strings cannot detect.
//
// Saying that plainly matters: an earlier comment here claimed every identity was
// individually covered, which was true of the code and not of the test.
func TestPinnedIdentitiesRequiresEveryNamedIdentity(t *testing.T) {
	sealed := sealTeamHOS(t)
	policy := policyRequiring(sealed[0].assessments[0].ProfileID())

	t.Run("a complete candidate passes", func(t *testing.T) {
		result := clauseIndex(Evaluate(policy, candidateFrom(sealed[0])))[ClausePinnedIdentities]
		if result.Verdict() != Pass {
			t.Fatalf("= %v/%v, want Pass", result.Verdict(), result.Unevaluated())
		}
	})

	// Only the identities a caller supplies can be blanked; the seven the kernel derives
	// cannot be, which is the point of them coming from authenticated artifacts. Those
	// are covered by the absent-artifact cases instead.
	t.Run("no execution identity is unevaluated", func(t *testing.T) {
		candidate := candidateFrom(sealed[0])
		candidate.ExecutionID = ""
		result := clauseIndex(Evaluate(policy, candidate))[ClausePinnedIdentities]
		if result.Verdict() != NotEvaluated || result.Unevaluated() != InformationAbsent {
			t.Fatalf("= %v/%v, want NotEvaluated/InformationAbsent",
				result.Verdict(), result.Unevaluated())
		}
	})

	t.Run("no plan is unevaluated", func(t *testing.T) {
		candidate := candidateFrom(sealed[0])
		candidate.Plan = semantic.Plan{}
		result := clauseIndex(Evaluate(policy, candidate))[ClausePinnedIdentities]
		if result.Verdict() != NotEvaluated {
			t.Fatalf("= %v, want NotEvaluated", result.Verdict())
		}
	})

	// Every identity can be present while the links between them contradict, which a
	// list of non-empty strings cannot rule out. That is adverse, not unknown.
	t.Run("a contradicting plan link fails", func(t *testing.T) {
		candidate := candidateFrom(sealed[0])
		candidate.Plan = otherPlan(t)
		result := clauseIndex(Evaluate(policy, candidate))[ClausePinnedIdentities]
		if result.Verdict() != Fail {
			t.Fatalf("= %v, want Fail", result.Verdict())
		}
	})

	t.Run("a contradicting assessment link fails", func(t *testing.T) {
		candidate := candidateFrom(sealed[0])
		candidate.Assessment = sealed[1].assessments[0]
		result := clauseIndex(Evaluate(policy, candidate))[ClausePinnedIdentities]
		if result.Verdict() != Fail {
			t.Fatalf("= %v, want Fail", result.Verdict())
		}
	})
}

// The provenance clause rests on the kernel refusing any policy but `changes`, which is
// a theorem rather than a comparison this clause performs. Asserted against the kernel
// so the caveat in its comment is anchored to something that would fail if it stopped
// being true.
func TestProvenanceIsEstablishedByTheKernelRefusingAnythingElse(t *testing.T) {
	sealed := sealTeamHOS(t)
	policy := policyRequiring(sealed[0].assessments[0].ProfileID())

	result := clauseIndex(Evaluate(policy, candidateFrom(sealed[0])))[ClauseSealedWithProvenance]
	if result.Verdict() != Pass {
		t.Fatalf("= %v/%v, want Pass", result.Verdict(), result.Unevaluated())
	}

	// The theorem the clause rests on: binding refuses any other provenance policy, and
	// Seal requires a binding, so no sealed artifact can carry anything else.
	//
	// The request below is otherwise valid and differs only in its policy, which the
	// first draft of this test got wrong: it passed a zero-valued request, which BindRun
	// refuses for a dozen reasons unrelated to provenance, so it would have passed with
	// the policy check deleted. Isolating the one field is what makes this an assertion.
	//
	// Verified by mutation, and the result is worth writing down: the kernel refuses a
	// foreign provenance policy in THREE independent places -- the binding request check,
	// the binding validity check, and the canonical encoder -- so this fails only when
	// all three are removed. That is the correct granularity. The theorem this clause
	// rests on is a property of BindRun's observable behaviour, not of any one check, and
	// it survives as long as any of them stands.
	inputs, err := teamhos.New(teamhos.Passing)
	if err != nil {
		t.Fatalf("teamhos.New: %v", err)
	}
	compilation, err := semantic.Compile(inputs.Compilation)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	plan, ok := compilation.Plan()
	if !ok {
		t.Fatal("the fixture did not compile")
	}
	valid := semantic.RunBindingRequest{
		Plan: plan, InitialState: inputs.InitialState, World: inputs.World,
		ExecutorIdentity: inputs.ExecutorIdentity, Policy: inputs.Policy,
	}
	if _, err := semantic.BindRun(valid); err != nil {
		t.Fatalf("the control case must succeed, or the next assertion proves nothing: %v", err)
	}

	other := valid
	other.Policy = semantic.ProvenancePolicy(2)
	if _, err := semantic.BindRun(other); err == nil {
		t.Fatal("BindRun accepted a provenance policy other than changes, so " +
			"'at least changes' is now a comparison this clause does not perform")
	}
}

// ── the comparison-corpus clause ────────────────────────────────────────────

// Absent comparison evidence is unevaluated, never failed. A candidate nobody supplied a
// comparison for says nothing adverse about itself, and telling an operator otherwise
// would send them to investigate a defect that does not exist.
func TestAbsentComparisonEvidenceIsUnevaluated(t *testing.T) {
	sealed := sealTeamHOS(t)
	candidate := candidateFrom(sealed[0])
	policy := policyRequiring(sealed[0].assessments[0].ProfileID())

	result := clauseIndex(Evaluate(policy, candidate))[ClauseComparisonCorpus]
	if result.Verdict() != NotEvaluated || result.Unevaluated() != InformationAbsent {
		t.Fatalf("= %v/%v, want NotEvaluated/InformationAbsent",
			result.Verdict(), result.Unevaluated())
	}
}

// Evidence that contradicts the comparison is a definite adverse finding, unlike evidence
// that is merely missing. Each of these is a way a side can have answered a different
// question while looking complete.
func TestComparisonEvidenceMustAnswerThisComparison(t *testing.T) {
	sealed := sealTeamHOS(t)
	policy := policyRequiring(sealed[0].assessments[0].ProfileID())

	t.Run("complete evidence passes", func(t *testing.T) {
		candidate := candidateFrom(sealed[0])
		candidate.Comparison = comparisonEvidenceFor(t, sealed[0])
		result := clauseIndex(Evaluate(policy, candidate))[ClauseComparisonCorpus]
		if result.Verdict() != Pass {
			t.Fatalf("= %v/%v, want Pass", result.Verdict(), result.Unevaluated())
		}
	})

	// The checkpoint being promoted must BE the candidate side. Without this, evidence
	// from a comparison of two entirely different declarations would satisfy the clause.
	t.Run("a comparison of some other checkpoint fails", func(t *testing.T) {
		candidate := candidateFrom(sealed[0])
		candidate.Comparison = comparisonEvidenceFor(t, sealed[1])
		result := clauseIndex(Evaluate(policy, candidate))[ClauseComparisonCorpus]
		if result.Verdict() != Fail {
			t.Fatalf("= %v, want Fail: this comparison is about another checkpoint",
				result.Verdict())
		}
	})

	t.Run("a side with no evidence is unevaluated", func(t *testing.T) {
		candidate := candidateFrom(sealed[0])
		evidence := comparisonEvidenceFor(t, sealed[0])
		evidence.Baseline = nil
		candidate.Comparison = evidence
		result := clauseIndex(Evaluate(policy, candidate))[ClauseComparisonCorpus]
		if result.Verdict() != NotEvaluated {
			t.Fatalf("= %v, want NotEvaluated: a side with no evidence is missing, "+
				"not wrong", result.Verdict())
		}
	})

	// Coverage: the case digests must re-derive the corpus the comparison names. A side
	// that ran a subset has answered a question about a smaller corpus.
	t.Run("a side not covering the corpus fails", func(t *testing.T) {
		candidate := candidateFrom(sealed[0])
		evidence := comparisonEvidenceFor(t, sealed[0])
		// Duplicate the single case, so the side has evidence whose digests cannot be
		// this corpus.
		evidence.Candidate = append(evidence.Candidate, evidence.Candidate[0])
		candidate.Comparison = evidence
		result := clauseIndex(Evaluate(policy, candidate))[ClauseComparisonCorpus]
		if result.Verdict() != Fail {
			t.Fatalf("= %v, want Fail: these cases are not this corpus", result.Verdict())
		}
	})

	// The artifact must realize the declaration its side names. A side that ran the right
	// corpus under the wrong checkpoint answered a different question.
	t.Run("a side's artifact realizing another declaration fails", func(t *testing.T) {
		candidate := candidateFrom(sealed[0])
		evidence := comparisonEvidenceFor(t, sealed[0])
		evidence.Candidate[0] = comparedCase(t, sealed[1],
			evidence.Comparison.Profile())
		candidate.Comparison = evidence
		result := clauseIndex(Evaluate(policy, candidate))[ClauseComparisonCorpus]
		if result.Verdict() != Fail {
			t.Fatalf("= %v, want Fail", result.Verdict())
		}
	})

	// An assessment bound to another checkpoint, or taken under another profile, cannot
	// support this comparison. §14.2 is explicit that an optimizer-ready baseline and a
	// merely CM-ready candidate cannot be compared.
	t.Run("an assessment under another profile fails", func(t *testing.T) {
		candidate := candidateFrom(sealed[0])
		evidence := comparisonEvidenceFor(t, sealed[0])
		other := otherProfileAssessment(t, sealed[0], evidence.Comparison.Profile())
		evidence.Candidate[0].Assessment = other
		candidate.Comparison = evidence
		result := clauseIndex(Evaluate(policy, candidate))[ClauseComparisonCorpus]
		if result.Verdict() != Fail {
			t.Fatalf("= %v, want Fail: the profile is what makes two sides comparable",
				result.Verdict())
		}
	})

	t.Run("an assessment of another checkpoint fails", func(t *testing.T) {
		candidate := candidateFrom(sealed[0])
		evidence := comparisonEvidenceFor(t, sealed[0])
		evidence.Candidate[0].Assessment = sealed[1].assessments[0]
		candidate.Comparison = evidence
		result := clauseIndex(Evaluate(policy, candidate))[ClauseComparisonCorpus]
		if result.Verdict() != Fail {
			t.Fatalf("= %v, want Fail", result.Verdict())
		}
	})
}

// PRODUCTION BREAK CAUGHT BY OWNER REVIEW: replay evidence had to be ASSESSED under the
// right profile, but not to be READY under it.
//
// Without the verdict check the clause means "these were assessed using the same
// questionnaire" rather than "both actually met it" — a materially weaker proposition,
// and the one §14.2 rules out when it says an optimizer-ready baseline cannot be compared
// to a merely CM-ready candidate.
//
// ClauseReadyAssessment does not cover this. It asks about the PROMOTED checkpoint under
// the target's profile and says nothing about the baseline replay cases, or about the
// candidate's other cases. Everything below is valid — same profile, correct binding,
// correct world, correct corpus, correct checkpoint — and only the verdict differs.
func TestReplayEvidenceMustBeReadyAndNotMerelyAssessed(t *testing.T) {
	sealed := sealTeamHOS(t)
	candidate := candidateFrom(sealed[0])
	evidence := comparisonEvidenceFor(t, sealed[0])

	needsInput := assessmentWithVerdict(t, sealed[0], semantic.NeedsInput)
	// Re-identify the comparison under the needs_input assessment's profile, so profile
	// agreement holds on both sides and readiness is the only thing that differs.
	evidence.Comparison = restateComparison(t, evidence.Comparison, needsInput.ProfileID())
	evidence.Candidate[0].Assessment = needsInput
	evidence.Baseline[0].Assessment = assessmentUnderProfile(
		t, sealed, evidence.Comparison.Baseline(), needsInput.ProfileID())
	candidate.Comparison = evidence

	result := clauseIndex(Evaluate(
		policyRequiring(needsInput.ProfileID()), candidate))[ClauseComparisonCorpus]
	if result.Verdict() != Fail {
		t.Fatalf("= %v, want Fail: replay evidence that was assessed and found "+
			"incomplete cannot support a comparison", result.Verdict())
	}
}

// PRODUCTION BREAK CAUGHT BY OWNER REVIEW: the comparison's profile was not tied to the
// target's required profile.
//
// The readiness clause independently proves the promoted assessment is under the required
// profile, and comparability independently proves the replay evidence is under the
// COMPARISON's profile. Neither established they are the same profile, so the gate could
// pass clause 4 under one and clause 6 under another — every implemented clause satisfied
// by answers to two different questions. Promotion is defined around one pinned profile.
func TestAComparisonUnderAnotherProfileCannotSupportPromotion(t *testing.T) {
	sealed := sealTeamHOS(t)
	candidate := candidateFrom(sealed[0])
	evidence := comparisonEvidenceFor(t, sealed[0])

	required := sealed[0].assessments[0].ProfileID()
	other := otherProfileAssessment(t, sealed[0], required)
	evidence.Comparison = restateComparison(t, evidence.Comparison, other.ProfileID())
	evidence.Candidate[0].Assessment = other
	evidence.Baseline[0].Assessment = assessmentUnderProfile(
		t, sealed, evidence.Comparison.Baseline(), other.ProfileID())
	candidate.Comparison = evidence

	byClause := clauseIndex(Evaluate(policyRequiring(required), candidate))
	comparison := byClause[ClauseComparisonCorpus]
	if comparison.Verdict() == Pass {
		t.Fatal("a comparison under another profile satisfied the clause, so the gate " +
			"would authorize on answers to two different questions")
	}
	// Unevaluated rather than adverse, for the same reason an assessment under the wrong
	// profile is: it says nothing about the required profile, so the corrective action is
	// to supply comparison evidence under it rather than to investigate a result.
	if comparison.Verdict() != NotEvaluated || comparison.Unevaluated() != InformationAbsent {
		t.Fatalf("= %v/%v, want NotEvaluated/InformationAbsent",
			comparison.Verdict(), comparison.Unevaluated())
	}
	// The readiness clause still passes, which is what made this dangerous: nothing else
	// in the decision looks wrong.
	if byClause[ClauseReadyAssessment].Verdict() != Pass {
		t.Fatal("the fixture no longer isolates the profile mismatch")
	}
}

// Absent and empty comparison evidence are the same answer, and the code says so now.
// An empty evidence struct contradicts nothing; it simply carries no comparison.
func TestAbsentAndEmptyComparisonEvidenceAgree(t *testing.T) {
	sealed := sealTeamHOS(t)
	policy := policyRequiring(sealed[0].assessments[0].ProfileID())

	absent := candidateFrom(sealed[0])
	empty := candidateFrom(sealed[0])
	empty.Comparison = &ComparisonEvidence{}

	first := clauseIndex(Evaluate(policy, absent))[ClauseComparisonCorpus]
	second := clauseIndex(Evaluate(policy, empty))[ClauseComparisonCorpus]
	if first.Verdict() != second.Verdict() || first.Unevaluated() != second.Unevaluated() {
		t.Fatalf("absent = %v/%v but empty = %v/%v; both are missing evidence",
			first.Verdict(), first.Unevaluated(), second.Verdict(), second.Unevaluated())
	}
	if first.Verdict() != NotEvaluated || first.Unevaluated() != InformationAbsent {
		t.Fatalf("= %v/%v, want NotEvaluated/InformationAbsent",
			first.Verdict(), first.Unevaluated())
	}
}

// THE OWNER'S FORWARD CONSTRAINT, ASSERTED: the world is checked on the ARTIFACT, never
// taken from whatever produced the evidence.
//
// §14.2 pins WorldID into the comparison question, and a side run reporting the right
// world is a projection — only the sealed artifact carries the world the execution
// actually pinned. A comparison naming a world its evidence did not run under is
// comparing over an unstated set of inputs.
func TestTheWorldIsCheckedOnTheArtifactRatherThanTheComparison(t *testing.T) {
	sealed := sealTeamHOS(t)
	candidate := candidateFrom(sealed[0])
	evidence := comparisonEvidenceFor(t, sealed[0])

	// Re-identify the comparison under a different world. Everything else is unchanged,
	// so the artifacts are exactly the ones that were produced — under the other world.
	elsewhere := worldWithReference(t)
	if elsewhere.ID() == evidence.Comparison.World() {
		t.Fatal("the fixture is wrong: the two worlds must differ")
	}
	restated, err := semantic.NewComparison(semantic.ComparisonRequest{
		Baseline:  evidence.Comparison.Baseline(),
		Candidate: evidence.Comparison.Candidate(),
		Profile:   evidence.Comparison.Profile(),
		World:     elsewhere.ID(),
		Corpus:    evidence.Comparison.Corpus(),
		Policy:    evidence.Comparison.Policy(),
	})
	if err != nil {
		t.Fatalf("NewComparison: %v", err)
	}
	evidence.Comparison = restated
	candidate.Comparison = evidence

	result := clauseIndex(Evaluate(
		policyRequiring(sealed[0].assessments[0].ProfileID()), candidate))[ClauseComparisonCorpus]
	if result.Verdict() != Fail {
		t.Fatalf("= %v, want Fail: the evidence did not run under the world this "+
			"comparison names", result.Verdict())
	}
}
