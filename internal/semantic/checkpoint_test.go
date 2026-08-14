package semantic

import (
	"bytes"
	"encoding/hex"
	"slices"
	"testing"
)

// Production break caught: sealing a declaration at the wrong accepted-plan
// prefix would let a partial or overrun history masquerade as that checkpoint.
func TestSealAcceptsOnlyExactCompiledCheckpointPrefixes(t *testing.T) {
	binding, c1, c2 := checkpointExecutionFixture(t, testGoExecutor)

	for _, test := range []struct {
		name       string
		checkpoint CheckpointKey
		outcome    TransitionOutcome
	}{
		{name: "C1", checkpoint: "team_formed.v1", outcome: c1},
		{name: "C2", checkpoint: "team_hos_aggregated.v1", outcome: c2},
	} {
		t.Run(test.name, func(t *testing.T) {
			outcome, err := Seal(SealRequest{
				Binding:          binding,
				Checkpoint:       test.checkpoint,
				State:            test.outcome.State(),
				Journal:          test.outcome.Journal(),
				InvariantResults: test.outcome.InvariantResults(),
			})
			if err != nil {
				t.Fatalf("Seal: %v", err)
			}
			artifact, ok := outcome.Artifact()
			if !ok || !outcome.Sealed() {
				t.Fatalf("checkpoint did not seal; failure=%v", sealFailureCode(outcome))
			}
			if artifact.ID() == "" || artifact.Digest() == "" || artifact.CheckpointID() == "" {
				t.Fatal("sealed checkpoint lacks distinct identities")
			}
			if artifact.StateDigest() != test.outcome.State().Digest() {
				t.Fatal("manifest does not bind exact prefix state")
			}
		})
	}

	wrong, err := Seal(SealRequest{
		Binding: binding, Checkpoint: "team_formed.v1", State: c2.State(),
		Journal: c2.Journal(), InvariantResults: c2.InvariantResults(),
	})
	if err != nil {
		t.Fatalf("Seal wrong prefix: %v", err)
	}
	assertSealIntegrity(t, wrong, ArtifactLinkInconsistent, ArtifactJournalPrefix)
}

// Production break caught: hashing a caller-selected subset would let missing,
// extra, duplicate, or failed protected obligations bless an invalid prefix.
func TestSealRefusesIncompleteInvariantSet(t *testing.T) {
	binding, c1, _ := checkpointExecutionFixture(t, testGoExecutor)
	results := c1.InvariantResults()
	results = results[:len(results)-1]
	outcome, err := Seal(SealRequest{
		Binding: binding, Checkpoint: "team_formed.v1", State: c1.State(),
		Journal: c1.Journal(), InvariantResults: results,
	})
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	assertSealIntegrity(t, outcome, ArtifactLinkInconsistent, ArtifactInvariantResultSet)
}

// Production break caught: trusting cached binding links during sealing would
// let a checkpoint claim replay inputs other than those that formed its run.
func TestSealReturnsTypedIntegrityForEstablishedBindingLinkDefects(t *testing.T) {
	binding, c1, _ := checkpointExecutionFixture(t, testGoExecutor)
	other := Digest("sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff")

	tests := []struct {
		name   string
		mutate func(*RunBinding)
		code   IntegrityCode
		kind   ArtifactKind
	}{
		{name: "wrong initial state link", code: ArtifactLinkInconsistent, kind: ArtifactState, mutate: func(b *RunBinding) { b.initialStateDigest = StateDigest(other) }},
		{name: "missing initial state link", code: ArtifactLinkInconsistent, kind: ArtifactState, mutate: func(b *RunBinding) { b.initialStateDigest = "" }},
		{name: "wrong input identity link", code: ArtifactLinkInconsistent, kind: ArtifactState, mutate: func(b *RunBinding) { b.inputID = InputID(other) }},
		{name: "missing input identity link", code: ArtifactLinkInconsistent, kind: ArtifactState, mutate: func(b *RunBinding) { b.inputID = "" }},
		{name: "wrong world link", code: ArtifactLinkInconsistent, kind: ArtifactWorld, mutate: func(b *RunBinding) { b.worldID = WorldID(other) }},
		{name: "missing world link", code: ArtifactLinkInconsistent, kind: ArtifactWorld, mutate: func(b *RunBinding) { b.worldID = "" }},
		{name: "corrupt world content identity", code: ArtifactDigestMismatch, kind: ArtifactWorld, mutate: func(b *RunBinding) { b.world.id = WorldID(other) }},
		{name: "wrong provenance policy link", code: ArtifactLinkInconsistent, kind: ArtifactJournalPrefix, mutate: func(b *RunBinding) { b.policyID = ProvenancePolicyID(other) }},
		{name: "missing provenance policy link", code: ArtifactLinkInconsistent, kind: ArtifactJournalPrefix, mutate: func(b *RunBinding) { b.policyID = "" }},
		{name: "wrong execution contract link", code: ArtifactLinkInconsistent, kind: ArtifactPlan, mutate: func(b *RunBinding) { b.executionID = ExecutionID(other) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			corrupt := binding
			test.mutate(&corrupt)
			outcome, err := Seal(SealRequest{Binding: corrupt, Checkpoint: "team_formed.v1", State: c1.State(), Journal: c1.Journal(), InvariantResults: c1.InvariantResults()})
			if err != nil {
				t.Fatalf("established-run defect escaped as Go error: %v", err)
			}
			assertSealIntegrity(t, outcome, test.code, test.kind)
		})
	}
}

