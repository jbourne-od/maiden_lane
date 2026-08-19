package app

import (
	"context"
	"errors"
	"testing"

	"github.com/optimaldynamics/maiden-lane/internal/adapters/memory"
	"github.com/optimaldynamics/maiden-lane/internal/fixtures/teamhos"
	"github.com/optimaldynamics/maiden-lane/internal/ports"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

// THE PROPERTY THIS SLICE RESTS ON: a comparison cannot be read out of a store. Its
// policy is derived from two compiled plans and the kernel's encoders are one way, so
// what a store returns is a description, and the only thing that makes the recovered
// comparison authentic is that the kernel re-derives the same identity from the stored
// components.
//
// Without that, a stored comparison would be a projection carrying authorization weight:
// clause 6 consumes the comparison, so a row edited to name a different corpus or a
// different profile would move the question the gate believes it is answering, and every
// artifact in the answer would still be individually valid.
func TestRehydrateComparisonRecoversTheStoredQuestion(t *testing.T) {
	fixture := newComparisonFixture(t)
	stores := fixture.stores(t)

	recovered, found, err := RehydrateComparison(
		t.Context(), stores, "acme", fixture.comparison.ID())
	if err != nil || !found {
		t.Fatalf("RehydrateComparison: found=%t err=%v", found, err)
	}

	if recovered.ID() != fixture.comparison.ID() {
		t.Fatalf("comparison ID = %s, want %s", recovered.ID(), fixture.comparison.ID())
	}
	// Identity equality is the guarantee, but a comparison that agreed on its name while
	// disagreeing on a component would mean the identity does not commit to that
	// component -- which is the kernel's problem, not this function's, and is exactly
	// what a golden vector in the kernel pins. Checked here so the two cannot drift
	// apart silently.
	for _, field := range []struct {
		name      string
		got, want string
	}{
		{"baseline", string(recovered.Baseline()), string(fixture.comparison.Baseline())},
		{"candidate", string(recovered.Candidate()), string(fixture.comparison.Candidate())},
		{"profile", string(recovered.Profile()), string(fixture.comparison.Profile())},
		{"world", string(recovered.World()), string(fixture.comparison.World())},
		{"corpus", string(recovered.Corpus()), string(fixture.comparison.Corpus())},
		{"policy", string(recovered.Policy().ID()), string(fixture.comparison.Policy().ID())},
	} {
		if field.got != field.want {
			t.Errorf("%s = %q, want %q", field.name, field.got, field.want)
		}
	}
}

// A comparison nobody created is an ordinary answer to a lookup, not a failure.
func TestRehydrateComparisonReportsAbsence(t *testing.T) {
	fixture := newComparisonFixture(t)
	stores := fixture.stores(t)

	recovered, found, err := RehydrateComparison(t.Context(), stores, "acme", "sha256:"+
		"0000000000000000000000000000000000000000000000000000000000000000")
	if err != nil {
		t.Fatalf("RehydrateComparison: %v", err)
	}
	if found {
		t.Fatalf("a comparison nobody stored was recovered: %s", recovered.ID())
	}

	// And another tenant's comparison is absent rather than recoverable, which the store
	// enforces and this states at the layer a handler actually calls.
	if _, found, err := RehydrateComparison(
		t.Context(), stores, "other", fixture.comparison.ID()); err != nil || found {
		t.Fatalf("another tenant's comparison was recovered: found=%t err=%v", found, err)
	}
}

// Production break caught: every component of a stored comparison is under the identity,
// so editing any one of them must stop the row rebuilding into the comparison it is
// filed under. A rehydrator that returned the record's components without re-deriving
// would pass every other test in this file.
func TestRehydrateComparisonRefusesAnEditedRow(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*ports.ComparisonRecord)
		want IntegrityCode
	}{
		{
			name: "profile",
			// The one §14.2 rules out by name: comparing an optimizer-ready baseline to
			// a merely CM-ready candidate cannot support promotion, so the profile the
			// comparison pins is not a label.
			edit: func(r *ports.ComparisonRecord) { r.Profile = "sha256:" + zeroDigest },
			want: IntegrityComparisonDiverged,
		},
		{
			name: "world",
			edit: func(r *ports.ComparisonRecord) { r.World = "sha256:" + zeroDigest },
			want: IntegrityComparisonDiverged,
		},
		{
			name: "corpus",
			// A different corpus is a different set of cases, so the comparison would be
			// answered over inputs it does not name.
			edit: func(r *ports.ComparisonRecord) { r.Corpus = "sha256:" + zeroDigest },
			want: IntegrityComparisonDiverged,
		},
		{
			name: "policy identity",
			// The policy is rebuilt from the plans and correspondences, so this column
			// disagreeing with them means the row carries two descriptions of one thing.
			edit: func(r *ports.ComparisonRecord) { r.PolicyID = "sha256:" + zeroDigest },
			want: IntegrityComparisonDiverged,
		},
		{
			name: "baseline checkpoint no longer the one the policy corresponds",
			// NewComparison refuses a comparison whose sides the policy does not declare
			// to correspond, so this cannot even be built rather than being built and
			// then found to have the wrong name.
			edit: func(r *ports.ComparisonRecord) {
				r.Baseline = r.Correspondences[0].Baseline
			},
			want: IntegrityComparisonDiverged,
		},
		{
			name: "a correspondence naming an undeclared checkpoint",
			edit: func(r *ports.ComparisonRecord) {
				r.Correspondences[0].Candidate = "sha256:" + zeroDigest
			},
			want: IntegrityComparisonCheckpointAbsent,
		},
		{
			name: "a correspondence mapping one baseline to two candidates",
			// The ambiguity §14.2 forbids. The kernel refuses to build such a policy, so
			// rehydration reports divergence rather than resolving it by first match.
			edit: func(r *ports.ComparisonRecord) {
				r.Correspondences[1].Baseline = r.Correspondences[0].Baseline
			},
			want: IntegrityComparisonDiverged,
		},
		{
			name: "a dropped correspondence",
			// A smaller mapping is a perfectly valid policy, which is what makes this
			// dangerous: nothing about the rebuilt policy looks wrong.
			edit: func(r *ports.ComparisonRecord) {
				r.Correspondences = r.Correspondences[:1]
			},
			want: IntegrityComparisonDiverged,
		},
		{
			name: "the two plans swapped",
			// Both plans exist and both declare the checkpoints named, so every part is
			// individually valid and only the identity catches it.
			edit: func(r *ports.ComparisonRecord) {
				r.BaselinePlan, r.CandidatePlan = r.CandidatePlan, r.BaselinePlan
			},
			want: IntegrityComparisonCheckpointAbsent,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newComparisonFixture(t)
			edited := ports.ProjectComparison("acme", fixture.comparison).Clone()
			test.edit(&edited)

			_, _, err := RehydrateComparison(t.Context(), ComparisonRehydrationStores{
				Plans:       fixture.plans(t),
				Comparisons: editedComparisons{record: edited},
			}, "acme", fixture.comparison.ID())

			var integrity IntegrityError
			if !errors.As(err, &integrity) {
				t.Fatalf("an edited row rehydrated, or failed for another reason: %v", err)
			}
			if integrity.Code != test.want {
				t.Fatalf("integrity code = %s, want %s", integrity.Code, test.want)
			}
		})
	}
}

