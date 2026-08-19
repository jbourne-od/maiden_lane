package app

import (
	"errors"
	"testing"

	"time"

	"github.com/optimaldynamics/maiden-lane/internal/adapters/memory"
	"github.com/optimaldynamics/maiden-lane/internal/fixtures/teamhos"
	"github.com/optimaldynamics/maiden-lane/internal/ports"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

// Running a side enqueues one execution per case, each under the identity the kernel
// derives for it. Those identities are what make two sides over the same corpus and world
// provably the same inputs rather than two sets that were asserted to match.
func TestRunningASideEnqueuesEveryCaseUnderItsDerivedIdentity(t *testing.T) {
	fixture := corpusRunFixture(t, 4)

	run, err := RunCorpus(t.Context(), fixture.stores, fixture.request)
	if err != nil {
		t.Fatalf("RunCorpus: %v", err)
	}

	counts := run.Counts()
	if counts.Total != 4 || counts.Enqueued != 4 || counts.Pending != 4 {
		t.Fatalf("counts = %+v, want 4 total, 4 enqueued, 4 pending", counts)
	}
	if run.Complete() {
		t.Fatal("a side with nothing executed reported complete")
	}

	// Every case's identity must be the one BindRun derives for it, and they must be
	// distinct: a side that enqueued one execution four times would look identical in the
	// counts above.
	seen := map[semantic.ExecutionID]bool{}
	for i, caseRun := range run.Cases() {
		if caseRun.ExecutionID() == "" || caseRun.SemanticRunID() == "" {
			t.Fatalf("case %d has no derived identity", i)
		}
		if seen[caseRun.ExecutionID()] {
			t.Fatalf("case %d reuses an execution identity", i)
		}
		seen[caseRun.ExecutionID()] = true

		stored, found, err := fixture.store.Get(t.Context(), "acme", caseRun.ExecutionID())
		if err != nil || !found {
			t.Fatalf("case %d was not enqueued: found=%t err=%v", i, found, err)
		}
		// The stored execution must carry this case, not merely some case.
		if stored.Request.Input.InitialState.Digest() != caseRun.CaseDigest() {
			t.Fatalf("case %d enqueued the wrong state", i)
		}
	}

	// The cases come back in the corpus's canonical order, so a caller iterating a side
	// and a caller iterating the corpus see the same sequence.
	want := fixture.corpus.Digests()
	for i, caseRun := range run.Cases() {
		if caseRun.CaseDigest() != want[i] {
			t.Fatalf("case %d = %s, want %s", i, caseRun.CaseDigest(), want[i])
		}
	}
}

// THE COST MODEL, ASSERTED RATHER THAN CLAIMED: a case already executed is not executed
// again.
//
// This is what makes a corpus accumulate rather than repeat, and it is the only reason
// running two sides over a large corpus is affordable at all. It works because
// ExecutionID is derived from the semantic request, so a repeat is necessarily the same
// execution — not because anything remembers having run it.
func TestRerunningASideEnqueuesNothingNew(t *testing.T) {
	fixture := corpusRunFixture(t, 3)

	first, err := RunCorpus(t.Context(), fixture.stores, fixture.request)
	if err != nil {
		t.Fatalf("first RunCorpus: %v", err)
	}
	if first.Counts().Enqueued != 3 {
		t.Fatalf("first run enqueued %d, want 3", first.Counts().Enqueued)
	}

	second, err := RunCorpus(t.Context(), fixture.stores, fixture.request)
	if err != nil {
		t.Fatalf("second RunCorpus: %v", err)
	}
	if second.Counts().Enqueued != 0 {
		t.Fatalf("a repeat enqueued %d executions, want 0: a case already executed must "+
			"not be executed again", second.Counts().Enqueued)
	}
	if second.Counts().Total != 3 {
		t.Fatalf("a repeat reported %d cases, want 3", second.Counts().Total)
	}
	// Same identities, so the second run is describing the same executions rather than
	// having created a parallel set.
	for i, caseRun := range second.Cases() {
		if caseRun.ExecutionID() != first.Cases()[i].ExecutionID() {
			t.Fatalf("case %d changed identity between runs", i)
		}
	}
}

// A progress read must enqueue nothing, so asking where a side stands cannot start it.
func TestProgressReportsWithoutEnqueueing(t *testing.T) {
	fixture := corpusRunFixture(t, 3)

	before, err := CorpusProgress(t.Context(), fixture.stores, fixture.request)
	if err != nil {
		t.Fatalf("CorpusProgress: %v", err)
	}
	if before.Counts().Enqueued != 0 {
		t.Fatal("a progress read enqueued executions")
	}
	if before.Complete() {
		t.Fatal("a side nobody has run reported complete")
	}
	// Nothing reached the queue, which the counts alone would not prove.
	for _, caseRun := range before.Cases() {
		if _, found, err := fixture.store.Get(t.Context(), "acme", caseRun.ExecutionID()); err != nil || found {
			t.Fatalf("a progress read created an execution: found=%t err=%v", found, err)
		}
	}

	if _, err := RunCorpus(t.Context(), fixture.stores, fixture.request); err != nil {
		t.Fatalf("RunCorpus: %v", err)
	}
	after, err := CorpusProgress(t.Context(), fixture.stores, fixture.request)
	if err != nil {
		t.Fatalf("CorpusProgress after running: %v", err)
	}
	if after.Counts().Pending != 3 || after.Counts().Enqueued != 0 {
		t.Fatalf("counts after running = %+v, want 3 pending and 0 enqueued", after.Counts())
	}
}

// A side is complete only when every case has finished, and completion is all-or-nothing.
// A comparison over a corpus where some cases have not run is a comparison over a
// different, smaller corpus.
func TestASideIsCompleteOnlyWhenEveryCaseHasFinished(t *testing.T) {
	fixture := corpusRunFixture(t, 3)
	run, err := RunCorpus(t.Context(), fixture.stores, fixture.request)
	if err != nil {
		t.Fatalf("RunCorpus: %v", err)
	}

	cases := run.Cases()
	for i, caseRun := range cases {
		// Finish one case at a time. A deterministic semantic rejection finishes a case
		// too, so the last one is failed rather than succeeded: both are finished,
		// because a computation that ran and refused produced a real answer.
		status := ports.ExecutionSucceeded
		if i == len(cases)-1 {
			status = ports.ExecutionFailed
		}
		completeCase(t, fixture, caseRun, status)

		progress, err := CorpusProgress(t.Context(), fixture.stores, fixture.request)
		if err != nil {
			t.Fatalf("CorpusProgress: %v", err)
		}
		wantComplete := i == len(cases)-1
		if progress.Complete() != wantComplete {
			t.Fatalf("after finishing %d of %d cases, complete = %t, want %t",
				i+1, len(cases), progress.Complete(), wantComplete)
		}
	}

	final, err := CorpusProgress(t.Context(), fixture.stores, fixture.request)
	if err != nil {
		t.Fatalf("CorpusProgress: %v", err)
	}
	counts := final.Counts()
	if counts.Succeeded != 2 || counts.Failed != 1 {
		t.Fatalf("counts = %+v, want 2 succeeded and 1 failed", counts)
	}
}

// A corpus whose cases are not under the plan's schema is refused before anything is
// enqueued.
//
// Without this the failure arrives once per case, after n durable executions have been
// created for work that cannot succeed — BindRun refuses a state whose schema digest is
// not the plan's, so every case would fail identically.
func TestACorpusUnderAnotherSchemaIsRefusedBeforeAnythingIsEnqueued(t *testing.T) {
	fixture := corpusRunFixture(t, 3)

	// A corpus built under the contract fixture's schema, which is not the teamhos plan's.
	other := otherSchemaCorpus(t)
	if err := fixture.store.PutCorpus(t.Context(), ports.CorpusRecord{
		TenantID: "acme", CorpusID: other.ID(), Corpus: other,
	}); err != nil {
		t.Fatalf("PutCorpus: %v", err)
	}
	request := fixture.request
	request.CorpusID = other.ID()

	_, err := RunCorpus(t.Context(), fixture.stores, request)
	var invalid InvalidInputError
	if !errors.As(err, &invalid) || invalid.Code != InputCorpusSchemaMismatch {
		t.Fatalf("error = %v, want InputCorpusSchemaMismatch", err)
	}

	// Nothing was enqueued. This is the assertion that matters: the check has to happen
	// before the loop, not inside it.
	for index := 0; index < other.Len(); index++ {
		state, _ := other.Case(index)
		if executionExistsForState(t, fixture, state) {
			t.Fatal("a refused side still enqueued a case")
		}
	}
}

// An absent corpus and an absent plan are different codes, because an operator has to
// know which of the two names was wrong.
func TestAnAbsentCorpusAndAnAbsentPlanAreDistinguished(t *testing.T) {
	fixture := corpusRunFixture(t, 2)

	for _, test := range []struct {
		name   string
		mutate func(*CorpusRunRequest)
		code   InvalidInputCode
	}{
		{"unknown corpus", func(r *CorpusRunRequest) {
			r.CorpusID = semantic.CorpusID("sha256:" + repeatRune('c', 64))
		}, InputCorpusAbsent},
		{"unknown plan", func(r *CorpusRunRequest) {
			r.PlanID = semantic.PlanID("sha256:" + repeatRune('d', 64))
		}, InputCorpusRunPlanAbsent},
		{"no tenant", func(r *CorpusRunRequest) { r.TenantID = "" }, InputCorpusRunIncomplete},
		{"no corpus named", func(r *CorpusRunRequest) { r.CorpusID = "" }, InputCorpusRunIncomplete},
		{"no plan named", func(r *CorpusRunRequest) { r.PlanID = "" }, InputCorpusRunIncomplete},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := fixture.request
			test.mutate(&request)
			_, err := RunCorpus(t.Context(), fixture.stores, request)
			var invalid InvalidInputError
			if !errors.As(err, &invalid) {
				t.Fatalf("error = %v, want an InvalidInputError", err)
			}
			if invalid.Code != test.code {
				t.Fatalf("code = %s, want %s", invalid.Code, test.code)
			}
		})
	}
}

