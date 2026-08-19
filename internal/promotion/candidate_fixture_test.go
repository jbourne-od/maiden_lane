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

	// The plan and the execution identity the checkpoint was produced under. The
	// pinned-identity and static-validation clauses need both, and neither is
	// recoverable from the artifact: PlanID commits to the plan without exposing its
	// parts, and executor identity is excluded from checkpoint identity entirely.
	plan         semantic.Plan
	executionID  semantic.ExecutionID
	initialState semantic.State
	world        semantic.World
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
			sealed = append(sealed, sealedFixture{
				artifact: artifact, assessments: assessments,
				plan: plan, executionID: binding.ExecutionID(),
				initialState: inputs.InitialState, world: inputs.World,
			})
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
	return candidateFrom(sealed[0])
}

// candidateFrom assembles the complete candidate for one sealed checkpoint: the
// artifact, the plan it was sealed under, the assessment bound to it, the witness it
// commits to, and the execution that produced it.
func candidateFrom(sealed sealedFixture) Candidate {
	return Candidate{
		Checkpoint:               sealed.artifact,
		Plan:                     sealed.plan,
		Assessment:               sealed.assessments[0],
		RetainedInvariantWitness: sealed.artifact.InvariantResultCanonicalBytes(),
		ExecutionID:              sealed.executionID,
	}
}

// samplePolicy is a complete policy at a version other than 1, so a decision that
// reported a hardcoded or defaulted version would be visible.
//
// Its required profile is a literal, which is now only correct for tests that do not
// exercise the readiness clause. Use policyRequiring for those: a literal profile makes
// that clause unevaluated for want of a matching assessment, which is a real answer but
// not the one most tests mean to ask about.
func samplePolicy() ports.TargetPolicy {
	return ports.TargetPolicy{
		TenantID:          "tenant-a",
		CustomerID:        "customer-a",
		Target:            "cm",
		Version:           7,
		RequiredProfileID: semantic.ProfileID("sha256:" + strings.Repeat("b", 64)),
	}
}