// Production break caught: tagging a policy digest as a journal-prefix
// ArtifactRef would make the failure evidence lie about the implicated bytes.
func TestSealPolicyLinkFailureReferencesCanonicalJournalPrefix(t *testing.T) {
	binding, c1, _ := checkpointExecutionFixture(t, testGoExecutor)
	other := ProvenancePolicyID("sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff")
	for _, test := range []struct {
		name   string
		policy ProvenancePolicyID
	}{
		{name: "wrong", policy: other},
		{name: "missing"},
	} {
		t.Run(test.name, func(t *testing.T) {
			corrupt := binding
			corrupt.policyID = test.policy
			outcome, err := Seal(SealRequest{Binding: corrupt, Checkpoint: "team_formed.v1", State: c1.State(), Journal: c1.Journal(), InvariantResults: c1.InvariantResults()})
			if err != nil {
				t.Fatalf("Seal: %v", err)
			}
			failure, _ := outcome.Failure()
			integrity, _ := failure.ArtifactIntegrity()
			prefixPolicy := test.policy
			if prefixPolicy == "" {
				prefixPolicy = binding.ProvenancePolicyID()
			}
			prefix, err := encodeJournalPrefix(binding.SemanticRunID(), prefixPolicy, c1.Journal().entries)
			if err != nil {
				t.Fatalf("encodeJournalPrefix: %v", err)
			}
			if got, want := integrity.Artifact().Digest(), canonicalDigest(prefix); got != want {
				t.Fatalf("artifact digest=%s, want journal-prefix content %s", got, want)
			}
		})
	}
}

// Production break caught: omitting the declared CheckpointID from the full
// manifest would weaken its internal checkpoint link to an implicit inference.
func TestCheckpointManifestBindsDeclaredCheckpointID(t *testing.T) {
	binding, c1, _ := checkpointExecutionFixture(t, testGoExecutor)
	artifact := mustSealedCheckpoint(t, SealRequest{Binding: binding, Checkpoint: "team_formed.v1", State: c1.State(), Journal: c1.Journal(), InvariantResults: c1.InvariantResults()})
	raw, err := decodeDigest(string(artifact.CheckpointID()))
	if err != nil {
		t.Fatalf("CheckpointID: %v", err)
	}
	if !bytes.Contains(artifact.CanonicalBytes(), raw[:]) {
		t.Fatal("checkpoint manifest omitted CheckpointID")
	}
}

