# Replay corpus and comparison identity

**Status:** Active. Authority is the High-Level Design §6, §14.1, and §14.2, and
the ratified decisions recorded in the publication-and-promotion programme. Where
this document and the HLD disagree, the HLD wins.

This is the first of the three programmes that stand between the promotion gate and
ever authorizing anything. It delivers §14.1's sixth clause — "baseline and candidate
checkpoint executions over the same replay corpus, corresponding checkpoint semantics,
and completeness profile" — and the comparison identity §14.2 requires.

## The decomposition that makes this tractable

Clause 6 and clause 7 are usually thought of as one thing, "did the change make things
worse", and they are not. Reading them apart is what lets this programme ship without
inventing a metric system:

- **Clause 6 is a comparability precondition.** It asks whether a valid comparison
  exists: the two sides ran over the same corpus, under the same completeness profile,
  in the same world, with an explicit correspondence between the checkpoint semantics
  being compared. It says nothing about the outcome.
- **Clause 7 is the outcome.** "No protected metric regression" needs a protected-metric
  concept and a regression policy, neither of which exists.

So this programme establishes that a comparison is *meaningful*, and the metrics
programme that follows establishes what it *says*. A comparison whose comparability
cannot be established is worthless whatever the metrics show, which is why this is
first.

## What the HLD fixes, and what it leaves to be decided

§14.2 states:

> Compare(C_baseline, C_candidate, ProfileID, WorldID, CorpusID)
>
> Those inputs and the comparison policy participate in comparison identity.
> Comparing an optimizer-ready baseline to a merely CM-ready candidate cannot support
> promotion. Plans under comparison may have different `PlanID` values, but the
> comparison contract must explicitly map semantically corresponding checkpoint
> declarations and fail closed when no correspondence exists.

Fixed by the spec: the five inputs, that the comparison policy participates in
identity, that differing `PlanID`s are legitimate, and that correspondence is explicit
and fails closed.

Left to be decided, and decided here:

### 1. A corpus is content-addressed, not a named collection

`CorpusID` is derived from the corpus's contents. The alternative — a named, versioned
collection like a target policy — was rejected, and the reason is the clause's own
wording. Clause 6 requires the two sides to have run over *the same* replay corpus. If a
corpus were mutable under a name, "the same corpus" would be a claim about a label, and
two executions could satisfy the clause while having run over different sets of cases. A
corpus that silently gained a case would keep its name, and every comparison ever made
against it would be retroactively about something else.

This is the same discipline as everywhere else here: identities are derived so that
sameness is provable rather than asserted. It also gives the right failure — adding a
case produces a different `CorpusID`, so a comparison over the enlarged corpus is
visibly a different comparison rather than the old one with better numbers.

Note this differs from the target-policy decision in the previous programme, and the
difference is in the spec rather than in taste. §14.1 says a publication record pins the
policy *version*, explicitly choosing a version over a derived identity. §14.2 says the
corpus participates in a derived *identity*. The specification made both calls; this
document is only following them.

### 2. A corpus is a set of case states; the world is pinned once

`InputID = H(StateDigest, WorldID)`, and §14.2 lists `WorldID` and `CorpusID` as
separate inputs to `Compare`. Both point the same way: a corpus is a set of initial
states, and the whole comparison is replayed against one pinned world.

\[
CorpusID = H(\text{ordered } StateDigest_i)
\]

Together `CorpusID` and `WorldID` therefore determine every case's `InputID`, which is
what makes "the same corpus under the same historical world" a checkable statement
rather than two labels that happen to match. The phrase §14.2 actually uses —
"historical world/corpus" — is one concept, and this is what it decomposes into.

The ordering must be canonical, not insertion order. A corpus is a set; two operators
assembling the same cases in different orders must produce the same identity, or the
clause becomes sensitive to the order somebody typed things in.

### 3. Comparison is a derived semantic artifact, of the same kind as an assessment