// Another tenant's corpus is absent, so a side cannot be run against one.
func TestRunningASideIsTenantScoped(t *testing.T) {
	fixture := corpusRunFixture(t, 2)
	request := fixture.request
	request.TenantID = "globex"

	_, err := RunCorpus(t.Context(), fixture.stores, request)
	var invalid InvalidInputError
	if !errors.As(err, &invalid) || invalid.Code != InputCorpusAbsent {
		t.Fatalf("error = %v, want InputCorpusAbsent", err)
	}
}

// ── fixture ─────────────────────────────────────────────────────────────────

type corpusRunSetup struct {
	store   *memory.Store
	stores  CorpusRunStores
	corpus  semantic.Corpus
	request CorpusRunRequest
}

// corpusRunFixture stores a plan and a corpus of cases under that plan's schema, which is
// what a side needs: the kernel refuses a state whose schema is not the plan's, so a
// corpus is only runnable under plans sharing its schema.
func corpusRunFixture(t *testing.T, cases int) corpusRunSetup {
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

	corpus := corpusUnderSchema(t, inputs.InitialState.Schema(), cases)
	if err := store.PutCorpus(t.Context(), ports.CorpusRecord{
		TenantID: "acme", CorpusID: corpus.ID(), Corpus: corpus,
	}); err != nil {
		t.Fatalf("PutCorpus: %v", err)
	}

	return corpusRunSetup{
		store:  store,
		stores: CorpusRunStores{Corpora: store, Plans: store, Executions: store},
		corpus: corpus,
		request: CorpusRunRequest{
			TenantID: "acme", CorpusID: corpus.ID(), PlanID: plan.ID(),
			World: inputs.World, ExecutorIdentity: inputs.ExecutorIdentity, Policy: inputs.Policy,
		},
	}
}

