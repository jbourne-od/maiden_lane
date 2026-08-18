package semantic

import (
	"bytes"
	"fmt"
	"slices"
	"sort"
)

// Corpus is an immutable, canonically ordered set of replay cases.
//
// HLD §14.2 makes a corpus one of the inputs a promotion comparison's identity is
// derived from, which is why it is content-addressed rather than a named collection an
// operator maintains. Clause 6 of §14.1 requires a baseline and a candidate to have run
// over *the same* replay corpus; if a corpus were mutable under a name, sameness would
// be a claim about a label, two sides could satisfy the clause having run over different
// sets, and every comparison ever made against that name would retroactively be about
// something else. Deriving the identity from the contents makes sameness provable, and
// gives the right failure: adding a case produces a different CorpusID, so a comparison
// over the enlarged corpus is visibly a different comparison rather than the old one
// with better numbers.
//
// A corpus holds cases as States rather than as state digests. Accepting digests would
// let a CorpusID be derived over content nobody has, and every digest here is then one
// the kernel computed from a state it validated. It also makes the value useful: running
// a side over a corpus needs the states, and a corpus of hashes could only be compared,
// never replayed.
//
// The world is deliberately NOT part of a corpus. §14.2 lists WorldID and CorpusID as
// separate inputs to Compare, and InputID = H(StateDigest, WorldID), so a corpus is the
// set of cases and the world is pinned once for the whole comparison. Together they
// determine every case's InputID, which is what makes "the same corpus under the same
// historical world" checkable rather than two labels that happen to match.
type Corpus struct {
	cases     []State
	digests   []StateDigest
	canonical []byte
	id        CorpusID
}

// NewCorpus validates, canonically orders, and identifies a set of replay cases.
//
// Ordering is by state digest rather than by the order the caller supplied. A corpus is
// a set, so two operators assembling the same cases in different orders must produce the
// same identity; otherwise the clause that compares corpora becomes sensitive to the
// order somebody typed things in.
func NewCorpus(cases []State) (Corpus, error) {
	if len(cases) == 0 {
		// An empty corpus is not a real artifact the way an empty world is. An empty
		// world is a genuine statement — nothing was pinned — while a comparison over
		// no cases establishes nothing about a candidate and must not be able to
		// satisfy a clause by being vacuously true.
		return Corpus{}, fmt.Errorf("replay corpus has no cases")
	}

	normalized := slices.Clone(cases)
	for _, state := range normalized {
		if err := verifyState(state); err != nil {
			// A state whose digest does not match its own bytes cannot contribute a
			// trustworthy case, and this is the only place it can be caught before its
			// digest enters an identity.
			return Corpus{}, fmt.Errorf("replay corpus case: %w", err)
		}
	}
	sort.Slice(normalized, func(i, j int) bool {
		return normalized[i].Digest() < normalized[j].Digest()
	})

	digests := make([]StateDigest, 0, len(normalized))
	for i, state := range normalized {
		if i > 0 && state.Digest() == normalized[i-1].Digest() {
			// Refused rather than deduplicated. A duplicate means the caller believes
			// something about this corpus that is not true — most likely that it has
			// more distinct cases than it does — and silently collapsing it would
			// answer a different question than the one asked while looking like it
			// answered this one.
			return Corpus{}, fmt.Errorf("replay corpus contains a duplicate case")
		}
		digests = append(digests, state.Digest())
	}

	var encoder canonicalEncoder
	encoder.tag(corpusDomainTag)
	encoder.uint64(uint64(len(digests)))
	for _, digest := range digests {
		encoder.digest(string(digest))
	}
	canonical, err := encoder.bytes()
	if err != nil {
		return Corpus{}, fmt.Errorf("canonicalize replay corpus: %w", err)
	}

	return Corpus{
		cases:     normalized,
		digests:   digests,
		canonical: canonical,
		id:        CorpusID(canonicalDigest(canonical)),
	}, nil
}

// ID returns the content identity of the canonical corpus.
func (c Corpus) ID() CorpusID { return c.id }

// Len reports how many cases the corpus holds.
//
// It exists so a caller can size work without copying every case, which matters here in
// a way it does not for a world: a corpus is expected to hold hundreds of states, and
// each case's accessor returns a deep copy.
func (c Corpus) Len() int { return len(c.cases) }

// Digests returns the canonically ordered case digests.
//
// This is the cheap view, and the one anything reasoning about identity should use.
func (c Corpus) Digests() []StateDigest { return slices.Clone(c.digests) }

// Case returns one case in canonical order, or reports that the index is outside the
// corpus. It is indexed rather than returning the whole set because copying every state
// to reach one would make iterating a corpus quadratic in its own size.
func (c Corpus) Case(index int) (State, bool) {
	if index < 0 || index >= len(c.cases) {
		return State{}, false
	}
	return c.cases[index], true
}

// CanonicalBytes returns a copy of the v1 corpus bytes.
func (c Corpus) CanonicalBytes() []byte { return bytes.Clone(c.canonical) }

// InputIdentities returns each case's InputID under a pinned world, in canonical order.
//
// This is the operation the corpus exists for. InputID = H(StateDigest, WorldID), so a
// corpus and a world together name exactly the set of inputs a side must run over, and
// both sides of a comparison naming the same corpus and world are provably running over
// the same inputs rather than asserting that they are.
func (c Corpus) InputIdentities(world World) ([]InputID, error) {
	identities := make([]InputID, 0, len(c.digests))
	for _, digest := range c.digests {
		encoded, err := encodeInputIdentity(digest, world.ID())
		if err != nil {
			return nil, fmt.Errorf("replay corpus input identity: %w", err)
		}
		identities = append(identities, InputID(canonicalDigest(encoded)))
	}
	return identities, nil
}
