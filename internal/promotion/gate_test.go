package promotion

import (
	"testing"

	"github.com/optimaldynamics/maiden-lane/internal/ports"
)

// Production break caught by construction rather than by review: if Pass were the
// zero value, every forgotten assignment, every struct a future caller builds
// without filling a field, and every clause nobody wired up would become a silent
// approval. Ordering the constants so NotEvaluated is zero is what makes
// authorization impossible to obtain by omission.
//
// This assertion looks trivial and is the load-bearing one in the package. A
// reordering that made Pass zero would still compile, still pass every other
// test, and would authorize publication on an empty decision.
func TestTheZeroVerdictRefuses(t *testing.T) {
	var unset Verdict
	if unset != NotEvaluated {
		t.Fatalf("the zero Verdict is %v, want NotEvaluated", unset)
	}
	if unset == Pass {
		t.Fatal("the zero Verdict is Pass, so a forgotten clause would authorize publication")
	}
}

// An empty decision must refuse. This is the shape a caller produces by
// forgetting to evaluate anything at all, and it is the shape a zero-valued
// Decision has.
func TestAnEmptyDecisionRefuses(t *testing.T) {
	if (Decision{}).Authorized() {
		t.Fatal("a zero-valued Decision authorized publication")
	}
	decision := decide(1, nil)
	if decision.Authorized() {
		t.Fatal("a decision with no clause verdicts authorized publication")
	}
	if got := len(decision.Clauses()); got != len(requiredClauses) {
		t.Fatalf("clauses reported = %d, want %d", got, len(requiredClauses))
	}
	for _, result := range decision.Clauses() {
		if result.Verdict() != NotEvaluated {
			t.Fatalf("clause %v = %v, want NotEvaluated", result.Clause(), result.Verdict())
		}
	}
}

// Production break caught: the overall verdict is computed by walking the closed
// clause list rather than the verdicts a caller supplied. Walking the supplied
// map instead would mean a caller who evaluated one clause and passed it would be
// authorized, because every clause it "checked" passed.
func TestAuthorizationWalksTheClauseListRatherThanTheSuppliedVerdicts(t *testing.T) {
	// One clause, passing. Under a map-walking implementation this authorizes.
	decision := decide(1, map[Clause]ClauseResult{
		ClauseStaticValidation: Passed(ClauseStaticValidation),
	})
	if decision.Authorized() {
		t.Fatal("a single passing clause authorized publication")
	}
	if got := len(decision.Refusals()); got != len(requiredClauses)-1 {
		t.Fatalf("refusals = %d, want %d", got, len(requiredClauses)-1)
	}
}

// Every clause constant must be in requiredClauses. This is the guard against a
// future developer adding a clause and evaluating it nowhere: without it, a new
// constant would simply never be checked, and the gate would silently shrink.
func TestEveryDeclaredClauseIsRequired(t *testing.T) {
	required := map[Clause]bool{}
	for _, clause := range requiredClauses {
		if required[clause] {
			t.Fatalf("clause %v appears twice in requiredClauses", clause)
		}
		required[clause] = true
	}
	// The declared constants are contiguous from ClauseStaticValidation, so
	// walking until String reports unknown enumerates them without a second list
	// to keep in step.
	for clause := ClauseStaticValidation; clause.String() != "unknown"; clause++ {
		if !required[clause] {
			t.Fatalf("clause %v is declared but not required, so nothing checks it", clause)
		}
	}
	if len(requiredClauses) != 9 {
		t.Fatalf("required clauses = %d, want the 9 in HLD §14.1", len(requiredClauses))
	}
}

// A verdict recorded under the wrong key must not let one clause's pass stand in
// for another's.
func TestAVerdictCannotBeAttributedToAnotherClause(t *testing.T) {
	decision := decide(1, map[Clause]ClauseResult{
		// Keyed as the assessment clause, but labelled as static validation.
		ClauseReadyAssessment: Passed(ClauseStaticValidation),
	})
	for _, result := range decision.Clauses() {
		if result.Clause() == ClauseStaticValidation && result.Verdict() == Pass {
			t.Fatal("a verdict keyed to one clause was reported under another")
		}
	}
}

