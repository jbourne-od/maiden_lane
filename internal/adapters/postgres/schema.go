package postgres

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// migrationFiles are embedded to be READ, never applied.
//
// The application does not migrate its own database, and this file is the reason
// that is a property rather than a convention: nothing here issues DDL. A runtime
// role therefore needs no ALTER or DROP privilege, which means a compromised
// process cannot rewrite the tables holding sealed artifacts. For a system whose
// proposition is that its record lineage is immutable and attributable, the
// alternative would replace a structural guarantee with trust.
//
// Embedding the files is what makes the check precise. The binary knows exactly
// which migrations it was built against, so it can refuse a database that is
// missing one instead of comparing against a version number somebody has to
// remember to bump.
//
//go:embed migrations/*.sql
var migrationFiles embed.FS

const migrationsDir = "migrations"

// migrationsTable is dbmate's ledger. It is the one table this package reads
// without owning.
const migrationsTable = "schema_migrations"

// ErrSchemaOutOfDate reports a database that is missing a migration this build
// requires. It is deliberately distinct from unreachable: the database answered,
// and what it said is that it is not the schema this binary was compiled for.
var ErrSchemaOutOfDate = errors.New("postgres: database schema is missing required migrations")

// requiredVersions returns the migration versions this build embeds, in order.
//
// The version is the numeric prefix dbmate assigns, which is also what it
// records. Parsing it from the filename rather than from a hand-maintained list
// means a migration cannot be added to the directory and forgotten here.
func requiredVersions() ([]string, error) {
	entries, err := fs.ReadDir(migrationFiles, migrationsDir)
	if err != nil {
		return nil, fmt.Errorf("postgres: embedded migrations are unreadable: %w", err)
	}
	versions := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		version, _, found := strings.Cut(entry.Name(), "_")
		if !found || version == "" {
			// A file this package cannot derive a version from is a build-time
			// mistake, not something to skip: skipping it would silently drop a
			// migration from the requirement set that the directory says exists.
			return nil, fmt.Errorf(
				"postgres: migration %q has no version prefix", entry.Name())
		}
		versions = append(versions, version)
	}
	if len(versions) == 0 {
		return nil, errors.New("postgres: no migrations are embedded")
	}
	slices.Sort(versions)
	return versions, nil
}

// verifySchema requires every embedded migration to be recorded as applied.
//
// It reads two things and writes nothing. Both failure directions matter: a new
// binary against a database nobody migrated, and a rolled-back binary against a
// schema it does not recognise. The first is the common one and the second is the
// one that corrupts data, because a binary will happily write rows to a table
// whose shape it misunderstands.
//
// Extra applied versions are permitted. A database ahead of this binary is the
// normal state during a rolling deploy, and refusing it would make every deploy a
// coordinated stop.
func verifySchema(ctx context.Context, pool *pgxpool.Pool) error {
	required, err := requiredVersions()
	if err != nil {
		return err
	}

	var present bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS (
		     SELECT 1 FROM information_schema.tables
		     WHERE table_schema = current_schema() AND table_name = $1
		 )`, migrationsTable).Scan(&present); err != nil {
		return fmt.Errorf("postgres: migration state could not be read: %w", err)
	}
	if !present {
		return fmt.Errorf(
			"%w: this database has never been migrated (no %s table); run: make migrate",
			ErrSchemaOutOfDate, migrationsTable)
	}

	rows, err := pool.Query(ctx, `SELECT version FROM `+migrationsTable)
	if err != nil {
		return fmt.Errorf("postgres: applied migrations could not be read: %w", err)
	}
	applied, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return fmt.Errorf("postgres: applied migrations could not be read: %w", err)
	}

	missing := make([]string, 0)
	for _, version := range required {
		if !slices.Contains(applied, version) {
			missing = append(missing, version)
		}
	}
	if len(missing) > 0 {
		// The versions are named because "run migrations" is not actionable when
		// an operator is trying to work out whether the deploy or the database is
		// the thing that is wrong.
		return fmt.Errorf("%w: missing %s; run: make migrate",
			ErrSchemaOutOfDate, strings.Join(missing, ", "))
	}
	return nil
}
