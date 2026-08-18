-- Publications: the versioned pointer recording what is published to a target,
-- and everything the authorization rested on (HLD §14.1).
--
-- Not idempotent, for the same reason the target policies migration is not: only
-- the initial migration had to adopt databases created before migrations existed,
-- and using IF NOT EXISTS here would let a divergence pass as if it had been
-- adopted.

-- migrate:up

CREATE TABLE publications (
    -- The publication key. §14.1 keys publication by tenant, customer, and target,
    -- and all three are in the primary key rather than columns to filter on, so a
    -- lookup that forgets one is not expressible against this table.
    tenant_id   text NOT NULL,
    customer_id text NOT NULL,
    target_key  text NOT NULL,

    -- Version is the compare-and-swap token. Rows are appended, never updated, so
    -- the current publication is the highest version and every lower one is a
    -- superseded publication that stays readable. That is what keeps an old
    -- decision explainable, and it is why PublicationStatus is derived rather than
    -- stored: a status column would be a second statement of what these versions
    -- already say, able to disagree with them.
    version bigint NOT NULL CHECK (version > 0),

    -- The target policy version that authorized this publication. Policies are
    -- immutable per version, so this resolves to the exact rule applied even after
    -- the target's policy advances. Deliberately not a foreign key: a policy lives
    -- in this database today, but the identity model does not require it to, and a
    -- constraint here would make retention of policy history a database invariant
    -- rather than the application promise it actually is.
    policy_version bigint NOT NULL CHECK (policy_version > 0),

    -- Every identity the authorization rested on. Each is NOT NULL and non-empty:
    -- a publication missing any one of them could not be re-derived, which is the
    -- only thing that makes it auditable.
    profile_id             text NOT NULL CHECK (profile_id <> ''),
    assessment_id          text NOT NULL CHECK (assessment_id <> ''),
    checkpoint_artifact_id text NOT NULL CHECK (checkpoint_artifact_id <> ''),
    semantic_run_id        text NOT NULL CHECK (semantic_run_id <> ''),
    execution_id           text NOT NULL CHECK (execution_id <> ''),

    -- The adapter's encoding version, for the same reason every other table
    -- carries one: a row written by a format this build does not understand is
    -- refused rather than guessed at.
    format integer NOT NULL,

    -- Operational metadata only. Nothing here participates in any identity, and
    -- nothing reads it to make a decision. A publication is ordered by its version,
    -- never by its clock: two clocks disagree and a version cannot.
    created_at timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (tenant_id, customer_id, target_key, version)
);

-- Resolving what is published to a target is reading its highest version, which the
-- primary key already orders within a target; a DESC scan of the key's tail answers
-- it and no separate index is needed.
--
-- "Where is this checkpoint published?" is a different question with no index here
-- yet, deliberately. It is a real question and it will want one, but adding an index
-- for a read nothing performs is guessing at a query shape.

-- migrate:down

DROP TABLE publications;
