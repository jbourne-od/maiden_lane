package app

import (
	"errors"
	"strings"
	"testing"

	"github.com/optimaldynamics/maiden-lane/internal/adapters/memory"
	"github.com/optimaldynamics/maiden-lane/internal/fixtures/teamhos"
	"github.com/optimaldynamics/maiden-lane/internal/ports"
	"github.com/optimaldynamics/maiden-lane/internal/promotion"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

// These tests are in-package rather than in app_test because two of the code paths
// that matter most are unreachable from outside: the gate refuses every candidate in
// this build, so nothing reaches Publish's authorize-and-advance path.
//
// The seam that would make it reachable — an injected gate function — is refused
// deliberately. It would create a capability to manufacture the very fact Publish
// exists to own, and any caller could then hold it. Direct tests of otherwise
// unreachable construction are the lesser evil, and they use the same real spine
// fixture as the behavioural tests so the mappings they assert are the real ones.

// THE ASSERTION THIS SLICE EXISTS FOR: publication ships able to publish nothing.
//
// Three of HLD §14.1's nine clauses have no implementation in this build and four
// more are not yet wired, so every decision contains a NotEvaluated and refuses.
// This is asserted rather than assumed because the alternative failure is silent and
// total: publication arriving before its checks, with a gate that looks present and
// authorizes on an incomplete evaluation.
func TestPublicationIsRefusedWhileAnyClauseIsUnevaluated(t *testing.T) {
	fixture := publishFixture(t)
	stores := storesWithPolicy(t, fixture)

	outcome, err := Publish(t.Context(), stores, fixture.request)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if outcome.Authorized() {
		t.Fatal("publication was authorized while clauses remain unevaluated")
	}
	if outcome.Result() != PublicationRefused {
		t.Fatalf("result = %v, want refused", outcome.Result())
	}
	if _, published := outcome.Publication(); published {
		t.Fatal("a refused request produced a publication")
	}
	// A refusal that still advanced the pointer would be worse than no gate at all.
	if _, found, err := stores.Publications.CurrentPublication(
		t.Context(), fixture.tenant, fixture.customer, fixture.target); err != nil || found {
		t.Fatalf("the target was published to anyway: found=%t err=%v", found, err)
	}
}

// A refusal has to say which clauses refused and why, or an operator is told "no"
// with nowhere to go. The two implemented clauses must pass on this candidate, so
// the refusal is attributable to the unimplemented ones rather than to bad evidence.
func TestARefusalNamesEveryClauseAndItsReason(t *testing.T) {
	fixture := publishFixture(t)
	outcome, err := Publish(t.Context(), storesWithPolicy(t, fixture), fixture.request)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	decision := outcome.Decision()
	if got := len(decision.Clauses()); got != 9 {
		t.Fatalf("clauses = %d, want the 9 in HLD §14.1", got)
	}
	if decision.PolicyVersion() != 1 {
		t.Fatalf("policy version = %d, want the 1 it was judged under", decision.PolicyVersion())
	}

	byClause := map[promotion.Clause]promotion.ClauseResult{}
	for _, result := range decision.Clauses() {
		byClause[result.Clause()] = result
	}
	for _, clause := range []promotion.Clause{
		promotion.ClauseProtectedInvariants, promotion.ClauseDigestConsistency,
	} {
		if got := byClause[clause].Verdict(); got != promotion.Pass {
			t.Fatalf("clause %v = %v, want Pass: this candidate carries the evidence "+
				"both need, so a refusal here would hide why publication was refused",
				clause, got)
		}
	}
	refusals := decision.Refusals()
	if len(refusals) != 7 {
		t.Fatalf("refusals = %d, want 7", len(refusals))
	}
	for _, result := range refusals {
		// UnsupportedByBuild rather than InformationAbsent: no evidence would satisfy
		// these, so an operator must be told engineering is missing rather than sent
		// looking for inputs.
		if result.Unevaluated() != promotion.UnsupportedByBuild {
			t.Fatalf("clause %v refused with reason %v, want UnsupportedByBuild",
				result.Clause(), result.Unevaluated())
		}
	}
}

// A target with no policy must refuse without looking like a fault. An unconfigured
// destination is the ordinary initial state, and the refusal must say the policy is
// what is missing.
func TestAnUnconfiguredTargetRefusesRatherThanFailing(t *testing.T) {
	fixture := publishFixture(t)
	store := memory.NewStore()

	outcome, err := Publish(t.Context(),
		PublicationStores{Policies: store, Publications: store}, fixture.request)
	if err != nil {
		t.Fatalf("Publish against an unconfigured target: %v", err)
	}
	if outcome.Authorized() {
		t.Fatal("publication was authorized with no policy")
	}
	if got := outcome.Decision().PolicyVersion(); got != 0 {
		t.Fatalf("policy version = %d, want 0 for a target with no policy", got)
	}
	for _, result := range outcome.Decision().Clauses() {
		if result.Unevaluated() != promotion.InformationAbsent {
			t.Fatalf("clause %v reason = %v, want InformationAbsent: the missing "+
				"information is the policy", result.Clause(), result.Unevaluated())
		}
	}
}

// PRODUCTION BREAK CAUGHT BY OWNER REVIEW, and this test is the one that would have
// caught it: an earlier version took a semantic.RunBinding and checked only that its
// SemanticRunID matched the checkpoint's.
//
// That proves E → S ← C, not E → C. BindRun runs BEFORE execution and establishes
// only that an ExecutionID is a valid execution contract for a semantic run, and a
// CheckpointArtifact excludes producing-executor identity by design. So a second
// executor could be bound over the identical semantic request, never executed, and
// its ExecutionID paired with a checkpoint another execution produced. The record
// would name an execution that did not produce the artifact beside it.
//
// A receipt closes it structurally: minting requires a SpineResult, which exists only
// because the spine ran, and the checkpoint must be among the ones that run retained.
// Note what this makes true — if a second executor DID run and produce the same
// checkpoint, its receipt is honest, because it really did produce it.
func TestAReceiptCannotBeMintedForAnExecutionThatDidNotRun(t *testing.T) {
	fixture := publishFixture(t)

	// The same construction the old code accepted: a valid binding for the same
	// semantic run under a different executor, never executed.
	forged := forgedBinding(t)
	if forged.SemanticRunID() != fixture.receipt.SemanticRunID() {
		t.Fatal("the fixture is wrong: this must be the same semantic run")
	}
	if forged.ExecutionID() == fixture.receipt.ExecutionID() {
		t.Fatal("the fixture is wrong: this must be a different execution")
	}

	// There is no way to turn that binding into a receipt. CheckpointReceipt's fields
	// are unexported and ReceiptFor is the only constructor, so this is a compile-time
	// property rather than a check. What remains testable is that ReceiptFor refuses
	// a checkpoint its result did not retain.
	empty := SpineResult{}
	if _, ok := empty.ReceiptFor(fixture.artifact); ok {
		t.Fatal("a result that established no execution minted a receipt")
	}
}

// A receipt is for one checkpoint, and must not vouch for another. An execution
// retains several, so a receipt covering the run rather than the artifact would let
// one checkpoint's evidence stand for another's.
func TestAReceiptDoesNotVouchForAnotherCheckpoint(t *testing.T) {
	fixture := publishFixture(t)
	if len(fixture.retained) < 2 {
		t.Fatal("the fixture must retain at least two checkpoints for this to mean anything")
	}

	first, second := fixture.retained[0], fixture.retained[1]
	receipt, ok := fixture.result.ReceiptFor(first)
	if !ok {
		t.Fatal("no receipt for a retained checkpoint")
	}

	// Both checkpoints are genuinely from this execution, so the receipt is honest --
	// it is simply for the wrong one, which validation must catch.
	request := fixture.request
	request.Receipt = receipt
	request.Candidate = promotion.Candidate{
		Checkpoint:               second,
		Assessment:               fixture.assessment,
		RetainedInvariantWitness: second.InvariantResultCanonicalBytes(),
	}

	_, err := Publish(t.Context(), storesWithPolicy(t, fixture), request)
	if err == nil {
		t.Fatal("a receipt for one checkpoint published another")
	}
	var invalid InvalidInputError
	if !errors.As(err, &invalid) || invalid.Code != InputPublishReceiptMismatch {
		t.Fatalf("error = %v, want InputPublishReceiptMismatch", err)
	}
	// No identifier may appear: these errors carry a closed code and fixed safe text,
	// so a surfaced error cannot become a channel for content.
	if strings.Contains(err.Error(), string(first.ID())) {
		t.Fatal("the error text carries a checkpoint identity")
	}
}

// A checkpoint the run sealed and then dropped must mint nothing. A checkpoint can be
// excluded from the retained frontier when its assessment fails verification, and
// publishing one of those would publish an artifact the run deliberately did not
// stand behind.
func TestAReceiptRequiresTheCheckpointToBeRetained(t *testing.T) {
	fixture := publishFixture(t)
	other := publishFixtureFor(t, teamhos.AnchorMismatch)

	// The mismatch variant seals only its first checkpoint, so the passing run's
	// second one was never retained by it.
	if _, ok := other.result.ReceiptFor(fixture.retained[len(fixture.retained)-1]); ok {
		t.Fatal("a result minted a receipt for a checkpoint it never retained")
	}
}

// Absent evidence must be refused before any store is touched, and as an error
// rather than a refusal: a request missing a piece is not a candidate the gate judged.
func TestAnIncompleteRequestIsAnErrorRatherThanARefusal(t *testing.T) {
	fixture := publishFixture(t)
	for _, test := range []struct {
		name   string
		mutate func(*PublishRequest)
	}{
		{"no tenant", func(r *PublishRequest) { r.TenantID = "" }},
		{"no customer", func(r *PublishRequest) { r.CustomerID = "" }},
		{"no target", func(r *PublishRequest) { r.Target = "" }},
		{"no checkpoint", func(r *PublishRequest) { r.Candidate.Checkpoint = semantic.CheckpointArtifact{} }},
		{"no assessment", func(r *PublishRequest) { r.Candidate.Assessment = semantic.Assessment{} }},
		{"no receipt", func(r *PublishRequest) { r.Receipt = CheckpointReceipt{} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := fixture.request
			test.mutate(&request)
			_, err := Publish(t.Context(), storesWithPolicy(t, fixture), request)
			if err == nil {
				t.Fatalf("Publish accepted a request with %s", test.name)
			}
			var invalid InvalidInputError
			if !errors.As(err, &invalid) || invalid.Code != InputPublishRequestIncomplete {
				t.Fatalf("error = %v, want InputPublishRequestIncomplete", err)
			}
		})
	}
}

// ── the authorize-and-advance path, tested directly ─────────────────────────

// PRODUCTION BREAK CAUGHT BY OWNER REVIEW: the earlier version of this test used a
// zero candidate and never asserted the four identities that come from it, while the
// behavioural test hand-built a ports.Publication instead of calling publicationFor.
// Every load-bearing mapping could therefore have been broken — swapped, dropped, or
// read from the wrong source — with neither test noticing.
//
// This compares the whole record field for field against real spine artifacts, so a
// crossed pair fails here rather than the first time a clause is wired and
// publication begins working.
func TestPublicationForPinsExactlyTheEvidenceItWasGiven(t *testing.T) {
	fixture := publishFixture(t)
	policy := ports.TargetPolicy{
		TenantID: fixture.tenant, CustomerID: fixture.customer, Target: fixture.target,
		Version: 7, RequiredProfileID: fixture.assessment.ProfileID(),
	}

	record := publicationFor(fixture.request, policy, 3)

	if want := (ports.Publication{
		TenantID:             fixture.tenant,
		CustomerID:           fixture.customer,
		Target:               fixture.target,
		Version:              3,
		PolicyVersion:        7,
		ProfileID:            fixture.assessment.ProfileID(),
		AssessmentID:         fixture.assessment.ID(),
		CheckpointArtifactID: fixture.artifact.ID(),
		SemanticRunID:        fixture.receipt.SemanticRunID(),
		ExecutionID:          fixture.receipt.ExecutionID(),
	}); record != want {
		t.Fatalf("publicationFor produced\n  %+v\nwant\n  %+v", record, want)
	}

	// Each identity must come from a distinct source, so a helper reading the wrong
	// one is visible rather than coincidentally right. These assert the sources are
	// genuinely distinguishable rather than accidentally equal.
	if record.SemanticRunID == semantic.SemanticRunID(record.ExecutionID) {
		t.Fatal("the fixture cannot distinguish the run from the execution")
	}
	if record.ProfileID == semantic.ProfileID(record.AssessmentID) {
		t.Fatal("the fixture cannot distinguish the profile from the assessment")
	}
	// The profile is the assessment's, not the policy's requirement. They agree here
	// on purpose -- the policy requires the profile the assessment used -- so this
	// asserts the record states a fact whose source is the assessment.
	if record.ProfileID != fixture.assessment.ProfileID() {
		t.Fatal("the profile was not taken from the assessment")
	}
}

// THE BLOCKER OWNER REVIEW FOUND: the caller must supply the expected current
// version, and a stale caller must lose.
//
// HLD §16 requires publication to take "the expected current target version". An
// earlier version of this code read whatever was current and wrote one past it, which
// protects only the window between that internal read and the write -- not the window
// that matters, which opens when the caller decides and closes when it publishes. If A
// publishes v7 after B formed its decision against v6, B would observe v7 and
// cheerfully write v8 over a result it never saw: a flawless compare-and-swap on the
// wrong proposition.
func TestAStalePublisherCannotAdvanceThePointer(t *testing.T) {
	fixture := publishFixture(t)
	store := memory.NewStore()

	// A publishes v1 while B is still deciding.
	first := fixture.request
	if _, err := advancePointer(t.Context(), store, first,
		policyAt(fixture, 1), promotion.Decision{}); err != nil {
		t.Fatalf("first publication: %v", err)
	}

	// B decided against version 0, which was true when it looked and is not now. Its
	// candidate differs, so this is a real second decision rather than a retry.
	stale := fixture.request
	stale.ExpectedCurrentVersion = 0
	stale.Candidate.Checkpoint = fixture.retained[0]
	stale.Receipt = receiptFor(t, fixture, fixture.retained[0])

	_, err := advancePointer(t.Context(), store, stale, policyAt(fixture, 1), promotion.Decision{})
	if !errors.Is(err, ports.ErrPublicationConflict) {
		t.Fatalf("a stale publisher got %v, want ErrPublicationConflict", err)
	}

	// The pointer did not move, and still holds what A published.
	current, _, err := store.CurrentPublication(t.Context(),
		fixture.tenant, fixture.customer, fixture.target)
	if err != nil {
		t.Fatalf("CurrentPublication: %v", err)
	}
	if current.Version != 1 {
		t.Fatalf("version = %d, want the 1 A published", current.Version)
	}
	if current.CheckpointArtifactID != fixture.artifact.ID() {
		t.Fatal("a stale publisher overwrote a result it never saw")
	}
}

// An up-to-date publisher advances the pointer, and the record is exactly what
// publicationFor built at expected+1.
func TestAnUpToDatePublisherAdvancesThePointer(t *testing.T) {
	fixture := publishFixture(t)
	store := memory.NewStore()

	outcome, err := advancePointer(t.Context(), store, fixture.request,
		policyAt(fixture, 1), promotion.Decision{})
	if err != nil {
		t.Fatalf("advancePointer: %v", err)
	}
	if outcome.Result() != PublicationRecorded {
		t.Fatalf("result = %v, want recorded", outcome.Result())
	}
	recorded, ok := outcome.Publication()
	if !ok {
		t.Fatal("a recorded publication was not returned")
	}
	if recorded.Version != 1 {
		t.Fatalf("version = %d, want expected+1 = 1", recorded.Version)
	}
	if want := publicationFor(fixture.request, policyAt(fixture, 1), 1); recorded != want {
		t.Fatalf("recorded\n  %+v\nwant\n  %+v", recorded, want)
	}
}

// The at-least-once retry: a second attempt re-derives the same decision from the
// same expected version, and by then its own earlier publication is current. It must
// report the target as already publishing exactly this rather than appending a second
// identical record or failing as stale.
func TestAnAtLeastOnceRetryAppendsNothing(t *testing.T) {
	fixture := publishFixture(t)
	store := memory.NewStore()

	if _, err := advancePointer(t.Context(), store, fixture.request,
		policyAt(fixture, 1), promotion.Decision{}); err != nil {
		t.Fatalf("first publication: %v", err)
	}

	// Identical request, identical expected version: this is what an expired lease
	// produces, because execution is deterministic and reproduces the same artifacts.
	outcome, err := advancePointer(t.Context(), store, fixture.request,
		policyAt(fixture, 1), promotion.Decision{})
	if err != nil {
		t.Fatalf("a retry failed rather than reporting the target already satisfied: %v", err)
	}
	if outcome.Result() != PublicationUnchanged {
		t.Fatalf("result = %v, want unchanged", outcome.Result())
	}
	if _, found, err := store.PublicationAtVersion(t.Context(),
		fixture.tenant, fixture.customer, fixture.target, 2); err != nil || found {
		t.Fatalf("a retry appended a second publication: found=%t err=%v", found, err)
	}
}

// A retry whose policy advanced in the meantime must not silently become a second
// publication of the same checkpoint.
//
// This is the case the expected-version token fixes only partly, and saying so is the
// point. The retry's record differs -- it pins the new policy version -- so it is not
// recognised as a retry. What the token guarantees is that it cannot advance history
// from a version it did not name: it names the version it decided against, finds
// something else there, and loses. Whether such a retry SHOULD republish under a new
// policy is a real question this slice does not answer; it just cannot happen silently.
func TestARetryUnderAChangedPolicyCannotAdvanceSilently(t *testing.T) {
	fixture := publishFixture(t)
	store := memory.NewStore()

	if _, err := advancePointer(t.Context(), store, fixture.request,
		policyAt(fixture, 5), promotion.Decision{}); err != nil {
		t.Fatalf("first publication: %v", err)
	}

	// The same request, re-evaluated after the target's policy advanced.
	_, err := advancePointer(t.Context(), store, fixture.request,
		policyAt(fixture, 6), promotion.Decision{})
	if !errors.Is(err, ports.ErrPublicationConflict) {
		t.Fatalf("a retry under a changed policy got %v, want ErrPublicationConflict", err)
	}
	if _, found, err := store.PublicationAtVersion(t.Context(),
		fixture.tenant, fixture.customer, fixture.target, 2); err != nil || found {
		t.Fatalf("history advanced silently: found=%t err=%v", found, err)
	}
}

// Production break caught by construction: comparing whole structs would make every
// repeat look new, and the at-least-once execution delivery this system is built on
// would leave a target's history showing the same checkpoint published repeatedly.
func TestSamePublicationIgnoresOnlyTheVersion(t *testing.T) {
	base := ports.Publication{
		TenantID: "acme", CustomerID: "cust", Target: "cm", Version: 4,
		PolicyVersion: 7, ProfileID: "sha256:profile", AssessmentID: "sha256:assessment",
		CheckpointArtifactID: "sha256:checkpoint", SemanticRunID: "sha256:run",
		ExecutionID: "sha256:execution",
	}

	t.Run("a different version alone is the same publication", func(t *testing.T) {
		later := base
		later.Version = 9
		if !samePublication(base, later) {
			t.Fatal("two records pinning identical evidence compared as different")
		}
	})

	// Every other field must distinguish. A field this ignored would let a genuinely
	// different decision be dropped as a retry, which is worse than a duplicate: the
	// publication would simply never happen.
	for _, test := range []struct {
		name   string
		mutate func(*ports.Publication)
	}{
		{"tenant", func(p *ports.Publication) { p.TenantID = "other" }},
		{"customer", func(p *ports.Publication) { p.CustomerID = "other" }},
		{"target", func(p *ports.Publication) { p.Target = "other" }},
		{"policy version", func(p *ports.Publication) { p.PolicyVersion = 8 }},
		{"profile", func(p *ports.Publication) { p.ProfileID = "sha256:other" }},
		{"assessment", func(p *ports.Publication) { p.AssessmentID = "sha256:other" }},
		{"checkpoint", func(p *ports.Publication) { p.CheckpointArtifactID = "sha256:other" }},
		{"semantic run", func(p *ports.Publication) { p.SemanticRunID = "sha256:other" }},
		{"execution", func(p *ports.Publication) { p.ExecutionID = "sha256:other" }},
	} {
		t.Run("a different "+test.name+" is a different publication", func(t *testing.T) {
			changed := base
			test.mutate(&changed)
			if samePublication(base, changed) {
				t.Fatalf("a record differing in %s compared as identical, so a real "+
					"publication would be discarded as a retry", test.name)
			}
		})
	}
}

// samePublication blanks both versions before comparing, which reads like a mutation
// of its arguments and is not one: it takes ports.Publication by value, so Go copies
// both and the caller's records cannot be reached. There is deliberately no test for
// that. Pass-by-value makes it unrepresentable rather than merely untrue, and a test
// asserting it could never fail -- staticcheck said so, and was right. If either
// parameter ever becomes a pointer, this note is the reason to stop and reconsider
// rather than a suggestion to add the test back.

// A refused outcome must never carry a publication, and its two views of the answer
// must agree. Authorized reads the decision while Result reads a field, so a
// mismatch between them is representable and has to be excluded.
func TestARefusedOutcomeCarriesNoPublication(t *testing.T) {
	var outcome PublicationOutcome
	if outcome.Authorized() {
		t.Fatal("a zero-valued outcome reported authorization")
	}
	if outcome.Result() != PublicationRefused {
		t.Fatalf("the zero Result is %v, want PublicationRefused", outcome.Result())
	}
	if _, published := outcome.Publication(); published {
		t.Fatal("a zero-valued outcome carried a publication")
	}
	if got := PublicationResult(255).String(); got != "unknown" {
		t.Fatalf("PublicationResult(255) = %q, want unknown", got)
	}
	if got := PublicationRefused.String(); got != "refused" {
		t.Fatalf("PublicationRefused = %q", got)
	}
}

// ── fixture ─────────────────────────────────────────────────────────────────

type publishSetup struct {
	tenant     ports.TenantID
	customer   ports.CustomerID
	target     ports.TargetKey
	result     SpineResult
	retained   []semantic.CheckpointArtifact
	artifact   semantic.CheckpointArtifact
	assessment semantic.Assessment
	receipt    CheckpointReceipt
	request    PublishRequest
}

func publishFixture(t *testing.T) publishSetup {
	t.Helper()
	return publishFixtureFor(t, teamhos.Passing)
}

// publishFixtureFor runs the real spine and takes its receipt for the last
// checkpoint retained.
//
// It runs the spine rather than driving the kernel directly, which it must: a
// CheckpointReceipt can only be minted by a SpineResult, and that is the whole point
// of the type. A fixture that assembled one another way would be testing a
// construction production cannot perform.
func publishFixtureFor(t *testing.T, variant teamhos.Variant) publishSetup {
	t.Helper()

	inputs, err := teamhos.New(variant)
	if err != nil {
		t.Fatalf("teamhos.New: %v", err)
	}
	result, err := Run(t.Context(), Request{
		Compilation:      inputs.Compilation,
		InitialState:     inputs.InitialState,
		World:            inputs.World,
		ExecutorIdentity: inputs.ExecutorIdentity,
		Policy:           inputs.Policy,
	}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	retained := result.Checkpoints()
	if len(retained) == 0 {
		t.Fatal("the fixture retained no checkpoint")
	}
	artifact := retained[len(retained)-1]

	receipt, ok := result.ReceiptFor(artifact)
	if !ok {
		t.Fatal("no receipt for a checkpoint the run retained")
	}

	// The assessment must be the one bound to this checkpoint. Picking any assessment
	// would make the fixture itself incoherent, and the digest-consistency clause
	// would then refuse for a reason the test did not intend.
	var assessment semantic.Assessment
	for _, candidate := range result.Assessments() {
		if candidate.CheckpointArtifactID() == artifact.ID() {
			assessment = candidate
			break
		}
	}
	if assessment.ID() == "" {
		t.Fatal("no assessment is bound to the fixture's checkpoint")
	}

	setup := publishSetup{
		tenant: "acme", customer: "cust", target: "cm",
		result: result, retained: retained, artifact: artifact,
		assessment: assessment, receipt: receipt,
	}
	setup.request = PublishRequest{
		TenantID: setup.tenant, CustomerID: setup.customer, Target: setup.target,
		ExpectedCurrentVersion: 0,
		Receipt:                receipt,
		Candidate: promotion.Candidate{
			Checkpoint:               artifact,
			Assessment:               assessment,
			RetainedInvariantWitness: artifact.InvariantResultCanonicalBytes(),
		},
	}
	return setup
}

// forgedBinding builds a valid RunBinding over the identical semantic request under a
// different executor, and never executes it.
//
// This is the construction the earlier RunBinding-based design accepted: the semantic
// run is the same, because executor identity is excluded from it, while the
// ExecutionID differs. It is kept as a fixture so the property that closed the hole
// stays anchored to the thing it closed.
func forgedBinding(t *testing.T) semantic.RunBinding {
	t.Helper()
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
	executor, err := semantic.NewExecutorIdentity("go",
		semantic.Digest("sha256:"+strings.Repeat("b", 64)))
	if err != nil {
		t.Fatalf("NewExecutorIdentity: %v", err)
	}
	binding, err := semantic.BindRun(semantic.RunBindingRequest{
		Plan: plan, InitialState: inputs.InitialState, World: inputs.World,
		ExecutorIdentity: executor, Policy: inputs.Policy,
	})
	if err != nil {
		t.Fatalf("BindRun: %v", err)
	}
	return binding
}

// storesWithPolicy returns one store serving both ports with a policy recorded for
// the fixture's target, requiring the profile its assessment was taken under.
func storesWithPolicy(t *testing.T, fixture publishSetup) PublicationStores {
	t.Helper()
	store := memory.NewStore()
	if err := store.PutPolicy(t.Context(), ports.TargetPolicy{
		TenantID: fixture.tenant, CustomerID: fixture.customer, Target: fixture.target,
		Version: 1, RequiredProfileID: fixture.assessment.ProfileID(),
	}); err != nil {
		t.Fatalf("PutPolicy: %v", err)
	}
	return PublicationStores{Policies: store, Publications: store}
}

// policyAt returns the fixture's policy at a given version, so a test can vary the
// version that authorized a publication without rebuilding the whole fixture.
func policyAt(fixture publishSetup, version ports.PolicyVersion) ports.TargetPolicy {
	return ports.TargetPolicy{
		TenantID: fixture.tenant, CustomerID: fixture.customer, Target: fixture.target,
		Version: version, RequiredProfileID: fixture.assessment.ProfileID(),
	}
}

// receiptFor mints a receipt for another of the run's retained checkpoints, so a test
// can build a second genuine publication rather than a doctored one.
func receiptFor(
	t *testing.T, fixture publishSetup, artifact semantic.CheckpointArtifact,
) CheckpointReceipt {
	t.Helper()
	receipt, ok := fixture.result.ReceiptFor(artifact)
	if !ok {
		t.Fatal("no receipt for a checkpoint the run retained")
	}
	return receipt
}