// A comparison whose plan is gone cannot be recovered at all: the policy is derived from
// the compiled plans, and a PlanID cannot be turned back into one.
func TestRehydrateComparisonReportsAnAbsentPlan(t *testing.T) {
	for _, test := range []struct {
		name  string
		store func(*testing.T, *rehydrationComparisonFixture) ports.PlanStore
	}{
		{
			name: "no plans at all",
			store: func(*testing.T, *rehydrationComparisonFixture) ports.PlanStore {
				return memory.NewStore()
			},
		},
		{
			name: "only the baseline plan",
			// The asymmetric case: half the comparison is recoverable, which is the
			// shape most likely to be handled by returning what could be built.
			store: func(t *testing.T, fixture *rehydrationComparisonFixture) ports.PlanStore {
				plans := memory.NewStore()
				mustPutPlan(t, plans, fixture.baselineRecord)
				return plans
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newComparisonFixture(t)
			comparisons := memory.NewStore()
			if err := comparisons.PutComparison(
				t.Context(), "acme", fixture.comparison); err != nil {
				t.Fatalf("PutComparison: %v", err)
			}

			_, _, err := RehydrateComparison(t.Context(), ComparisonRehydrationStores{
				Plans: test.store(t, fixture), Comparisons: comparisons,
			}, "acme", fixture.comparison.ID())

			var integrity IntegrityError
			if !errors.As(err, &integrity) {
				t.Fatalf("rehydration survived an absent plan: %v", err)
			}
			if integrity.Code != IntegrityComparisonPlanAbsent {
				t.Fatalf("integrity code = %s, want %s",
					integrity.Code, IntegrityComparisonPlanAbsent)
			}
		})
	}
}

