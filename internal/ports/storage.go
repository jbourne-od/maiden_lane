// Package ports declares the storage interfaces the application owns.
//
// The interfaces live here rather than beside an implementation because the
// consumer owns them (AGENTS.md section 11): the application states what it
// needs, and an adapter satisfies it. That direction is what lets a durable
// PostgreSQL or S3 adapter replace the in-process one without any change to
// internal/app or internal/semantic.
//
// These interfaces carry the kernel's own immutable values rather than a
// serialized form. Nothing here re-derives a semantic identity, and no wire or
// storage encoding may ever become a second source of canonical meaning
// (Inviolate 4). A durable adapter must persist the kernel's canonical bytes
// verbatim and rehydrate through the kernel's constructors.
package ports

import (
	"context"

	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

// TenantID scopes every stored artifact. It is a distinct type so a tenant can
// never be passed where an artifact identity is expected, and every store
// method takes it explicitly rather than reading it from ambient state.
type TenantID string

// PlanRecord is one compiled plan retained for later execution.
//
// It holds the compiled Schema in addition to the Compilation because a plan
// pins only its schema digest, while binding a later run needs the schema
// itself to construct the initial state. Storing it here keeps the client from
// re-supplying a schema that could disagree with the one that was compiled.
// Every field is an immutable kernel value whose accessors return deep copies.
// That is a load-bearing property, not an incidental one: it is what allows an
// adapter to store and return a record by ordinary assignment without any
// defensive copying of its own.
//
// A bare semantic.CompileRequest was briefly retained here and had to be
// removed: it is an ordinary authoring structure of exported slices and
// pointers, so storing one handed every caller a mutable alias into the store.
// Do not reintroduce it, or any other value whose interior can be reached and
// changed after it is stored. Input carries the same information immutably.
type PlanRecord struct {
	TenantID TenantID
	PlanID   semantic.PlanID

	// Input is what a durable adapter persists.
	//
	// A Compilation cannot be serialized: its fields are private, Compile is
	// the only way to obtain one, and the kernel's canonical encoders are
	// one-way with no decoder to rehydrate from. An adapter therefore stores
	// this input in its own encoding, recompiles on read, and requires the
	// resulting PlanID to equal the one it stored. Storage consequently cannot
	// return a plan under an identity it did not actually produce.
	Input semantic.CompilationInput

	// Schema is the compiled schema, retained so an initial state can be
	// constructed without re-deriving it. A plan pins only its schema digest.
	Schema semantic.Schema

	// Compilation is the accepted artifact, retained so a read can project the
	// plan without recompiling.
	Compilation semantic.Compilation
}

// PlanStore persists compiled plans.
//
// Every method is tenant scoped by signature. There is deliberately no
// "get by identity" method: a handler cannot forget to filter, because no
// unscoped lookup exists to call.
type PlanStore interface {
	// PutPlan stores a plan for its tenant.
	//
	// Plan identity is content derived, so storing identical declarations
	// twice is idempotent rather than a conflict: the same identity always
	// denotes the same plan. It returns an error only for an incomplete
	// record or a cancelled context.
	PutPlan(context.Context, PlanRecord) error

	// GetPlan reports the plan for this tenant. A plan belonging to another
	// tenant is reported as absent, never as an error: distinguishing the two
	// would leak its existence to a caller with no right to know.
	GetPlan(context.Context, TenantID, semantic.PlanID) (PlanRecord, bool, error)
}
