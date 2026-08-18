package app

import (
	"testing"

	"github.com/optimaldynamics/maiden-lane/internal/ports"
)

// The gate refuses every candidate in this build, so Publish's authorize-and-advance
// path is unreachable from outside this package. These tests exercise the two helpers
// on that path directly.
//
// The alternative was a seam through which a test could supply an authorizing
// decision. That is refused deliberately: Publish calls promotion.Evaluate directly
// rather than through an injected function precisely so no caller — test or
// production — can hand it an authorization it did not compute. The cost is that
// this path needs unit tests instead of an end-to-end one, and untested code that
// becomes load-bearing the moment a clause is wired is the worse trade.

// Production break caught by construction: comparing whole structs would make every
// repeat look new, and the at-least-once execution delivery this system is built on
// would leave a target's history showing the same checkpoint published repeatedly.
// Nothing later could tell that from several real decisions.
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

	// Every other field must distinguish. A field this comparison ignored would let
	// a genuinely different decision be silently dropped as a retry, which is worse
	// than a duplicate: the publication would simply never happen.
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

// samePublication blanks both versions before comparing, which reads like a
// mutation of its arguments and is not one: it takes ports.Publication by value, so
// Go copies both and the caller's records cannot be reached. There is deliberately
// no test for that. Pass-by-value makes it unrepresentable rather than merely
// untrue, and a test asserting it could never fail — staticcheck said so, and was
// right. If either parameter ever becomes a pointer, this note is the reason to
// stop and reconsider rather than a suggestion to add the test back.

// Production break caught by construction: the record is only auditable because the
// decision can be re-derived from what it pins, so a helper that dropped or
// crossed a field would produce a complete-looking record that re-derives a
// different decision.
func TestPublicationForPinsTheEvidenceItWasGiven(t *testing.T) {
	// A zero Candidate and RunBinding are used deliberately: this asserts which
	// source each field is read from, and kernel accessors on a zero value return
	// empty strings rather than panicking. Validation refuses such a request before
	// this helper runs in production, so the values here only have to be
	// distinguishable, not real.
	policy := ports.TargetPolicy{
		TenantID: "acme", CustomerID: "cust", Target: "cm", Version: 7,
		RequiredProfileID: "sha256:required",
	}
	request := PublishRequest{TenantID: "acme", CustomerID: "cust", Target: "cm"}

	record := publicationFor(request, policy, 3)

	if record.Version != 3 {
		t.Fatalf("version = %d, want the 3 it was told to write", record.Version)
	}
	if record.PolicyVersion != policy.Version {
		t.Fatalf("policy version = %d, want the policy's %d", record.PolicyVersion, policy.Version)
	}
	// The profile pinned is the assessment's, not the policy's requirement. With a
	// zero assessment that is the empty string, which is exactly the distinction
	// being asserted: a helper reading the policy instead would put "sha256:required"
	// here and record what was asked for rather than what was true.
	if record.ProfileID == policy.RequiredProfileID {
		t.Fatal("the profile was taken from the policy's requirement rather than " +
			"from the assessment, so the record would state a requirement as a fact")
	}
	if record.TenantID != "acme" || record.CustomerID != "cust" || record.Target != "cm" {
		t.Fatalf("the key was not carried through: %+v", record)
	}
}

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
