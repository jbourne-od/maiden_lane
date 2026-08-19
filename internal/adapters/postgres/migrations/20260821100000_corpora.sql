-- Replay corpora: the content-addressed set of cases a promotion comparison runs both
-- sides over (HLD §14.2).
--
-- Not idempotent, for the same reason the target-policy and publication migrations are
-- not: only the initial migration had to adopt databases created before migrations
-- existed.

-- migrate:up

CREATE TABLE corpora (
    -- Tenancy is in the primary key rather than a column to filter on, so a lookup that
    -- forgets it is not expressible against this table.
    tenant_id text NOT NULL,

    -- The content identity. There is no version column and no updated_at, because a
    -- corpus cannot be edited: its identity IS its contents, so different cases are a
    -- different corpus and both rows must remain readable. Comparisons pin a CorpusID,
    -- and deleting the row it names would leave them describing a set of cases nothing
    -- can reconstruct.
    corpus_id text NOT NULL,

    -- The schema every case shares. Stored once rather than per case because the kernel
    -- refuses a corpus whose cases do not share one, which is itself required: BindRun
    -- rejects a state whose schema digest is not the plan's, so a mixed-schema corpus
    -- could never be replayed under any plan at all.
    schema_digest text NOT NULL CHECK (schema_digest <> ''),

    -- How many cases the corpus holds, so a reader can size the work without decoding
    -- the document. Operational only: the identity is derived from the cases themselves,
    -- and this column is checked against them on read rather than trusted.
    case_count integer NOT NULL CHECK (case_count > 0),

    -- The cases in the adapter's own encoding. A semantic.Corpus cannot be serialized:
    -- its fields are private and the kernel's canonical encoders are one-way with no
    -- decoder. So the cases are stored as documents, rebuilt through the kernel's
    -- constructors on read, and the re-derived CorpusID is required to equal corpus_id.
    -- Storage therefore cannot return a corpus under an identity it did not produce.
    document jsonb NOT NULL,

    -- The adapter's encoding version, for the same reason every other table carries one.
    format integer NOT NULL,

    -- Operational metadata only. Nothing here participates in any identity.
    created_at timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (tenant_id, corpus_id)
);

-- migrate:down

DROP TABLE corpora;