// Production break caught: a seal that accepts any syntactically encodable
// result set could bless failed, duplicate, extra, malformed, or wrong-boundary
// protected evidence rather than the exact accepted prefix.
func TestSealRejectsEveryNonExactInvariantSet(t *testing.T) {
	binding, c1, _ := checkpointExecutionFixture(t, testGoExecutor)
	tests := []struct {
		name   string
		mutate func([]InvariantResult) []InvariantResult
	}{
		{name: "missing", mutate: func(in []InvariantResult) []InvariantResult { return in[:len(in)-1] }},
		{name: "extra", mutate: func(in []InvariantResult) []InvariantResult { return append(in, in[0]) }},
		{name: "duplicate", mutate: func(in []InvariantResult) []InvariantResult { in[1] = in[0]; return in }},
		{name: "failing", mutate: func(in []InvariantResult) []InvariantResult { in[0].passed = false; return in }},
		{name: "malformed code", mutate: func(in []InvariantResult) []InvariantResult { in[0].code = InvariantCode("UNKNOWN"); return in }},
		{name: "wrong boundary", mutate: func(in []InvariantResult) []InvariantResult { in[0].boundary = "aggregate_team_hos.v1"; return in }},
		{name: "changed evidence", mutate: func(in []InvariantResult) []InvariantResult { in[0].entities = nil; return in }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			results := test.mutate(c1.InvariantResults())
			outcome, err := Seal(SealRequest{Binding: binding, Checkpoint: "team_formed.v1", State: c1.State(), Journal: c1.Journal(), InvariantResults: results})
			if err != nil {
				t.Fatalf("Seal: %v", err)
			}
			assertSealIntegrity(t, outcome, ArtifactLinkInconsistent, ArtifactInvariantResultSet)
		})
	}
}

// Production break caught: trusting a supplied state or malformed journal
// instead of replaying the accepted prefix would seal disconnected history.
func TestSealClassifiesJournalAndStateIntegrityDefects(t *testing.T) {
	binding, c1, c2 := checkpointExecutionFixture(t, testGoExecutor)

	corruptJournal := c1.Journal()
	corruptJournal.entries[0].digest = JournalEntryDigest("sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff")
	journalOutcome, err := Seal(SealRequest{Binding: binding, Checkpoint: "team_formed.v1", State: c1.State(), Journal: corruptJournal, InvariantResults: c1.InvariantResults()})
	if err != nil {
		t.Fatalf("Seal corrupt journal: %v", err)
	}
	assertSealIntegrity(t, journalOutcome, ArtifactDigestMismatch, ArtifactJournalEntry)

	incomplete, err := Seal(SealRequest{Binding: binding, Checkpoint: "team_hos_aggregated.v1", State: c1.State(), Journal: c1.Journal(), InvariantResults: c1.InvariantResults()})
	if err != nil {
		t.Fatalf("Seal incomplete journal: %v", err)
	}
	assertSealIntegrity(t, incomplete, ArtifactLinkInconsistent, ArtifactJournalPrefix)

	stateMismatch, err := Seal(SealRequest{Binding: binding, Checkpoint: "team_formed.v1", State: c2.State(), Journal: c1.Journal(), InvariantResults: c1.InvariantResults()})
	if err != nil {
		t.Fatalf("Seal state mismatch: %v", err)
	}
	assertSealIntegrity(t, stateMismatch, ReplayDivergence, ArtifactState)
}

// Production break caught: reporting a stale cached digest for corrupt bytes
// would implicate the earlier valid artifact instead of the supplied content.
func TestSealIntegrityReportsUseSuppliedContentDigest(t *testing.T) {
	binding, c1, _ := checkpointExecutionFixture(t, testGoExecutor)
	originalStateDigest := Digest(c1.State().Digest())
	corruptState := c1.State()
	corruptState.canonical = corruptCanonicalCopy(corruptState.canonical)
	outcome, err := Seal(SealRequest{Binding: binding, Checkpoint: "team_formed.v1", State: corruptState, Journal: c1.Journal(), InvariantResults: c1.InvariantResults()})
	if err != nil {
		t.Fatalf("Seal corrupt state: %v", err)
	}
	assertSealDigestEvidence(t, outcome, ArtifactState, canonicalDigest(corruptState.canonical), originalStateDigest)

	corruptBinding := binding
	corruptBinding.plan.canonical = corruptCanonicalCopy(corruptBinding.plan.canonical)
	outcome, err = Seal(SealRequest{Binding: corruptBinding, Checkpoint: "team_formed.v1", State: c1.State(), Journal: c1.Journal(), InvariantResults: c1.InvariantResults()})
	if err != nil {
		t.Fatalf("Seal corrupt plan: %v", err)
	}
	assertSealDigestEvidence(t, outcome, ArtifactPlan, canonicalDigest(corruptBinding.plan.canonical), Digest(binding.Plan().ID()))

	corruptBinding = binding
	corruptBinding.world.canonical = corruptCanonicalCopy(corruptBinding.world.canonical)
	outcome, err = Seal(SealRequest{Binding: corruptBinding, Checkpoint: "team_formed.v1", State: c1.State(), Journal: c1.Journal(), InvariantResults: c1.InvariantResults()})
	if err != nil {
		t.Fatalf("Seal corrupt world: %v", err)
	}
	assertSealDigestEvidence(t, outcome, ArtifactWorld, canonicalDigest(corruptBinding.world.canonical), Digest(binding.WorldID()))
}

