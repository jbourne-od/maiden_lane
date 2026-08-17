package semantic

import (
	"bytes"
	"testing"
)

// Production break caught by construction: retaining the invariant-result witness
// on the artifact must not change any identity or digest. CheckpointArtifactID is
// derived from invariantResultDigest, and the manifest encoder must not read the
// retained bytes, or every previously sealed checkpoint's identity would move and
// every stored artifact would stop reproducing.
//
// This asserts it directly rather than relying on the rest of the suite passing:
// two artifacts differing ONLY in the retained witness must produce byte-identical
// manifests. If the encoder ever starts reading the field, this fails.
func TestTheRetainedWitnessIsOutsideEveryIdentity(t *testing.T) {
	binding, c1, _ := checkpointExecutionFixture(t, testGoExecutor)
	artifact := mustSealedCheckpoint(t, SealRequest{
		Binding:          binding,
		Checkpoint:       "team_formed.v1",
		State:            c1.State(),
		Journal:          c1.Journal(),
		InvariantResults: c1.InvariantResults(),
	})

	tampered := artifact
	tampered.invariantResultCanonical = []byte("bytes the encoder must never read")

	original, err := encodeCheckpointArtifact(artifact)
	if err != nil {
		t.Fatalf("encode original manifest: %v", err)
	}
	altered, err := encodeCheckpointArtifact(tampered)
	if err != nil {
		t.Fatalf("encode altered manifest: %v", err)
	}
	if !bytes.Equal(original, altered) {
		t.Fatal("the retained witness reached the manifest encoder, so it participates in identity")
	}
	if canonicalDigest(original) != canonicalDigest(altered) {
		t.Fatal("the retained witness changed the artifact digest")
	}
}

// The witness must actually be the bytes the artifact's digest was derived from.
// If it were anything else, verification would fail for every genuine seal, and
// the invariant-digest leg of the promotion gate could never pass.
func TestASealedArtifactCarriesTheWitnessItsDigestCommitsTo(t *testing.T) {
	binding, c1, c2 := checkpointExecutionFixture(t, testGoExecutor)
	for _, test := range []struct {
		name       string
		checkpoint CheckpointKey
		outcome    TransitionOutcome
	}{
		{"C1", "team_formed.v1", c1},
		{"C2", "team_hos_aggregated.v1", c2},
	} {
		t.Run(test.name, func(t *testing.T) {
			artifact := mustSealedCheckpoint(t, SealRequest{
				Binding:          binding,
				Checkpoint:       test.checkpoint,
				State:            test.outcome.State(),
				Journal:          test.outcome.Journal(),
				InvariantResults: test.outcome.InvariantResults(),
			})

			witness := artifact.InvariantResultCanonicalBytes()
			if len(witness) == 0 {
				t.Fatal("a sealed artifact carries no invariant witness")
			}
			if !VerifyInvariantResultDigest(witness, artifact.InvariantResultDigest()) {
				t.Fatal("the retained witness does not reproduce the committed digest")
			}
		})
	}
}

// The verifier must reject anything that is not the committed bytes, including
// absent evidence. Returning true for empty input would let missing evidence
// satisfy the check, which is the failure mode the promotion gate exists to
// prevent.
func TestVerifyInvariantResultDigestRejectsAnythingElse(t *testing.T) {
	binding, c1, _ := checkpointExecutionFixture(t, testGoExecutor)
	artifact := mustSealedCheckpoint(t, SealRequest{
		Binding:          binding,
		Checkpoint:       "team_formed.v1",
		State:            c1.State(),
		Journal:          c1.Journal(),
		InvariantResults: c1.InvariantResults(),
	})
	committed := artifact.InvariantResultDigest()
	witness := artifact.InvariantResultCanonicalBytes()

	for _, test := range []struct {
		name      string
		canonical []byte
	}{
		{"absent evidence", nil},
		{"empty evidence", []byte{}},
		{"unrelated bytes", []byte("not the committed results")},
		{"truncated witness", witness[:len(witness)-1]},
		{"witness with one byte altered", alterLastByte(witness)},
	} {
		t.Run(test.name, func(t *testing.T) {
			if VerifyInvariantResultDigest(test.canonical, committed) {
				t.Fatal("verification accepted bytes the artifact never committed to")
			}
		})
	}

	// A witness from a different checkpoint must not verify against this one, or
	// evidence could be transplanted between checkpoints.
	other := mustSealedCheckpoint(t, SealRequest{
		Binding:          binding,
		Checkpoint:       "team_hos_aggregated.v1",
		State:            mustSecondOutcome(t, binding).State(),
		Journal:          mustSecondOutcome(t, binding).Journal(),
		InvariantResults: mustSecondOutcome(t, binding).InvariantResults(),
	})
	if VerifyInvariantResultDigest(other.InvariantResultCanonicalBytes(), committed) {
		t.Fatal("another checkpoint's witness verified against this checkpoint's commitment")
	}
}

// The accessor must hand out a copy. A caller able to write through it could
// alter the evidence a later verification reads, inside one process.
func TestTheWitnessAccessorReturnsACopy(t *testing.T) {
	binding, c1, _ := checkpointExecutionFixture(t, testGoExecutor)
	artifact := mustSealedCheckpoint(t, SealRequest{
		Binding:          binding,
		Checkpoint:       "team_formed.v1",
		State:            c1.State(),
		Journal:          c1.Journal(),
		InvariantResults: c1.InvariantResults(),
	})

	returned := artifact.InvariantResultCanonicalBytes()
	for i := range returned {
		returned[i] ^= 0xff
	}
	if !VerifyInvariantResultDigest(artifact.InvariantResultCanonicalBytes(),
		artifact.InvariantResultDigest()) {
		t.Fatal("writing through the accessor corrupted the artifact's witness")
	}
}

// Production break caught: struct assignment copies a slice header and shares its
// backing array, so a clone that forgets the new field leaves two artifacts
// aliasing one buffer.
func TestCloningAnArtifactDoesNotAliasItsWitness(t *testing.T) {
	binding, c1, _ := checkpointExecutionFixture(t, testGoExecutor)
	artifact := mustSealedCheckpoint(t, SealRequest{
		Binding:          binding,
		Checkpoint:       "team_formed.v1",
		State:            c1.State(),
		Journal:          c1.Journal(),
		InvariantResults: c1.InvariantResults(),
	})

	clone := cloneCheckpointArtifact(artifact)
	for i := range clone.invariantResultCanonical {
		clone.invariantResultCanonical[i] ^= 0xff
	}
	if !VerifyInvariantResultDigest(artifact.InvariantResultCanonicalBytes(),
		artifact.InvariantResultDigest()) {
		t.Fatal("mutating a clone's witness altered the original's")
	}
}

func alterLastByte(input []byte) []byte {
	altered := bytes.Clone(input)
	if len(altered) > 0 {
		altered[len(altered)-1] ^= 0xff
	}
	return altered
}

func mustSecondOutcome(t *testing.T, _ RunBinding) TransitionOutcome {
	t.Helper()
	_, _, c2 := checkpointExecutionFixture(t, testGoExecutor)
	return c2
}
