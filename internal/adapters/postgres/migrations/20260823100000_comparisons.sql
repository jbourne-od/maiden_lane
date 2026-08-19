-- Comparison questions: the promotion comparison a corpus is replayed to answer
-- (HLD §14.2).
--
-- Not idempotent, for the same reason the corpora, target-policy and publication
-- migrations are not: only the initial migration had to adopt databases created before
-- migrations existed.

-- migrate:up

CREATE TABLE comparisons (
    -- Tenancy is in the primary key rather than a column to filter on, so a lookup that
    -- forgets it is not expressible against this table.
    tenant_id text NOT NULL,

    -- The content identity of the QUESTION. There is no version column and no
    -- updated_at, because a comparison cannot be edited: comparing anything else is a
    -- different question and both rows must remain readable.
    --
    -- The executions and checkpoint artifacts that eventually ANSWER it are deliberately
    -- not here and not in the identity. Folding evidence into the question would make a
    -- comparison's name change every time it was re-evidenced.
    comparison_id text NOT NULL,

    -- HLD §14.2's five inputs. Baseline and candidate are checkpoint DECLARATIONS, not
    -- realized artifacts: a comparison over n cases has n realizations of each side.
    baseline_checkpoint_id text NOT NULL CHECK (baseline_checkpoint_id <> ''),
    candidate_checkpoint_id text NOT NULL CHECK (candidate_checkpoint_id <> ''),
    profile_id text NOT NULL CHECK (profile_id <> ''),
    world_id text NOT NULL CHECK (world_id <> ''),
    corpus_id text NOT NULL CHECK (corpus_id <> ''),

    -- The comparison policy, which §14.2 requires to participate in the identity. Its
    -- own identity is stored beside the two plans it describes, because a CheckpointID
    -- is a digest: it commits to its plan without saying which plan that is, and a
    -- reader needs to know before it can decide whether this policy describes the
    -- comparison it is holding.
    policy_id text NOT NULL CHECK (policy_id <> ''),
    baseline_plan_id text NOT NULL CHECK (baseline_plan_id <> ''),
    candidate_plan_id text NOT NULL CHECK (candidate_plan_id <> ''),

    -- Operational metadata only. Nothing here participates in any identity.
    created_at timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (tenant_id, comparison_id)
);

-- There is deliberately NO constraint requiring the two checkpoint sides to differ.
-- An earlier draft had one, on the belief that the kernel already refuses a comparison
-- of a checkpoint with itself. It does not: NewComparisonPolicy requires each side to be
-- declared and the mapping to be one-to-one, and NewComparison requires the policy to
-- declare the supplied pair, so one plan mapped to itself is valid kernel state.
--
-- Such a comparison is degenerate for ordinary candidate promotion but it is not
-- nonsensical, because the two sides are still answered by different executions: an
-- ExecutionID is deliberately outside ComparisonID, so the same declaration can be
-- realized by different executors or backends. Storage must not invent a semantic
-- exclusion the kernel does not make, or one adapter accepts a valid comparison the
-- other refuses. If this exclusion is ever wanted it belongs in the kernel first.

-- There is no foreign key to plans, corpora or profiles, and that is deliberate. Those
-- rows are content addressed and never deleted, so a reference cannot dangle by ordinary
-- operation; and a comparison whose plan really has gone must still be READABLE, so that
-- rehydration can report precisely which component is missing rather than the row
-- vanishing from the table.

CREATE TABLE comparison_correspondences (
    tenant_id text NOT NULL,
    comparison_id text NOT NULL,

    -- The canonical position, so the projection a store returns matches the one the
    -- kernel produced. It is presentation rather than content: rehydration re-sorts
    -- whatever it is given, so a wrong position cannot change what a row rebuilds into.
    -- It is stored so that two adapters describe one comparison identically.
    position integer NOT NULL CHECK (position >= 0),

    baseline_checkpoint_id text NOT NULL CHECK (baseline_checkpoint_id <> ''),
    candidate_checkpoint_id text NOT NULL CHECK (candidate_checkpoint_id <> ''),

    PRIMARY KEY (tenant_id, comparison_id, position),

    FOREIGN KEY (tenant_id, comparison_id)
        REFERENCES comparisons (tenant_id, comparison_id) ON DELETE CASCADE,

    -- The mapping is one-to-one in BOTH directions, which the kernel requires: one
    -- baseline corresponding to two candidates makes "corresponding" ambiguous, and a
    -- comparison cannot fail closed on an ambiguity something silently resolved by
    -- picking the first match. The kernel refuses to build such a policy; these say the
    -- table cannot hold one either.
    UNIQUE (tenant_id, comparison_id, baseline_checkpoint_id),
    UNIQUE (tenant_id, comparison_id, candidate_checkpoint_id)
);

-- migrate:down

DROP TABLE comparison_correspondences;
DROP TABLE comparisons;