// Production break caught: cached identities and canonical content have
// opposite expected/observed roles depending on which representation changed.
func TestSealIntegrityEvidenceDistinguishesCachedIdentityCorruption(t *testing.T) {
	binding, c1, _ := checkpointExecutionFixture(t, testGoExecutor)
	corrupt := Digest("sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff")
	tests := []struct {
		name   string
		kind   ArtifactKind
		mutate func(*RunBinding)
		bytes  func(RunBinding) []byte
	}{
		{name: "plan", kind: ArtifactPlan, mutate: func(b *RunBinding) { b.plan.id = PlanID(corrupt) }, bytes: func(b RunBinding) []byte { return b.plan.canonical }},
		{name: "initial state", kind: ArtifactState, mutate: func(b *RunBinding) { b.initialState.digest = StateDigest(corrupt) }, bytes: func(b RunBinding) []byte { return b.initialState.canonical }},
		{name: "world", kind: ArtifactWorld, mutate: func(b *RunBinding) { b.world.id = WorldID(corrupt) }, bytes: func(b RunBinding) []byte { return b.world.canonical }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			corruptBinding := binding
			test.mutate(&corruptBinding)
			outcome, err := Seal(SealRequest{Binding: corruptBinding, Checkpoint: "team_formed.v1", State: c1.State(), Journal: c1.Journal(), InvariantResults: c1.InvariantResults()})
			if err != nil {
				t.Fatalf("Seal: %v", err)
			}
			content := canonicalDigest(test.bytes(corruptBinding))
			assertSealDigestEvidenceValues(t, outcome, test.kind, content, content, corrupt)
		})
	}
}

// Production break caught: authoring order or executor build identity entering
// checkpoint semantics would make equivalent certified execution diverge.
func TestCheckpointIdentityIgnoresInvariantConstructionAndExecutorIdentity(t *testing.T) {
	firstBinding, firstC1, firstC2 := checkpointExecutionFixture(t, mustExecutorIdentityForTests("go", "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"))
	secondBinding, secondC1, secondC2 := checkpointExecutionFixture(t, mustExecutorIdentityForTests("go", "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"))
	if firstBinding.ExecutionID() == secondBinding.ExecutionID() {
		t.Fatal("executor change did not change ExecutionID")
	}

	for _, test := range []struct {
		name   string
		key    CheckpointKey
		first  TransitionOutcome
		second TransitionOutcome
	}{
		{name: "C1", key: "team_formed.v1", first: firstC1, second: secondC1},
		{name: "C2", key: "team_hos_aggregated.v1", first: firstC2, second: secondC2},
	} {
		t.Run(test.name, func(t *testing.T) {
			shuffled := test.first.InvariantResults()
			slices.Reverse(shuffled)
			first := mustSealedCheckpoint(t, SealRequest{Binding: firstBinding, Checkpoint: test.key, State: test.first.State(), Journal: test.first.Journal(), InvariantResults: shuffled})
			second := mustSealedCheckpoint(t, SealRequest{Binding: secondBinding, Checkpoint: test.key, State: test.second.State(), Journal: test.second.Journal(), InvariantResults: test.second.InvariantResults()})
			if first.CheckpointID() != second.CheckpointID() || first.ID() != second.ID() || first.Digest() != second.Digest() || !bytes.Equal(first.CanonicalBytes(), second.CanonicalBytes()) {
				t.Fatal("executor or invariant construction order changed checkpoint artifact")
			}
		})
	}
}

