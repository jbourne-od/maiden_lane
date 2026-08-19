package app

import (
	"errors"
	"testing"

	"github.com/optimaldynamics/maiden-lane/internal/adapters/memory"
	"github.com/optimaldynamics/maiden-lane/internal/fixtures/teamhos"
	"github.com/optimaldynamics/maiden-lane/internal/ports"
	"github.com/optimaldynamics/maiden-lane/internal/promotion"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

// THE POINT OF THE SLICE: evidence assembled from stored executions satisfies clause 6.
//
// Everything before this made comparability decidable; nothing could actually decide it,
// because a comparison's evidence is authenticated kernel artifacts and those cannot be
// read out of a store. This assembles them by re-deriving both sides, and the assertion
// that matters is not that assembly succeeds but that what it produces passes the clause.
func TestAssembledEvidenceSatisfiesTheComparisonClause(t *testing.T) {
	fixture := comparisonFixture(t, 2)
	runBothSides(t, fixture)

	assembly, err := AssembleComparison(t.Context(), fixture.stores, fixture.request)
	if err != nil {
		t.Fatalf("AssembleComparison: %v", err)
	}
	if missing := assembly.Missing(); len(missing) != 0 {
		t.Fatalf("evidence is incomplete: %+v", missing)
	}
	evidence, ok := assembly.Evidence()
	if !ok {
		t.Fatal("complete assembly produced no evidence")
	}
	if len(evidence.Baseline) != 2 || len(evidence.Candidate) != 2 {
		t.Fatalf("evidence covers %d baseline and %d candidate cases, want 2 each",
			len(evidence.Baseline), len(evidence.Candidate))
	}

	// The clause, evaluated against evidence nothing constructed by hand.
	candidate := fixture.promotedCandidate(t)
	candidate.Comparison = evidence
	result := clauseFor(promotion.Evaluate(fixture.policy, candidate), promotion.ClauseComparisonCorpus)
	if result.Verdict() != promotion.Pass {
		t.Fatalf("clause 6 = %v/%v on assembled evidence, want Pass",
			result.Verdict(), result.Unevaluated())
	}
}

// Every case must be accounted for, and a missing one must say what to do about it. The
// reasons are distinct because two are answered by running something and two mean the
// execution ran and did not produce what the comparison needs.
func TestAnIncompleteSideReportsWhichCasesAreMissingAndWhy(t *testing.T) {
	fixture := comparisonFixture(t, 2)

	// Nothing run at all.
	assembly, err := AssembleComparison(t.Context(), fixture.stores, fixture.request)
	if err != nil {
		t.Fatalf("AssembleComparison: %v", err)
	}
	if _, ok := assembly.Evidence(); ok {
		t.Fatal("evidence was produced for a comparison neither side has run")
	}
	missing := assembly.Missing()
	if len(missing) != 4 {
		t.Fatalf("missing = %d, want 4: two cases on each side", len(missing))
	}
	baseline, candidate := 0, 0
	for _, absent := range missing {
		if absent.Reason != MissingNotExecuted {
			t.Fatalf("reason = %s, want not_executed", absent.Reason)
		}
		if absent.CaseDigest == "" || absent.ExecutionID == "" {
			t.Fatal("a missing case does not say which case or which execution")
		}
		if absent.Baseline {
			baseline++
		} else {
			candidate++
		}
	}
	if baseline != 2 || candidate != 2 {
		t.Fatalf("missing %d baseline and %d candidate, want 2 each", baseline, candidate)
	}

	// Enqueued but not answered is a different reason, because waiting fixes it.
	enqueueBothSides(t, fixture)
	pending, err := AssembleComparison(t.Context(), fixture.stores, fixture.request)
	if err != nil {
		t.Fatalf("AssembleComparison: %v", err)
	}
	for _, absent := range pending.Missing() {
		if absent.Reason != MissingNotAnswered {
			t.Fatalf("reason = %s, want not_answered", absent.Reason)
		}
	}
}

// PARTIAL EVIDENCE IS NEVER RETURNED. Comparability is a statement about a whole corpus,
// and handing back what happened to be available would let a caller evaluate a comparison
// over a smaller corpus than the one it names — which is exactly the substitution the
// corpus identity exists to prevent.
func TestPartialEvidenceIsNeverReturned(t *testing.T) {
	fixture := comparisonFixture(t, 2)
	runBothSides(t, fixture)

	// Complete first, so the contrast is about the missing case rather than about setup.
	if assembly, err := AssembleComparison(t.Context(), fixture.stores, fixture.request); err != nil {
		t.Fatalf("AssembleComparison: %v", err)
	} else if _, ok := assembly.Evidence(); !ok {
		t.Fatal("complete sides produced no evidence")
	}

	// A comparison over a corpus with one more case: the extra case has not run, so the
	// evidence for the two that did must not be handed back.
	larger := fixture.withCorpus(t, 3)
	assembly, err := AssembleComparison(t.Context(), fixture.stores, larger)
	if err != nil {
		t.Fatalf("AssembleComparison: %v", err)
	}
	if evidence, ok := assembly.Evidence(); ok {
		t.Fatalf("partial evidence was returned: %d baseline cases for a 3-case corpus",
			len(evidence.Baseline))
	}
	if len(assembly.Missing()) == 0 {
		t.Fatal("an incomplete assembly reported nothing missing")
	}
}

// The supplied world must be the one the comparison was identified under. A WorldID is
// one-way, so the world has to be supplied as a value — and verified rather than trusted,
// or a caller could bind the cases under a different world and derive executions the
// comparison does not name.
func TestTheSuppliedWorldMustBeTheComparisonsOwn(t *testing.T) {
	fixture := comparisonFixture(t, 2)
	request := fixture.request

	reference, err := semantic.NewWorldReference(
		semantic.WorldReferenceSnapshot, semantic.Digest("sha256:"+repeatRune('e', 64)))
	if err != nil {
		t.Fatalf("NewWorldReference: %v", err)
	}
	elsewhere, err := semantic.NewWorld([]semantic.WorldReference{reference})
	if err != nil {
		t.Fatalf("NewWorld: %v", err)
	}
	request.World = elsewhere

	_, err = AssembleComparison(t.Context(), fixture.stores, request)
	var invalid InvalidInputError
	if !errors.As(err, &invalid) || invalid.Code != InputComparisonWorldMismatch {
		t.Fatalf("error = %v, want InputComparisonWorldMismatch", err)
	}
}

// An absent corpus is reported as such rather than as an empty comparison, and an
// incomplete request is refused before any store is read.
func TestAssemblyRefusesAnUnusableRequest(t *testing.T) {
	fixture := comparisonFixture(t, 2)

	for _, test := range []struct {
		name   string
		mutate func(*AssembleComparisonRequest)
		code   InvalidInputCode
	}{
		{"no tenant", func(r *AssembleComparisonRequest) { r.TenantID = "" }, InputComparisonIncomplete},
		{"no comparison", func(r *AssembleComparisonRequest) {
			r.Comparison = semantic.Comparison{}
		}, InputComparisonIncomplete},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := fixture.request
			test.mutate(&request)
			_, err := AssembleComparison(t.Context(), fixture.stores, request)
			var invalid InvalidInputError
			if !errors.As(err, &invalid) || invalid.Code != test.code {
				t.Fatalf("error = %v, want %s", err, test.code)
			}
		})
	}

	t.Run("a corpus nobody stored", func(t *testing.T) {
		empty := comparisonFixtureWithoutCorpus(t)
		_, err := AssembleComparison(t.Context(), empty.stores, empty.request)
		var invalid InvalidInputError
		if !errors.As(err, &invalid) || invalid.Code != InputCorpusAbsent {
			t.Fatalf("error = %v, want InputCorpusAbsent", err)
		}
	})
}

