package app

import (
	"errors"
	"reflect"
	"testing"

	"github.com/optimaldynamics/maiden-lane/internal/adapters/memory"
	"github.com/optimaldynamics/maiden-lane/internal/fixtures/teamhos"
	"github.com/optimaldynamics/maiden-lane/internal/ports"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

// A faithfully stored execution must rehydrate into artifacts the kernel just produced,
// with every stored identity, digest, and canonical byte matching.
//
// This is what makes a stored checkpoint usable by the gate at all. Kernel values cannot
// be reconstructed from bytes, so the only way to hold a real CheckpointArtifact for a
// past execution is to produce one again -- and the products matching what the store
// recorded is simultaneously the proof that the store was faithful.
func TestAFaithfullyStoredExecutionRehydratesAndVerifies(t *testing.T) {
	stores, record := storedExecution(t, teamhos.Passing)

	rehydrated, err := Rehydrate(t.Context(), stores, record.Request.TenantID, record.Request.ExecutionID)
	if err != nil {
		t.Fatalf("Rehydrate: %v", err)
	}
	if rehydrated.Outcome() != RehydrationVerified {
		t.Fatalf("outcome = %v, want verified", rehydrated.Outcome())
	}

	result, ok := rehydrated.Result()
	if !ok {
		t.Fatal("a verified rehydration returned no result")
	}
	// The re-derived identities must be the stored ones, which is the whole claim.
	if id, _ := result.ExecutionID(); id != record.Request.ExecutionID {
		t.Fatalf("executionID = %s, want the stored %s", id, record.Request.ExecutionID)
	}
	if run, _ := result.SemanticRunID(); run != record.Request.RunID {
		t.Fatalf("semanticRunID = %s, want the stored %s", run, record.Request.RunID)
	}
	if len(result.Checkpoints()) != len(record.Result.Checkpoints) {
		t.Fatalf("checkpoints = %d, want the stored %d",
			len(result.Checkpoints()), len(record.Result.Checkpoints))
	}
	// And they are live kernel values, not projections: this witness verifies against
	// the artifact's own commitment, which no stored form could establish for itself.
	for _, artifact := range result.Checkpoints() {
		if !semantic.VerifyInvariantResultDigest(
			artifact.InvariantResultCanonicalBytes(), artifact.InvariantResultDigest()) {
			t.Fatal("a rehydrated artifact does not verify against its own commitment")
		}
	}
}

// A rehydrated execution must yield everything publication needs, which is the point of
// the whole slice: the gate becomes reachable from a stored record.
func TestARehydratedExecutionIsPublishable(t *testing.T) {
	stores, record := storedExecution(t, teamhos.Passing)
	rehydrated, err := Rehydrate(t.Context(), stores, record.Request.TenantID, record.Request.ExecutionID)
	if err != nil {
		t.Fatalf("Rehydrate: %v", err)
	}

	stored := record.Result.Assessments[0]
	publishable, ok := rehydrated.Publishable(
		checkpointKeyFor(t, record, stored.CheckpointArtifactID), stored.ProfileID)
	if !ok {
		t.Fatal("a verified rehydration produced nothing publishable")
	}

	// The receipt must bind the execution the store recorded to this checkpoint. It is
	// minted by the fresh spine result, so it attests to an execution that genuinely
	// happened -- in this process, moments ago, reproducing the stored one.
	if publishable.Receipt.ExecutionID() != record.Request.ExecutionID {
		t.Fatalf("receipt execution = %s, want the stored %s",
			publishable.Receipt.ExecutionID(), record.Request.ExecutionID)
	}
	if publishable.Receipt.CheckpointArtifactID() != stored.CheckpointArtifactID {
		t.Fatal("the receipt is not for the checkpoint that was asked for")
	}
	if publishable.Candidate.Assessment.ProfileID() != stored.ProfileID {
		t.Fatal("the candidate carries an assessment under a different profile")
	}
	if publishable.Candidate.Plan.ID() != record.Request.PlanID {
		t.Fatal("the candidate carries a different plan than the execution named")
	}

	// End to end: this candidate reaches the gate, which is what was impossible before.
	// It still refuses, on the three clauses no build can answer yet.
	store := memory.NewStore()
	if err := store.PutPolicy(t.Context(), ports.TargetPolicy{
		TenantID: "acme", CustomerID: "cust", Target: "cm",
		Version: 1, RequiredProfileID: stored.ProfileID,
	}); err != nil {
		t.Fatalf("PutPolicy: %v", err)
	}
	outcome, err := Publish(t.Context(),
		PublicationStores{Policies: store, Publications: store},
		PublishRequest{
			TenantID: "acme", CustomerID: "cust", Target: "cm",
			Receipt: publishable.Receipt, Candidate: publishable.Candidate,
		})
	if err != nil {
		t.Fatalf("Publish from a rehydrated execution: %v", err)
	}
	if outcome.Authorized() {
		t.Fatal("publication was authorized while three clauses are unanswerable")
	}
	if got := len(outcome.Decision().Refusals()); got != 3 {
		t.Fatalf("refusals = %d, want the 3 unimplemented clauses: a rehydrated "+
			"candidate must satisfy every clause this build can answer", got)
	}
}

// THE ASSERTION THIS SLICE EXISTS FOR: a store whose contents the kernel cannot
// reproduce must be refused, field by field.
//
// This is the case no verifier over stored fields can catch. Recomputing an identity
// from stored components establishes that a tuple agrees with itself, and a
// wrong-but-self-consistent record already does. Only re-deriving from the inputs
// detects it.
func TestADivergentStoreIsRefused(t *testing.T) {
	stores, record := storedExecution(t, teamhos.Passing)

	for _, test := range []struct {
		name   string
		field  string
		break_ func(*ports.ExecutionResult)
	}{
		{"a rewritten final state digest", "finalStateDigest",
			func(r *ports.ExecutionResult) { r.FinalStateDigest = semantic.StateDigest(digestOf("other")) }},
		{"a rewritten journal prefix", "journalPrefixDigest",
			func(r *ports.ExecutionResult) {
				r.JournalPrefixDigest = semantic.JournalPrefixDigest(digestOf("other"))
			}},
		{"a dropped accepted rule", "acceptedRules",
			func(r *ports.ExecutionResult) { r.AcceptedRules = r.AcceptedRules[:1] }},
		{"a dropped checkpoint", "checkpoints.length",
			func(r *ports.ExecutionResult) { r.Checkpoints = r.Checkpoints[:1] }},
		{"a checkpoint's identity rewritten", "checkpoints[0].checkpointArtifactID",
			func(r *ports.ExecutionResult) {
				r.Checkpoints[0].CheckpointArtifactID = semantic.CheckpointArtifactID(digestOf("other"))
			}},
		// The one that matters most: identities agree, the artifact does not. No digest
		// recomputation over stored fields would see this.
		{"a checkpoint's canonical bytes altered", "checkpoints[0].canonicalBytes",
			func(r *ports.ExecutionResult) {
				r.Checkpoints[0].CanonicalBytes = append([]byte{0x00}, r.Checkpoints[0].CanonicalBytes...)
			}},
		{"an invariant witness altered", "checkpoints[0].invariantResultCanonicalBytes",
			func(r *ports.ExecutionResult) {
				altered := append([]byte(nil), r.Checkpoints[0].InvariantResultCanonicalBytes...)
				altered[len(altered)-1] ^= 0xff
				r.Checkpoints[0].InvariantResultCanonicalBytes = altered
			}},
		{"a readiness verdict flipped", "assessments[0].verdict",
			func(r *ports.ExecutionResult) { r.Assessments[0].Verdict = semantic.NeedsInput }},
		// This one has to target the assessment that actually has missing
		// requirements. An earlier version doctored assessment 0, which is already
		// ready with none, so it altered nothing and the test failed for the right
		// reason: rehydration correctly found nothing wrong.
		{"a missing requirement removed", "missingRequirements",
			func(r *ports.ExecutionResult) {
				for i := range r.Assessments {
					if len(r.Assessments[i].MissingRequirements) > 0 {
						r.Assessments[i].MissingRequirements = nil
						return
					}
				}
				panic("the fixture has no assessment with missing requirements")
			}},
		{"a failure invented", "failure.presence",
			func(r *ports.ExecutionResult) {
				r.Failure = &ports.StoredFailure{Kind: semantic.ProtectedInvariantFailed, Code: "X"}
			}},
	} {
		t.Run(test.name, func(t *testing.T) {
			doctored := storeWithDoctoredResult(t, stores, record, test.break_)

			_, err := Rehydrate(t.Context(), doctored,
				record.Request.TenantID, record.Request.ExecutionID)
			var integrity IntegrityError
			if !errors.As(err, &integrity) {
				t.Fatalf("error = %v, want an IntegrityError", err)
			}
			if integrity.Code != IntegrityResultDiverged {
				t.Fatalf("code = %s, want %s", integrity.Code, IntegrityResultDiverged)
			}
			if !contains(integrity.Detail, test.field) {
				t.Fatalf("detail = %q, want it to name %q: an integrity failure has to "+
					"say what diverged, or it sends someone to read everything",
					integrity.Detail, test.field)
			}
		})
	}
}

// PRODUCTION BREAK CAUGHT BY CONSTRUCTION: a field the comparison forgets is a field in
// which storage may diverge undetected, and nothing about the code would look wrong.
//
// This walks every field of every struct the comparison covers and requires each to be
// caught, so adding a field without adding it to divergence fails here rather than
// quietly widening the hole. It is the reason the comparison can be hand-written at all
// instead of reaching for reflect.DeepEqual, which would detect everything and diagnose
// nothing.
func TestEveryStoredFieldIsCompared(t *testing.T) {
	_, record := storedExecution(t, teamhos.Passing)
	original := *record.Result

	for _, path := range fieldPaths(reflect.TypeOf(ports.ExecutionResult{})) {
		t.Run(path.name, func(t *testing.T) {
			altered := cloneResult(original)
			// A field the walker cannot alter is a field this guard does not cover, so
			// it fails rather than skipping. Skipping was the first version and it hid
			// one uncovered field behind a green run.
			if !path.alter(reflect.ValueOf(&altered).Elem()) {
				t.Fatalf("no distinct value could be produced for %s, so nothing here "+
					"asserts that storage diverging in it would be caught", path.name)
			}
			if field := divergence(original, altered); field == "" {
				t.Fatalf("altering %s was not detected, so storage may diverge there "+
					"undetected", path.name)
			}
		})
	}
}

// An execution nobody stored is absent, not an error. Another tenant's is absent too,
// which the store guarantees and this asserts at the boundary a caller actually uses.
func TestAnAbsentExecutionRehydratesToNothing(t *testing.T) {
	stores, record := storedExecution(t, teamhos.Passing)

	for _, test := range []struct {
		name   string
		tenant ports.TenantID
		id     semantic.ExecutionID
	}{
		{"no such execution", record.Request.TenantID, semantic.ExecutionID(digestOf("nothing"))},
		{"another tenant's execution", "globex", record.Request.ExecutionID},
	} {
		t.Run(test.name, func(t *testing.T) {
			rehydrated, err := Rehydrate(t.Context(), stores, test.tenant, test.id)
			if err != nil {
				t.Fatalf("Rehydrate: %v", err)
			}
			if rehydrated.Outcome() != RehydrationAbsent {
				t.Fatalf("outcome = %v, want absent", rehydrated.Outcome())
			}
			if _, ok := rehydrated.Result(); ok {
				t.Fatal("an absent execution produced a result")
			}
		})
	}
}

// An enqueued execution has no result to reproduce. That is an ordinary state on the way
// to having one, not a fault, and it must be distinguishable from absence so a caller
// knows whether to wait or to give up.
func TestAPendingExecutionRehydratesToPending(t *testing.T) {
	store := memory.NewStore()
	request := storedRequest(t, teamhos.Passing)
	if _, err := store.Enqueue(t.Context(), request); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	rehydrated, err := Rehydrate(t.Context(),
		RehydrationStores{Plans: store, Executions: store},
		request.TenantID, request.ExecutionID)
	if err != nil {
		t.Fatalf("Rehydrate: %v", err)
	}
	if rehydrated.Outcome() != RehydrationPending {
		t.Fatalf("outcome = %v, want pending", rehydrated.Outcome())
	}
	if rehydrated.Status() != ports.ExecutionPending {
		t.Fatalf("status = %s, want pending: the lifecycle status has to survive so a "+
			"caller can tell waiting from missing without a second read", rehydrated.Status())
	}
	if _, ok := rehydrated.Result(); ok {
		t.Fatal("a pending execution produced a result")
	}
}

// An execution naming a plan the store does not have cannot be re-derived at all.
// Execution identity is derived from the plan, so the plan is not recoverable from the
// execution: this is the store failing to answer for what it holds, not an absence.
func TestAnExecutionNamingAnAbsentPlanIsAnIntegrityFailure(t *testing.T) {
	_, record := storedExecution(t, teamhos.Passing)

	// A store holding the execution and no plan at all.
	store := memory.NewStore()
	if _, err := store.Enqueue(t.Context(), record.Request); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if err := store.Complete(t.Context(), *record.Result); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	_, err := Rehydrate(t.Context(),
		RehydrationStores{Plans: store, Executions: store},
		record.Request.TenantID, record.Request.ExecutionID)
	var integrity IntegrityError
	if !errors.As(err, &integrity) {
		t.Fatalf("error = %v, want an IntegrityError", err)
	}
	if integrity.Code != IntegrityPlanAbsent {
		t.Fatalf("code = %s, want %s", integrity.Code, IntegrityPlanAbsent)
	}
}

// An integrity error must name a field and carry no content from either side. It is
// produced while handling material that is already suspect, so putting any of it in a
// message would make a corrupt store a channel for whatever it contains.
func TestAnIntegrityErrorCarriesNoStoredContent(t *testing.T) {
	stores, record := storedExecution(t, teamhos.Passing)
	poison := "sha256:" + repeatByte('c', 64)
	doctored := storeWithDoctoredResult(t, stores, record, func(r *ports.ExecutionResult) {
		r.Checkpoints[0].CheckpointArtifactID = semantic.CheckpointArtifactID(poison)
	})

	_, err := Rehydrate(t.Context(), doctored, record.Request.TenantID, record.Request.ExecutionID)
	if err == nil {
		t.Fatal("a doctored store was accepted")
	}
	if got := err.Error(); contains(got, poison) {
		t.Fatalf("the error text carries stored content: %q", got)
	}
	// The real identity must not leak either, in case it is the sensitive one.
	if got := err.Error(); contains(got, string(record.Result.Checkpoints[0].CheckpointArtifactID)) {
		t.Fatal("the error text carries the expected identity")
	}
}

// Rehydration writes nothing. It reads a store to check the store, and a check that
// mutated what it was checking could make a divergent record agree with itself.
func TestRehydrationWritesNothing(t *testing.T) {
	stores, record := storedExecution(t, teamhos.Passing)
	before := cloneResult(*record.Result)

	if _, err := Rehydrate(t.Context(), stores,
		record.Request.TenantID, record.Request.ExecutionID); err != nil {
		t.Fatalf("Rehydrate: %v", err)
	}

	after, found, err := stores.Executions.Get(t.Context(),
		record.Request.TenantID, record.Request.ExecutionID)
	if err != nil || !found {
		t.Fatalf("Get: found=%t err=%v", found, err)
	}
	if field := divergence(before, *after.Result); field != "" {
		t.Fatalf("rehydration altered the stored result at %s", field)
	}
}

// ── fixture ─────────────────────────────────────────────────────────────────

// storedExecution runs a variant, stores the plan and the completed execution exactly as
// the worker would, and returns the store and the record.
//
// It projects through the same Project the worker uses. Anything else would compare
// rehydration against a second projection, and a test whose expected value comes from a
// different implementation than production's proves the wrong thing.
func storedExecution(t *testing.T, variant teamhos.Variant) (RehydrationStores, ports.ExecutionRecord) {
	t.Helper()

	store := memory.NewStore()
	request := storedRequest(t, variant)
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
	if err := store.PutPlan(t.Context(), ports.PlanRecord{
		TenantID: request.TenantID, PlanID: plan.ID(),
		Input: compilation.Input(), Schema: inputs.InitialState.Schema(), Compilation: compilation,
	}); err != nil {
		t.Fatalf("PutPlan: %v", err)
	}
	if _, err := store.Enqueue(t.Context(), request); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	result, err := Run(t.Context(), Request{
		Compilation: inputs.Compilation, InitialState: inputs.InitialState,
		World: inputs.World, ExecutorIdentity: inputs.ExecutorIdentity, Policy: inputs.Policy,
	}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if err := store.Complete(t.Context(), Project(request, result)); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	record, found, err := store.Get(t.Context(), request.TenantID, request.ExecutionID)
	if err != nil || !found {
		t.Fatalf("Get: found=%t err=%v", found, err)
	}
	return RehydrationStores{Plans: store, Executions: store}, record
}

func storedRequest(t *testing.T, variant teamhos.Variant) ports.ExecutionRequest {
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
		Plan: plan, InitialState: inputs.InitialState, World: inputs.World,
		ExecutorIdentity: inputs.ExecutorIdentity, Policy: inputs.Policy,
	})
	if err != nil {
		t.Fatalf("BindRun: %v", err)
	}
	return ports.ExecutionRequest{
		TenantID: "acme", ExecutionID: binding.ExecutionID(),
		RunID: binding.SemanticRunID(), PlanID: plan.ID(),
		Input: ports.ExecutionInput{
			InitialState: inputs.InitialState, World: inputs.World,
			ExecutorIdentity: inputs.ExecutorIdentity, Policy: inputs.Policy,
		},
	}
}

