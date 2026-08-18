package app_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/optimaldynamics/maiden-lane/internal/adapters/memory"
	"github.com/optimaldynamics/maiden-lane/internal/app"
	"github.com/optimaldynamics/maiden-lane/internal/fixtures/teamhos"
	"github.com/optimaldynamics/maiden-lane/internal/ports"
	"github.com/optimaldynamics/maiden-lane/internal/promotion"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

// THE ASSERTION THIS SLICE EXISTS FOR: publication ships able to publish nothing.
//
// Three of HLD §14.1's nine clauses have no implementation in this build and four
// more are not yet wired. Every decision therefore contains a NotEvaluated and
// refuses. This is asserted rather than assumed because the alternative failure is
// silent and total: publication arriving before its checks, with a gate that looks
// present and authorizes on an incomplete evaluation.
//
// When a clause is wired this test must be updated deliberately, which is the point.
func TestPublicationIsRefusedWhileAnyClauseIsUnevaluated(t *testing.T) {
	fixture := publishFixture(t)
	stores := storesWithPolicy(t, fixture)

	outcome, err := app.Publish(t.Context(), stores, fixture.request)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	if outcome.Authorized() {
		t.Fatal("publication was authorized while clauses remain unevaluated")
	}
	if outcome.Result() != app.PublicationRefused {
		t.Fatalf("result = %v, want refused", outcome.Result())
	}
	if _, published := outcome.Publication(); published {
		t.Fatal("a refused request produced a publication")
	}

	// Nothing reached the target. A refusal that still advanced the pointer would
	// be worse than no gate at all.
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
	outcome, err := app.Publish(t.Context(), storesWithPolicy(t, fixture), fixture.request)
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

	// Every refusal must be UnsupportedByBuild rather than InformationAbsent: no
	// evidence would satisfy these, so an operator must be told engineering is
	// missing rather than sent looking for inputs.
	refusals := decision.Refusals()
	if len(refusals) != 7 {
		t.Fatalf("refusals = %d, want 7", len(refusals))
	}
	for _, result := range refusals {
		if result.Unevaluated() != promotion.UnsupportedByBuild {
			t.Fatalf("clause %v refused with reason %v, want UnsupportedByBuild",
				result.Clause(), result.Unevaluated())
		}
	}
}

