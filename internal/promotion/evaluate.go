package promotion

import (
	"github.com/optimaldynamics/maiden-lane/internal/ports"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

// Candidate is the evidence one promotion decision is made from.
//
// Its kernel values are authenticated by their types rather than by a check this
// package performs. semantic.CheckpointArtifact and semantic.Assessment have
// unexported fields, no exported constructor, and no decoder, so outside the
// kernel the only non-zero value of either is one that Seal or Assess produced,
// having already verified everything those functions verify.
//
// A stored projection deliberately does not appear here, and must never be given
// authorization weight in this package. A storage adapter's content guarantee is
// only "I return exactly the bytes I was given"; it says nothing about whether
// those bytes were mutually consistent when written. A wrong-but-self-consistent
// row therefore survives forever with perfect fidelity, and a gate that read one
// would authorize publication on the strength of a record agreeing with itself.
//
// The zero value of each kernel field is still constructible, so evaluation checks
// for it explicitly. The type rules out a forged artifact, not an absent one.
type Candidate struct {
	// Checkpoint is the sealed artifact under consideration.
	Checkpoint semantic.CheckpointArtifact

	// Plan is the compiled plan the checkpoint was sealed under.
	//
	// It is here because the artifact cannot supply what two clauses need. Static
	// validation is established by a Plan existing at all: Compile returns a plan or
	// a compilation failure and never both, so a plan with an identity is a plan that
	// validated. And the pinned-identity clause names the schema, ruleset, and
	// compiler identities, which PlanID commits to but does not expose — PlanID is a
	// digest over them, so holding it proves they were fixed without saying what they
	// were. Plan exposes all three.
	//
	// Supplying it does not weaken anything, because it is checked against the
	// checkpoint rather than trusted: an artifact names its PlanID, so a plan that is
	// not the one sealed under is detectable rather than assumed away.
	Plan semantic.Plan

	// Assessment is the readiness answer whose binding to Checkpoint the digest
	// consistency clause establishes. Whether its verdict is `ready` is a
	// different clause's question and is not read here.
	Assessment semantic.Assessment

	// RetainedInvariantWitness is the canonical invariant-result evidence the
	// caller holds for Checkpoint.
	//
	// It is supplied separately rather than read from Checkpoint on purpose. The
	// point of the clause is that evidence recovered from somewhere else — a
	// storage adapter, a transport, another process — is the evidence this
	// artifact committed to. Passing Checkpoint.InvariantResultCanonicalBytes()
	// is legitimate and always verifies, because in that case the artifact is
	// vouching for itself; it establishes the clause without establishing
	// anything about what was retained.
	//
	// These bytes are opaque here and must stay that way. The only operation
	// defined on them is semantic.VerifyInvariantResultDigest.
	RetainedInvariantWitness []byte

	// Comparison is the evidence answering one promotion comparison, or nil when none
	// was supplied.
	//
	// A pointer rather than a value so that "not supplied" is explicit at the call site.
	// ComparisonEvidence contains a semantic.Comparison whose zero value means nothing, so
	// a value field would make every caller that omits it look like one supplying an
	// empty comparison.
	//
	// It does NOT distinguish nil from an empty struct, and an earlier comment here
	// claimed it did — that nil was unevaluated while present-but-empty was adverse. The
	// code never did that, and it should not: an empty evidence struct contradicts
	// nothing, it simply carries no comparison. Both are missing evidence and both report
	// InformationAbsent.
	Comparison *ComparisonEvidence

	// ExecutionID is the execution that produced the checkpoint, which §14.1's
	// pinned-identity clause requires and no artifact here carries: executor identity
	// is excluded from checkpoint identity by design, so one semantic run can be
	// executed by several backends and each produces the same checkpoint.
	//
	// This gate cannot authenticate that attribution, and does not pretend to. It
	// checks the identity is present, and whoever assembles a Candidate is
	// responsible for having established that this execution produced this
	// checkpoint — which internal/app does with a receipt its own spine result mints.
	// Naming the limit here is better than a clause that looks like it verified
	// something it read from a field.
	ExecutionID semantic.ExecutionID

	// Executor is the executor build identity used to produce this execution,
	// evaluated by the certified-backend clause.
	Executor semantic.ExecutorIdentity
}

// sealed reports whether a real sealed artifact was supplied at all.
//
// Every kernel identity is a digest string, so the zero artifact has an empty ID
// and no genuine one does. This is checked rather than assumed because an
// exported struct type always has a constructible zero value: the type system
// prevents a caller from forging a particular artifact, not from supplying none.
func (c Candidate) sealed() bool { return c.Checkpoint.ID() != "" }

// witnessVerifies reports whether the retained witness is the exact byte sequence
// this artifact's invariant-result digest was derived from.
func (c Candidate) witnessVerifies() bool {
	return semantic.VerifyInvariantResultDigest(c.RetainedInvariantWitness,
		c.Checkpoint.InvariantResultDigest())
}

// established reports whether a target policy is one at all.
//
// Version zero is the obvious case: policy versions begin at 1, so zero is the
// value a missing lookup returns. The rest matter for the same reason. HLD §14.1
// keys publication by tenant, customer, and target and says the policy explicitly
// binds the profile required for publication, so a value missing any of those does
// not name a destination or a rule -- it is a half-written row, and judging a
// candidate against it would produce a decision that reads like progress.
//
// The required profile is checked even though no clause reads it yet. It is the
// entire content of a policy today; a policy binding no profile cannot authorize
// publication under any reading of §14.1, and letting one through would mean the
// first evaluation to matter is against a rule that says nothing.
func established(policy ports.TargetPolicy) bool {
	return policy.Version > 0 && policy.TenantID != "" && policy.CustomerID != "" &&
		policy.Target != "" && policy.RequiredProfileID != ""
}

// Evaluate produces a gate decision for one candidate under one target policy.
//
// All nine of HLD §14.1's clauses are evaluated from the candidate, comparison evidence,
// and target policy.
//
// It takes the whole policy rather than just the version it currently reads. The
// readiness clause needs the required profile, and more importantly a caller
// holding a bare version could pass one detached from the rule it came from, which
// is the exact pairing the immutable versioned policy type exists to keep intact.
//
// Every clause is dispositioned explicitly here, so adding a clause constant forces
// a decision about it rather than defaulting into a refusal whose stated reason is wrong.
func Evaluate(policy ports.TargetPolicy, candidate Candidate) Decision {
	if !established(policy) {
		// No rule to judge against, so nothing about the candidate has been
		// established. Every clause is unevaluated rather than failed: a
		// destination with no usable policy says nothing adverse about a
		// checkpoint. The version is still recorded as supplied, so a refusal
		// reads as "judged under this version, nothing established" and points an
		// operator at the policy rather than at the artifact.
		return decide(policy.Version, nil)
	}
	return decide(policy.Version, map[Clause]ClauseResult{
		ClauseStaticValidation:     staticValidation(candidate),
		ClauseSealedWithProvenance: sealedWithProvenance(candidate),
		ClauseProtectedInvariants:  protectedInvariants(candidate),
		ClauseReadyAssessment:      readyAssessment(policy, candidate),
		ClausePinnedIdentities:     pinnedIdentities(candidate),
		ClauseComparisonCorpus:     comparisonCorpus(policy, candidate, candidate.Comparison),
		ClauseNoMetricRegression:   noMetricRegression(policy, candidate, candidate.Comparison),
		ClauseDigestConsistency:    digestConsistency(candidate),
		ClauseCertifiedBackend:     certifiedBackend(candidate),
	})
}

// staticValidation establishes HLD §14.1's "successful static plan validation".
//
// It is established by a compiled Plan existing, not by re-running validation. Compile
// returns either a Plan or a CompilationFailure and never both, and Plan has
// unexported fields with no exported constructor, so outside the kernel a plan with an
// identity is a plan the compiler accepted. Re-validating here would be a second
// implementation of the compiler's own judgment, and a second implementation is a
// second answer.
//
// The plan is checked against the checkpoint rather than merely accepted. An artifact
// names the PlanID it was sealed under, so a plan supplied that is not that one is a
// definite disagreement about which program produced this checkpoint — adverse, and
// therefore Fail rather than unevaluated.
func staticValidation(candidate Candidate) ClauseResult {
	if !candidate.sealed() || candidate.Plan.ID() == "" {
		return Unestablished(ClauseStaticValidation)
	}
	if candidate.Plan.ID() != candidate.Checkpoint.PlanID() {
		return Failed(ClauseStaticValidation)
	}
	return Passed(ClauseStaticValidation)
}

// sealedWithProvenance establishes HLD §14.1's "sealed selected checkpoint with at
// least `changes` provenance".
//
// Sealing is established by holding the artifact. Provenance follows from the kernel
// refusing anything else: BindRun rejects any ProvenancePolicy but ChangesProvenance,
// Seal requires a binding, and plan identity itself commits to the policy token. So a
// sealed artifact necessarily carries `changes` provenance, and "at least" is
// trivially satisfied because it is the only policy that exists.
//
// LOAD-BEARING CAVEAT, and it is the same shape as the protected-invariant clause's:
// this holds only while ChangesProvenance is the sole provenance policy. Introducing a
// second one — weaker or stronger — makes "at least changes" a real comparison that
// this clause does not perform, and it would keep returning Pass. Such a change must
// come with an exported way to compare provenance policy identities, and with this
// clause rewritten to use it.
func sealedWithProvenance(candidate Candidate) ClauseResult {
	if !candidate.sealed() {
		return Unestablished(ClauseSealedWithProvenance)
	}
	if candidate.Checkpoint.ProvenancePolicyID() == "" {
		// Unreachable through Seal, which derives this from a verified binding.
		// Checked because "unreachable" is a claim about today's code.
		return Unestablished(ClauseSealedWithProvenance)
	}
	return Passed(ClauseSealedWithProvenance)
}

// readyAssessment establishes HLD §14.1's "a `ready` assessment under the target's
// pinned ProfileID".
//
// Two things can go wrong and they are not the same answer, which is the whole reason
// the verdict vocabulary has three values rather than two:
//
//   - The assessment is for a DIFFERENT profile. Then nothing is known about the
//     profile the target requires, so this is unevaluated for want of the right
//     evidence. Reading a verdict taken under another profile would answer a question
//     nobody asked and call it authorization.
//   - The assessment is for the right profile and says needs_input. That is a real,
//     adverse answer about this candidate: Fail.
//
// The assessment must also be bound to this checkpoint. The digest-consistency clause
// checks that too, and this one cannot lean on it: clause results are independent, so a
// clause that assumed another had passed would authorize on an assumption.
func readyAssessment(policy ports.TargetPolicy, candidate Candidate) ClauseResult {
	assessment := candidate.Assessment
	if !candidate.sealed() || assessment.ID() == "" || policy.RequiredProfileID == "" {
		return Unestablished(ClauseReadyAssessment)
	}
	if assessment.CheckpointArtifactID() != candidate.Checkpoint.ID() {
		// An assessment of another checkpoint says nothing about this one.
		return Unestablished(ClauseReadyAssessment)
	}
	if assessment.ProfileID() != policy.RequiredProfileID {
		return Unestablished(ClauseReadyAssessment)
	}
	if assessment.Verdict() != semantic.Ready {
		return Failed(ClauseReadyAssessment)
	}
	return Passed(ClauseReadyAssessment)
}

// pinnedIdentities establishes HLD §14.1's "pinned input, world, schema, ruleset,
// compiler, semantic-run, execution, checkpoint, profile, and assessment identities".
//
// All ten are named explicitly rather than counted, because the clause's content is
// that each one is fixed. Seven come from the sealed artifact and the assessment,
// which cannot be constructed without them; schema, ruleset, and compiler come from
// the plan, since PlanID is a digest over them and proves they were fixed without
// saying what they were; and the execution identity comes from the caller, because no
// artifact here carries it.
//
// Absence is unevaluated and disagreement is Fail. A missing identity means the
// candidate was not fully described; a cross-link that contradicts means the identities
// present do not describe one thing, which is adverse.
func pinnedIdentities(candidate Candidate) ClauseResult {
	if !candidate.sealed() {
		return Unestablished(ClausePinnedIdentities)
	}
	checkpoint, plan, assessment := candidate.Checkpoint, candidate.Plan, candidate.Assessment

	present := []string{
		string(checkpoint.InputID()),
		string(checkpoint.WorldID()),
		string(plan.SchemaDigest()),
		string(plan.RulesetDigest()),
		string(plan.CompilerVersion()),
		string(checkpoint.SemanticRunID()),
		string(candidate.ExecutionID),
		string(checkpoint.CheckpointID()),
		string(assessment.ProfileID()),
		string(assessment.ID()),
	}
	for _, identity := range present {
		if identity == "" {
			return Unestablished(ClausePinnedIdentities)
		}
	}

	// The identities must describe one thing. Each of these is a link that could
	// disagree while every field is populated, which is the state a list of non-empty
	// strings cannot rule out on its own.
	if plan.ID() != checkpoint.PlanID() ||
		assessment.CheckpointArtifactID() != checkpoint.ID() {
		return Failed(ClausePinnedIdentities)
	}
	return Passed(ClausePinnedIdentities)
}

// protectedInvariants establishes HLD §14.1's "all protected dynamic invariants
// applicable to that checkpoint prefix passed".
//
// It reaches that conclusion without reading the evidence, because it cannot: the
// kernel's canonical encoding is one-way and there is no decoder, by design.
// What it establishes instead is that the witness it holds is the exact byte
// sequence this artifact's InvariantResultDigest was derived from. Sealing did the
// rest, and did it before any identity existed — Seal refuses unless the supplied
// invariant results are exactly one passing result per invariant declared over the
// accepted prefix, each matching its declaration's code, scope, and boundary. A
// verified witness over an artifact that genuinely sealed therefore means every
// applicable protected obligation passed. That is a theorem the kernel enforces,
// not a property inferred from a stored record.
//
// LOAD-BEARING CAVEAT: this holds only while every InvariantCode is protected. If
// an unprotected invariant code is ever introduced, the sealed set stops being
// all-protected, "all protected invariants passed" stops following from the fact
// of sealing, and this clause would need to read the witness — which it must never
// be able to do. Unprotected invariants must arrive as a separate vocabulary and a
// separate type, never as a `protected=false` flag on InvariantCode, or this
// theorem is lost silently and this function keeps returning Pass.
//
// The clause consequently cannot Fail today: an artifact whose protected
// invariants failed cannot be sealed, so no such artifact can reach here. It
// passes or it is unevaluated, and that is a property of the kernel rather than an
// omission in this function.
func protectedInvariants(candidate Candidate) ClauseResult {
	if !candidate.sealed() {
		return Unestablished(ClauseProtectedInvariants)
	}
	if !candidate.witnessVerifies() {
		// A witness that does not verify is unevaluated, never failed. Fail
		// asserts something adverse about the candidate, and unattributable
		// evidence asserts nothing at all: it does not say the invariants failed,
		// it says this evidence cannot be attributed to this checkpoint. Judging
		// the contents of evidence that cannot be attributed is exactly the trust
		// this design refuses to extend. Digest consistency below is where a
		// mismatch becomes an adverse finding.
		return Unestablished(ClauseProtectedInvariants)
	}
	return Passed(ClauseProtectedInvariants)
}

// digestConsistency establishes HLD §14.1's "internally consistent checkpoint
// state, journal-prefix, assessment, and invariant-result digests".
//
// Two of those four legs are checkable here and two hold by construction, which is
// worth stating rather than leaving as an inference from a passing test:
//
//   - state and journal-prefix: the artifact's own StateDigest and
//     JournalPrefixDigest were derived by Seal from a replay-verified journal and
//     a state whose digest Seal recomputed, and CheckpointArtifactID is derived
//     from both. Holding a semantic.CheckpointArtifact is holding that agreement.
//   - invariant-result: the retained witness must reproduce the artifact's
//     committed digest. This is the leg that can actually disagree, because the
//     witness is the one input that may have travelled through storage.
//   - assessment: the assessment must be bound to this artifact's
//     CheckpointArtifactID. Without this, an assessment of a different checkpoint
//     could be presented alongside this one, and a later clause reading its
//     verdict would answer a question about the wrong artifact.
//
// A mismatch on either checkable leg is Fail rather than unevaluated. Unlike the
// protected-invariants clause, this one is asking whether the record agrees with
// itself, and a definite disagreement is a definite adverse answer.
func digestConsistency(candidate Candidate) ClauseResult {
	if !candidate.sealed() || candidate.Assessment.ID() == "" {
		return Unestablished(ClauseDigestConsistency)
	}
	if len(candidate.RetainedInvariantWitness) == 0 {
		// Absent evidence is not inconsistent evidence. Nothing disagrees yet.
		return Unestablished(ClauseDigestConsistency)
	}
	if !candidate.witnessVerifies() {
		return Failed(ClauseDigestConsistency)
	}
	if candidate.Assessment.CheckpointArtifactID() != candidate.Checkpoint.ID() {
		return Failed(ClauseDigestConsistency)
	}
	return Passed(ClauseDigestConsistency)
}
