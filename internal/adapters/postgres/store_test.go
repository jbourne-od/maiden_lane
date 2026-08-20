package postgres

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/optimaldynamics/maiden-lane/internal/ports"
	"github.com/optimaldynamics/maiden-lane/internal/ports/storagecontract"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

// databaseURLVariable names the database these tests run against. `make
// store-check` always sets it, so CI never skips these; running `go test ./...`
// on a workstation without a database skips them instead of failing, which is
// what keeps `make verify` free of infrastructure.
const databaseURLVariable = "MAIDEN_LANE_TEST_POSTGRES_URL"

// The adapter is held to the same contract as every other PlanStore. If it
// needed its own weaker version of any assertion, it would not be substitutable
// and the port's promise would be false.
func TestStoreSatisfiesThePlanStoreContract(t *testing.T) {
	url := requireDatabase(t)
	storagecontract.RunPlanStoreContract(t, func(t *testing.T) ports.PlanStore {
		return freshStore(t, url)
	})
}

// Production break caught: this is the assertion the whole design exists to
// support. A row that was corrupted, truncated, tampered with, or swapped for
// another tenant's content must never be returned under the identity it claims.
// Returning it would make the database an unaudited source of semantic meaning.
func TestCorruptedRowsFailClosed(t *testing.T) {
	url := requireDatabase(t)

	tests := []struct {
		name    string
		corrupt string
	}{
		{
			"flipped declaration bytes",
			// Change one byte of the stored declarations. The recompiled plan
			// identity can no longer match.
			`UPDATE plans SET declarations = overlay(declarations placing '\x20'::bytea from 2 for 1)`,
		},
		{
			"truncated declarations",
			`UPDATE plans SET declarations = substring(declarations from 1 for 12)`,
		},
		{
			"emptied declarations",
			`UPDATE plans SET declarations = '\x'::bytea`,
		},
		{
			"declarations replaced with valid but different ones",
			// The most dangerous case: syntactically fine, decodes cleanly,
			// compiles successfully, and describes a different program.
			`UPDATE plans SET declarations = $1`,
		},
		{
			"input digest altered",
			`UPDATE plans SET input_digest = 'sha256:` +
				`1111111111111111111111111111111111111111111111111111111111111111'`,
		},
		{
			"unknown storage format",
			`UPDATE plans SET format = 99`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := freshStore(t, url)
			record := storagecontract.PlanRecordFixture(t, "acme", "corruption.v1")
			if err := store.PutPlan(t.Context(), record); err != nil {
				t.Fatalf("PutPlan: %v", err)
			}
			// Confirm it reads back before corruption, so a failure afterwards
			// is attributable to the corruption rather than to the fixture.
			if _, found, err := store.GetPlan(t.Context(), "acme", record.PlanID); err != nil || !found {
				t.Fatalf("baseline read failed: found=%t err=%v", found, err)
			}

			execute(t, url, test.corrupt, otherDeclarations(t))

			got, found, err := store.GetPlan(t.Context(), "acme", record.PlanID)
			if err == nil {
				t.Fatalf("a corrupted row was returned: found=%t record=%+v", found, got.PlanID)
			}
			if !errors.Is(err, ErrIntegrity) {
				t.Fatalf("err = %v, want an integrity failure", err)
			}
			if found {
				t.Error("a failed read also reported the record as found")
			}
			if got.PlanID != "" {
				t.Errorf("a failed read returned data: %+v", got)
			}
		})
	}
}