// storeWithDoctoredResult builds a store holding the same plan and execution but with a
// deliberately altered result, which is how a divergent store is simulated without
// needing an adapter that permits corruption.
func storeWithDoctoredResult(
	t *testing.T, original RehydrationStores, record ports.ExecutionRecord,
	doctor func(*ports.ExecutionResult),
) RehydrationStores {
	t.Helper()
	plan, found, err := original.Plans.GetPlan(t.Context(), record.Request.TenantID, record.Request.PlanID)
	if err != nil || !found {
		t.Fatalf("GetPlan: found=%t err=%v", found, err)
	}

	store := memory.NewStore()
	if err := store.PutPlan(t.Context(), plan); err != nil {
		t.Fatalf("PutPlan: %v", err)
	}
	if _, err := store.Enqueue(t.Context(), record.Request); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	doctored := cloneResult(*record.Result)
	doctor(&doctored)
	if err := store.Complete(t.Context(), doctored); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	return RehydrationStores{Plans: store, Executions: store}
}

func checkpointKeyFor(
	t *testing.T, record ports.ExecutionRecord, artifact semantic.CheckpointArtifactID,
) semantic.CheckpointKey {
	t.Helper()
	for _, checkpoint := range record.Result.Checkpoints {
		if checkpoint.CheckpointArtifactID == artifact {
			return checkpoint.CheckpointKey
		}
	}
	t.Fatal("no stored checkpoint has that artifact identity")
	return ""
}

