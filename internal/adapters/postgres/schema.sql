-- Maiden Lane control-plane schema.
--
-- Applied by the adapter on Open. It is idempotent, so a process starting
-- against an existing database is a no-op rather than an error.

CREATE TABLE IF NOT EXISTS plans (
    -- Tenancy is part of the primary key rather than a column to filter on.
    -- An unscoped read is therefore not expressible against this table: there
    -- is no index that answers "give me plan X" without naming its owner.
    tenant_id text NOT NULL,

    -- The plan identity is content derived by the semantic kernel. Storing it
    -- as the key means the database enforces "one identity resolves to one
    -- content" for us, rather than that invariant living only in application
    -- code.
    plan_id text NOT NULL,

    -- The canonical identity of the declarations below, as the kernel computed
    -- it at compile time. A read recompiles and requires both this and the plan
    -- identity to match, which is what makes a corrupted or silently re-encoded
    -- row impossible to return under the identity it claims.
    input_digest text NOT NULL,

    -- The adapter's own encoding version. It exists so a future change to the
    -- stored representation is detectable rather than misread. A row written by
    -- a format this build does not understand is refused, not guessed at.
    format integer NOT NULL,

    -- The declarations, byte exact.
    --
    -- This is deliberately bytea and not jsonb. Postgres jsonb reorders object
    -- keys, drops duplicates, and normalizes numeric forms; for a system whose
    -- identities derive from exact canonical bytes, that is a silent mutation of
    -- the recipe. bytea keeps the bytes this adapter wrote, and the identity
    -- check on read would fail loudly if anything altered them.
    --
    -- Do not migrate this column to jsonb to make it queryable in SQL. If the
    -- declarations need to be searchable, add derived operational columns
    -- alongside it and leave the authoritative bytes alone.
    declarations bytea NOT NULL,

    -- Operational metadata only. Nothing here participates in any identity, so
    -- jsonb would be acceptable for columns of this character.
    created_at timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (tenant_id, plan_id)
);
