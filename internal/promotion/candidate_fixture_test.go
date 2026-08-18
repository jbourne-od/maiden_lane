package promotion

import (
	"slices"
	"strings"
	"testing"

	"github.com/optimaldynamics/maiden-lane/internal/fixtures/teamhos"
	"github.com/optimaldynamics/maiden-lane/internal/ports"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

// sealedFixture is a real sealed checkpoint and its real assessments.
//
// The gate is tested against artifacts the kernel actually produced rather than
// against hand-built values, and it has to be: a semantic.CheckpointArtifact
// cannot be constructed outside the kernel, which is precisely the property the
// clauses here rely on. Hand-built stand-ins would test a different type than the
// one production passes.
type sealedFixture struct {
	artifact    semantic.CheckpointArtifact
	assessments []semantic.Assessment
}

// sealTeamHOS runs the kernel over the passing golden fixture and returns every
// sealed checkpoint in plan order with the assessments taken against it.
func sealTeamHOS(t *testing.T) []sealedFixture {
	t.Helper()
	sealed, rejected := sealTeamHOSVariant(t, teamhos.Passing)
	if rejected {
		t.Fatal("the passing golden fixture was rejected")
	}
	return sealed
}

// sealTeamHOSVariant runs the kernel over one golden variant and returns every
// checkpoint that sealed before the run ended, plus whether the run ended in a
// deterministic semantic rejection rather than completing.
//
// It drives the kernel directly rather than calling internal/app, which sits above
// this package: a test import in the other direction would become a build cycle
// the moment the application wires the gate in.
func sealTeamHOSVariant(t *testing.T, variant teamhos.Variant) ([]sealedFixture, bool) {
	t.Helper()

	inputs, err := teamhos.New(variant)
	if err != nil {
		t.Fatalf("teamhos.New: %v", err)
	}
	compilation, err := semantic.Compile(inputs.Compilation)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if failure, refused := compilation.Failure(); refused {
		t.Fatalf("fixture plan was refused: %v", failure)
	}
	plan, ok := compilation.Plan()
	if !ok {
		t.Fatal("compilation produced neither plan nor failure")
	}
	binding, err := semantic.BindRun(semantic.RunBindingRequest{
		Plan:             plan,
		InitialState:     inputs.InitialState,
		World:            inputs.World,
		ExecutorIdentity: inputs.ExecutorIdentity,
		Policy:           inputs.Policy,
	})
	if err != nil {
		t.Fatalf("bind run: %v", err)
	}

	state, journal := inputs.InitialState, semantic.NewJournal()
	sealed := make([]sealedFixture, 0, len(plan.Checkpoints()))
	known := make([]semantic.CheckpointArtifact, 0, len(plan.Checkpoints()))
	knownAssessments := make([]semantic.Assessment, 0)

	for _, transformation := range plan.Transformations() {
		rule := transformation.Declaration().ID
		outcome, err := semantic.ExecuteTransition(binding, rule, state, journal)
		if err != nil {
			t.Fatalf("execute %s: %v", rule, err)
		}
		if _, refused := outcome.Failure(); refused {
			// A deterministic rejection ends the run. Whatever sealed before it
			// stays sealed, which is the case the gate has to reason about.
			return sealed, true
		}
		state, journal = outcome.State(), outcome.Journal()

		for _, checkpoint := range plan.Checkpoints() {
			if checkpoint.After != rule {
				continue
			}
			sealOutcome, err := semantic.Seal(semantic.SealRequest{
				Binding:          binding,
				Checkpoint:       checkpoint.Key,
				State:            state,
				Journal:          journal,
				InvariantResults: outcome.InvariantResults(),
				KnownArtifacts:   slices.Clone(known),
			})
			if err != nil {
				t.Fatalf("seal %s: %v", checkpoint.Key, err)
			}
			if failure, refused := sealOutcome.Failure(); refused {
				t.Fatalf("seal %s refused: %v", checkpoint.Key, failure)
			}
			artifact, ok := sealOutcome.Artifact()
			if !ok {
				t.Fatalf("seal %s produced neither artifact nor failure", checkpoint.Key)
			}
			known = append(known, artifact)

			assessments := make([]semantic.Assessment, 0, len(compilation.Profiles()))
			for _, profile := range compilation.Profiles() {
				assessOutcome, err := semantic.Assess(semantic.AssessmentRequest{
					Checkpoint:       artifact,
					State:            state,
					Profile:          profile,
					KnownAssessments: slices.Clone(knownAssessments),
				})
				if err != nil {
					t.Fatalf("assess %s under %s: %v", checkpoint.Key, profile.Key(), err)
				}
				if failure, refused := assessOutcome.Failure(); refused {
					t.Fatalf("assess %s under %s refused: %v", checkpoint.Key, profile.Key(), failure)
				}
				assessment, ok := assessOutcome.Assessment()
				if !ok {
					t.Fatal("assessment outcome carries neither value nor failure")
				}
				assessments = append(assessments, assessment)
				knownAssessments = append(knownAssessments, assessment)
			}
			sealed = append(sealed, sealedFixture{artifact: artifact, assessments: assessments})
		}
	}

	if variant == teamhos.Passing && len(sealed) < 2 {
		t.Fatalf("fixture sealed %d checkpoints, want at least 2 so cross-checkpoint "+
			"evidence transplants are testable", len(sealed))
	}
	return sealed, false
}

// wholeCandidate is the fixture's first sealed checkpoint with the witness the
// artifact itself commits to and an assessment bound to it: everything the two
// implemented clauses need.
func wholeCandidate(t *testing.T) Candidate {
	t.Helper()
	sealed := sealTeamHOS(t)
	return Candidate{
		Checkpoint:               sealed[0].artifact,
		Assessment:               sealed[0].assessments[0],
		RetainedInvariantWitness: sealed[0].artifact.InvariantResultCanonicalBytes(),
	}
}

// samplePolicy is a complete policy at a version other than 1, so a decision that
// reported a hardcoded or defaulted version would be visible.
//
// The required profile is a literal rather than one of the fixture's compiled
// ProfileIDs because no clause compares it to anything yet: the gate only requires
// that a policy bind one. When the readiness clause lands this must become the
// fixture's real ProfileID, or that clause will be tested against a profile no
// assessment was ever taken under.
func samplePolicy() ports.TargetPolicy {
	return ports.TargetPolicy{
		TenantID:          "tenant-a",
		CustomerID:        "customer-a",
		Target:            "cm",
		Version:           7,
		RequiredProfileID: semantic.ProfileID("sha256:" + strings.Repeat("b", 64)),
	}
}

// clauseIndex collapses a decision to a lookup, since clause order is stable but
// assertions read better by name.
func clauseIndex(decision Decision) map[Clause]ClauseResult {
	byClause := make(map[Clause]ClauseResult, len(decision.Clauses()))
	for _, result := range decision.Clauses() {
		byClause[result.Clause()] = result
	}
	return byClause
}

// Each of these blanks exactly one field of an otherwise complete policy, so a
// guard that checked only some of them is visible per case rather than as one
// aggregate failure.
func withVersion(policy ports.TargetPolicy, version ports.PolicyVersion) ports.TargetPolicy {
	policy.Version = version
	return policy
}

func withoutTenant(policy ports.TargetPolicy) ports.TargetPolicy {
	policy.TenantID = ""
	return policy
}

func withoutCustomer(policy ports.TargetPolicy) ports.TargetPolicy {
	policy.CustomerID = ""
	return policy
}

func withoutTarget(policy ports.TargetPolicy) ports.TargetPolicy {
	policy.Target = ""
	return policy
}

func withoutProfile(policy ports.TargetPolicy) ports.TargetPolicy {
	policy.RequiredProfileID = ""
	return policy
}