// cloneResult deep copies a stored result so a test can alter one without touching the
// original, including the byte slices a shallow copy would alias.
func cloneResult(result ports.ExecutionResult) ports.ExecutionResult {
	clone := result
	clone.AcceptedRules = append([]semantic.RuleID(nil), result.AcceptedRules...)
	clone.Checkpoints = make([]ports.SealedCheckpoint, len(result.Checkpoints))
	for i, checkpoint := range result.Checkpoints {
		clone.Checkpoints[i] = checkpoint
		clone.Checkpoints[i].CanonicalBytes = append([]byte(nil), checkpoint.CanonicalBytes...)
		clone.Checkpoints[i].InvariantResultCanonicalBytes =
			append([]byte(nil), checkpoint.InvariantResultCanonicalBytes...)
	}
	clone.Assessments = make([]ports.StoredAssessment, len(result.Assessments))
	for i, assessment := range result.Assessments {
		clone.Assessments[i] = assessment
		clone.Assessments[i].CanonicalBytes = append([]byte(nil), assessment.CanonicalBytes...)
		clone.Assessments[i].MissingRequirements =
			append([]semantic.RequirementCode(nil), assessment.MissingRequirements...)
	}
	if result.Failure != nil {
		failure := *result.Failure
		clone.Failure = &failure
	}
	return clone
}