// Assembly reads a store to check it and writes nothing. It also must not enqueue: paying
// for evidence is not the same as starting the work that produces it.
func TestAssemblyWritesNothing(t *testing.T) {
	fixture := comparisonFixture(t, 2)

	if _, err := AssembleComparison(t.Context(), fixture.stores, fixture.request); err != nil {
		t.Fatalf("AssembleComparison: %v", err)
	}
	// Nothing was enqueued by assembling, so a progress read still finds nothing.
	progress, err := CorpusProgress(t.Context(),
		CorpusRunStores{Corpora: fixture.store, Plans: fixture.store, Executions: fixture.store},
		fixture.runRequest(true))
	if err != nil {
		t.Fatalf("CorpusProgress: %v", err)
	}
	for _, caseRun := range progress.Cases() {
		if caseRun.Status() != "" {
			t.Fatalf("assembling created an execution: case %s is %s",
				caseRun.CaseDigest(), caseRun.Status())
		}
	}
}

func clauseFor(decision promotion.Decision, clause promotion.Clause) promotion.ClauseResult {
	for _, result := range decision.Clauses() {
		if result.Clause() == clause {
			return result
		}
	}
	return promotion.ClauseResult{}
}

// ── fixture ─────────────────────────────────────────────────────────────────

type comparisonSetup struct {
	store       *memory.Store
	stores      ComparisonStores
	corpus      semantic.Corpus
	plan        semantic.Plan
	compilation semantic.CompileRequest
	profile     semantic.ProfileID
	policy      ports.TargetPolicy
	request     AssembleComparisonRequest
}

// comparisonFixture stores a plan and a corpus, and builds a comparison between two
// checkpoint declarations of that plan.
//
// One plan on both sides, which the correspondence contract permits: it is the smallest
// shape that exercises assembly end to end, and it means both sides share a corpus without
// needing two plans that pin the same schema. The distinction the assembler actually
// depends on is between two CheckpointIDs, and two declarations of one plan have that.
func comparisonFixture(t *testing.T, cases int) comparisonSetup {
	t.Helper()
	return comparisonFixtureWith(t, cases, true)
}

func comparisonFixtureWithoutCorpus(t *testing.T) comparisonSetup {
	t.Helper()
	return comparisonFixtureWith(t, 2, false)
}