// Production break caught: two different contents under one identity would mean
// the content-addressing guarantee had been violated somewhere upstream. The
// store must report that rather than silently keeping whichever arrived first.
func TestOneIdentityCannotHoldTwoContents(t *testing.T) {
	url := requireDatabase(t)
	store := freshStore(t, url)

	record := storagecontract.PlanRecordFixture(t, "acme", "conflict.v1")
	if err := store.PutPlan(t.Context(), record); err != nil {
		t.Fatalf("PutPlan: %v", err)
	}

	// Same key, different content. Only reachable by forcing it, which is the
	// point: if it ever happens for real, the store must not paper over it.
	conflicting := storagecontract.PlanRecordFixture(t, "acme", "conflict.v2")
	conflicting.PlanID = record.PlanID

	err := store.PutPlan(t.Context(), conflicting)
	if !errors.Is(err, ErrIntegrity) {
		t.Fatalf("err = %v, want an integrity failure", err)
	}

	// The original must still be readable and unchanged.
	got, found, err := store.GetPlan(t.Context(), "acme", record.PlanID)
	if err != nil || !found {
		t.Fatalf("the original became unreadable: found=%t err=%v", found, err)
	}
	if got.Input.Digest() != record.Input.Digest() {
		t.Fatal("a refused write changed the stored content")
	}
}

// Production break caught: declarations must be stored as opaque bytes. If the
// column were jsonb, Postgres would reorder keys and normalize numbers, and the
// bytes read back would not be the bytes written.
func TestDeclarationsAreStoredAsExactBytes(t *testing.T) {
	url := requireDatabase(t)
	store := freshStore(t, url)
	record := storagecontract.PlanRecordFixture(t, "acme", "bytes.v1")
	if err := store.PutPlan(t.Context(), record); err != nil {
		t.Fatalf("PutPlan: %v", err)
	}

	pool := connect(t, url)
	var columnType string
	if err := pool.QueryRow(t.Context(), `
		SELECT data_type FROM information_schema.columns
		WHERE table_name = 'plans' AND column_name = 'declarations'`).Scan(&columnType); err != nil {
		t.Fatalf("inspect column: %v", err)
	}
	if columnType != "bytea" {
		t.Fatalf("declarations column is %s; identity-bearing content must not be jsonb", columnType)
	}

	// Reading the same row twice must yield byte-identical declarations.
	var first, second []byte
	query := `SELECT declarations FROM plans WHERE tenant_id = 'acme' AND plan_id = $1`
	if err := pool.QueryRow(t.Context(), query, string(record.PlanID)).Scan(&first); err != nil {
		t.Fatalf("read declarations: %v", err)
	}
	if err := pool.QueryRow(t.Context(), query, string(record.PlanID)).Scan(&second); err != nil {
		t.Fatalf("read declarations: %v", err)
	}
	if string(first) != string(second) {
		t.Fatal("two reads of one row returned different bytes")
	}
}

// Production break caught: tenancy is part of the primary key so an unscoped
// read is not expressible. If a later migration split them, a query could
// answer "give me plan X" without naming its owner.
func TestTenancyIsPartOfThePrimaryKey(t *testing.T) {
	url := requireDatabase(t)
	freshStore(t, url)
	pool := connect(t, url)

	rows, err := pool.Query(t.Context(), `
		SELECT a.attname
		FROM pg_index i
		JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = ANY(i.indkey)
		WHERE i.indrelid = 'plans'::regclass AND i.indisprimary
		ORDER BY a.attname`)
	if err != nil {
		t.Fatalf("inspect primary key: %v", err)
	}
	defer rows.Close()

	columns := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		columns = append(columns, name)
	}
	if len(columns) != 2 || columns[0] != "plan_id" || columns[1] != "tenant_id" {
		t.Fatalf("primary key = %v, want plan_id and tenant_id", columns)
	}
}

// Production break caught: an unreachable database must fail loudly at open
// rather than yield a store that appears to work and loses everything.
func TestOpenFailsOnAnUnreachableDatabase(t *testing.T) {
	_, err := Open(t.Context(),
		"postgres://nobody:nothing@127.0.0.1:1/absent?sslmode=disable&connect_timeout=1")
	if err == nil {
		t.Fatal("Open succeeded against an unreachable database")
	}
	// The connection string may carry a password, so it must not be echoed.
	if got := err.Error(); strings.Contains(got, "nothing") {
		t.Fatalf("error echoed the credential: %s", got)
	}
}

func requireDatabase(t *testing.T) string {
	t.Helper()
	url := os.Getenv(databaseURLVariable)
	if url == "" {
		t.Skipf("%s is not set; run these adapter tests with `make store-check`", databaseURLVariable)
	}
	return url
}

