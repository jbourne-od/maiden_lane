-- Attempt generation: the operational identity of one attempt at an execution
-- (HLD §6 and §17).
--
-- It exists because returning a terminally failed execution to the queue reopens a row
-- that was closed, and without a generation an abandoned attempt can write across that
-- boundary — a stale operational failure would terminate the retry generation performed
-- to escape it. The terminal status used to protect the row; reattempting removes that
-- protection, so the attempt has to carry it.

-- migrate:up

ALTER TABLE executions
    -- Monotonic per execution, advanced by every claim and by every reattempt. It is an
    -- occurrence rather than a meaning: §6 puts AttemptID deliberately outside semantic
    -- identity, so nothing derived from it may ever reach an artifact.
    --
    -- DEFAULT 0 rather than 1, so a row that predates this column has a generation no
    -- attempt can present. Existing rows are all terminal or unclaimed; a claim advances
    -- to 1 before anything can report against it.
    ADD COLUMN current_attempt_id bigint NOT NULL DEFAULT 0
        CHECK (current_attempt_id >= 0);

-- migrate:down

ALTER TABLE executions DROP COLUMN current_attempt_id;