func comparisonFixtureWith(t *testing.T, cases int, storeCorpus bool) comparisonSetup {
	t.Helper()

	inputs, err := teamhosInputs()
	if err != nil {
		t.Fatalf("teamhos inputs: %v", err)
	}
	compilation, err := semantic.Compile(inputs.Compilation)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	plan, ok := compilation.Plan()
	if !ok {
		t.Fatal("the fixture did not compile")
	}

	store := memory.NewStore()
	if err := store.PutPlan(t.Context(), ports.PlanRecord{
		TenantID: "acme", PlanID: plan.ID(), Input: compilation.Input(),
		Schema: inputs.InitialState.Schema(), Compilation: compilation,
	}); err != nil {
		t.Fatalf("PutPlan: %v", err)
	}

	corpus := corpusWithCompleteObservations(t, inputs.InitialState.Schema(), cases)
	if storeCorpus {
		if err := store.PutCorpus(t.Context(), ports.CorpusRecord{
			TenantID: "acme", CorpusID: corpus.ID(), Corpus: corpus,
		}); err != nil {
			t.Fatalf("PutCorpus: %v", err)
		}
	}

	checkpoints := plan.Checkpoints()
	if len(checkpoints) < 2 {
		t.Fatal("the fixture plan needs two checkpoint declarations")
	}
	policy, err := semantic.NewComparisonPolicy(plan, plan, []semantic.CheckpointPair{
		{Baseline: checkpoints[0].Key, Candidate: checkpoints[1].Key},
	})
	if err != nil {
		t.Fatalf("NewComparisonPolicy: %v", err)
	}
	// The first profile, because both compared checkpoints are ready under it and clause
	// 6 requires replay evidence to be ready. TestAssemblyPicksAssessmentsUnderThe
	// ComparisonsProfile covers the profile filter separately, using the second profile,
	// because with the first an assembler ignoring the profile would pick correctly by
	// accident.
	profile := compilation.Profiles()[0].ID()
	comparison, err := semantic.NewComparison(semantic.ComparisonRequest{
		Baseline:  checkpointIdentityFor(t, plan, checkpoints[0].Key),
		Candidate: checkpointIdentityFor(t, plan, checkpoints[1].Key),
		Profile:   profile, World: inputs.World.ID(), Corpus: corpus.ID(), Policy: policy,
	})
	if err != nil {
		t.Fatalf("NewComparison: %v", err)
	}

	side := ComparisonSide{ExecutorIdentity: inputs.ExecutorIdentity, Policy: inputs.Policy}
	return comparisonSetup{
		store:       store,
		stores:      ComparisonStores{Corpora: store, Plans: store, Executions: store},
		corpus:      corpus,
		plan:        plan,
		compilation: inputs.Compilation,
		profile:     profile,
		policy: ports.TargetPolicy{
			TenantID: "acme", CustomerID: "cust", Target: "cm",
			Version: 1, RequiredProfileID: profile,
		},
		request: AssembleComparisonRequest{
			TenantID: "acme", Comparison: comparison, World: inputs.World,
			Baseline: side, Candidate: side,
		},
	}
}

// withCorpus rebuilds the request over a corpus of a different size, so a test can ask for
// evidence covering cases that were never run.
func (s comparisonSetup) withCorpus(t *testing.T, cases int) AssembleComparisonRequest {
	t.Helper()
	inputs, err := teamhosInputs()
	if err != nil {
		t.Fatalf("teamhos inputs: %v", err)
	}
	larger := corpusWithCompleteObservations(t, inputs.InitialState.Schema(), cases)
	if err := s.store.PutCorpus(t.Context(), ports.CorpusRecord{
		TenantID: "acme", CorpusID: larger.ID(), Corpus: larger,
	}); err != nil {
		t.Fatalf("PutCorpus: %v", err)
	}

	old := s.request.Comparison
	comparison, err := semantic.NewComparison(semantic.ComparisonRequest{
		Baseline: old.Baseline(), Candidate: old.Candidate(), Profile: old.Profile(),
		World: old.World(), Corpus: larger.ID(), Policy: old.Policy(),
	})
	if err != nil {
		t.Fatalf("NewComparison: %v", err)
	}
	request := s.request
	request.Comparison = comparison
	return request
}

// runRequest builds the side run for this fixture's corpus and plan.
func (s comparisonSetup) runRequest(baseline bool) CorpusRunRequest {
	inputs, _ := teamhosInputs()
	return CorpusRunRequest{
		TenantID: "acme", CorpusID: s.corpus.ID(), PlanID: s.plan.ID(),
		World: inputs.World, ExecutorIdentity: inputs.ExecutorIdentity, Policy: inputs.Policy,
	}
}

