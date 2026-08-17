package postgres

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// The requirement set must come from the embedded files, so that adding a
// migration to the directory cannot leave a hand-maintained version list behind.
func TestRequiredVersionsComeFromTheEmbeddedFiles(t *testing.T) {
	versions, err := requiredVersions()
	if err != nil {
		t.Fatalf("requiredVersions: %v", err)
	}
	if len(versions) == 0 {
		t.Fatal("no versions were derived from the embedded migrations")
	}
	for _, version := range versions {
		if version == "" || strings.ContainsAny(version, "_.") {
			t.Fatalf("version %q is not a bare numeric prefix", version)
		}
	}
	// Sorted, because a later migration's version must compare after an earlier
	// one for any ordering decision built on this.
	for i := 1; i < len(versions); i++ {
		if versions[i-1] >= versions[i] {
			t.Fatalf("versions are not sorted ascending: %v", versions)
		}
	}
}

// Production break caught: this is the whole point of verifying instead of
// migrating. A binary deployed against a database nobody migrated must refuse to
// serve rather than write rows into tables that do not exist, or worse, into
// tables whose shape it misunderstands.
func TestOpenRefusesAnUnmigratedDatabase(t *testing.T) {
	base := requireDatabase(t)
	unmigrated := createScratchDatabase(t, base)

	store, err := Open(context.Background(), unmigrated)
	if err == nil {
		store.Close()
		t.Fatal("Open accepted a database that has never been migrated")
	}
	if !errors.Is(err, ErrSchemaOutOfDate) {
		t.Fatalf("error = %v, want ErrSchemaOutOfDate", err)
	}
	// The message must be actionable. An operator seeing this is trying to work
	// out whether the deploy or the database is the thing that is wrong.
	if !strings.Contains(err.Error(), "make migrate") {
		t.Fatalf("error does not say what to run: %v", err)
	}
}

// Production break caught: a partially migrated database must be refused too,
// and must name what is missing. Reporting only "not migrated" would send an
// operator looking at the wrong thing when the ledger exists but is behind.
func TestOpenNamesTheMissingMigration(t *testing.T) {
	base := requireDatabase(t)
	scratch := createScratchDatabase(t, base)

	pool := poolFor(t, scratch)
	// An empty ledger, which is what a database migrated by an older build with
	// fewer migrations looks like from here.
	if _, err := pool.Exec(context.Background(),
		`CREATE TABLE `+migrationsTable+` (version varchar(255) PRIMARY KEY)`); err != nil {
		t.Fatalf("create ledger: %v", err)
	}
	pool.Close()

	required, err := requiredVersions()
	if err != nil {
		t.Fatalf("requiredVersions: %v", err)
	}

	store, err := Open(context.Background(), scratch)
	if err == nil {
		store.Close()
		t.Fatal("Open accepted a database whose ledger is behind")
	}
	if !errors.Is(err, ErrSchemaOutOfDate) {
		t.Fatalf("error = %v, want ErrSchemaOutOfDate", err)
	}
	if !strings.Contains(err.Error(), required[0]) {
		t.Fatalf("error does not name the missing version %s: %v", required[0], err)
	}
}

// A database ahead of this binary is the normal state during a rolling deploy,
// where old and new tasks run together. Refusing it would turn every deploy into
// a coordinated stop.
func TestOpenAcceptsADatabaseAheadOfThisBuild(t *testing.T) {
	url := requireDatabase(t)
	pool := poolFor(t, url)
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO `+migrationsTable+` (version) VALUES ('99999999999999')
		 ON CONFLICT DO NOTHING`); err != nil {
		t.Fatalf("insert future version: %v", err)
	}
	defer func() {
		if _, err := pool.Exec(context.Background(),
			`DELETE FROM `+migrationsTable+` WHERE version = '99999999999999'`); err != nil {
			t.Errorf("cleanup: %v", err)
		}
		pool.Close()
	}()

	store, err := Open(context.Background(), url)
	if err != nil {
		t.Fatalf("Open refused a database ahead of this build: %v", err)
	}
	store.Close()
}

// Production break caught: the initial migration has to adopt a database whose
// tables were created by the adapter's former implicit CREATE TABLE on open.
// Applying it there must change nothing rather than fail, or every existing
// database would have to be rebuilt to join the migration history.
//
// Executing the up section twice is the same shape as applying it to a database
// that already had the tables.
func TestInitialMigrationAdoptsAnExistingDatabase(t *testing.T) {
	base := requireDatabase(t)
	scratch := createScratchDatabase(t, base)
	pool := poolFor(t, scratch)
	defer pool.Close()

	up := upSection(t)
	for attempt := 1; attempt <= 2; attempt++ {
		if _, err := pool.Exec(context.Background(), up); err != nil {
			t.Fatalf("applying the initial migration (attempt %d): %v", attempt, err)
		}
	}
}

// upSection returns the statements dbmate would run for the first migration.
func upSection(t *testing.T) string {
	t.Helper()
	versions, err := requiredVersions()
	if err != nil {
		t.Fatalf("requiredVersions: %v", err)
	}
	entries, err := migrationFiles.ReadDir(migrationsDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), versions[0]) {
			continue
		}
		body, err := migrationFiles.ReadFile(migrationsDir + "/" + entry.Name())
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		_, after, found := strings.Cut(string(body), "-- migrate:up")
		if !found {
			t.Fatal("the migration has no -- migrate:up marker, so dbmate would not apply it")
		}
		statements, _, _ := strings.Cut(after, "-- migrate:down")
		return statements
	}
	t.Fatalf("no migration file matched version %s", versions[0])
	return ""
}

// createScratchDatabase makes an empty database beside the test one and returns
// its URL. A scratch database is the only honest way to test the unmigrated case,
// because store-check migrates the shared one before the tests run.
func createScratchDatabase(t *testing.T, baseURL string) string {
	t.Helper()
	parsed, err := url.Parse(baseURL)
	if err != nil {
		t.Fatalf("parse base URL: %v", err)
	}
	name := fmt.Sprintf("ml_scratch_%d", uniqueSuffix())

	admin := poolFor(t, baseURL)
	// CREATE DATABASE cannot run inside a transaction, and pgx sends this
	// unprepared, which is what makes it work here.
	if _, err := admin.Exec(context.Background(), `CREATE DATABASE `+name); err != nil {
		admin.Close()
		t.Fatalf("create scratch database: %v", err)
	}
	admin.Close()

	t.Cleanup(func() {
		cleanup := poolFor(t, baseURL)
		defer cleanup.Close()
		if _, err := cleanup.Exec(context.Background(),
			`DROP DATABASE IF EXISTS `+name+` WITH (FORCE)`); err != nil {
			t.Errorf("drop scratch database %s: %v", name, err)
		}
	})

	scratch := *parsed
	scratch.Path = "/" + name
	return scratch.String()
}

// poolFor opens a pool the caller closes. The package's connect helper defers
// closing to t.Cleanup, which does not work here: a scratch database cannot be
// dropped while any connection to it remains open, and cleanup ordering would
// leave one alive.
func poolFor(t *testing.T, url string) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		t.Fatalf("ping: %v", err)
	}
	return pool
}

var scratchCounter int

// uniqueSuffix keeps scratch database names distinct within one test binary.
// Tests in a package run sequentially unless they call t.Parallel, and these do
// not, so a plain counter is sufficient and avoids depending on a clock.
func uniqueSuffix() int {
	scratchCounter++
	return scratchCounter
}
