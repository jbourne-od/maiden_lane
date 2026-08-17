-- Target policies: the immutable, versioned rule a destination applies before a
-- sealed checkpoint may publish to it (HLD §14.1).
--
-- Deliberately not idempotent. The previous migration used IF NOT EXISTS because
-- it had to adopt databases created before migrations existed; this one does not,
-- and using it here would let a divergence pass silently as if it had been
-- adopted.

-- migrate:up

CREATE TABLE target_policies (
    -- All three parts of the publication key are in the primary key rather than
    -- columns to filter on, for the same reason tenancy is on plans: a lookup
    -- that forgets one is not expressible against this table. Publication is
    -- keyed by tenant, customer, and target, so a policy scoped by any less than
    -- that could authorize a publication it was never written for.
    tenant_id   text NOT NULL,
    customer_id text NOT NULL,
    target_key  text NOT NULL,

    -- Version orders one target's policies. It is a version rather than a
    -- content-derived identity on purpose: the ratified identity model in HLD §6
    -- enumerates every derived identity in this system and a target policy is not
    -- among them, because it is control-plane configuration naming which semantic
    -- contract a destination demands rather than a semantic artifact itself.
    --
    -- A publication record pins the version that authorized it, which is why rows
    -- here are never updated or deleted. CHECK enforces what the port promises,
    -- so a direct SQL write cannot introduce a version zero that application code
    -- would have refused.
    version bigint NOT NULL CHECK (version > 0),

    -- The compiled completeness profile a checkpoint must hold a `ready`
    -- assessment under. A ProfileID rather than a profile key: a key is a name an
    -- author chose and can reuse, while this identifies one specific compiled
    -- contract, so the requirement cannot change underneath a publication that
    -- was already authorized by it.
    required_profile_id text NOT NULL,

    -- The adapter's encoding version, for the same reason plans and executions
    -- carry one: a row written by a format this build does not understand is
    -- refused rather than guessed at.
    format integer NOT NULL,

    -- Operational metadata only. Nothing here participates in any identity.
    created_at timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (tenant_id, customer_id, target_key, version)
);

-- Resolving a target's active policy is reading its highest version. The primary
-- key already orders by version within a target, so this needs no separate index;
-- a DESC scan of the key's tail answers it.

-- migrate:down

DROP TABLE target_policies;
