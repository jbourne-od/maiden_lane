// Package postgres implements the application's storage ports against
// PostgreSQL.
//
// The adapter never trusts what it read. A stored plan is returned only after
// its declarations have been recompiled and the resulting plan identity and
// compilation-input digest match the ones stored alongside them. A row that was
// corrupted, truncated, tampered with, or silently re-encoded therefore cannot
// produce a plan under the identity it claims: the read fails closed instead.
//
// That property is what makes the storage format itself require no trust. This
// adapter owns its encoding and could change it freely; what it cannot do is
// return an artifact whose identity it did not actually reproduce. It is also
// why the encoding may be version-fragile without being dangerous. If a future
// kernel renumbered a closed enum, rows written by an older build would
// recompile into a different plan, the identities would disagree, and the read
// would fail loudly rather than quietly meaning something else.
//
// Nothing here decides semantic meaning. The kernel compiles; this package
// stores bytes and checks the answer.
package postgres

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/optimaldynamics/maiden-lane/internal/ports"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

// schema is applied on Open. Embedding it keeps the DDL beside the code that
// depends on it, so the two cannot drift apart in a deployment.
//
//go:embed schema.sql
var schema string

// storedFormat is the adapter's encoding version. Increment it when the stored
// representation changes shape; a row carrying an unknown format is refused
// rather than interpreted.
const storedFormat = 1

// ErrIntegrity reports a stored row that cannot reproduce the identity it
// claims. It is deliberately distinct from "absent": absence is a normal
// answer, while this means the database holds something untrustworthy and a
// human needs to know.
var ErrIntegrity = errors.New("postgres: stored plan does not reproduce its own identity")

// ErrIncompleteRecord reports a record missing its tenant or artifact identity.
var ErrIncompleteRecord = errors.New("postgres: record is missing its tenant or artifact identity")

// Store is a PostgreSQL-backed implementation of the storage ports.
type Store struct {
	pool *pgxpool.Pool
}

var _ ports.PlanStore = (*Store)(nil)

// Open connects, applies the schema, and returns a ready store.
//
// The schema is applied here rather than by a separate migration step because
// this slice has exactly one table and no migration history to honor. That will
// not remain true. When a second version of the schema exists this becomes a
// real migration boundary: an implicit CREATE TABLE IF NOT EXISTS cannot alter
// an existing table, so the first schema change must arrive with an explicit
// migration step rather than by editing schema.sql and hoping.
func Open(ctx context.Context, url string) (*Store, error) {
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		// The URL may carry a password, so the parse error is not surfaced.
		return nil, errors.New("postgres: connection string is not usable")
	}
	// An explicit bound rather than a tuned one. The point is that the pool
	// cannot grow without limit under load, not that this number is optimal.
	config.MaxConns = 8

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, errors.New("postgres: connection pool could not be created")
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres: database is unreachable: %w", err)
	}
	if _, err := pool.Exec(ctx, schema); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres: schema could not be applied: %w", err)
	}
	return &Store{pool: pool}, nil
}

// Close releases the pool.
func (s *Store) Close() { s.pool.Close() }

// PutPlan stores a plan for its tenant, idempotently.
//
// Plan identity is content derived, so an existing row under the same key
// already holds the same plan and the insert is a no-op. That assumption is
// verified rather than assumed: if a row exists whose input digest differs, the
// database is holding two different contents under one identity and the write
// is refused.
func (s *Store) PutPlan(ctx context.Context, record ports.PlanRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if record.TenantID == "" || record.PlanID == "" {
		return ErrIncompleteRecord
	}

	declarations, err := encodeDeclarations(record)
	if err != nil {
		return err
	}

	tag, err := s.pool.Exec(ctx, `
		INSERT INTO plans (tenant_id, plan_id, input_digest, format, declarations)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (tenant_id, plan_id) DO NOTHING`,
		string(record.TenantID), string(record.PlanID),
		string(record.Input.Digest()), storedFormat, declarations)
	if err != nil {
		return fmt.Errorf("postgres: plan could not be stored: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return nil
	}

	// The row already existed. Content-derived identity means it must hold the
	// same content; confirming that turns a silent collision into a reported one.
	var existing string
	err = s.pool.QueryRow(ctx,
		`SELECT input_digest FROM plans WHERE tenant_id = $1 AND plan_id = $2`,
		string(record.TenantID), string(record.PlanID)).Scan(&existing)
	if err != nil {
		return fmt.Errorf("postgres: stored plan could not be re-read: %w", err)
	}
	if existing != string(record.Input.Digest()) {
		return fmt.Errorf("%w: one plan identity holds two compilation inputs", ErrIntegrity)
	}
	return nil
}

// GetPlan reports the plan for this tenant, or absence.
//
// A plan belonging to another tenant is absent, never an error: distinguishing
// the two would leak its existence to a caller with no right to know.
func (s *Store) GetPlan(ctx context.Context, tenant ports.TenantID, planID semantic.PlanID) (ports.PlanRecord, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.PlanRecord{}, false, err
	}
	if tenant == "" || planID == "" {
		return ports.PlanRecord{}, false, nil
	}

	var (
		storedDigest string
		format       int
		declarations []byte
	)
	err := s.pool.QueryRow(ctx, `
		SELECT input_digest, format, declarations
		FROM plans WHERE tenant_id = $1 AND plan_id = $2`,
		string(tenant), string(planID)).Scan(&storedDigest, &format, &declarations)
	if errors.Is(err, pgx.ErrNoRows) {
		return ports.PlanRecord{}, false, nil
	}
	if err != nil {
		return ports.PlanRecord{}, false, fmt.Errorf("postgres: plan could not be read: %w", err)
	}
	if format != storedFormat {
		// Refuse rather than guess. A row written by a representation this build
		// does not understand could decode into something plausible but wrong.
		return ports.PlanRecord{}, false, fmt.Errorf(
			"%w: row uses storage format %d, this build understands %d", ErrIntegrity, format, storedFormat)
	}

	record, err := rebuild(tenant, planID, semantic.CompilationInputDigest(storedDigest), declarations)
	if err != nil {
		return ports.PlanRecord{}, false, err
	}
	return record, true, nil
}

