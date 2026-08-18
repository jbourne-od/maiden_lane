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
// Two of HLD §14.1's nine clauses are answered from the candidate. The other
// seven are UnsupportedByBuild rather than absent: this build has no code that
// could answer them, so no candidate satisfies them and no additional evidence
// would help. Reporting them as unsupported is what keeps a nine-clause gate from
// reading as satisfied while checking two.
//
// It takes the whole policy rather than just the version it currently reads. The
// readiness clause needs the required profile, and more importantly a caller
// holding a bare version could pass one detached from the rule it came from, which
// is the exact pairing the immutable versioned policy type exists to keep intact.
//
// Every clause is dispositioned explicitly here even when the disposition is
// "unsupported", so adding a clause constant forces a decision about it rather
// than defaulting into a refusal whose stated reason is wrong.
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
		ClauseStaticValidation:     Unsupported(ClauseStaticValidation),
		ClauseSealedWithProvenance: Unsupported(ClauseSealedWithProvenance),
		ClauseProtectedInvariants:  protectedInvariants(candidate),
		ClauseReadyAssessment:      Unsupported(ClauseReadyAssessment),
		ClausePinnedIdentities:     Unsupported(ClausePinnedIdentities),
		ClauseComparisonCorpus:     Unsupported(ClauseComparisonCorpus),
		ClauseNoMetricRegression:   Unsupported(ClauseNoMetricRegression),
		ClauseDigestConsistency:    digestConsistency(candidate),
		ClauseCertifiedBackend:     Unsupported(ClauseCertifiedBackend),
	})
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