// fieldPath names one alterable leaf of a stored result and knows how to change it.
type fieldPath struct {
	name  string
	alter func(reflect.Value) bool
}

// fieldPaths enumerates every field the divergence comparison must cover, walking the
// struct rather than listing them, so a field added to ports.ExecutionResult and its
// nested types appears here automatically.
func fieldPaths(t reflect.Type) []fieldPath {
	paths := make([]fieldPath, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		index := i
		switch field.Type.Kind() {
		case reflect.Slice:
			if field.Type.Elem().Kind() == reflect.Struct {
				for _, nested := range fieldPaths(field.Type.Elem()) {
					nested := nested
					paths = append(paths, fieldPath{
						name: field.Name + "[0]." + nested.name,
						alter: func(v reflect.Value) bool {
							list := v.Field(index)
							if list.Len() == 0 {
								return false
							}
							return nested.alter(list.Index(0))
						},
					})
				}
				continue
			}
			paths = append(paths, fieldPath{name: field.Name, alter: sliceAlterer(index)})
		case reflect.Ptr:
			paths = append(paths, fieldPath{name: field.Name, alter: pointerAlterer(index)})
		default:
			paths = append(paths, fieldPath{name: field.Name, alter: scalarAlterer(index)})
		}
	}
	return paths
}