// policyRequiring is samplePolicy bound to a profile an assessment was really taken
// under, which is what the readiness clause needs to reach an answer about the
// candidate rather than about its own missing evidence.
func policyRequiring(profile semantic.ProfileID) ports.TargetPolicy {
	policy := samplePolicy()
	policy.RequiredProfileID = profile
	return policy
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

// otherPlan compiles a genuinely different program, so a test can supply a plan that is
// real but is not the one a checkpoint was sealed under.
//
// It is the anchor-mismatch variant's declarations, which differ from the passing
// variant only in the initial state -- so this actually compiles to the SAME plan, and
// that is worth knowing rather than working around. A different program is needed, so
// this drops a checkpoint declaration, which changes plan identity while still
// compiling.
func otherPlan(t *testing.T) semantic.Plan {
	t.Helper()
	inputs, err := teamhos.New(teamhos.Passing)
	if err != nil {
		t.Fatalf("teamhos.New: %v", err)
	}
	request := inputs.Compilation
	request.Rules.Checkpoints = request.Rules.Checkpoints[:1]

	compilation, err := semantic.Compile(request)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if failure, refused := compilation.Failure(); refused {
		t.Fatalf("the altered program did not compile: %v", failure)
	}
	plan, ok := compilation.Plan()
	if !ok {
		t.Fatal("compilation produced neither plan nor failure")
	}
	return plan
}

// comparisonEvidenceFor builds a real comparison and the evidence answering it, for a
// checkpoint that was actually sealed.
//
// The two sides are two checkpoint declarations of ONE plan, which is mechanically valid
// — the correspondence contract maps declarations and permits both sides to belong to the
// same plan — and is the smallest fixture that exercises every check: correspondence,
// coverage, world agreement, and profile agreement. A realistic comparison is across two
// plans over a corpus of many cases; that shape belongs to the application slice that
// assembles evidence from stored executions, because it needs both sides actually
// executed rather than constructed.
//
// The corpus is the one initial state these artifacts were produced from, which is what
// makes coverage verify: a checkpoint carries the initial state its run was bound to, and
// the corpus is a set of initial states.
func comparisonEvidenceFor(t *testing.T, promoted sealedFixture) *ComparisonEvidence {
	t.Helper()
	sealed := sealTeamHOS(t)

	// The promoted checkpoint must be the CANDIDATE side, so the clause's linkage check
	// has something true to find.
	var baseline sealedFixture
	for _, candidate := range sealed {
		if candidate.artifact.CheckpointID() != promoted.artifact.CheckpointID() {
			baseline = candidate
			break
		}
	}
	if baseline.artifact.ID() == "" {
		t.Fatal("the fixture needs two distinct checkpoint declarations to compare")
	}

	corpus, err := semantic.NewCorpus([]semantic.State{promoted.initialState})
	if err != nil {
		t.Fatalf("NewCorpus: %v", err)
	}
	policy, err := semantic.NewComparisonPolicy(promoted.plan, promoted.plan,
		[]semantic.CheckpointPair{{
			Baseline:  baseline.artifact.Checkpoint().Key,
			Candidate: promoted.artifact.Checkpoint().Key,
		}})
	if err != nil {
		t.Fatalf("NewComparisonPolicy: %v", err)
	}

	profile := promoted.assessments[0].ProfileID()
	comparison, err := semantic.NewComparison(semantic.ComparisonRequest{
		Baseline:  baseline.artifact.CheckpointID(),
		Candidate: promoted.artifact.CheckpointID(),
		Profile:   profile,
		World:     promoted.world.ID(),
		Corpus:    corpus.ID(),
		Policy:    policy,
	})
	if err != nil {
		t.Fatalf("NewComparison: %v", err)
	}

	return &ComparisonEvidence{
		Comparison: comparison,
		Baseline:   []ComparedCase{comparedCase(t, baseline, profile)},
		Candidate:  []ComparedCase{comparedCase(t, promoted, profile)},
	}
}

func comparedCase(t *testing.T, sealed sealedFixture, profile semantic.ProfileID) ComparedCase {
	t.Helper()
	for _, assessment := range sealed.assessments {
		if assessment.ProfileID() == profile {
			return ComparedCase{Checkpoint: sealed.artifact, Assessment: assessment}
		}
	}
	t.Fatalf("no assessment under profile %s for this checkpoint", profile)
	return ComparedCase{}
}

// otherProfileAssessment returns an assessment of the same checkpoint under a different
// profile, so the profile-agreement check can be exercised against real evidence.
func otherProfileAssessment(
	t *testing.T, sealed sealedFixture, exclude semantic.ProfileID,
) semantic.Assessment {
	t.Helper()
	for _, assessment := range sealed.assessments {
		if assessment.ProfileID() != exclude {
			return assessment
		}
	}
	t.Fatal("the fixture needs two profiles for this")
	return semantic.Assessment{}
}

// worldWithReference builds a world that is not the fixture's empty one.
func worldWithReference(t *testing.T) semantic.World {
	t.Helper()
	reference, err := semantic.NewWorldReference(
		semantic.WorldReferenceSnapshot, semantic.Digest("sha256:"+strings.Repeat("e", 64)))
	if err != nil {
		t.Fatalf("NewWorldReference: %v", err)
	}
	world, err := semantic.NewWorld([]semantic.WorldReference{reference})
	if err != nil {
		t.Fatalf("NewWorld: %v", err)
	}
	return world
}

// assessmentWithVerdict returns an assessment of this checkpoint with a given verdict, so
// readiness can be varied while everything else about the evidence stays valid.
func assessmentWithVerdict(
	t *testing.T, sealed sealedFixture, verdict semantic.ReadinessVerdict,
) semantic.Assessment {
	t.Helper()
	for _, assessment := range sealed.assessments {
		if assessment.Verdict() == verdict {
			return assessment
		}
	}
	t.Fatalf("the fixture has no %s assessment for this checkpoint", verdict)
	return semantic.Assessment{}
}

// assessmentUnderProfile finds the assessment of one checkpoint declaration under a
// profile, across the whole run.
func assessmentUnderProfile(
	t *testing.T, sealed []sealedFixture,
	checkpoint semantic.CheckpointID, profile semantic.ProfileID,
) semantic.Assessment {
	t.Helper()
	for _, side := range sealed {
		if side.artifact.CheckpointID() != checkpoint {
			continue
		}
		for _, assessment := range side.assessments {
			if assessment.ProfileID() == profile {
				return assessment
			}
		}
	}
	t.Fatal("no assessment under that profile for that checkpoint")
	return semantic.Assessment{}
}

// restateComparison re-identifies a comparison under a different profile, leaving every
// other input alone, so a test can vary the profile without disturbing what it is
// comparing.
func restateComparison(
	t *testing.T, comparison semantic.Comparison, profile semantic.ProfileID,
) semantic.Comparison {
	t.Helper()
	restated, err := semantic.NewComparison(semantic.ComparisonRequest{
		Baseline:  comparison.Baseline(),
		Candidate: comparison.Candidate(),
		Profile:   profile,
		World:     comparison.World(),
		Corpus:    comparison.Corpus(),
		Policy:    comparison.Policy(),
	})
	if err != nil {
		t.Fatalf("NewComparison: %v", err)
	}
	return restated
}