// Production break caught: permitting one claim identity to resolve to two
// manifests would make replay and publication evidence ambiguous.
func TestSealRejectsOneCheckpointIdentityWithTwoManifestDigests(t *testing.T) {
	binding, c1, _ := checkpointExecutionFixture(t, testGoExecutor)
	accepted := mustSealedCheckpoint(t, SealRequest{Binding: binding, Checkpoint: "team_formed.v1", State: c1.State(), Journal: c1.Journal(), InvariantResults: c1.InvariantResults()})
	conflict := accepted
	conflict.canonical = append(conflict.CanonicalBytes(), 0xff)
	conflict.digest = CheckpointArtifactDigest(canonicalDigest(conflict.canonical))

	outcome, err := Seal(SealRequest{Binding: binding, Checkpoint: "team_formed.v1", State: c1.State(), Journal: c1.Journal(), InvariantResults: c1.InvariantResults(), KnownArtifacts: []CheckpointArtifact{conflict}})
	if err != nil {
		t.Fatalf("Seal conflict: %v", err)
	}
	assertSealIntegrity(t, outcome, ArtifactLinkInconsistent, ArtifactCheckpoint)
	if _, ok := outcome.Artifact(); ok {
		t.Fatal("identity conflict returned a checkpoint artifact")
	}
	failure, _ := outcome.Failure()
	integrity, _ := failure.ArtifactIntegrity()
	if integrity.Artifact().Digest() != Digest(accepted.Digest()) {
		t.Fatalf("conflict implicated %s, want rejected candidate %s", integrity.Artifact().Digest(), accepted.Digest())
	}
	wantExpected, wantObserved := Digest(conflict.Digest()), Digest(accepted.Digest())
	if got, ok := integrity.ExpectedDigest(); !ok || got != wantExpected {
		t.Fatalf("expected digest=(%s,%t), want %s", got, ok, wantExpected)
	}
	if got, ok := integrity.ObservedDigest(); !ok || got != wantObserved {
		t.Fatalf("observed digest=(%s,%t), want %s", got, ok, wantObserved)
	}
	if got, ok := integrity.LastVerifiedCheckpointArtifactID(); !ok || got != conflict.ID() {
		t.Fatalf("last checkpoint=(%s,%t), want %s", got, ok, conflict.ID())
	}
	wantRefs := []ArtifactRef{{kind: ArtifactCheckpoint, digest: wantObserved}, {kind: ArtifactCheckpoint, digest: wantExpected}}
	slices.SortFunc(wantRefs, func(a, b ArtifactRef) int { return compare(string(a.digest), string(b.digest)) })
	if got := integrity.References(); !slices.Equal(got, wantRefs) {
		t.Fatalf("references=%v, want %v", got, wantRefs)
	}
}

// Production break caught: leaking internal byte slices would allow an
// already sealed manifest to mutate beneath its content digest.
func TestCheckpointArtifactsAndSealResultsAreImmutable(t *testing.T) {
	binding, c1, _ := checkpointExecutionFixture(t, testGoExecutor)
	results := c1.InvariantResults()
	artifact := mustSealedCheckpoint(t, SealRequest{Binding: binding, Checkpoint: "team_formed.v1", State: c1.State(), Journal: c1.Journal(), InvariantResults: results})
	wantBytes, wantDigest := artifact.CanonicalBytes(), artifact.Digest()

	results[0].passed = false
	returned := artifact.CanonicalBytes()
	returned[0] ^= 0xff
	checkpoint := artifact.Checkpoint()
	checkpoint.Key = "mutated.v1"

	if artifact.Digest() != wantDigest || !bytes.Equal(artifact.CanonicalBytes(), wantBytes) || artifact.Checkpoint().Key != "team_formed.v1" {
		t.Fatal("caller mutation changed sealed checkpoint")
	}
}

