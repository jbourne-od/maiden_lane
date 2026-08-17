// Package promotion evaluates whether a sealed checkpoint may publish to a
// target.
//
// It is pure. It reads no clock, no database, and no network, and it holds no
// state between evaluations: a decision is a function of the artifacts and the
// policy handed to it. That is what allows the gate to be tested exhaustively
// without a database, and it is why the gate lives here rather than beside the
// storage that supplies its inputs.
//
// The gate never publishes. It answers whether publication is authorized, and
// the answer is a per-clause record rather than a boolean, because an operator
// told only "refused" cannot act. Publication itself is a compare-and-swap in
// the storage layer.
package promotion

// Verdict is the ratified gate vocabulary (HLD §14).
//
// NotEvaluated is the ZERO VALUE, and that is the single most important
// decision in this file. A clause nobody filled in, a struct built by a future
// caller who forgot a field, a slice that came back empty — every one of those
// yields NotEvaluated, which refuses. Authorization can therefore never be
// granted by omission, only by an explicit Pass on every required clause.
//
// Ordering the constants any other way would make Pass the zero value and turn
// every forgotten assignment into a silent approval.
type Verdict uint8

const (
	// NotEvaluated means the gate could not establish whether the clause holds.
	// It says nothing adverse about the candidate.
	NotEvaluated Verdict = iota
	// Pass means the clause was evaluated and holds.
	Pass
	// Fail means the clause was evaluated with the information it needed, and
	// the candidate does not satisfy it. This is the state that says something
	// adverse about the candidate, which NotEvaluated does not.
	Fail
)

func (v Verdict) String() string {
	switch v {
	case Pass:
		return "pass"
	case Fail:
		return "fail"
	default:
		return "not_evaluated"
	}
}

// Unevaluated explains why a clause could not be established. It is meaningful
// only when the verdict is NotEvaluated, and is a separate axis rather than two
// more verdict values because HLD §14 ratifies exactly three verdicts.
//
// The distinction it carries is operational, not cosmetic. Both values refuse,
// but they tell an operator to do different things: one is answered by
// implementing something, the other by supplying something.
type Unevaluated uint8

const (
	// UnevaluatedNotApplicable is the zero value, used when a clause was
	// actually evaluated and this axis carries no meaning.
	UnevaluatedNotApplicable Unevaluated = iota
	// UnsupportedByBuild means this build cannot answer the clause at all,
	// because the concept it refers to is not implemented. No candidate can
	// satisfy it and no input would help.
	UnsupportedByBuild
	// InformationAbsent means the clause is implemented, but an input it needs
	// was not supplied. A different candidate, or the same one with more
	// evidence, could satisfy it.
	InformationAbsent
)

func (u Unevaluated) String() string {
	switch u {
	case UnsupportedByBuild:
		return "unsupported_by_build"
	case InformationAbsent:
		return "information_absent"
	default:
		return "not_applicable"
	}
}

// Clause is the closed vocabulary of gate requirements from HLD §14.1, one
// constant per bullet in that list. There are nine, and there is no catch-all.
//
// "No conflicting concurrent publication" is deliberately absent. It is not a
// gate clause: it is the compare-and-swap that publication itself performs, and
// it cannot be evaluated purely because it is a fact about the moment of
// writing rather than about the candidate.
type Clause uint8

const (
	ClauseStaticValidation Clause = iota + 1
	ClauseSealedWithProvenance
	ClauseProtectedInvariants
	ClauseReadyAssessment
	ClausePinnedIdentities
	ClauseComparisonCorpus
	ClauseNoMetricRegression
	ClauseDigestConsistency
	ClauseCertifiedBackend
)

// requiredClauses is every clause the gate must produce a result for.
//
// The overall verdict is computed by walking this list rather than the results
// a caller happened to produce. That inversion is what makes a forgotten clause
// refuse: adding a constant here without evaluating it yields NotEvaluated for
// it, which refuses, instead of quietly shrinking the set of things checked.
var requiredClauses = []Clause{
	ClauseStaticValidation,
	ClauseSealedWithProvenance,
	ClauseProtectedInvariants,
	ClauseReadyAssessment,
	ClausePinnedIdentities,
	ClauseComparisonCorpus,
	ClauseNoMetricRegression,
	ClauseDigestConsistency,
	ClauseCertifiedBackend,
}

func (c Clause) String() string {
	switch c {
	case ClauseStaticValidation:
		return "static_validation"
	case ClauseSealedWithProvenance:
		return "sealed_with_provenance"
	case ClauseProtectedInvariants:
		return "protected_invariants"
	case ClauseReadyAssessment:
		return "ready_assessment"
	case ClausePinnedIdentities:
		return "pinned_identities"
	case ClauseComparisonCorpus:
		return "comparison_corpus"
	case ClauseNoMetricRegression:
		return "no_metric_regression"
	case ClauseDigestConsistency:
		return "digest_consistency"
	case ClauseCertifiedBackend:
		return "certified_backend"
	default:
		return "unknown"
	}
}

// ClauseResult is one clause's outcome.
type ClauseResult struct {
	Clause      Clause
	Verdict     Verdict
	Unevaluated Unevaluated
}

// Decision is the gate's complete answer.
//
// It carries every clause result and the policy version under which the decision
// was made. That version is here rather than only on a later publication record
// so two questions can be answered independently: which semantic facts produced
// this result, and under which destination policy was it judged. The policy
// version participates in no semantic identity and never reaches the kernel.
type Decision struct {
	clauses       []ClauseResult
	policyVersion uint64
	authorized    bool
}

// Authorized reports whether every required clause passed.
func (d Decision) Authorized() bool { return d.authorized }

// PolicyVersion is the target policy version this decision was made under.
func (d Decision) PolicyVersion() uint64 { return d.policyVersion }

// Clauses returns the per-clause results in the order of requiredClauses, so a
// rendering of a refusal is stable rather than dependent on evaluation order.
func (d Decision) Clauses() []ClauseResult {
	return append([]ClauseResult(nil), d.clauses...)
}

// Refusals returns only the clauses that did not pass, which is what an operator
// reading a refusal actually wants.
func (d Decision) Refusals() []ClauseResult {
	refused := make([]ClauseResult, 0, len(d.clauses))
	for _, result := range d.clauses {
		if result.Verdict != Pass {
			refused = append(refused, result)
		}
	}
	return refused
}

// decide assembles a decision from the clause verdicts an evaluation produced.
//
// It walks requiredClauses rather than the supplied map, so a clause with no
// entry is NotEvaluated and refuses. Authorization requires an explicit Pass on
// every clause; there is no path by which an absent, defaulted, or zero value
// becomes an approval.
func decide(policyVersion uint64, verdicts map[Clause]ClauseResult) Decision {
	decision := Decision{
		policyVersion: policyVersion,
		clauses:       make([]ClauseResult, 0, len(requiredClauses)),
		authorized:    true,
	}
	for _, clause := range requiredClauses {
		result, present := verdicts[clause]
		if !present {
			result = ClauseResult{Clause: clause, Verdict: NotEvaluated}
		}
		// Guard against a verdict recorded under the wrong key, which would
		// otherwise let one clause's pass stand in for another's.
		result.Clause = clause
		if result.Verdict != Pass {
			decision.authorized = false
		}
		decision.clauses = append(decision.clauses, result)
	}
	return decision
}