§6 enumerates every derived identity in the system and neither `CorpusID` nor
`ComparisonID` appears. In the previous programme that absence was decisive: a target
policy was not in §6, so it is a version rather than an identity. Here it is not
decisive, because §14.2 says in as many words that the inputs "participate in comparison
identity". The specification asserts the identity exists; §6 simply does not enumerate
it, in the same way §6 covers completeness assessment and §14.2 covers comparison.

The shape follows `AssessmentID = H(CheckpointArtifactID, ProfileID)`, which is the
existing precedent for "a derived judgment about artifacts":

\[
ComparisonID = H(
CheckpointID_{baseline},
CheckpointID_{candidate},
ProfileID,
WorldID,
CorpusID,
ComparisonPolicyID)
\]

The two sides are `CheckpointID`s, not `CheckpointArtifactID`s, and that is the crux.
`CheckpointID = H(PlanID, CheckpointKey)` identifies a checkpoint *declaration* — a
piece of semantics — while `CheckpointArtifactID` identifies one realized checkpoint of
one run. A comparison over a corpus has no single artifact per side; it has one per case.
So the thing being compared is the semantics, evaluated across the corpus, which is
exactly what §14.2's phrase "corresponding checkpoint semantics" says.

### 4. Correspondence is declared, never inferred

§14.2 requires the contract to "explicitly map semantically corresponding checkpoint
declarations and fail closed when no correspondence exists". Inference by name is
forbidden and the reason is worth stating: two plans may legitimately name the same
semantics differently, and — far worse — may name different semantics the same. A
comparison that matched `team_hos_aggregated.v1` to `team_hos_aggregated.v1` across two
plans would be right most of the time, and the times it was wrong would be
indistinguishable from the times it was right.

So the comparison policy carries an explicit map, and a checkpoint on either side with
no counterpart is a refusal rather than a skipped row. `ComparisonPolicyID` is derived
from that map, so a comparison cannot be reinterpreted under a different correspondence
after the fact.

### 5. The profile is compared, not merely recorded

§14.2: "Comparing an optimizer-ready baseline to a merely CM-ready candidate cannot
support promotion." One `ProfileID` participates in the identity, and both sides must be
assessed `ready` under *that* profile. This is the same distinction the readiness clause
already draws: an assessment under a different profile establishes nothing about the one
required, so it is `not_evaluated` rather than a pass.

## What this costs

A corpus of *n* cases requires *n* executions per side. That is the headline cost, and
the rest of this section exists because an earlier draft of it was wrong in a way that
would have become an architectural assumption.

That draft said a corpus costs *n* executions once and comparisons over it afterwards
cost lookups. It does not, and the reason is a decision this programme inherits rather
than one it can revisit. Comparison must consume authenticated artifacts, and the
rehydration slice established that recovering authenticated kernel values from stored
history means **re-executing the stored inputs**: kernel values cannot be rebuilt from
bytes, and recomputing an identity from stored components proves only that a stored
tuple agrees with itself. So the true shape is:

| | cost |
|---|---|
| A case never executed | one execution, which produces and stores it |
| A case already executed, compared later | a lookup **plus a deterministic re-execution to authenticate it** |

A comparison performed immediately, in the process that ran the cases, can use the live
kernel artifacts it already holds. A comparison performed later, over persisted history,
cannot — so comparing two sides of a persisted corpus costs roughly **2n re-executions**,
not 2n once followed by cheap lookups.

Three distinctions must stay separate, because collapsing them is how the wrong version
of this paragraph gets rebuilt as infrastructure:

1. **Execution reuse.** A derived `ExecutionID` means a case already executed is never
   enqueued or stored a second time. This is real and it is what makes a corpus
   accumulate rather than repeat.
2. **Authentication cost.** Recovering kernel artifacts from stored history currently
   costs a re-execution. This is not a missing optimization; it is the price of refusing
   to let a stored projection carry authorization weight.