// Production break caught: changing a checkpoint tag, field order, replay
// link, formula input, or digest layer without a version migration would make
// an existing sealed artifact acquire different bytes or identity.
func TestCheckpointCanonicalVectors(t *testing.T) {
	binding, c1, c2 := checkpointExecutionFixture(t, testGoExecutor)
	tests := []struct {
		name           string
		key            CheckpointKey
		transition     TransitionOutcome
		checkpointHex  string
		checkpointID   CheckpointID
		claimHex       string
		artifactID     CheckpointArtifactID
		manifestHex    string
		artifactDigest CheckpointArtifactDigest
	}{
		{
			name: "C1", key: "team_formed.v1", transition: c1,
			checkpointHex:  "000000000000001c6d616964656e2d6c616e652e636865636b706f696e742d69642e763150209570a7d87e7ff39b24c6c9ab9cf8c6602967730716869b0e68949ff05b79000000000000000e7465616d5f666f726d65642e7631",
			checkpointID:   "sha256:1b51810bc824a16a189cd02919c6061c7539338a10b0cadd1aeb7e9b0ad3a5b8",
			claimHex:       "00000000000000256d616964656e2d6c616e652e636865636b706f696e742d61727469666163742d69642e76317c7903238e0817740175b0008990f6b0328b9a4bfa13295f490952326c8d1e461b51810bc824a16a189cd02919c6061c7539338a10b0cadd1aeb7e9b0ad3a5b801bbe3e8166806714cbffd76295a03cfb136c192df655fe04a046754241cf821d6c99a35a97bec79401b846de374f854bbdfd33d701846b498e054ecd97a1b4d826f94e0538b904b85ce415844b42f204cdfa042301d6c9f01b946db9f12365f903749613b90e317c3f9a84cb43676e8ce28a0daa23a19748884cf0a59b3f479",
			artifactID:     "sha256:7896471d41a3a2b40fd97621c19faf41f34101bd83238bc4d6d8b6d195aaa4d9",
			manifestHex:    "00000000000000226d616964656e2d6c616e652e636865636b706f696e742d61727469666163742e763150209570a7d87e7ff39b24c6c9ab9cf8c6602967730716869b0e68949ff05b791b51810bc824a16a189cd02919c6061c7539338a10b0cadd1aeb7e9b0ad3a5b8000000000000000e7465616d5f666f726d65642e7631000000000000000c666f726d5f7465616d2e76317c7903238e0817740175b0008990f6b0328b9a4bfa13295f490952326c8d1e468adc700baea26ec5c59e140c8d95495a4871e90fdc28d894c3c32043ddbcbfba3cda816df1b3f246fb50837e45eb27363b20113403f5c2d98edebbc92ec28536b12ff2816c22117ecc377d715fc4b509f5eb4f6420e4a0a1375d4cd9e9596057903749613b90e317c3f9a84cb43676e8ce28a0daa23a19748884cf0a59b3f47901bbe3e8166806714cbffd76295a03cfb136c192df655fe04a046754241cf821d6c99a35a97bec79401b846de374f854bbdfd33d701846b498e054ecd97a1b4d826f94e0538b904b85ce415844b42f204cdfa042301d6c9f01b946db9f12365f",
			artifactDigest: "sha256:3b6a50c1c83f6299d6e188b3535426241affb8c9d1a8664a0066facd862678e7",
		},
		{
			name: "C2", key: "team_hos_aggregated.v1", transition: c2,
			checkpointHex:  "000000000000001c6d616964656e2d6c616e652e636865636b706f696e742d69642e763150209570a7d87e7ff39b24c6c9ab9cf8c6602967730716869b0e68949ff05b7900000000000000167465616d5f686f735f616767726567617465642e7631",
			checkpointID:   "sha256:448d23e5023920e11cab67521f7b6f64c8ec5dd528c9b71cb5646e715f84f6ff",
			claimHex:       "00000000000000256d616964656e2d6c616e652e636865636b706f696e742d61727469666163742d69642e76317c7903238e0817740175b0008990f6b0328b9a4bfa13295f490952326c8d1e46448d23e5023920e11cab67521f7b6f64c8ec5dd528c9b71cb5646e715f84f6ffd5040ca5d33533417e18a044c1e5a1f8368b2d36b9ed560c1364cbced9e8db43f51c64069d669784e3af5d5953da2741f37e91fc9a86cbc66023150596996cbf5960f066a28ea5c600645a7aa088fdaa892783ca7bc63fb3bfd3b08ffd1d21c5903749613b90e317c3f9a84cb43676e8ce28a0daa23a19748884cf0a59b3f479",
			artifactID:     "sha256:9a445fac0799b6f33872d013e6e0a7fa5d219175a061d550617da19ce5e9b0bd",
			manifestHex:    "00000000000000226d616964656e2d6c616e652e636865636b706f696e742d61727469666163742e763150209570a7d87e7ff39b24c6c9ab9cf8c6602967730716869b0e68949ff05b79448d23e5023920e11cab67521f7b6f64c8ec5dd528c9b71cb5646e715f84f6ff00000000000000167465616d5f686f735f616767726567617465642e763100000000000000156167677265676174655f7465616d5f686f732e76317c7903238e0817740175b0008990f6b0328b9a4bfa13295f490952326c8d1e468adc700baea26ec5c59e140c8d95495a4871e90fdc28d894c3c32043ddbcbfba3cda816df1b3f246fb50837e45eb27363b20113403f5c2d98edebbc92ec28536b12ff2816c22117ecc377d715fc4b509f5eb4f6420e4a0a1375d4cd9e9596057903749613b90e317c3f9a84cb43676e8ce28a0daa23a19748884cf0a59b3f479d5040ca5d33533417e18a044c1e5a1f8368b2d36b9ed560c1364cbced9e8db43f51c64069d669784e3af5d5953da2741f37e91fc9a86cbc66023150596996cbf5960f066a28ea5c600645a7aa088fdaa892783ca7bc63fb3bfd3b08ffd1d21c5",
			artifactDigest: "sha256:116ff0c283ef091aee34e3302495006adcb4e4fc6edf91638e851976325a6d40",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			artifact := mustSealedCheckpoint(t, SealRequest{Binding: binding, Checkpoint: test.key, State: test.transition.State(), Journal: test.transition.Journal(), InvariantResults: test.transition.InvariantResults()})
			checkpointBytes, err := encodeCheckpointID(binding.Plan().ID(), test.key)
			if err != nil {
				t.Fatalf("encodeCheckpointID: %v", err)
			}
			claimBytes, err := encodeCheckpointArtifactID(binding.SemanticRunID(), artifact.CheckpointID(), artifact.StateDigest(), artifact.JournalPrefixDigest(), artifact.InvariantResultDigest(), binding.ProvenancePolicyID())
			if err != nil {
				t.Fatalf("encodeCheckpointArtifactID: %v", err)
			}
			if got := hex.EncodeToString(checkpointBytes); got != test.checkpointHex || artifact.CheckpointID() != test.checkpointID {
				t.Fatalf("checkpoint vector=(%s,%s), want (%s,%s)", got, artifact.CheckpointID(), test.checkpointHex, test.checkpointID)
			}
			if got := hex.EncodeToString(claimBytes); got != test.claimHex || artifact.ID() != test.artifactID {
				t.Fatalf("claim vector=(%s,%s), want (%s,%s)", got, artifact.ID(), test.claimHex, test.artifactID)
			}
			if got := hex.EncodeToString(artifact.CanonicalBytes()); got != test.manifestHex || artifact.Digest() != test.artifactDigest {
				t.Fatalf("manifest vector=(%s,%s), want (%s,%s)", got, artifact.Digest(), test.manifestHex, test.artifactDigest)
			}
		})
	}
}