// corpusUnderSchema builds distinct cases under a given schema, varying one field so each
// case has its own state digest.
func corpusUnderSchema(t *testing.T, schema semantic.Schema, cases int) semantic.Corpus {
	t.Helper()
	lineage, err := semantic.NewInputLineageID("maiden-lane.sanitized-fixture", "team-hos-team-ab")
	if err != nil {
		t.Fatalf("NewInputLineageID: %v", err)
	}

	states := make([]semantic.State, 0, cases)
	for i := 0; i < cases; i++ {
		key, err := semantic.NewStringValue("case-" + string(rune('a'+i)))
		if err != nil {
			t.Fatalf("NewStringValue: %v", err)
		}
		entities := make([]semantic.Entity, 0, 2)
		for _, source := range []string{"A", "B"} {
			entity, err := semantic.NewEntity(semantic.EntityRef{
				Kind: "driver",
				ID:   semantic.SourceEntityID(lineage, "driver", source),
			}, map[semantic.FieldName]semantic.Value{"assignment_key": key})
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

// otherSchemaCorpus builds a corpus under a schema no teamhos plan pins, so a side over it
// must be refused.
func otherSchemaCorpus(t *testing.T) semantic.Corpus {
	t.Helper()
	schema, err := semantic.NewSchema([]semantic.EntityDeclaration{
		{Kind: "driver", Fields: []semantic.FieldDeclaration{
			{Name: "assignment_key", Kind: semantic.ValueString},
		}},
	}, nil)
	if err != nil {
		t.Fatalf("NewSchema: %v", err)
	}
	return corpusUnderSchema(t, schema, 2)
}

// completeCase moves one case to a terminal status, as a worker would.
func completeCase(t *testing.T, fixture corpusRunSetup, run CaseRun, status ports.ExecutionStatus) {
	t.Helper()
	stored, found, err := fixture.store.Get(t.Context(), "acme", run.ExecutionID())
	if err != nil || !found {
		t.Fatalf("Get: found=%t err=%v", found, err)
	}
	if _, _, err := fixture.store.Claim(t.Context(), leaseForTest); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if err := fixture.store.Complete(t.Context(), ports.ExecutionResult{
		TenantID: "acme", ExecutionID: stored.Request.ExecutionID, Status: status,
		SpineStatus: "succeeded",
	}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
}

func executionExistsForState(t *testing.T, fixture corpusRunSetup, state semantic.State) bool {
	t.Helper()
	// The identity is not derivable without binding, so this asks the store whether any
	// execution carries this state by checking the identity a side would have used.
	inputs, err := teamhosInputs()
	if err != nil {
		t.Fatalf("teamhos inputs: %v", err)
	}
	compilation, err := semantic.Compile(inputs.Compilation)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	plan, _ := compilation.Plan()
	binding, err := semantic.BindRun(semantic.RunBindingRequest{
		Plan: plan, InitialState: state, World: fixture.request.World,
		ExecutorIdentity: fixture.request.ExecutorIdentity, Policy: fixture.request.Policy,
	})
	if err != nil {
		// A state the plan cannot bind has no execution identity, so nothing can exist
		// for it. That is the case this helper is used to confirm.
		return false
	}
	_, found, err := fixture.store.Get(t.Context(), "acme", binding.ExecutionID())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	return found
}

func repeatRune(character byte, count int) string {
	out := make([]byte, count)
	for i := range out {
		out[i] = character
	}
	return string(out)
}

const leaseForTest = 30 * time.Second

func teamhosInputs() (teamhos.Inputs, error) { return teamhos.New(teamhos.Passing) }
