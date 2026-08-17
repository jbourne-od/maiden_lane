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
	"time"

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

// ExecutionStatus is the control-plane lifecycle of a stored execution.
//
// It is application state, deliberately outside canonical semantic identity:
// the semantic layer answers what a computation meant, and this answers what
// happened while it ran. New lifecycle vocabulary must never force a semantic
// schema change, and new semantic outcomes must never require the execution
// controller to own semantic vocabulary.
type ExecutionStatus string

const (
	ExecutionPending   ExecutionStatus = "pending"
	ExecutionRunning   ExecutionStatus = "running"
	ExecutionSucceeded ExecutionStatus = "succeeded"
	ExecutionFailed    ExecutionStatus = "failed"
)

// ExecutionInput is the pinned semantic input of one execution.
//
// It is stored so a worker can run the execution later and so a reclaimed
// attempt reproduces it exactly. Every field is an immutable kernel value; as
// with PlanRecord, nothing whose interior can be reached and mutated after
// storing belongs here.
type ExecutionInput struct {
	InitialState     semantic.State
	World            semantic.World
	ExecutorIdentity semantic.ExecutorIdentity
	Policy           semantic.ProvenancePolicy
}

// ExecutionRequest is one queued execution.
//
// The identities are derived by the kernel from the input, never allocated
// here. That is what makes enqueueing idempotent without a deduplication key:
// the same semantic request always produces the same ExecutionID.
type ExecutionRequest struct {
	TenantID    TenantID
	ExecutionID semantic.ExecutionID
	RunID       semantic.SemanticRunID
	PlanID      semantic.PlanID
	Input       ExecutionInput
}

// SealedCheckpoint is one sealed artifact retained from a completed execution.
//
// The canonical bytes are kept, not only the digest. Sealing produces an
// artifact; storing only its identity would keep the receipt and discard the
// goods, and a later publication step must expose an artifact that was already
// validated rather than re-derive one. The bytes are opaque here: they are
// comparable without being decodable, which is exactly what replay
// verification needs and all it needs.
type SealedCheckpoint struct {
	CheckpointKey        semantic.CheckpointKey
	CheckpointID         semantic.CheckpointID
	CheckpointArtifactID semantic.CheckpointArtifactID
	Digest               semantic.CheckpointArtifactDigest
	StateDigest          semantic.StateDigest
	CanonicalBytes       []byte

	// InvariantResultDigest is the commitment the witness below is checked
	// against. Both must survive: retaining the witness alone leaves it
	// unverifiable, because the digest lives inside the artifact's canonical bytes
	// and the kernel has no decoder to recover it from there.
	InvariantResultDigest semantic.InvariantResultDigest

	// InvariantResultCanonicalBytes is the witness the artifact's
	// InvariantResultDigest commits to, retained so a later reader can establish
	// that the evidence it holds is the evidence this checkpoint sealed against.
	//
	// It is opaque here and must stay that way. Storage cannot read it, and the
	// kernel exposes no way to turn it back into invariant results; the only
	// operation defined on it is semantic.VerifyInvariantResultDigest. An adapter
	// that ever decides it knows what these bytes mean has become a second source
	// of semantic meaning.
	//
	// A digest is a commitment, not a witness. Retaining only the digest, as this
	// struct did, kept the commitment and discarded what it committed to, which
	// left the promotion gate unable to establish anything about protected
	// invariants at all.
	InvariantResultCanonicalBytes []byte
}

// StoredAssessment is one readiness answer retained from a completed execution.
type StoredAssessment struct {
	AssessmentID         semantic.AssessmentID
	Digest               semantic.AssessmentDigest
	CheckpointArtifactID semantic.CheckpointArtifactID
	ProfileID            semantic.ProfileID
	ProfileKey           semantic.ProfileKey
	Verdict              semantic.ReadinessVerdict
	MissingRequirements  []semantic.RequirementCode
	CanonicalBytes       []byte
}

// StoredFailure is a deterministic semantic rejection, retained as closed
// tokens. It carries no evidence body, entity reference, or free-form text: a
// rejection's canonical detail lives in the semantic artifacts, and everything
// here is safe to render to a caller.
type StoredFailure struct {
	Kind semantic.FailureKind
	Code string
}

// ExecutionResult is the completed projection of one execution.
//
// It is identities, digests, closed codes, and the artifacts the run sealed.
// It deliberately does not attempt to store a rehydratable Journal, State, or
// Assessment: those are kernel values that cannot be reconstructed from bytes,
// and a caller needing one re-executes, which is deterministic.
type ExecutionResult struct {
	TenantID            TenantID
	ExecutionID         semantic.ExecutionID
	Status              ExecutionStatus
	SpineStatus         string
	FinalStateDigest    semantic.StateDigest
	JournalPrefixDigest semantic.JournalPrefixDigest
	InputID             semantic.InputID
	WorldID             semantic.WorldID
	AcceptedRules       []semantic.RuleID
	Checkpoints         []SealedCheckpoint
	Assessments         []StoredAssessment
	Failure             *StoredFailure
}

// ExecutionRecord is what a read returns: the queued request, the lifecycle
// status, and the result once one exists.
type ExecutionRecord struct {
	Request ExecutionRequest
	Status  ExecutionStatus

	// Result is present only once the execution completed, successfully or
	// with a deterministic semantic rejection. A pending or running execution
	// has none, and a caller must not infer one from the status alone.
	Result *ExecutionResult

	// FailureReason is a bounded operational code explaining why the execution
	// could not be attempted, distinct from a semantic rejection inside
	// Result. It never carries a raw cause.
	FailureReason string
}

// ExecutionStore persists executions and serves as their work queue.
//
// The queue is this store rather than a separate system. A worker polling the
// same database that holds the work has no dual write to reconcile, which is
// why no transactional outbox appears here; an outbox becomes necessary only
// when dispatch targets something else.
//
// Every method is tenant scoped except Claim, which necessarily selects across
// tenants because a worker serves all of them. Claim is therefore the one
// method that must never be reachable from a request path.
type ExecutionStore interface {
	// Enqueue stores a pending execution, idempotently on ExecutionID, and
	// reports whether it created one or found it already present.
	//
	// Idempotence is not a convenience here: ExecutionID is derived from the
	// semantic request, so a repeated submission is necessarily the same
	// execution and must not become a second one.
	Enqueue(context.Context, ExecutionRequest) (bool, error)

	// Claim leases one pending execution for the given duration and reports
	// whether it found any.
	//
	// A lease rather than a lock, because a worker can die. When it expires the
	// execution becomes claimable again, which is safe precisely because
	// execution is deterministic: a second attempt reproduces byte-identical
	// artifacts, so at-least-once delivery cannot produce a divergent result.
	Claim(context.Context, time.Duration) (ExecutionRequest, bool, error)

	// Complete stores the result and moves the execution out of the queue.
	Complete(context.Context, ExecutionResult) error

	// Fail records that an execution could not be attempted, with a bounded
	// reason. A deterministic semantic rejection is NOT a failure here: that is
	// a completed execution whose result carries a typed failure, because the
	// computation produced a real answer.
	Fail(context.Context, TenantID, semantic.ExecutionID, string) error

	// Get reports the execution for this tenant, or absence. Another tenant's
	// execution is absent, never an error.
	Get(context.Context, TenantID, semantic.ExecutionID) (ExecutionRecord, bool, error)
}