// NotEvaluated and Fail both refuse, but they must stay distinguishable: only one
// says something adverse about the candidate, and the reason axis must survive
// into the decision so an operator learns whether to implement something or to
// supply something.
func TestRefusalDistinguishesUnevaluatedFromFailed(t *testing.T) {
	decision := decide(7, map[Clause]ClauseResult{
		ClauseReadyAssessment:     Failed(ClauseReadyAssessment),
		ClauseComparisonCorpus:    Unsupported(ClauseComparisonCorpus),
		ClauseProtectedInvariants: Unestablished(ClauseProtectedInvariants),
	})

	byClause := map[Clause]ClauseResult{}
	for _, result := range decision.Clauses() {
		byClause[result.Clause()] = result
	}

	failed := byClause[ClauseReadyAssessment]
	if failed.Verdict() != Fail {
		t.Fatalf("assessment clause = %v, want Fail", failed.Verdict())
	}
	// A failed clause says nothing about evaluability, so the reason axis must be
	// inert rather than carrying a value a caller might render.
	if failed.Unevaluated() != UnevaluatedNotApplicable {
		t.Fatalf("a failed clause carried reason %v", failed.Unevaluated())
	}

	unsupported := byClause[ClauseComparisonCorpus]
	if unsupported.Verdict() != NotEvaluated || unsupported.Unevaluated() != UnsupportedByBuild {
		t.Fatalf("comparison clause = %v/%v, want NotEvaluated/UnsupportedByBuild",
			unsupported.Verdict(), unsupported.Unevaluated())
	}

	absent := byClause[ClauseProtectedInvariants]
	if absent.Verdict() != NotEvaluated || absent.Unevaluated() != InformationAbsent {
		t.Fatalf("invariants clause = %v/%v, want NotEvaluated/InformationAbsent",
			absent.Verdict(), absent.Unevaluated())
	}

	if decision.PolicyVersion() != 7 {
		t.Fatalf("policy version = %d, want 7", decision.PolicyVersion())
	}
}

// The clause order must be stable, so a rendered refusal does not reorder itself
// between evaluations of the same candidate.
func TestClauseOrderIsStable(t *testing.T) {
	first := decide(1, nil).Clauses()
	second := decide(1, nil).Clauses()
	for i := range first {
		if first[i].Clause() != second[i].Clause() {
			t.Fatalf("clause order differs at %d: %v then %v",
				i, first[i].Clause(), second[i].Clause())
		}
		if first[i].Clause() != requiredClauses[i] {
			t.Fatalf("clause %d = %v, want %v", i, first[i].Clause(), requiredClauses[i])
		}
	}
}

// Clauses must be a copy. A caller cannot alter a result's fields any more --
// they are unexported -- but it can still replace whole elements of the returned
// slice, which would rewrite a recorded decision if the slice were shared.
func TestClausesCannotBeMutatedThroughTheReturnedSlice(t *testing.T) {
	decision := decide(1, map[Clause]ClauseResult{
		ClauseStaticValidation: Passed(ClauseStaticValidation),
	})
	returned := decision.Clauses()
	for i := range returned {
		returned[i] = Passed(returned[i].Clause())
	}
	for _, result := range decision.Clauses() {
		if result.Clause() != ClauseStaticValidation && result.Verdict() == Pass {
			t.Fatal("replacing elements of the returned slice altered the decision")
		}
	}
	if decision.Authorized() {
		t.Fatal("the decision became authorized through its returned slice")
	}
}