// A target with no policy must refuse without looking like a fault. An
// unconfigured destination is the ordinary initial state, and the refusal must say
// the policy is what is missing.
func TestAnUnconfiguredTargetRefusesRatherThanFailing(t *testing.T) {
	fixture := publishFixture(t)
	store := memory.NewStore()
	stores := app.PublicationStores{Policies: store, Publications: store}

	outcome, err := app.Publish(t.Context(), stores, fixture.request)
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

// Production break caught by construction: a binding whose semantic run did not
// produce the checkpoint would yield a record naming an execution that did not
// produce the artifact beside it — complete-looking and self-contradictory.
//
// A CheckpointArtifact carries no execution identity on purpose, because executor
// identity affects only ExecutionID and one semantic run can be executed more than
// once. So this pairing cannot be checked from the checkpoint alone, which is why
// the request takes a RunBinding: only BindRun produces one, and it commits to both
// identities together.
func TestABindingFromAnotherRunIsRefusedBeforeAnythingIsRead(t *testing.T) {
	fixture := publishFixture(t)
	other := publishFixtureFor(t, teamhos.AnchorMismatch)

	request := fixture.request
	request.Binding = other.binding

	_, err := app.Publish(t.Context(), storesWithPolicy(t, fixture), request)
	if err == nil {
		t.Fatal("a binding from a different semantic run was accepted")
	}
	// An error rather than a gate refusal: this says nothing about the candidate,
	// it says the request cannot produce a truthful record. The code distinguishes a
	// wrong pairing from a missing piece, because those call for different action.
	var invalid app.InvalidInputError
	if !errors.As(err, &invalid) {
		t.Fatalf("error = %v, want an InvalidInputError", err)
	}
	if invalid.Code != app.InputPublishBindingMismatch {
		t.Fatalf("code = %s, want %s", invalid.Code, app.InputPublishBindingMismatch)
	}
	// No identifier may appear in the text: these errors carry a closed code and
	// fixed safe text, so a surfaced error cannot become a channel for content.
	if strings.Contains(err.Error(), string(other.binding.SemanticRunID())) {
		t.Fatal("the error text carries a semantic run identity")
	}
}

// Absent evidence must be refused before any store is touched, and must be an
// error rather than a refusal, for the same reason: a request missing a piece is
// not a candidate the gate judged.
func TestAnIncompleteRequestIsAnErrorRatherThanARefusal(t *testing.T) {
	fixture := publishFixture(t)
	for _, test := range []struct {
		name   string
		mutate func(*app.PublishRequest)
	}{
		{"no tenant", func(r *app.PublishRequest) { r.TenantID = "" }},
		{"no customer", func(r *app.PublishRequest) { r.CustomerID = "" }},
		{"no target", func(r *app.PublishRequest) { r.Target = "" }},
		{"no checkpoint", func(r *app.PublishRequest) { r.Candidate.Checkpoint = semantic.CheckpointArtifact{} }},
		{"no assessment", func(r *app.PublishRequest) { r.Candidate.Assessment = semantic.Assessment{} }},
		{"no binding", func(r *app.PublishRequest) { r.Binding = semantic.RunBinding{} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := fixture.request
			test.mutate(&request)
			_, err := app.Publish(t.Context(), storesWithPolicy(t, fixture), request)
			if err == nil {
				t.Fatalf("Publish accepted a request with %s", test.name)
			}
			var invalid app.InvalidInputError
			if !errors.As(err, &invalid) || invalid.Code != app.InputPublishRequestIncomplete {
				t.Fatalf("error = %v, want InputPublishRequestIncomplete", err)
			}
		})
	}
}

// Production break caught by construction: with the gate refusing everything, the
// pointer-advancing path has no test coverage from the outside at all. This drives
// it directly so the record's shape is asserted now rather than the first time a
// clause is wired — at which point publication would begin working and its pinned
// identities would be load-bearing immediately.
func TestTheRecordPinsWhatAuthorizedIt(t *testing.T) {
	fixture := publishFixture(t)
	stores := storesWithPolicy(t, fixture)

	// The pointer is advanced through the store directly, with the record the use
	// case would build. Reaching authorization is not possible in this build, and
	// faking a Decision is not either: its fields are unexported and its
	// constructors refuse an incoherent result, which is the property that keeps a
	// test from inventing an authorization.
	publication := ports.Publication{
		TenantID: fixture.tenant, CustomerID: fixture.customer, Target: fixture.target,
		Version:              1,
		PolicyVersion:        1,
		ProfileID:            fixture.assessment.ProfileID(),
		AssessmentID:         fixture.assessment.ID(),
		CheckpointArtifactID: fixture.artifact.ID(),
		SemanticRunID:        fixture.binding.SemanticRunID(),
		ExecutionID:          fixture.binding.ExecutionID(),
	}
	if err := stores.Publications.Publish(t.Context(), publication); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	current, found, err := stores.Publications.CurrentPublication(
		t.Context(), fixture.tenant, fixture.customer, fixture.target)
	if err != nil || !found {
		t.Fatalf("CurrentPublication: found=%t err=%v", found, err)
	}

	// Each pinned identity must be one the kernel actually derived, so a record
	// re-read later resolves to real artifacts rather than to plausible strings.
	if current.CheckpointArtifactID != fixture.artifact.ID() {
		t.Fatal("the record does not name the checkpoint that was published")
	}
	if current.AssessmentID != fixture.assessment.ID() {
		t.Fatal("the record does not name the assessment relied on")
	}
	if current.SemanticRunID != fixture.artifact.SemanticRunID() {
		t.Fatal("the record's semantic run is not the checkpoint's")
	}
	if current.ExecutionID != fixture.binding.ExecutionID() {
		t.Fatal("the record does not name the execution that ran")
	}
	// The assessment is bound to this checkpoint, which is what makes pinning both
	// coherent rather than two independent claims.
	if fixture.assessment.CheckpointArtifactID() != fixture.artifact.ID() {
		t.Fatal("the fixture's assessment is not bound to its checkpoint")
	}
}

// ── fixture ─────────────────────────────────────────────────────────────────

type publishSetup struct {
	tenant     ports.TenantID
	customer   ports.CustomerID
	target     ports.TargetKey
	binding    semantic.RunBinding
	artifact   semantic.CheckpointArtifact
	assessment semantic.Assessment
	request    app.PublishRequest
}

func publishFixture(t *testing.T) publishSetup {
	t.Helper()
	return publishFixtureFor(t, teamhos.Passing)
}

// publishFixtureFor runs the kernel over a golden variant and returns the last
// checkpoint it sealed with an assessment taken against it.
//
// It drives the kernel rather than app.Run because it needs the RunBinding, which
// the spine result does not expose — and because a publication must be built from
// artifacts the kernel actually produced, since a CheckpointArtifact cannot be
// constructed any other way.
func publishFixtureFor(t *testing.T, variant teamhos.Variant) publishSetup {
	t.Helper()

	inputs, err := teamhos.New(variant)
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
	binding, err := semantic.BindRun(semantic.RunBindingRequest{
		Plan:             plan,
		InitialState:     inputs.InitialState,
		World:            inputs.World,
		ExecutorIdentity: inputs.ExecutorIdentity,
		Policy:           inputs.Policy,
	})
	if err != nil {
		t.Fatalf("BindRun: %v", err)
	}

	state, journal := inputs.InitialState, semantic.NewJournal()
	var (
		artifact   semantic.CheckpointArtifact
		assessment semantic.Assessment
		known      []semantic.CheckpointArtifact
	)
	for _, transformation := range plan.Transformations() {
		outcome, err := semantic.ExecuteTransition(
			binding, transformation.Declaration().ID, state, journal)
		if err != nil {
			t.Fatalf("ExecuteTransition: %v", err)
		}
		if _, refused := outcome.Failure(); refused {
			break
		}
		state, journal = outcome.State(), outcome.Journal()

		for _, checkpoint := range plan.Checkpoints() {
			if checkpoint.After != transformation.Declaration().ID {
				continue
			}
			sealed, err := semantic.Seal(semantic.SealRequest{
				Binding: binding, Checkpoint: checkpoint.Key, State: state, Journal: journal,
				InvariantResults: outcome.InvariantResults(),
				KnownArtifacts:   slices.Clone(known),
			})
			if err != nil {
				t.Fatalf("Seal: %v", err)
			}
			candidate, ok := sealed.Artifact()
			if !ok {
				t.Fatal("Seal produced neither artifact nor failure")
			}
			artifact = candidate
			known = append(known, candidate)

			assessed, err := semantic.Assess(semantic.AssessmentRequest{
				Checkpoint: candidate, State: state, Profile: compilation.Profiles()[0],
			})
			if err != nil {
				t.Fatalf("Assess: %v", err)
			}
			answer, ok := assessed.Assessment()
			if !ok {
				t.Fatal("Assess produced neither assessment nor failure")
			}
			assessment = answer
		}
	}
	if artifact.ID() == "" {
		t.Fatal("the fixture sealed no checkpoint")
	}

	setup := publishSetup{
		tenant: "acme", customer: "cust", target: "cm",
		binding: binding, artifact: artifact, assessment: assessment,
	}
	setup.request = app.PublishRequest{
		TenantID: setup.tenant, CustomerID: setup.customer, Target: setup.target,
		Binding: binding,
		Candidate: promotion.Candidate{
			Checkpoint:               artifact,
			Assessment:               assessment,
			RetainedInvariantWitness: artifact.InvariantResultCanonicalBytes(),
		},
	}
	return setup
}

// storesWithPolicy returns one store serving both ports with a policy recorded for
// the fixture's target, requiring the profile its assessment was taken under.
func storesWithPolicy(t *testing.T, fixture publishSetup) app.PublicationStores {
	t.Helper()
	store := memory.NewStore()
	if err := store.PutPolicy(t.Context(), ports.TargetPolicy{
		TenantID: fixture.tenant, CustomerID: fixture.customer, Target: fixture.target,
		Version: 1, RequiredProfileID: fixture.assessment.ProfileID(),
	}); err != nil {
		t.Fatalf("PutPolicy: %v", err)
	}
	return app.PublicationStores{Policies: store, Publications: store}
}