// Production break caught: replay that merely trusts the supplied state would
// fail to establish that a sealed checkpoint is reproducible from pinned S0.
func TestCheckpointReplayReproducesC1AndC2(t *testing.T) {
	binding, c1, c2 := checkpointExecutionFixture(t, testGoExecutor)
	uninterruptedC1 := mustSealedCheckpoint(t, SealRequest{Binding: binding, Checkpoint: "team_formed.v1", State: c1.State(), Journal: c1.Journal(), InvariantResults: c1.InvariantResults()})
	uninterruptedC2 := mustSealedCheckpoint(t, SealRequest{Binding: binding, Checkpoint: "team_hos_aggregated.v1", State: c2.State(), Journal: c2.Journal(), InvariantResults: c2.InvariantResults()})
	replayedC1, replayedJournal1, issue := replayVerifiedJournal(binding, c1.Journal())
	if issue != nil {
		t.Fatalf("replay C1: %v", issue.code)
	}
	if !bytes.Equal(replayedC1.CanonicalBytes(), c1.State().CanonicalBytes()) {
		t.Fatal("S0 -> T1 replay did not reproduce C1")
	}
	replayedArtifactC1 := mustSealedCheckpoint(t, SealRequest{Binding: binding, Checkpoint: "team_formed.v1", State: replayedC1, Journal: replayedJournal1, InvariantResults: journalInvariantResults(replayedJournal1)})
	assertCheckpointArtifactEqual(t, replayedArtifactC1, uninterruptedC1)
	replayedC2, _, issue := replayVerifiedJournal(binding, c2.Journal())
	if issue != nil {
		t.Fatalf("replay C2: %v", issue.code)
	}
	if !bytes.Equal(replayedC2.CanonicalBytes(), c2.State().CanonicalBytes()) {
		t.Fatal("S0 -> T1 -> T2 replay did not reproduce C2")
	}
	t2 := mustAcceptedTransition(t, binding, "aggregate_team_hos.v1", replayedC1, replayedJournal1)
	if !bytes.Equal(t2.State().CanonicalBytes(), c2.State().CanonicalBytes()) {
		t.Fatal("lawful C1 -> T2 replay did not reproduce C2")
	}
	replayedArtifactC2 := mustSealedCheckpoint(t, SealRequest{Binding: binding, Checkpoint: "team_hos_aggregated.v1", State: t2.State(), Journal: t2.Journal(), InvariantResults: t2.InvariantResults()})
	assertCheckpointArtifactEqual(t, replayedArtifactC2, uninterruptedC2)
}

