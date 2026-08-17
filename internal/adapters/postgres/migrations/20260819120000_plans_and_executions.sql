-- Adopts the schema that existed before migrations did.
--
-- Every statement is idempotent, and that is load-bearing rather than
-- defensive: this migration must be applicable to a database whose tables were
-- already created by the adapter's former implicit CREATE TABLE on open. Running
-- it there records the version and changes nothing, which is what lets an
-- existing database join the migration history instead of having to be rebuilt.
--
-- Later migrations should NOT use IF NOT EXISTS. Once a database is under
-- migration control, a statement that silently does nothing hides a divergence
-- rather than adopting one.

-- migrate:up

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

CREATE TABLE IF NOT EXISTS executions (
    -- Tenancy is part of the primary key for the same reason as plans: an
    -- unscoped read is not expressible against this table.
    tenant_id    text NOT NULL,
    execution_id text NOT NULL,

    -- Both derived by the kernel from the pinned input, never allocated here.
    -- That is what makes enqueueing idempotent with no deduplication key: the
    -- same semantic request necessarily produces the same execution_id.
    run_id  text NOT NULL,
    plan_id text NOT NULL,

    status text NOT NULL,

    -- The adapter's encoding version, for the same reason plans carry one.
    -- This codec persists raw enum ordinals, so a renumbered kernel constant
    -- would make an old row decode into a DIFFERENT value while its content
    -- hash still matched, because the bytes never changed. Unlike a plan, an
    -- execution is not recompiled on read, so nothing else would catch it. A
    -- row written by a format this build does not understand is refused.
    format integer NOT NULL,

    -- The pinned semantic input, byte exact and self-describing: it carries the
    -- schema alongside the state so a worker can rehydrate it without consulting
    -- another table. bytea, never jsonb, for the reason given on plans.
    --
    -- The identity columns above are also covered by request_hash. Leaving them
    -- outside it would let an UPDATE alter execution_id or run_id while both
    -- hashes stayed valid, so a worker would execute a request bound to an
    -- identity the kernel never derived for that input.
    request      bytea NOT NULL,
    request_hash text  NOT NULL,

    -- The completed projection, present only once the execution finished. Its
    -- hash lets a read prove storage returned the bytes it was given.
    result      bytea,
    result_hash text,

    -- A bounded operational reason, distinct from a semantic rejection, which
    -- lives inside result because the computation produced a real answer.
    failure_reason text NOT NULL DEFAULT '',

    -- Operational state. A lease rather than a lock, because a worker can die;
    -- when it expires the execution becomes claimable again, which is safe
    -- because execution is deterministic.
    lease_expires_at timestamptz,
    enqueued_at      timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (tenant_id, execution_id)
);

-- Claiming scans only unfinished executions, so a queue that has processed a
-- large history does not slow down.
CREATE INDEX IF NOT EXISTS executions_claimable
    ON executions (enqueued_at)
    WHERE status IN ('pending', 'running');

-- migrate:down

-- Reversible on purpose, and destructive on purpose. These tables hold sealed
-- artifacts, which are the product rather than a cache: nothing recreates them
-- and no execution can be re-run to regenerate one, because a terminally
-- recorded execution cannot be cleared. This exists so a development database
-- can be torn down, and running it anywhere else destroys the record lineage
-- the system exists to protect.

DROP INDEX IF EXISTS executions_claimable;
DROP TABLE IF EXISTS executions;
DROP TABLE IF EXISTS plans;