// promotedCandidate builds the gate candidate for the checkpoint the comparison's
// candidate side names, from a real execution of the first corpus case.
func (s comparisonSetup) promotedCandidate(t *testing.T) promotion.Candidate {
	t.Helper()
	state, ok := s.corpus.Case(0)
	if !ok {
		t.Fatal("the corpus has no first case")
	}
	inputs, err := teamhosInputs()
	if err != nil {
		t.Fatalf("teamhos inputs: %v", err)
	}
	// The fixture's own candidate plan, which for an asymmetric comparison is not the
	// teamhos default: the promoted checkpoint must be the one the comparison's candidate
	// side names, and that declaration exists only in the candidate's plan.
	result, err := Run(t.Context(), Request{
		Compilation: s.compilation, InitialState: state, World: inputs.World,
		ExecutorIdentity: inputs.ExecutorIdentity, Policy: inputs.Policy,
	}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var artifact semantic.CheckpointArtifact
	for _, sealed := range result.Checkpoints() {
		if sealed.CheckpointID() == s.request.Comparison.Candidate() {
			artifact = sealed
		}
	}
	if artifact.ID() == "" {
		t.Fatal("the run sealed no checkpoint for the comparison's candidate side")
	}
	var assessment semantic.Assessment
	for _, candidate := range result.Assessments() {
		if candidate.CheckpointArtifactID() == artifact.ID() &&
			candidate.ProfileID() == s.profile {
			assessment = candidate
		}
	}
	if assessment.ID() == "" {
		t.Fatal("no assessment under the comparison's profile")
	}
	plan, _ := result.Plan()
	receipt, _ := result.ReceiptFor(artifact)
	return promotion.Candidate{
		Checkpoint:               artifact,
		Plan:                     plan,
		Assessment:               assessment,
		RetainedInvariantWitness: artifact.InvariantResultCanonicalBytes(),
		ExecutionID:              receipt.ExecutionID(),
	}
}

// enqueueBothSides queues every case without running it, so "enqueued but not answered"
// can be distinguished from "never executed".
func enqueueBothSides(t *testing.T, fixture comparisonSetup) {
	t.Helper()
	stores := CorpusRunStores{
		Corpora: fixture.store, Plans: fixture.store, Executions: fixture.store,
	}
	if _, err := RunCorpus(t.Context(), stores, fixture.runRequest(true)); err != nil {
		t.Fatalf("RunCorpus: %v", err)
	}
}

// runBothSides executes every case for real and stores the results the way the worker
// does, so assembly has genuine executions to rehydrate.
func runBothSides(t *testing.T, fixture comparisonSetup) {
	t.Helper()
	enqueueBothSides(t, fixture)

	inputs, err := teamhosInputs()
	if err != nil {
		t.Fatalf("teamhos inputs: %v", err)
	}
	for index := 0; index < fixture.corpus.Len(); index++ {
		state, _ := fixture.corpus.Case(index)
		binding, err := semantic.BindRun(semantic.RunBindingRequest{
			Plan: fixture.plan, InitialState: state, World: inputs.World,
			ExecutorIdentity: inputs.ExecutorIdentity, Policy: inputs.Policy,
		})
		if err != nil {
			t.Fatalf("BindRun: %v", err)
		}
		result, err := Run(t.Context(), Request{
			Compilation: inputs.Compilation, InitialState: state, World: inputs.World,
			ExecutorIdentity: inputs.ExecutorIdentity, Policy: inputs.Policy,
		}, nil)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		stored, found, err := fixture.store.Get(t.Context(), "acme", binding.ExecutionID())
		if err != nil || !found {
			t.Fatalf("Get: found=%t err=%v", found, err)
		}
		projected, err := Project(stored.Request, result)
		if err != nil {
			t.Fatalf("Project: %v", err)
		}
		leased, _, err := fixture.store.Claim(t.Context(), leaseForTest)
		if err != nil {
			t.Fatalf("Claim: %v", err)
		}
		if err := fixture.store.Complete(t.Context(), leased.AttemptID, projected); err != nil {
			t.Fatalf("Complete: %v", err)
		}
	}
}

func checkpointIdentityFor(t *testing.T, plan semantic.Plan, key semantic.CheckpointKey) semantic.CheckpointID {
	t.Helper()
	// The kernel derives this; a test recomputing the formula would prove only that two
	// copies of one mistake agree. Sealing is the other identity-producing path, so the
	// identity is taken from a real seal of this checkpoint.
	inputs, err := teamhosInputs()
	if err != nil {
		t.Fatalf("teamhos inputs: %v", err)
	}
	result, err := Run(t.Context(), Request{
		Compilation: inputs.Compilation, InitialState: inputs.InitialState, World: inputs.World,
		ExecutorIdentity: inputs.ExecutorIdentity, Policy: inputs.Policy,
	}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, sealed := range result.Checkpoints() {
		if sealed.Checkpoint().Key == key {
			return sealed.CheckpointID()
		}
	}
	t.Fatalf("no sealed checkpoint for %s", key)
	return ""
}

// corpusWithCompleteObservations builds cases that run the whole plan.
//
// The corpus-run tests only need cases that bind, so they carry an assignment key and
// nothing else. Assembly needs cases that reach the SECOND checkpoint, which means every
// driver must carry a complete and self-consistent HOS tuple: the same duty anchor, and
// driving hours no greater than elapsed. Cases missing that are refused at the aggregation
// boundary and seal nothing there, which the assembler correctly reports as
// checkpoint_not_sealed — that is how this fixture's first version was found to be wrong.
func corpusWithCompleteObservations(t *testing.T, schema semantic.Schema, cases int) semantic.Corpus {
	t.Helper()
	lineage, err := semantic.NewInputLineageID("maiden-lane.sanitized-fixture", "team-hos-team-ab")
	if err != nil {
		t.Fatalf("NewInputLineageID: %v", err)
	}
	anchor, err := semantic.NewAtomValue("T0")
	if err != nil {
		t.Fatalf("NewAtomValue: %v", err)
	}

	states := make([]semantic.State, 0, cases)
	for i := 0; i < cases; i++ {
		key, err := semantic.NewStringValue("X")
		if err != nil {
			t.Fatalf("NewStringValue: %v", err)
		}
		entities := make([]semantic.Entity, 0, 2)
		for driver, source := range []string{"A", "B"} {
			// Distinct hours per driver and per case, always with driving no greater than
			// elapsed, so every case is a different corpus member and every one of them
			// aggregates.
			elapsed := int64(10 + i*2 + driver)
			entity, err := semantic.NewEntity(semantic.EntityRef{
				Kind: "driver",
				ID:   semantic.SourceEntityID(lineage, "driver", source),
			}, map[semantic.FieldName]semantic.Value{
				"assignment_key":    key,
				"hos_anchor":        anchor,
				"hos_elapsed_hours": semantic.NewInt64Value(elapsed),
				"hos_driving_hours": semantic.NewInt64Value(elapsed - 2),
			})
			if err != nil {
				t.Fatalf("NewEntity: %v", err)
			}
			entities = append(entities, entity)
		}
		state, err := semantic.NewState(schema, lineage, entities, nil)
		if err != nil {
			t.Fatalf("NewState: %v", err)
		}
		states = append(states, state)
	}

	corpus, err := semantic.NewCorpus(states)
	if err != nil {
		t.Fatalf("NewCorpus: %v", err)
	}
	return corpus
}

// PRODUCTION BREAK CAUGHT BY MUTATION TESTING: the assembler must select assessments
// under the COMPARISON's profile, and the main fixture could not observe that.
//
// It compares under the first profile, so an assembler that ignored the profile and took
// whichever assessment came first would pick the right one by accident — verified, the
// mutation escaped the whole suite. This one compares under the second profile and checks
// the selected assessments directly rather than through the clause, because the two
// checkpoints are not both ready under that profile and readiness is a different clause's
// question.
func TestAssemblyPicksAssessmentsUnderTheComparisonsProfile(t *testing.T) {
	fixture := comparisonFixture(t, 2)
	runBothSides(t, fixture)

	inputs, err := teamhosInputs()
	if err != nil {
		t.Fatalf("teamhos inputs: %v", err)
	}
	compilation, err := semantic.Compile(inputs.Compilation)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	profiles := compilation.Profiles()
	if len(profiles) < 2 {
		t.Fatal("the fixture needs two profiles for the filter to be observable")
	}
	second := profiles[1].ID()
	if second == fixture.profile {
		t.Fatal("the two profiles must differ")
	}

	old := fixture.request.Comparison
	restated, err := semantic.NewComparison(semantic.ComparisonRequest{
		Baseline: old.Baseline(), Candidate: old.Candidate(), Profile: second,
		World: old.World(), Corpus: old.Corpus(), Policy: old.Policy(),
	})
	if err != nil {
		t.Fatalf("NewComparison: %v", err)
	}
	request := fixture.request
	request.Comparison = restated

	assembly, err := AssembleComparison(t.Context(), fixture.stores, request)
	if err != nil {
		t.Fatalf("AssembleComparison: %v", err)
	}
	evidence, ok := assembly.Evidence()
	if !ok {
		t.Fatalf("assembly is incomplete under the second profile: %+v", assembly.Missing())
	}

	for _, side := range [][]promotion.ComparedCase{evidence.Baseline, evidence.Candidate} {
		for i, compared := range side {
			if compared.Assessment.ProfileID() != second {
				t.Fatalf("case %d carries an assessment under %s, want the comparison's %s",
					i, compared.Assessment.ProfileID(), second)
			}
		}
	}
}

// PRODUCTION BREAK CAUGHT BY OWNER REVIEW, and by the very lesson this slice recorded:
// the main fixture used ONE plan and ONE set of side settings for both sides, so neither
// selection was observable.
//
// Two load-bearing bugs escaped the whole suite — using the candidate's plan for both
// sides, and the candidate's executor and provenance settings for both — because baseline
// and candidate were identical in exactly the dimensions the broken code would confuse.
// Verified before this test existed.
//
// This fixture is asymmetric in both: two different plans sharing a schema, with the
// compared checkpoint renamed on the candidate side so the correspondence has real work
// to do, and two different executor identities so each side resolves to its own
// executions. It is one realistic fixture rather than several narrow tests, because what
// needs proving is that assembly picks the right plan AND the right executions together.
func TestAssemblyResolvesEachSideToItsOwnPlanAndExecutions(t *testing.T) {
	fixture := asymmetricComparisonFixture(t, 2)

	// The two sides really are distinct, or this test proves nothing.
	if fixture.baselinePlan.ID() == fixture.candidatePlan.ID() {
		t.Fatal("the fixture must use two different plans")
	}
	if fixture.request.Baseline.ExecutorIdentity == fixture.request.Candidate.ExecutorIdentity {
		t.Fatal("the fixture must use two different executor identities")
	}

	assembly, err := AssembleComparison(t.Context(), fixture.stores, fixture.request)
	if err != nil {
		t.Fatalf("AssembleComparison: %v", err)
	}
	evidence, ok := assembly.Evidence()
	if !ok {
		t.Fatalf("assembly is incomplete: %+v", assembly.Missing())
	}

	// Each side's artifacts must realize its OWN checkpoint declaration, which differ
	// because the plans differ. This is what fails when both sides resolve to one plan.
	for i, compared := range evidence.Baseline {
		if compared.Checkpoint.CheckpointID() != fixture.request.Comparison.Baseline() {
			t.Fatalf("baseline case %d realizes the wrong declaration", i)
		}
		if compared.Checkpoint.PlanID() != fixture.baselinePlan.ID() {
			t.Fatalf("baseline case %d came from the candidate's plan", i)
		}
	}
	for i, compared := range evidence.Candidate {
		if compared.Checkpoint.CheckpointID() != fixture.request.Comparison.Candidate() {
			t.Fatalf("candidate case %d realizes the wrong declaration", i)
		}
		if compared.Checkpoint.PlanID() != fixture.candidatePlan.ID() {
			t.Fatalf("candidate case %d came from the baseline's plan", i)
		}
	}

	// And each side's artifacts must come from ITS executions. Executor identity is
	// excluded from checkpoint identity by design, so the checkpoints alone cannot show
	// this — the semantic runs are identical across executors, and only the execution
	// identities differ. Comparing the run identities would prove nothing; the two sides'
	// artifacts are checked against the executions each side's settings derive.
	assertSideRanUnder(t, fixture, evidence.Baseline,
		fixture.baselinePlan, fixture.request.Baseline)
	assertSideRanUnder(t, fixture, evidence.Candidate,
		fixture.candidatePlan, fixture.request.Candidate)

	// The whole point: this evidence satisfies clause 6.
	candidate := fixture.promotedCandidate(t)
	candidate.Comparison = evidence
	result := clauseFor(promotion.Evaluate(fixture.policy, candidate),
		promotion.ClauseComparisonCorpus)
	if result.Verdict() != promotion.Pass {
		t.Fatalf("clause 6 = %v/%v on asymmetric evidence, want Pass",
			result.Verdict(), result.Unevaluated())
	}
}

// Splitting unattempted from pending, which these reasons exist for: one is waited on and
// the other never resolves, so collapsing them tells an operator to do the wrong thing.
func TestAnUnattemptedCaseIsDistinguishedFromAPendingOne(t *testing.T) {
	fixture := comparisonFixture(t, 2)
	enqueueBothSides(t, fixture)

	// Both cases are enqueued and unfinished.
	pending, err := AssembleComparison(t.Context(), fixture.stores, fixture.request)
	if err != nil {
		t.Fatalf("AssembleComparison: %v", err)
	}
	for _, absent := range pending.Missing() {
		if absent.Reason != MissingNotAnswered {
			t.Fatalf("reason = %s, want not_answered", absent.Reason)
		}
	}

	// Terminally fail one of them without a result, which is what Fail records for an
	// execution that could not be attempted.
	first := pending.Missing()[0]
	leased, _, err := fixture.store.Claim(t.Context(), leaseForTest)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if err := fixture.store.Fail(t.Context(), "acme", leased.Request.ExecutionID,
		leased.AttemptID, "plan_absent"); err != nil {
		t.Fatalf("Fail: %v", err)
	}

	after, err := AssembleComparison(t.Context(), fixture.stores, fixture.request)
	if err != nil {
		t.Fatalf("AssembleComparison: %v", err)
	}
	unattempted, waiting := 0, 0
	for _, absent := range after.Missing() {
		switch absent.Reason {
		case MissingUnattempted:
			unattempted++
			// This fixture's two sides share one plan and one executor, so they resolve
			// to the same executions and one failed execution is reported once per side.
			// Every unattempted entry must therefore name the execution that was failed.
			if absent.ExecutionID != first.ExecutionID {
				t.Fatal("a case other than the failed one was reported as unattempted")
			}
		case MissingNotAnswered:
			waiting++
		default:
			t.Fatalf("unexpected reason %s", absent.Reason)
		}
	}
	if unattempted == 0 {
		t.Fatal("the failed case was not reported as unattempted, so waiting on it would " +
			"never resolve and nothing says so")
	}
	if waiting == 0 {
		t.Fatal("no case is still merely pending, so the two reasons are not distinguished")
	}
}

// The returned evidence must be a copy. A caller writing to it would otherwise write into
// the assembly's own evidence, which every other result type here forbids.
func TestReturnedEvidenceIsACopy(t *testing.T) {
	fixture := comparisonFixture(t, 2)
	runBothSides(t, fixture)

	assembly, err := AssembleComparison(t.Context(), fixture.stores, fixture.request)
	if err != nil {
		t.Fatalf("AssembleComparison: %v", err)
	}
	first, _ := assembly.Evidence()
	for i := range first.Baseline {
		first.Baseline[i] = promotion.ComparedCase{}
	}
	for i := range first.Candidate {
		first.Candidate[i] = promotion.ComparedCase{}
	}

	second, ok := assembly.Evidence()
	if !ok {
		t.Fatal("the assembly stopped reporting evidence")
	}
	for i, compared := range second.Baseline {
		if compared.Checkpoint.ID() == "" {
			t.Fatalf("baseline case %d was cleared through the returned slice", i)
		}
	}
	for i, compared := range second.Candidate {
		if compared.Checkpoint.ID() == "" {
			t.Fatalf("candidate case %d was cleared through the returned slice", i)
		}
	}
}

// ── asymmetric fixture ──────────────────────────────────────────────────────

type asymmetricSetup struct {
	comparisonSetup
	baselinePlan  semantic.Plan
	candidatePlan semantic.Plan
}

// asymmetricComparisonFixture builds a comparison whose two sides genuinely differ, in
// both dimensions the assembler selects on.
//
// The plans differ by a renamed checkpoint, which changes PlanID while leaving the schema
// alone — so one corpus is replayable under both, which the kernel requires. The executor
// identities differ, which changes ExecutionID while leaving SemanticRunID alone, so each
// side resolves to its own executions over the same semantic runs.
func asymmetricComparisonFixture(t *testing.T, cases int) asymmetricSetup {
	t.Helper()

	inputs, err := teamhosInputs()
	if err != nil {
		t.Fatalf("teamhos inputs: %v", err)
	}
	baselinePlan := compilePlan(t, inputs.Compilation)

	renamed := inputs.Compilation
	checkpoints := append([]semantic.CheckpointDeclaration(nil), renamed.Rules.Checkpoints...)
	renamedKey := semantic.CheckpointKey("team_hos_reconciled.v2")
	found := false
	for i := range checkpoints {
		if checkpoints[i].Key == "team_hos_aggregated.v1" {
			checkpoints[i].Key = renamedKey
			found = true
		}
	}
	if !found {
		t.Fatal("the fixture no longer declares team_hos_aggregated.v1")
	}
	renamed.Rules.Checkpoints = checkpoints
	candidatePlan := compilePlan(t, renamed)

	if baselinePlan.SchemaDigest() != candidatePlan.SchemaDigest() {
		t.Fatal("the two plans must share a schema, or one corpus cannot run under both")
	}

	otherExecutor, err := semantic.NewExecutorIdentity("go",
		semantic.Digest("sha256:"+repeatRune('b', 64)))
	if err != nil {
		t.Fatalf("NewExecutorIdentity: %v", err)
	}

	store := memory.NewStore()
	for _, plan := range []semantic.Plan{baselinePlan, candidatePlan} {
		compilation := compileFor(t, plan, inputs, renamedKey)
		if err := store.PutPlan(t.Context(), ports.PlanRecord{
			TenantID: "acme", PlanID: plan.ID(), Input: compilation.Input(),
			Schema: inputs.InitialState.Schema(), Compilation: compilation,
		}); err != nil {
			t.Fatalf("PutPlan: %v", err)
		}
	}

	corpus := corpusWithCompleteObservations(t, inputs.InitialState.Schema(), cases)
	if err := store.PutCorpus(t.Context(), ports.CorpusRecord{
		TenantID: "acme", CorpusID: corpus.ID(), Corpus: corpus,
	}); err != nil {
		t.Fatalf("PutCorpus: %v", err)
	}

	profile := compileFor(t, baselinePlan, inputs, renamedKey).Profiles()[0].ID()
	policy, err := semantic.NewComparisonPolicy(baselinePlan, candidatePlan,
		[]semantic.CheckpointPair{{Baseline: "team_hos_aggregated.v1", Candidate: renamedKey}})
	if err != nil {
		t.Fatalf("NewComparisonPolicy: %v", err)
	}
	comparison, err := semantic.NewComparison(semantic.ComparisonRequest{
		Baseline:  checkpointIdentityIn(t, baselinePlan, "team_hos_aggregated.v1"),
		Candidate: checkpointIdentityIn(t, candidatePlan, renamedKey),
		Profile:   profile, World: inputs.World.ID(), Corpus: corpus.ID(), Policy: policy,
	})
	if err != nil {
		t.Fatalf("NewComparison: %v", err)
	}

	setup := asymmetricSetup{
		comparisonSetup: comparisonSetup{
			store:       store,
			stores:      ComparisonStores{Corpora: store, Plans: store, Executions: store},
			corpus:      corpus,
			plan:        candidatePlan,
			compilation: compileFor(t, candidatePlan, inputs, renamedKey).Input().Request(),
			profile:     profile,
			policy: ports.TargetPolicy{
				TenantID: "acme", CustomerID: "cust", Target: "cm",
				Version: 1, RequiredProfileID: profile,
			},
			request: AssembleComparisonRequest{
				TenantID: "acme", Comparison: comparison, World: inputs.World,
				Baseline:  ComparisonSide{ExecutorIdentity: inputs.ExecutorIdentity, Policy: inputs.Policy},
				Candidate: ComparisonSide{ExecutorIdentity: otherExecutor, Policy: inputs.Policy},
			},
		},
		baselinePlan:  baselinePlan,
		candidatePlan: candidatePlan,
	}

	// Execute each side under its own plan and executor, so the two sides really do have
	// separate executions to be resolved to.
	runSide(t, setup, baselinePlan, setup.request.Baseline, inputs, renamedKey)
	runSide(t, setup, candidatePlan, setup.request.Candidate, inputs, renamedKey)
	return setup
}

func compilePlan(t *testing.T, request semantic.CompileRequest) semantic.Plan {
	t.Helper()
	compilation, err := semantic.Compile(request)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	plan, ok := compilation.Plan()
	if !ok {
		t.Fatal("the fixture did not compile")
	}
	return plan
}

// compileFor recompiles whichever of the two plans is asked for, so a record can carry the
// compilation its plan came from.
func compileFor(
	t *testing.T, plan semantic.Plan, inputs teamhos.Inputs, renamedKey semantic.CheckpointKey,
) semantic.Compilation {
	t.Helper()
	request := inputs.Compilation
	if planDeclaresCheckpoint(plan, renamedKey) {
		checkpoints := append([]semantic.CheckpointDeclaration(nil), request.Rules.Checkpoints...)
		for i := range checkpoints {
			if checkpoints[i].Key == "team_hos_aggregated.v1" {
				checkpoints[i].Key = renamedKey
			}
		}
		request.Rules.Checkpoints = checkpoints
	}
	compilation, err := semantic.Compile(request)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return compilation
}

func planDeclaresCheckpoint(plan semantic.Plan, key semantic.CheckpointKey) bool {
	for _, checkpoint := range plan.Checkpoints() {
		if checkpoint.Key == key {
			return true
		}
	}
	return false
}

// runSide executes every corpus case under one plan and one executor, storing the results
// the way the worker does.
func runSide(
	t *testing.T, fixture asymmetricSetup, plan semantic.Plan, side ComparisonSide,
	inputs teamhos.Inputs, renamedKey semantic.CheckpointKey,
) {
	t.Helper()
	compilation := compileFor(t, plan, inputs, renamedKey)
	for index := 0; index < fixture.corpus.Len(); index++ {
		state, _ := fixture.corpus.Case(index)
		binding, err := semantic.BindRun(semantic.RunBindingRequest{
			Plan: plan, InitialState: state, World: inputs.World,
			ExecutorIdentity: side.ExecutorIdentity, Policy: side.Policy,
		})
		if err != nil {
			t.Fatalf("BindRun: %v", err)
		}
		request := ports.ExecutionRequest{
			TenantID: "acme", ExecutionID: binding.ExecutionID(),
			RunID: binding.SemanticRunID(), PlanID: plan.ID(),
			Input: ports.ExecutionInput{
				InitialState: state, World: inputs.World,
				ExecutorIdentity: side.ExecutorIdentity, Policy: side.Policy,
			},
		}
		if _, err := fixture.store.Enqueue(t.Context(), request); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
		result, err := Run(t.Context(), Request{
			Compilation: compilation.Input().Request(), InitialState: state, World: inputs.World,
			ExecutorIdentity: side.ExecutorIdentity, Policy: side.Policy,
		}, nil)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		projected, err := Project(request, result)
		if err != nil {
			t.Fatalf("Project: %v", err)
		}
		leased, _, err := fixture.store.Claim(t.Context(), leaseForTest)
		if err != nil {
			t.Fatalf("Claim: %v", err)
		}
		if err := fixture.store.Complete(t.Context(), leased.AttemptID, projected); err != nil {
			t.Fatalf("Complete: %v", err)
		}
	}
}

// assertSideRanUnder requires each of a side's artifacts to be one this side's plan and
// executor settings actually produced, by re-deriving the execution and requiring the
// stored result to hold that artifact.
func assertSideRanUnder(
	t *testing.T, fixture asymmetricSetup, cases []promotion.ComparedCase,
	plan semantic.Plan, side ComparisonSide,
) {
	t.Helper()
	inputs, err := teamhosInputs()
	if err != nil {
		t.Fatalf("teamhos inputs: %v", err)
	}
	for i, compared := range cases {
		state, ok := fixture.corpus.Case(i)
		if !ok {
			t.Fatalf("the corpus has no case %d", i)
		}
		binding, err := semantic.BindRun(semantic.RunBindingRequest{
			Plan: plan, InitialState: state, World: inputs.World,
			ExecutorIdentity: side.ExecutorIdentity, Policy: side.Policy,
		})
		if err != nil {
			t.Fatalf("BindRun: %v", err)
		}
		stored, found, err := fixture.store.Get(t.Context(), "acme", binding.ExecutionID())
		if err != nil || !found {
			t.Fatalf("case %d has no execution under this side's settings: found=%t err=%v",
				i, found, err)
		}
		held := false
		for _, checkpoint := range stored.Result.Checkpoints {
			if checkpoint.CheckpointArtifactID == compared.Checkpoint.ID() {
				held = true
			}
		}
		if !held {
			t.Fatalf("case %d's artifact did not come from this side's execution", i)
		}
	}
}

func checkpointIdentityIn(t *testing.T, plan semantic.Plan, key semantic.CheckpointKey) semantic.CheckpointID {
	t.Helper()
	inputs, err := teamhosInputs()
	if err != nil {
		t.Fatalf("teamhos inputs: %v", err)
	}
	compilation := compileFor(t, plan, inputs, key)
	result, err := Run(t.Context(), Request{
		Compilation: compilation.Input().Request(), InitialState: inputs.InitialState,
		World: inputs.World, ExecutorIdentity: inputs.ExecutorIdentity, Policy: inputs.Policy,
	}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, sealed := range result.Checkpoints() {
		if sealed.Checkpoint().Key == key {
			return sealed.CheckpointID()
		}
	}
	t.Fatalf("no sealed checkpoint for %s", key)
	return ""
}