func scalarAlterer(index int) func(reflect.Value) bool {
	return func(v reflect.Value) bool {
		field := v.Field(index)
		switch field.Kind() {
		case reflect.String:
			field.SetString(field.String() + "-altered")
			return true
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			field.SetUint(field.Uint() + 1)
			return true
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			field.SetInt(field.Int() + 1)
			return true
		}
		return false
	}
}

// sliceAlterer appends a zero element rather than truncating.
//
// Truncating was the first version and it silently could not alter an empty slice, so
// the guard skipped whichever fields happened to be empty in the fixture -- and a
// skipped field is an uncovered field. Appending changes an empty slice and a full one
// alike, which is what makes the guard total rather than fixture-dependent.
func sliceAlterer(index int) func(reflect.Value) bool {
	return func(v reflect.Value) bool {
		field := v.Field(index)
		field.Set(reflect.Append(field, reflect.Zero(field.Type().Elem())))
		return true
	}
}

func pointerAlterer(index int) func(reflect.Value) bool {
	return func(v reflect.Value) bool {
		field := v.Field(index)
		if field.IsNil() {
			// Presence itself is the alteration: a run that committed becoming one that
			// refused is the most consequential divergence there is.
			field.Set(reflect.New(field.Type().Elem()))
			return true
		}
		field.Set(reflect.Zero(field.Type()))
		return true
	}
}

func digestOf(label string) string {
	sum := make([]byte, 0, 64)
	for i := 0; i < 64; i++ {
		sum = append(sum, "0123456789abcdef"[(int(label[i%len(label)])+i)%16])
	}
	return "sha256:" + string(sum)
}

func repeatByte(b byte, n int) string {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return string(out)
}

func contains(haystack, needle string) bool {
	if needle == "" {
		return false
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