// Production break caught by owner review: with exported fields,
// ClauseResult{Verdict: Pass, Unevaluated: InformationAbsent} was constructible,
// and decide would authorize it because authorization reads only the verdict. The
// zero-value guarantee was structural while "the reason axis is meaningful only
// alongside NotEvaluated" remained disciplinary.
//
// The fields are unexported now, so that combination is unrepresentable outside
// this package. Inside it, the constructors are the only path, and this asserts
// they cannot produce an incoherent pair.
func TestTheConstructorsCannotProduceAContradiction(t *testing.T) {
	for _, result := range []ClauseResult{
		Passed(ClauseStaticValidation),
		Failed(ClauseStaticValidation),
		Unsupported(ClauseStaticValidation),
		Unestablished(ClauseStaticValidation),
	} {
		if !result.coherent() {
			t.Fatalf("%v/%v is incoherent", result.Verdict(), result.Unevaluated())
		}
	}
	// An evaluated clause must carry no reason, because a reason on a decided
	// clause is a value a caller might render and act on.
	for _, result := range []ClauseResult{
		Passed(ClauseStaticValidation), Failed(ClauseStaticValidation),
	} {
		if result.Unevaluated() != UnevaluatedNotApplicable {
			t.Fatalf("%v carried reason %v", result.Verdict(), result.Unevaluated())
		}
	}
	// An unevaluated clause must carry one, or it refuses without saying whether
	// engineering or evidence is missing.
	for _, result := range []ClauseResult{
		Unsupported(ClauseStaticValidation), Unestablished(ClauseStaticValidation),
	} {
		if result.Unevaluated() == UnevaluatedNotApplicable {
			t.Fatal("an unevaluated clause carried no reason")
		}
	}
}

// A contradictory result reaching decide must refuse rather than authorize.
// Unreachable through the constructors, which is why decide checks instead of
// trusting: "unreachable" is a claim about today's code.
func TestDecideRefusesAnIncoherentResult(t *testing.T) {
	contradiction := ClauseResult{
		clause: ClauseStaticValidation, verdict: Pass, unevaluated: InformationAbsent,
	}
	decision := decide(1, map[Clause]ClauseResult{ClauseStaticValidation: contradiction})
	if decision.Authorized() {
		t.Fatal("an incoherent result authorized its clause")
	}
	for _, result := range decision.Clauses() {
		if result.Clause() != ClauseStaticValidation {
			continue
		}
		if result.Verdict() == Pass {
			t.Fatal("an incoherent result kept its Pass")
		}
		// Nothing the contradictory value asserted is carried forward, because a
		// result whose axes disagree is not evidence of anything.
		if result.Unevaluated() != InformationAbsent {
			t.Fatalf("reason = %v, want the collapsed InformationAbsent", result.Unevaluated())
		}
	}
}

// Production break caught by owner review: an out-of-range value must not render
// as a legitimate vocabulary word. Authorization still fails closed for one, since
// anything but Pass refuses, but an audit-oriented system must not report
// corruption as a recognized state.
func TestOutOfRangeValuesRenderAsUnknown(t *testing.T) {
	if got := Verdict(255).String(); got != "unknown" {
		t.Fatalf("Verdict(255) = %q, want unknown", got)
	}
	if got := Unevaluated(255).String(); got != "unknown" {
		t.Fatalf("Unevaluated(255) = %q, want unknown", got)
	}
	// The valid zero values still render as themselves rather than falling into
	// the default case.
	if got := NotEvaluated.String(); got != "not_evaluated" {
		t.Fatalf("NotEvaluated = %q", got)
	}
	if got := UnevaluatedNotApplicable.String(); got != "not_applicable" {
		t.Fatalf("UnevaluatedNotApplicable = %q", got)
	}
	// An out-of-range verdict must still refuse.
	if decide(1, map[Clause]ClauseResult{
		ClauseStaticValidation: {clause: ClauseStaticValidation, verdict: Verdict(255)},
	}).Authorized() {
		t.Fatal("an out-of-range verdict authorized its clause")
	}
}

// The policy version keeps its type rather than being erased to an integer at
// this boundary, so an ordinary number cannot reach a place a policy version
// belongs.
func TestPolicyVersionKeepsItsType(t *testing.T) {
	var version ports.PolicyVersion = 7
	if got := decide(version, nil).PolicyVersion(); got != version {
		t.Fatalf("policy version = %d, want %d", got, version)
	}
}