func checkpointExecutionFixture(t *testing.T, executor ExecutorIdentity) (RunBinding, TransitionOutcome, TransitionOutcome) {
	t.Helper()
	fields := passingDriverFields(t)
	plan, state, world := executionFixture(t, false, &fields)
	binding := mustBindRun(t, plan, state, world, executor)
	c1 := mustAcceptedTransition(t, binding, "form_team.v1", state, NewJournal())
	c2 := mustAcceptedTransition(t, binding, "aggregate_team_hos.v1", c1.State(), c1.Journal())
	return binding, c1, c2
}

func mustSealedCheckpoint(t *testing.T, request SealRequest) CheckpointArtifact {
	t.Helper()
	outcome, err := Seal(request)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	artifact, ok := outcome.Artifact()
	if !ok {
		t.Fatalf("Seal rejected: %s", sealFailureCode(outcome))
	}
	return artifact
}

func assertCheckpointArtifactEqual(t *testing.T, got, want CheckpointArtifact) {
	t.Helper()
	if got.CheckpointID() != want.CheckpointID() || got.ID() != want.ID() || got.Digest() != want.Digest() ||
		got.JournalPrefixDigest() != want.JournalPrefixDigest() || got.InvariantResultDigest() != want.InvariantResultDigest() ||
		!bytes.Equal(got.CanonicalBytes(), want.CanonicalBytes()) {
		t.Fatalf("replayed checkpoint differs: got=(%s,%s,%s), want=(%s,%s,%s)", got.CheckpointID(), got.ID(), got.Digest(), want.CheckpointID(), want.ID(), want.Digest())
	}
}

func assertSealIntegrity(t *testing.T, outcome SealOutcome, code IntegrityCode, kind ArtifactKind) {
	t.Helper()
	if outcome.Sealed() {
		t.Fatal("invalid checkpoint sealed")
	}
	failure, ok := outcome.Failure()
	if !ok {
		t.Fatal("seal refusal has no typed failure")
	}
	integrity, ok := failure.ArtifactIntegrity()
	if !ok || integrity.Code() != code || integrity.ArtifactKind() != kind {
		t.Fatalf("integrity=(%v,%v,%v), want (%v,%v,true)", integrity.Code(), integrity.ArtifactKind(), ok, code, kind)
	}
}

func assertSealDigestEvidence(t *testing.T, outcome SealOutcome, kind ArtifactKind, observed, expected Digest) {
	t.Helper()
	assertSealDigestEvidenceValues(t, outcome, kind, observed, expected, observed)
}

func assertSealDigestEvidenceValues(t *testing.T, outcome SealOutcome, kind ArtifactKind, artifact, expected, observed Digest) {
	t.Helper()
	assertSealIntegrity(t, outcome, ArtifactDigestMismatch, kind)
	failure, _ := outcome.Failure()
	integrity, _ := failure.ArtifactIntegrity()
	if integrity.Artifact().Digest() != artifact {
		t.Fatalf("artifact digest=%s, want supplied content %s", integrity.Artifact().Digest(), artifact)
	}
	if got, ok := integrity.ObservedDigest(); !ok || got != observed {
		t.Fatalf("observed digest=(%s,%t), want %s", got, ok, observed)
	}
	if got, ok := integrity.ExpectedDigest(); !ok || got != expected {
		t.Fatalf("expected digest=(%s,%t), want %s", got, ok, expected)
	}
}

func corruptCanonicalCopy(input []byte) []byte {
	result := bytes.Clone(input)
	result[len(result)-1] ^= 0xff
	return result
}

func sealFailureCode(outcome SealOutcome) string {
	failure, ok := outcome.Failure()
	if !ok {
		return ""
	}
	return failure.Code()
}