// freshStore returns a store over an empty plans table. The contract suite
// requires each store to start empty, and truncating is how that holds for a
// database that outlives the process.
func freshStore(t *testing.T, url string) *Store {
	t.Helper()
	store, err := Open(t.Context(), url)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(store.Close)

	execute(t, url, `TRUNCATE plans`, nil)
	return store
}

func connect(t *testing.T, url string) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// execute runs a statement directly, bypassing the adapter, so tests can
// corrupt storage the way an operator or a bug actually would.
func execute(t *testing.T, url, statement string, argument []byte) {
	t.Helper()
	pool := connect(t, url)
	var err error
	if argument != nil && strings.Contains(statement, "$1") {
		_, err = pool.Exec(context.Background(), statement, argument)
	} else {
		_, err = pool.Exec(context.Background(), statement)
	}
	if err != nil {
		t.Fatalf("execute %q: %v", statement, err)
	}
}

// otherDeclarations encodes a different but entirely valid program, for the
// substitution case: storage returning a plan that compiles cleanly and is
// simply not the one that was asked for. This is the most dangerous corruption,
// because nothing about the row looks wrong.
//
// The test lives in this package so it can use the adapter's own encoder rather
// than requiring production code to export a hook for testing.
func otherDeclarations(t *testing.T) []byte {
	t.Helper()
	encoded, err := encodeDeclarations(storagecontract.PlanRecordFixture(t, "acme", "substituted.v1"))
	if err != nil {
		t.Fatalf("encode substitute declarations: %v", err)
	}
	return encoded
}

// A stored declaration must recompile to the plan that was stored.
//
// THIS NEEDS NO DATABASE, and that is the point: the drift it catches is in the serialization,
// not in SQL, and every test that did need a database skipped without one. encodeDeclarations
// and rebuild are the two halves of the round trip, so calling them directly exercises exactly
// the property a stored plan depends on.
//
// Production break caught: semantic.Value's fields are unexported and it had no marshaller, so
// encoding/json wrote `{}` and returned no error. Neither frozen operator carries a Value, so
// the first authored rule with a literal -- which is any select-and-assign rule that assigns a
// constant -- was writable and unreadable. PutPlan returned 201, every GetPlan afterwards
// recompiled to an UnsupportedOperator diagnostic and answered 500, and the worker treated the
// storage error as transient and retried the execution forever.
func TestStoredDeclarationsRecompileToTheSamePlan(t *testing.T) {
	record := storagecontract.PlanRecordFixture(t, "acme", "semantics.v1")

	encoded, err := encodeDeclarations(record)
	if err != nil {
		t.Fatalf("encodeDeclarations: %v", err)
	}
	rebuilt, err := rebuild(record.TenantID, record.PlanID, record.Input.Digest(), encoded)
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if rebuilt.PlanID != record.PlanID {
		t.Fatalf("rebuilt plan %s, want %s", rebuilt.PlanID, record.PlanID)
	}
	if rebuilt.Input.Digest() != record.Input.Digest() {
		t.Fatalf("rebuilt compilation input %s, want %s", rebuilt.Input.Digest(), record.Input.Digest())
	}

	// And the literals came back as literals rather than as the zero Value, which is the
	// specific way this failed. Asserting only the identity would catch it too, but would not
	// say what broke.
	rule := rebuilt.Input.Request().Rules.Transformations[0]
	if rule.SelectAssign == nil {
		t.Fatal("the select-and-assign payload did not survive storage")
	}
	literals := 0
	for _, assignment := range rule.SelectAssign.Assignments {
		if assignment.Value.Kind != semantic.ExprLiteral {
			continue
		}
		literals++
		if assignment.Value.Literal == nil || !assignment.Value.Literal.Valid() {
			t.Fatalf("assignment to %s came back with an invalid literal", assignment.Target)
		}
	}
	if literals == 0 {
		t.Fatal("the fixture carries no literal, so this test cannot see the defect it exists for")
	}
}