// storedDeclarations is the adapter's encoding of a compiler request.
//
// It holds the kernel's own exported declaration types directly rather than
// re-describing them field by field. That is deliberate: a parallel set of
// storage structs would silently omit any field added upstream, which is the
// same aliasing-by-omission problem that made deep-copying in an adapter the
// wrong choice. Here, a new field on a declaration is carried automatically,
// and if it somehow were not, the identity check on read would fail.
//
// The schema declaration is decomposed because SchemaDeclaration keeps its
// contents private; its entity and relation declarations are fully exported.
type storedDeclarations struct {
	CompilerSemanticsVersion semantic.CompilerSemanticsVersion    `json:"compiler_semantics_version"`
	Entities                 []semantic.EntityDeclaration         `json:"entities"`
	Relations                []semantic.RelationDeclaration       `json:"relations"`
	Transformations          []semantic.TransformationDeclaration `json:"transformations"`
	Checkpoints              []semantic.CheckpointDeclaration     `json:"checkpoints"`
	Profiles                 []semantic.ProfileDeclaration        `json:"profiles"`
}

func encodeDeclarations(record ports.PlanRecord) ([]byte, error) {
	request := record.Input.Request()
	encoded, err := json.Marshal(storedDeclarations{
		CompilerSemanticsVersion: request.CompilerSemanticsVersion,
		Entities:                 request.Schema.EntityDeclarations(),
		Relations:                request.Schema.RelationDeclarations(),
		Transformations:          request.Rules.Transformations,
		Checkpoints:              request.Rules.Checkpoints,
		Profiles:                 request.Profiles,
	})
	if err != nil {
		return nil, fmt.Errorf("postgres: declarations could not be encoded: %w", err)
	}
	return encoded, nil
}

// rebuild decodes stored declarations, recompiles them, and returns a record
// only if the reproduced identities match the ones that were stored.
func rebuild(tenant ports.TenantID, planID semantic.PlanID, storedDigest semantic.CompilationInputDigest, declarations []byte) (ports.PlanRecord, error) {
	var stored storedDeclarations
	if err := json.Unmarshal(declarations, &stored); err != nil {
		return ports.PlanRecord{}, fmt.Errorf("%w: declarations could not be decoded", ErrIntegrity)
	}

	schema, err := semantic.NewSchema(stored.Entities, stored.Relations)
	if err != nil {
		return ports.PlanRecord{}, fmt.Errorf("%w: stored schema is invalid", ErrIntegrity)
	}

	compilation, err := semantic.Compile(semantic.CompileRequest{
		Schema: schema.Declaration(),
		Rules: semantic.RulesetDeclaration{
			Transformations: stored.Transformations,
			Checkpoints:     stored.Checkpoints,
		},
		Profiles:                 stored.Profiles,
		CompilerSemanticsVersion: stored.CompilerSemanticsVersion,
	})
	if err != nil {
		return ports.PlanRecord{}, fmt.Errorf("%w: stored declarations could not be compiled", ErrIntegrity)
	}
	plan, ok := compilation.Plan()
	if !ok {
		// Only accepted plans are ever stored, so declarations that now fail to
		// compile mean the row no longer describes what was accepted.
		return ports.PlanRecord{}, fmt.Errorf("%w: stored declarations no longer compile to a plan", ErrIntegrity)
	}

	// The two checks that make storage unable to lie.
	if plan.ID() != planID {
		return ports.PlanRecord{}, fmt.Errorf("%w: recompiled plan identity differs from the stored one", ErrIntegrity)
	}
	if compilation.InputDigest() != storedDigest {
		return ports.PlanRecord{}, fmt.Errorf("%w: recompiled input digest differs from the stored one", ErrIntegrity)
	}

	return ports.PlanRecord{
		TenantID:    tenant,
		PlanID:      plan.ID(),
		Input:       compilation.Input(),
		Schema:      schema,
		Compilation: compilation,
	}, nil
}