// An integrity error names the field that diverged and carries no value from either
// side, because it is produced while handling content that is already suspect.
func TestRehydrateComparisonIntegrityErrorsCarryNoStoredContent(t *testing.T) {
	fixture := newComparisonFixture(t)
	edited := ports.ProjectComparison("acme", fixture.comparison).Clone()
	poisoned := semantic.CorpusID("sha256:" + zeroDigest)
	edited.Corpus = poisoned

	_, _, err := RehydrateComparison(t.Context(), ComparisonRehydrationStores{
		Plans:       fixture.plans(t),
		Comparisons: editedComparisons{record: edited},
	}, "acme", fixture.comparison.ID())
	if err == nil {
		t.Fatal("an edited row rehydrated")
	}
	if contains(err.Error(), string(poisoned)) {
		t.Fatalf("the error repeated stored content: %v", err)
	}
}

const zeroDigest = "0000000000000000000000000000000000000000000000000000000000000000"

// editedComparisons returns one record regardless of what is asked for, so a test can
// present a row no store would accept through PutComparison.
//
// It exists because the write path takes an authenticated kernel value: a corrupted
// comparison cannot be stored, which is the point, and is also why the rehydrator's
// refusals cannot be exercised through a real store.
type editedComparisons struct {
	record ports.ComparisonRecord
}

func (e editedComparisons) PutComparison(
	context.Context, ports.TenantID, semantic.Comparison,
) error {
	return errors.New("editedComparisons does not store")
}

func (e editedComparisons) GetComparison(
	_ context.Context, _ ports.TenantID, _ semantic.ComparisonID,
) (ports.ComparisonRecord, bool, error) {
	return e.record.Clone(), true, nil
}

// rehydrationComparisonFixture is one asymmetric comparison and the two plans it names.
//
// The asymmetry is load-bearing, and the lesson is one this programme has already paid
// for twice: a fixture comparing a plan to itself cannot distinguish a rehydrator that
// uses the candidate's plan for both sides, and that is the bug most likely to be
// written. The two plans here differ only by a renamed checkpoint, so both remain
// runnable over one corpus while their identities differ.
type rehydrationComparisonFixture struct {
	comparison      semantic.Comparison
	baselineRecord  ports.PlanRecord
	candidateRecord ports.PlanRecord
}

const renamedCandidateCheckpoint semantic.CheckpointKey = "team_hos_aggregated.v2"