3. **Any future cache.** Something that makes authentication cheaper would need its own
   trust argument, and it may not simply promote the stored projection to authority. That
   is the option already rejected, and it does not become correct by being called a cache.

None of this makes the programme intractable. Hundreds of deterministic executions are
affordable, and determinism is precisely what makes them safe to repeat. But the
affordability argument has to rest on the real number, because a later slice that
"optimizes" comparison by reading stored results would be reintroducing the trust model
this system spent three slices removing.

## Where this code belongs

- **`internal/semantic`** gains the corpus and comparison identities and their canonical
  encoders, because they are content-derived semantic identities and nothing else in this
  system is allowed to derive one. One-way as always: encoders, no decoders.
- **`internal/promotion`** gains the comparability evaluation, which is pure: given two
  sides' artifacts, a corpus, a profile, and a correspondence, does a valid comparison
  exist? It reaches no store, exactly like the rest of the gate.
- **`internal/ports` and the adapters** gain corpus and comparison persistence. A corpus
  is a set somebody curated and it has to survive a restart; a comparison is a derived
  record worth keeping so `GET /v1/comparisons/{comparisonID}` can answer.
- **`internal/app`** gains the orchestration: assemble a corpus, run a side over it,
  evaluate comparability.

## Slices

**Slice 1 — corpus identity in the kernel.** `CorpusID` derived from a canonically
ordered set of state digests, with a domain-tagged versioned encoder like every other
identity here. Includes the assertion that assembly order does not affect identity, and
that a corpus with duplicate cases is refused rather than silently deduplicated — a
duplicate means the caller believes something about the corpus that is not true.

**Slice 2 — the comparison contract.** The explicit checkpoint correspondence, its
identity, and failing closed on an unmapped checkpoint on either side. This slice is
where "no correspondence exists" becomes a refusal with a reason rather than an empty
result.

**Slice 3 — corpus persistence.** A corpus behind `ports`, both adapters, one contract
suite. Append-only: a corpus is immutable once identified, because its identity is its
contents, so "editing" one produces a different corpus and both must remain readable.

**Slice 4 — running a side over a corpus.** Enqueueing every case for one plan and
profile, and reporting which cases have completed. This is the expensive slice and the
one where determinism does the work: a case already executed is already done.

**Slice 5 — comparability evaluation, and clause 6.** The pure evaluation, then wiring
`ClauseComparisonCorpus`. After this the gate answers seven of nine, and still refuses.

**Slice 6 — the HTTP surface.** `POST /v1/comparisons` and
`GET /v1/comparisons/{comparisonID}`, both already in §16's list.

## Constraints carried from earlier programmes

- **No projection may carry authorization weight.** A stored comparison record is
  evidence about history, not authority. Clause 6 must be established from re-derived
  artifacts, never from a stored comparison verdict, for the same reason the publication
  record stores no gate verdict.
- **Fail closed structurally, not by discipline.** The zero value of every new verdict
  refuses; closed vocabularies are walked rather than supplied maps; illegal states are
  unrepresentable through unexported fields and constructors.
- **One-way kernel encoding.** New identities get encoders and no decoders. If a stored
  corpus must come back as kernel values, it comes back through a validating constructor
  and its identity is re-derived and required to match — the pattern plans already use.
- **A refusal is an answer.** Comparability that cannot be established is a reported
  result with a reason, not an error and not an empty success.
- **A comparison identity names the question, not the evidence.** `ComparisonID`
  identifies the semantic comparison being asked: two checkpoint declarations, a profile,
  a world, a corpus, and a correspondence. The *n* `ExecutionID`s and
  `CheckpointArtifactID`s that answered it are evidence, and must not be folded into it
  for auditability. Doing so would collapse question and evidence — the same category
  error as storing a gate verdict beside the artifacts it summarizes — and would make a
  comparison's identity change every time it was re-evidenced.
- **Deliberate omissions get written down.** Anything this programme could plausibly
  include and does not — protected metrics above all — is recorded as absent rather than
  left to be assumed present.