func newComparisonFixture(t *testing.T) *rehydrationComparisonFixture {
	t.Helper()

	baseline, baselineRecord := comparisonPlan(t, teamhos.CheckpointTeamHOSAggregated)
	candidate, candidateRecord := comparisonPlan(t, renamedCandidateCheckpoint)
	if baseline.ID() == candidate.ID() {
		t.Fatal("the fixture built one plan twice, so neither side is observable")
	}

	policy, err := semantic.NewComparisonPolicy(baseline, candidate,
		[]semantic.CheckpointPair{
			{Baseline: teamhos.CheckpointTeamFormed, Candidate: teamhos.CheckpointTeamFormed},
			{Baseline: teamhos.CheckpointTeamHOSAggregated, Candidate: renamedCandidateCheckpoint},
		})
	if err != nil {
		t.Fatalf("NewComparisonPolicy: %v", err)
	}

	baselineCheckpoint, declared := baseline.CheckpointID(teamhos.CheckpointTeamHOSAggregated)
	if !declared {
		t.Fatal("the baseline plan does not declare the compared checkpoint")
	}
	candidateCheckpoint, declared := candidate.CheckpointID(renamedCandidateCheckpoint)
	if !declared {
		t.Fatal("the candidate plan does not declare the compared checkpoint")
	}

	world, err := semantic.NewWorld(nil)
	if err != nil {
		t.Fatalf("NewWorld: %v", err)
	}
	profiles := baselineRecord.Compilation.Profiles()
	if len(profiles) == 0 {
		t.Fatal("the ratified fixture compiled no profiles")
	}

	comparison, err := semantic.NewComparison(semantic.ComparisonRequest{
		Baseline:  baselineCheckpoint,
		Candidate: candidateCheckpoint,
		Profile:   profiles[0].ID(),
		World:     world.ID(),
		Corpus:    comparisonCorpus(t).ID(),
		Policy:    policy,
	})
	if err != nil {
		t.Fatalf("NewComparison: %v", err)
	}
	return &rehydrationComparisonFixture{
		comparison:      comparison,
		baselineRecord:  baselineRecord,
		candidateRecord: candidateRecord,
	}
}

// plans returns a store holding both of the fixture's plans.
func (f *rehydrationComparisonFixture) plans(t *testing.T) ports.PlanStore {
	t.Helper()
	plans := memory.NewStore()
	mustPutPlan(t, plans, f.baselineRecord)
	mustPutPlan(t, plans, f.candidateRecord)
	return plans
}

// stores returns both stores with the fixture's comparison already written.
func (f *rehydrationComparisonFixture) stores(t *testing.T) ComparisonRehydrationStores {
	t.Helper()
	comparisons := memory.NewStore()
	if err := comparisons.PutComparison(t.Context(), "acme", f.comparison); err != nil {
		t.Fatalf("PutComparison: %v", err)
	}
	return ComparisonRehydrationStores{Plans: f.plans(t), Comparisons: comparisons}
}

func mustPutPlan(t *testing.T, plans ports.PlanStore, record ports.PlanRecord) {
	t.Helper()
	if err := plans.PutPlan(t.Context(), record); err != nil {
		t.Fatalf("PutPlan: %v", err)
	}
}

// comparisonPlan compiles the ratified team-HOS ruleset with its final checkpoint under
// the given key, and returns it with a storable record.
func comparisonPlan(t *testing.T, finalKey semantic.CheckpointKey) (semantic.Plan, ports.PlanRecord) {
	t.Helper()

	inputs, err := teamhos.New(teamhos.Passing)
	if err != nil {
		t.Fatalf("teamhos.New: %v", err)
	}
	request := inputs.Compilation
	checkpoints := make([]semantic.CheckpointDeclaration, len(request.Rules.Checkpoints))
	copy(checkpoints, request.Rules.Checkpoints)
	renamed := false
	for i := range checkpoints {
		if checkpoints[i].Key == teamhos.CheckpointTeamHOSAggregated {
			checkpoints[i].Key = finalKey
			renamed = true
		}
	}
	if !renamed {
		t.Fatal("the ratified fixture no longer declares the checkpoint this fixture renames")
	}
	request.Rules.Checkpoints = checkpoints

	compilation, err := semantic.Compile(request)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	plan, ok := compilation.Plan()
	if !ok {
		failure, _ := compilation.Failure()
		t.Fatalf("the fixture did not compile: %v", failure.Diagnostics())
	}
	return plan, ports.PlanRecord{
		TenantID:    "acme",
		PlanID:      plan.ID(),
		Input:       compilation.Input(),
		Schema:      inputs.InitialState.Schema(),
		Compilation: compilation,
	}
}

// comparisonCorpus builds a corpus over the ratified fixture's own initial state, so the
// comparison names a set of cases that could actually be replayed under both plans.
func comparisonCorpus(t *testing.T) semantic.Corpus {
	t.Helper()
	inputs, err := teamhos.New(teamhos.Passing)
	if err != nil {
		t.Fatalf("teamhos.New: %v", err)
	}
	corpus, err := semantic.NewCorpus([]semantic.State{inputs.InitialState})
	if err != nil {
		t.Fatalf("NewCorpus: %v", err)
	}
	return corpus
}
