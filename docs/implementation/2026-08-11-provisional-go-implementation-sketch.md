# Maiden Lane Provisional Go Implementation Sketch

**Status:** Exploratory and expected to change substantially

**Date:** 2026-08-11

**Normative design:** [Maiden Lane High-Level Design](../superpowers/specs/2026-08-11-maiden-lane-high-level-design.md)

## 1. How to read this document

This is the requested rough Go and chi implementation draft. It tests whether
the approved boundaries can be expressed coherently in Go. It is not an
implementation plan, production milestone, infrastructure commitment, final
package layout, or authorization to scaffold these files.

When this sketch and the high-level design disagree, the high-level design
wins. Types and package names below are deliberately easy to replace. The
important claims are the dependency direction, the separation of semantic and
execution identity, deterministic entity construction, immutable patches,
fail-closed validation, and guarded publication.

The examples intentionally stop before deciding:

- The authored JSON or YAML rule syntax.
- The physical state representation used for large customer extracts.
- PostgreSQL table definitions and S3 segmentation thresholds.
- Authentication provider and customer authorization vocabulary.
- The first customer transformation or deployment milestone.
- The shape of a future SQL compiler.

## 2. Candidate repository shape

This is a conversation aid, not a final file manifest:

```text
go.mod
cmd/maiden-lane/
    main.go                 # dispatches serve or worker mode
internal/model/
    ids.go                  # semantic identifiers and status enums
    value.go                # closed value representation
    state.go                # entity graph
    patch.go                # structural operations
    plan.go                 # backend-independent semantic plan
internal/canonical/
    encode.go               # Maiden Lane-owned canonical bytes
internal/identity/
    identity.go             # InputID, SemanticRunID, ExecutionID, EntityID
internal/rules/
    ast.go                  # closed authored rule AST
internal/compile/
    compiler.go             # validation plus canonical planning
internal/execute/
    executor.go             # deterministic reference executor
    apply.go                # atomic patch application
internal/invariant/
    invariant.go            # operation, rule, and execution checks
internal/provenance/
    journal.go              # semantic journal artifacts
internal/promotion/
    gate.go                 # publication eligibility
internal/ports/
    ports.go                # infrastructure interfaces owned by Maiden Lane
internal/app/
    service.go              # use-case orchestration
internal/httpapi/
    router.go               # chi routes and middleware
    handlers.go             # wire translation only
    problem.go              # RFC 9457 responses
internal/adapters/
    memory/                 # local exploration and tests
    stochflow/              # byte hasher and optional comparison adapter
    postgres/               # later control-plane adapter
    s3/                     # later immutable artifact adapter
    batch/                  # later AWS Batch dispatcher
api/openapi.yaml            # later authoritative wire contract
```

The proposed module path is illustrative:

```go
module github.com/optimaldynamics/maiden-lane

go 1.26
```

The first direct HTTP dependency would be
`github.com/go-chi/chi/v5`. Chi composes standard `net/http` handlers and
middleware, which keeps the HTTP boundary small and replaceable. Dependency
versions should be pinned only when a real Go module is created.

## 3. Semantic identifiers

The semantic model uses distinct named types so an execution cannot be passed
where a semantic run is required by accident.

```go
// internal/model/ids.go
package model

type Digest string

type TenantID string
type CustomerID string
type InputLineageID string

type InputID string
type PlanID string
type SemanticRunID string
type ExecutionID string
type AttemptID string

type ExecutorIdentity string
type RuleID string
type EntityKind string
type EntityID string
type FieldPath string

type Scope struct {
	TenantID   TenantID
	CustomerID CustomerID
}

type ProvenancePolicy string

const (
	ProvenanceSummary ProvenancePolicy = "summary"
	ProvenanceChanges ProvenancePolicy = "changes"
	ProvenanceFull    ProvenancePolicy = "full"
)

func (p ProvenancePolicy) Publishable() bool {
	return p == ProvenanceChanges || p == ProvenanceFull
}

type ExecutionStatus string

const (
	ExecutionPending   ExecutionStatus = "pending"
	ExecutionRunning   ExecutionStatus = "running"
	ExecutionSucceeded ExecutionStatus = "succeeded"
	ExecutionFailed    ExecutionStatus = "failed"
)

type GateVerdict string

const (
	GateNotEvaluated GateVerdict = "not_evaluated"
	GatePass         GateVerdict = "pass"
	GateFail         GateVerdict = "fail"
)

type PublicationStatus string

const (
	PublicationUnpublished PublicationStatus = "unpublished"
	PublicationPublished   PublicationStatus = "published"
	PublicationSuperseded  PublicationStatus = "superseded"
)
```

`AttemptID` is operational and may be randomly generated outside semantic
packages. Randomness is forbidden for `InputID`, `PlanID`, `SemanticRunID`,
`ExecutionID`, state digests, patch digests, and entity IDs.

## 4. Canonical bytes and hashing

Maiden Lane owns the encoding. A hashing adapter never receives `any`; it only
receives already canonical bytes.

```go
// internal/ports/ports.go
package ports

import "github.com/optimaldynamics/maiden-lane/internal/model"

type ContentHasher interface {
	HashCanonical(data []byte) model.Digest
}
```

The eventual canonical format needs a versioned specification. This small
length-prefixed tuple encoder is enough to show the identity boundary without
mistaking ordinary JSON marshaling for the final canonical format:

```go
// internal/canonical/encode.go
package canonical

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

const FormatVersion = "maiden-lane-canonical-v1"

func StringTuple(parts ...string) []byte {
	var out bytes.Buffer
	writeString(&out, FormatVersion)
	for _, part := range parts {
		writeString(&out, part)
	}
	return out.Bytes()
}

func writeString(out *bytes.Buffer, value string) {
	if err := binary.Write(out, binary.BigEndian, uint64(len(value))); err != nil {
		panic(fmt.Sprintf("canonical length write: %v", err))
	}
	_, _ = out.WriteString(value)
}
```

The `panic` above represents an impossible in-memory `bytes.Buffer` write
failure, not input validation. Full plan, state, and patch encoding should use
closed canonical DTOs with explicit ordering and format versions. Semantic
identity must not depend on Go map iteration, `time.Time` formatting, floats,
or backend serialization.

The stochflow adapter remains deliberately boring:

```go
// internal/adapters/stochflow/hash.go
package stochflow

import (
	"github.com/optimaldynamics/maiden-lane/internal/model"
	stochjournal "github.com/optimaldynamics/stochflow/journal"
)

type Hasher struct{}

func (Hasher) HashCanonical(data []byte) model.Digest {
	return model.Digest(stochjournal.Hash(data))
}
```

## 5. Identity construction

Identity functions use canonical bytes and a byte-level hasher. The executor
identity and provenance policy affect `ExecutionID`, never `SemanticRunID`.

```go
// internal/identity/identity.go
package identity

import (
	"fmt"
	"slices"

	"github.com/optimaldynamics/maiden-lane/internal/canonical"
	"github.com/optimaldynamics/maiden-lane/internal/model"
	"github.com/optimaldynamics/maiden-lane/internal/ports"
)

type Builder struct {
	Hasher ports.ContentHasher
}

func (b Builder) SemanticRun(input model.InputID, plan model.PlanID) model.SemanticRunID {
	data := canonical.StringTuple("semantic-run-v1", string(input), string(plan))
	return model.SemanticRunID(b.Hasher.HashCanonical(data))
}

func (b Builder) Execution(
	semantic model.SemanticRunID,
	executor model.ExecutorIdentity,
	policy model.ProvenancePolicy,
) model.ExecutionID {
	data := canonical.StringTuple(
		"execution-v1",
		string(semantic),
		string(executor),
		string(policy),
	)
	return model.ExecutionID(b.Hasher.HashCanonical(data))
}

type Progenitor struct {
	Role string
	Ref  model.EntityRef
}

type SyntheticEntityInput struct {
	Lineage     model.InputLineageID
	Kind        model.EntityKind
	RuleID      model.RuleID
	Progenitors []Progenitor
	OutputKey   string
}

func (b Builder) SyntheticEntity(in SyntheticEntityInput) (model.EntityID, error) {
	if in.Lineage == "" || in.Kind == "" || in.RuleID == "" || in.OutputKey == "" {
		return "", fmt.Errorf("synthetic identity requires lineage, kind, rule, and output key")
	}

	progenitors := slices.Clone(in.Progenitors)
	slices.SortFunc(progenitors, func(a, z Progenitor) int {
		if a.Role != z.Role {
			if a.Role < z.Role {
				return -1
			}
			return 1
		}
		if a.Ref.Kind != z.Ref.Kind {
			if a.Ref.Kind < z.Ref.Kind {
				return -1
			}
			return 1
		}
		if a.Ref.ID < z.Ref.ID {
			return -1
		}
		if a.Ref.ID > z.Ref.ID {
			return 1
		}
		return 0
	})

	parts := []string{
		"synthetic-entity-v1",
		string(in.Lineage),
		string(in.Kind),
		string(in.RuleID),
		in.OutputKey,
	}
	for _, p := range progenitors {
		parts = append(parts, p.Role, string(p.Ref.Kind), string(p.Ref.ID))
	}

	return model.EntityID(b.Hasher.HashCanonical(canonical.StringTuple(parts...))), nil
}
```

The sketch sorts role-aware progenitors. An operator whose inputs are an
unordered set would normalize every role to the empty string before invoking
the builder. A split must produce a distinct semantic output key for each
output. Attempt IDs, execution IDs, clock values, and generated UUIDs are not
accepted by this function.

## 6. Closed values and entity graph

The rule engine needs a closed value space. A tagged value avoids letting
backend-native values leak into semantic comparisons.

```go
// internal/model/value.go
package model

type ValueKind string

const (
	ValueNull    ValueKind = "null"
	ValueString  ValueKind = "string"
	ValueInteger ValueKind = "integer"
	ValueDecimal ValueKind = "decimal"
	ValueBoolean ValueKind = "boolean"
	ValueInstant ValueKind = "instant"
)

type Value struct {
	Kind    ValueKind
	String  string
	Integer int64
	Boolean bool
}

func Null() Value                    { return Value{Kind: ValueNull} }
func String(v string) Value          { return Value{Kind: ValueString, String: v} }
func Integer(v int64) Value          { return Value{Kind: ValueInteger, Integer: v} }
func Decimal(canonical string) Value { return Value{Kind: ValueDecimal, String: canonical} }
func Boolean(v bool) Value           { return Value{Kind: ValueBoolean, Boolean: v} }
func Instant(rfc3339Nano string) Value {
	return Value{Kind: ValueInstant, String: rfc3339Nano}
}
```

Decimals and instants are shown as canonical strings to avoid float and local
timezone ambiguity. Constructors would eventually validate their formats.

```go
// internal/model/state.go
package model

type EntityRef struct {
	Kind EntityKind
	ID   EntityID
}

type Entity struct {
	Ref    EntityRef
	Fields map[FieldPath]Value
}

type RelationKind string

type Relation struct {
	Kind RelationKind
	From EntityRef
	To   EntityRef
}

type RelationKey struct {
	Kind RelationKind
	From EntityRef
	To   EntityRef
}

type State struct {
	SchemaDigest Digest
	LineageID    InputLineageID
	Entities     map[EntityRef]Entity
	Relations    map[RelationKey]Relation
	Digest       Digest
}

type ReferenceRow map[FieldPath]Value

type ReferenceSnapshot struct {
	Digest Digest
	Rows   []ReferenceRow
}

type World struct {
	Digest     Digest
	References map[string]ReferenceSnapshot
}
```

The maps are lookup structures, not canonical order. Encoders and planners
must sort their keys. A larger implementation could replace these maps with
immutable manifests or database-backed partitions without changing the public
semantic types used by the compiler.

## 7. Structural patches

A tagged union makes operation serialization explicit and prevents arbitrary
application callbacks from masquerading as semantic changes.

```go
// internal/model/patch.go
package model

type OperationKind string

const (
	OperationInsert   OperationKind = "insert"
	OperationDelete   OperationKind = "delete"
	OperationUpdate   OperationKind = "update"
	OperationMerge    OperationKind = "merge"
	OperationSplit    OperationKind = "split"
	OperationRelate   OperationKind = "relate"
	OperationUnrelate OperationKind = "unrelate"
)

type InsertOperation struct {
	Entity Entity
}

type DeleteOperation struct {
	Before Entity
}

type UpdateOperation struct {
	Before Entity
	After  Entity
}

type MergeOperation struct {
	Inputs []Entity
	Output Entity
}

type SplitOperation struct {
	Input   Entity
	Outputs []Entity
}

type RelateOperation struct {
	Relation Relation
}

type UnrelateOperation struct {
	Relation Relation
}

type Operation struct {
	Kind     OperationKind
	Insert   *InsertOperation
	Delete   *DeleteOperation
	Update   *UpdateOperation
	Merge    *MergeOperation
	Split    *SplitOperation
	Relate   *RelateOperation
	Unrelate *UnrelateOperation
}

type EvidenceRef struct {
	Kind   string
	Digest Digest
}

type Patch struct {
	RuleID     RuleID
	Operations []Operation
	Evidence   []EvidenceRef
	Digest     Digest
}
```

`Operation.ValidateShape` would require exactly one payload matching `Kind`.
Before-images are authoritative. A future large-state representation may store
those images by digest while preserving the same logical operation.

The patch applier is a semantic service, not a method that mutates `State` in
place:

```go
// internal/execute/apply.go
package execute

import (
	"context"

	"github.com/optimaldynamics/maiden-lane/internal/model"
)

type PatchApplier interface {
	Apply(ctx context.Context, before model.State, patch model.Patch) (model.State, error)
	Undo(ctx context.Context, after model.State, patch model.Patch) (model.State, error)
}
```

An implementation clones or writes into an isolated candidate, checks every
before-image against the predecessor, applies all operations, computes the
candidate digest, and returns only after the complete patch succeeds.

## 8. Closed rule AST and semantic plan

The authored grammar is represented as data. No rule contains a Go function,
SQL fragment, or dynamically selected field name.

```go
// internal/rules/ast.go
package rules

import "github.com/optimaldynamics/maiden-lane/internal/model"

type ExprKind string

const (
	ExprLiteral ExprKind = "literal"
	ExprField   ExprKind = "field"
	ExprAll     ExprKind = "all"
	ExprAny     ExprKind = "any"
	ExprNot     ExprKind = "not"
	ExprEqual   ExprKind = "equal"
	ExprLess    ExprKind = "less"
	ExprExists  ExprKind = "exists"
	ExprIsNull  ExprKind = "is_null"
	ExprAdd     ExprKind = "add"
	ExprLookup  ExprKind = "lookup"
)

type Expr struct {
	Kind     ExprKind
	Literal  *model.Value
	Field    model.FieldPath
	Args     []Expr
	Relation string
	Key      *Expr
	Value    model.FieldPath
}

type TransformKind string

const (
	TransformSetField TransformKind = "set_field"
	TransformInsert   TransformKind = "insert"
	TransformDelete   TransformKind = "delete"
	TransformMerge    TransformKind = "merge"
	TransformSplit    TransformKind = "split"
	TransformRelate   TransformKind = "relate"
	TransformUnrelate TransformKind = "unrelate"
)

type Transform struct {
	Kind       TransformKind
	TargetKind model.EntityKind
	Target     model.FieldPath
	Value      Expr
	OutputKey  Expr
}

type InvariantSpec struct {
	Code      string
	Predicate Expr
	Protected bool
}

type Rule struct {
	ID             model.RuleID
	When           Expr
	Transform      Transform
	DeclaredReads  []model.FieldPath
	DeclaredWrites []model.FieldPath
	After          []model.RuleID
	Preconditions  []InvariantSpec
	Postconditions []InvariantSpec
}

type RuleSet struct {
	FormatVersion string
	Rules         []Rule
}
```

The exact AST will change as real mapper cases are studied. The closed-union
shape is the important decision: the compiler can derive types, reads, writes,
determinism, and backend requirements from every node.

```go
// internal/model/plan.go
package model

type BackendRequirement string

const (
	RequiresStructuralJournal BackendRequirement = "structural_journal"
	RequiresPinnedLookups     BackendRequirement = "pinned_lookups"
)

type PlannedTransform struct {
	RuleID        RuleID
	Operator      string
	DerivedReads  []FieldPath
	DerivedWrites []FieldPath
	PayloadFormat string
	Payload       []byte
	PayloadDigest Digest
}

type Plan struct {
	FormatVersion       string
	CompilerVersion     string
	SchemaDigest        Digest
	RuleSetDigest       Digest
	Levels              [][]PlannedTransform
	ExecutionInvariants []string
	Requirements        []BackendRequirement
	ID                  PlanID
}
```

The plan embeds a canonical, backend-neutral compiled payload and its digest;
execution records reference the immutable plan rather than repeating that
payload. `PayloadFormat` is versioned per closed operator family. A backend
must reject payload formats it is not certified to interpret.

## 9. Compiler seam

The compiler pipeline is explicit even while individual rule operators remain
under design.

```go
// internal/compile/compiler.go
package compile

import (
	"context"

	"github.com/optimaldynamics/maiden-lane/internal/model"
	"github.com/optimaldynamics/maiden-lane/internal/rules"
)

type Schema struct {
	Digest model.Digest
}

type StaticValidator interface {
	ValidateSchema(ctx context.Context, schema Schema) error
	ValidateRules(ctx context.Context, schema Schema, set rules.RuleSet) error
}

type DependencyPlanner interface {
	Build(ctx context.Context, schema Schema, set rules.RuleSet) ([][]model.PlannedTransform, error)
}

type PlanFinalizer interface {
	Finalize(ctx context.Context, draft model.Plan) (model.Plan, error)
}

type Compiler struct {
	Version  string
	Validate StaticValidator
	Plan     DependencyPlanner
	Finalize PlanFinalizer
}

func (c Compiler) Compile(
	ctx context.Context,
	schema Schema,
	set rules.RuleSet,
	ruleSetDigest model.Digest,
) (model.Plan, error) {
	if err := c.Validate.ValidateSchema(ctx, schema); err != nil {
		return model.Plan{}, err
	}
	if err := c.Validate.ValidateRules(ctx, schema, set); err != nil {
		return model.Plan{}, err
	}

	levels, err := c.Plan.Build(ctx, schema, set)
	if err != nil {
		return model.Plan{}, err
	}

	draft := model.Plan{
		FormatVersion:   "maiden-lane-plan-v1",
		CompilerVersion: c.Version,
		SchemaDigest:    schema.Digest,
		RuleSetDigest:   ruleSetDigest,
		Levels:          levels,
		Requirements: []model.BackendRequirement{
			model.RequiresStructuralJournal,
			model.RequiresPinnedLookups,
		},
	}
	return c.Finalize.Finalize(ctx, draft)
}
```

`StaticValidator` owns type checking, output-key validation, declared-versus-
derived access checks, and operator composition. `DependencyPlanner` owns cycle
detection, explicit ordering, write-conflict rejection, stable topological
levels, and lexical ordering within a level. `PlanFinalizer` canonicalizes the
draft, computes `PlanID`, and verifies round-trip encoding.

These interfaces can collapse later if real implementations prove the split is
ceremonial. They are shown separately now to expose the reasoning boundaries.

## 10. Transformer and invariant seams

Compiled operators resolve to deterministic Go transformers:

```go
// internal/execute/executor.go
package execute

import (
	"context"

	"github.com/optimaldynamics/maiden-lane/internal/model"
)

type Transformer interface {
	Propose(
		ctx context.Context,
		transform model.PlannedTransform,
		state model.State,
		world model.World,
	) (model.Patch, error)
}

type TransformerRegistry interface {
	Resolve(operator string) (Transformer, bool)
}
```

Invariant failures are values with stable codes. They do not expose raw
customer fields in API problems or logs.

```go
// internal/invariant/invariant.go
package invariant

import (
	"context"

	"github.com/optimaldynamics/maiden-lane/internal/model"
)

type Scope string

const (
	ScopeOperation Scope = "operation"
	ScopeRule      Scope = "rule"
	ScopeExecution Scope = "execution"
)

type Violation struct {
	Code       string
	Scope      Scope
	RuleID     model.RuleID
	EntityRefs []model.EntityRef
	Evidence   []model.EvidenceRef
	Protected  bool
}

type Engine interface {
	BeforePatch(ctx context.Context, state model.State, patch model.Patch) []Violation
	AfterPatch(ctx context.Context, before, after model.State, patch model.Patch) []Violation
	AfterExecution(ctx context.Context, state model.State, plan model.Plan) []Violation
}
```

The team-HOS rule would be an ordinary transformer plus postconditions. Its
merge output key derives from canonical driver roles and IDs. Its protected
postconditions can require a common aggregation anchor and
`driving_duration <= elapsed_duration`; failure prevents the merge from
committing.

## 11. Semantic journal

Accepted entries describe committed semantics. Rejected proposals and
violations are stored separately so replay never mistakes a failed proposal
for state history.

```go
// internal/provenance/journal.go
package provenance

import "github.com/optimaldynamics/maiden-lane/internal/model"

type Entry struct {
	Sequence          uint64
	RuleID            model.RuleID
	BeforeStateDigest model.Digest
	AfterStateDigest  model.Digest
	Patch             model.Patch
	PatchDigest       model.Digest
	Evidence          []model.EvidenceRef
}

type Manifest struct {
	ExecutionID  model.ExecutionID
	Policy       model.ProvenancePolicy
	EntryDigests []model.Digest
	FinalState   model.Digest
	Digest       model.Digest
}

type RejectedProposal struct {
	RuleID           model.RuleID
	StateDigest      model.Digest
	PatchDigest      model.Digest
	ViolationDigests []model.Digest
}
```

At `summary` provenance, the durable representation may replace `Patch` with a
summary artifact. At `changes` and `full`, it preserves structural operations
and before-images or their immutable references. The semantic digest contract
must define the representation for each level before backend certification.

## 12. Deterministic reference executor

The executor returns a candidate and evidence. It does not publish anything.

```go
// internal/execute/executor.go, continued
package execute

import (
	"context"
	"fmt"

	"github.com/optimaldynamics/maiden-lane/internal/invariant"
	"github.com/optimaldynamics/maiden-lane/internal/model"
	"github.com/optimaldynamics/maiden-lane/internal/provenance"
)

type JournalBuilder interface {
	Accepted(
		sequence uint64,
		before, after model.State,
		patch model.Patch,
	) (provenance.Entry, error)
	Rejected(
		state model.State,
		patch model.Patch,
		violations []invariant.Violation,
	) (provenance.RejectedProposal, error)
}

type Result struct {
	Status      model.ExecutionStatus
	Candidate   model.State
	Entries     []provenance.Entry
	Rejected    *provenance.RejectedProposal
	Violations  []invariant.Violation
	FailureCode string
}

type Executor struct {
	Registry   TransformerRegistry
	Apply      PatchApplier
	Invariants invariant.Engine
	Journal    JournalBuilder
}

func (e Executor) Execute(
	ctx context.Context,
	plan model.Plan,
	initial model.State,
	world model.World,
) (Result, error) {
	state := initial
	entries := make([]provenance.Entry, 0)
	var sequence uint64

	for _, level := range plan.Levels {
		// The reference executor is sequential even within independent levels.
		for _, transform := range level {
			if err := ctx.Err(); err != nil {
				return Result{}, err
			}

			worker, ok := e.Registry.Resolve(transform.Operator)
			if !ok {
				return Result{}, fmt.Errorf("executor not certified for operator %q", transform.Operator)
			}

			patch, err := worker.Propose(ctx, transform, state, world)
			if err != nil {
				return Result{
					Status:      model.ExecutionFailed,
					Candidate:   state,
					Entries:     entries,
					FailureCode: "transform_proposal_failed",
				}, nil
			}

			if violations := e.Invariants.BeforePatch(ctx, state, patch); len(violations) > 0 {
				rejected, buildErr := e.Journal.Rejected(state, patch, violations)
				if buildErr != nil {
					return Result{}, buildErr
				}
				return Result{
					Status:      model.ExecutionFailed,
					Candidate:   state,
					Entries:     entries,
					Rejected:    &rejected,
					Violations:  violations,
					FailureCode: "operation_invariant_failed",
				}, nil
			}

			candidate, err := e.Apply.Apply(ctx, state, patch)
			if err != nil {
				return Result{
					Status:      model.ExecutionFailed,
					Candidate:   state,
					Entries:     entries,
					FailureCode: "patch_rejected",
				}, nil
			}

			if violations := e.Invariants.AfterPatch(ctx, state, candidate, patch); len(violations) > 0 {
				rejected, buildErr := e.Journal.Rejected(state, patch, violations)
				if buildErr != nil {
					return Result{}, buildErr
				}
				return Result{
					Status:      model.ExecutionFailed,
					Candidate:   state,
					Entries:     entries,
					Rejected:    &rejected,
					Violations:  violations,
					FailureCode: "rule_invariant_failed",
				}, nil
			}

			entry, err := e.Journal.Accepted(sequence, state, candidate, patch)
			if err != nil {
				return Result{}, err
			}
			entries = append(entries, entry)
			sequence++
			state = candidate
		}
	}

	violations := e.Invariants.AfterExecution(ctx, state, plan)
	return Result{
		Status:     model.ExecutionSucceeded,
		Candidate:  state,
		Entries:    entries,
		Violations: violations,
	}, nil
}
```

The distinction between returned infrastructure errors and classified semantic
failure results is intentional. AWS Batch may retry an infrastructure error.
It must not retry a deterministic invariant failure in the hope that semantics
change between attempts.

The result does not set a gate verdict. Application orchestration combines the
execution result, execution-level invariants, provenance policy, comparison,
and backend certification into that separate decision.

## 13. Infrastructure ports

Ports carry explicit scope and semantic identities. Adapters do not infer a
tenant from an artifact key.

```go
// internal/ports/ports.go, continued
package ports

import (
	"context"
	"errors"

	"github.com/optimaldynamics/maiden-lane/internal/invariant"
	"github.com/optimaldynamics/maiden-lane/internal/model"
	"github.com/optimaldynamics/maiden-lane/internal/provenance"
)

var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict")
)

type ArtifactRef struct {
	Digest model.Digest
	URI    string
}

type ArtifactStore interface {
	PutImmutable(ctx context.Context, scope model.Scope, canonical []byte) (ArtifactRef, error)
	Get(ctx context.Context, scope model.Scope, ref ArtifactRef) ([]byte, error)
}

type JournalCheckpoint struct {
	Sequence uint64
	Entry    provenance.Entry
	State    ArtifactRef
}

type JournalStore interface {
	AppendCheckpoint(
		ctx context.Context,
		scope model.Scope,
		execution model.ExecutionID,
		attempt model.AttemptID,
		expectedSequence uint64,
		checkpoint JournalCheckpoint,
	) error
	ReadPrefix(
		ctx context.Context,
		scope model.Scope,
		execution model.ExecutionID,
	) ([]JournalCheckpoint, error)
	Finalize(
		ctx context.Context,
		scope model.Scope,
		execution model.ExecutionID,
		manifest provenance.Manifest,
	) error
}

type InputRecord struct {
	Scope model.Scope
	ID    model.InputID
	State ArtifactRef
	World ArtifactRef
}

type InputStore interface {
	Get(ctx context.Context, scope model.Scope, id model.InputID) (InputRecord, error)
}

type PlanStore interface {
	Put(ctx context.Context, scope model.Scope, plan model.Plan) (model.Plan, bool, error)
	Get(ctx context.Context, scope model.Scope, id model.PlanID) (model.Plan, error)
}

type SemanticRunRecord struct {
	Scope model.Scope
	ID    model.SemanticRunID
	Input model.InputID
	Plan  model.PlanID
}

type ExecutionRecord struct {
	Scope         model.Scope
	ID            model.ExecutionID
	SemanticRunID model.SemanticRunID
	Executor      model.ExecutorIdentity
	Provenance    model.ProvenancePolicy
	Status        model.ExecutionStatus
	Gate          model.GateVerdict
	Output        *ArtifactRef
	Journal       *ArtifactRef
	Version       uint64
}

type ExecutionStore interface {
	CreateOrGet(
		ctx context.Context,
		semantic SemanticRunRecord,
		execution ExecutionRecord,
	) (ExecutionRecord, bool, error)
	Get(ctx context.Context, scope model.Scope, id model.ExecutionID) (ExecutionRecord, error)
	GetSemanticRun(ctx context.Context, scope model.Scope, id model.SemanticRunID) (SemanticRunRecord, error)
	StartAttempt(ctx context.Context, scope model.Scope, execution model.ExecutionID, attempt model.AttemptID) error
	CompleteExecution(ctx context.Context, completion ExecutionCompletion) error
	RecordGate(ctx context.Context, gate GateRecord) error
}

type ExecutionCompletion struct {
	Scope           model.Scope
	ExecutionID     model.ExecutionID
	ExpectedVersion uint64
	Status          model.ExecutionStatus
	Output          *ArtifactRef
	Journal         *provenance.Manifest
	Violations      []invariant.Violation
}

type GateRecord struct {
	Scope           model.Scope
	ExecutionID     model.ExecutionID
	ExpectedVersion uint64
	Verdict         model.GateVerdict
	Evidence        ArtifactRef
}

type Dispatcher interface {
	Enqueue(ctx context.Context, scope model.Scope, execution model.ExecutionID) error
}

type ComparisonPair struct {
	InputID   model.InputID
	Baseline  model.ExecutionID
	Candidate model.ExecutionID
}

type ComparisonRequest struct {
	Pairs []ComparisonPair
}

type Comparison struct {
	Digest      model.Digest
	Regressions []string
}

type CandidateComparator interface {
	Compare(ctx context.Context, scope model.Scope, req ComparisonRequest) (Comparison, error)
}

type Publication struct {
	Scope       model.Scope
	ExecutionID model.ExecutionID
	Version     uint64
}

type PublicationStore interface {
	Get(ctx context.Context, scope model.Scope) (Publication, error)
	CompareAndSwap(
		ctx context.Context,
		scope model.Scope,
		expectedVersion uint64,
		next Publication,
	) (Publication, error)
}
```

A comparator must reject an empty or partial pair set. For every pair it loads
both execution records in the supplied scope and verifies that each underlying
semantic run references the declared `InputID`. This keeps baseline and
candidate evaluation paired even when a corpus contains many inputs.

`JournalStore.AppendCheckpoint` is the crash boundary: the next state artifact
must already exist immutably, and the store atomically advances exactly one
expected semantic sequence. Resume loads the ordered prefix, rejects gaps or
digest mismatches, verifies the original plan/input/executor identities, and
continues after the final accepted entry. It never fills a gap by silently
rerunning an earlier transform against current external data.

`CompleteExecution` and `RecordGate` are separate optimistic transitions.
Completing a worker cannot manufacture a gate pass; the comparison and
promotion use case records that later decision with its immutable evidence
artifact. Publication remains a third transition in `PublicationStore`.

The physical adapter APIs will probably split as real persistence behavior
emerges. The important constraints are scope on every call, immutable artifact
writes, idempotent content creation, optimistic publication, and no database
types crossing into semantic packages.

## 14. Application service

The application service calculates identities, records an execution and its
outbox intent, and returns immediately. In a real PostgreSQL adapter,
`CreateExecutionAndEnqueue` would be one transaction rather than two unrelated
port calls. The combined port below makes that requirement visible.

```go
// internal/app/service.go
package app

import (
	"context"

	"github.com/optimaldynamics/maiden-lane/internal/identity"
	"github.com/optimaldynamics/maiden-lane/internal/model"
	"github.com/optimaldynamics/maiden-lane/internal/ports"
)

type ExecutionCreator interface {
	CreateExecutionAndEnqueue(
		ctx context.Context,
		semantic ports.SemanticRunRecord,
		execution ports.ExecutionRecord,
	) (ports.ExecutionRecord, bool, error)
}

type SubmitExecution struct {
	Scope      model.Scope
	InputID    model.InputID
	PlanID     model.PlanID
	Executor   model.ExecutorIdentity
	Provenance model.ProvenancePolicy
}

type SubmitResult struct {
	SemanticRunID model.SemanticRunID
	ExecutionID   model.ExecutionID
	Created       bool
	Status        model.ExecutionStatus
}

type Service struct {
	Identity   identity.Builder
	Executions ExecutionCreator
	Plans      ports.PlanStore
	Inputs     ports.InputStore
}

func (s Service) SubmitExecution(ctx context.Context, cmd SubmitExecution) (SubmitResult, error) {
	if _, err := s.Plans.Get(ctx, cmd.Scope, cmd.PlanID); err != nil {
		return SubmitResult{}, err
	}
	if _, err := s.Inputs.Get(ctx, cmd.Scope, cmd.InputID); err != nil {
		return SubmitResult{}, err
	}

	semanticID := s.Identity.SemanticRun(cmd.InputID, cmd.PlanID)
	executionID := s.Identity.Execution(semanticID, cmd.Executor, cmd.Provenance)

	record, created, err := s.Executions.CreateExecutionAndEnqueue(
		ctx,
		ports.SemanticRunRecord{
			Scope: cmd.Scope,
			ID:    semanticID,
			Input: cmd.InputID,
			Plan:  cmd.PlanID,
		},
		ports.ExecutionRecord{
			Scope:         cmd.Scope,
			ID:            executionID,
			SemanticRunID: semanticID,
			Executor:      cmd.Executor,
			Provenance:    cmd.Provenance,
			Status:        model.ExecutionPending,
			Gate:          model.GateNotEvaluated,
		},
	)
	if err != nil {
		return SubmitResult{}, err
	}

	return SubmitResult{
		SemanticRunID: semanticID,
		ExecutionID:   executionID,
		Created:       created,
		Status:        record.Status,
	}, nil
}
```

The submission use case accepts only a previously registered `InputID` and
verifies it in the caller's scope. A separate input-registration use case will
validate immutable state and world artifacts, canonicalize their descriptor,
and derive `InputID = H(S0, C)`. Its wire shape is intentionally not invented
before the input manifest is designed.

## 15. Promotion gate

Promotion is a pure decision over recorded facts. Publication is a separate
effect performed only after the decision passes.

```go
// internal/promotion/gate.go
package promotion

import "github.com/optimaldynamics/maiden-lane/internal/model"

type Facts struct {
	ExecutionStatus    model.ExecutionStatus
	Provenance         model.ProvenancePolicy
	ProtectedFailures  int
	ComparisonComplete bool
	MetricRegressions  int
	DigestsConsistent  bool
	BackendCertified   bool
}

type Decision struct {
	Verdict model.GateVerdict
	Codes   []string
}

func Evaluate(f Facts) Decision {
	codes := make([]string, 0)
	if f.ExecutionStatus != model.ExecutionSucceeded {
		codes = append(codes, "execution_not_succeeded")
	}
	if !f.Provenance.Publishable() {
		codes = append(codes, "provenance_not_publishable")
	}
	if f.ProtectedFailures > 0 {
		codes = append(codes, "protected_invariant_failed")
	}
	if !f.ComparisonComplete {
		codes = append(codes, "comparison_not_complete")
	}
	if f.MetricRegressions > 0 {
		codes = append(codes, "protected_metric_regressed")
	}
	if !f.DigestsConsistent {
		codes = append(codes, "artifact_digest_mismatch")
	}
	if !f.BackendCertified {
		codes = append(codes, "backend_not_certified")
	}
	if len(codes) > 0 {
		return Decision{Verdict: model.GateFail, Codes: codes}
	}
	return Decision{Verdict: model.GatePass}
}
```

There is no `force` Boolean. A future soft-policy approval is a separately
authorized decision type; it cannot waive protected invariants by smuggling an
override into this function.

## 16. chi API boundary

Chi is used only for routing and middleware composition. Handlers use standard
`net/http`, translate DTOs, call an application interface, and return RFC 9457
problems. Transformation logic never appears here.

```go
// internal/httpapi/router.go
package httpapi

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/optimaldynamics/maiden-lane/internal/app"
	"github.com/optimaldynamics/maiden-lane/internal/model"
	"github.com/optimaldynamics/maiden-lane/internal/ports"
)

type Application interface {
	SubmitExecution(context.Context, app.SubmitExecution) (app.SubmitResult, error)
	GetSemanticRun(context.Context, model.Scope, model.SemanticRunID) (ports.SemanticRunRecord, error)
	GetExecution(context.Context, model.Scope, model.ExecutionID) (ports.ExecutionRecord, error)
	Publish(context.Context, model.Scope, model.ExecutionID, uint64) (ports.Publication, error)
	Ready(context.Context) error
}

type ScopeMiddleware func(http.Handler) http.Handler

type Handler struct {
	App Application
}

func NewRouter(h Handler, scope ScopeMiddleware) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)

	r.Get("/healthz", h.health)
	r.Get("/readyz", h.ready)

	r.Route("/v1", func(r chi.Router) {
		r.Use(scope)

		r.Get("/semantic-runs/{semanticRunID}", h.getSemanticRun)
		r.Post("/executions", h.createExecution)
		r.Get("/executions/{executionID}", h.getExecution)
		r.Get("/executions/{executionID}/journal", h.getJournal)
		r.Get("/executions/{executionID}/violations", h.getViolations)
		r.Post("/publications", h.publish)
		r.Get("/publications/{customerID}", h.getPublication)
	})

	return r
}
```

Plan and comparison handlers are omitted from this one router block only to
keep the example readable; the high-level design's routes remain authoritative.
They would call separate application interfaces and follow the same pattern.

The authenticated scope uses a private context key:

```go
// internal/httpapi/router.go, continued
package httpapi

import (
	"context"
	"net/http"

	"github.com/optimaldynamics/maiden-lane/internal/model"
)

type scopeKey struct{}

func WithScope(next http.Handler, resolve func(*http.Request) (model.Scope, error)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scope, err := resolve(r)
		if err != nil {
			writeProblem(w, http.StatusUnauthorized, "unauthorized", "authentication required")
			return
		}
		ctx := context.WithValue(r.Context(), scopeKey{}, scope)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func requestScope(r *http.Request) (model.Scope, bool) {
	scope, ok := r.Context().Value(scopeKey{}).(model.Scope)
	return scope, ok
}
```

### 16.1 Execution request handler

```go
// internal/httpapi/handlers.go
package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/optimaldynamics/maiden-lane/internal/app"
	"github.com/optimaldynamics/maiden-lane/internal/model"
	"github.com/optimaldynamics/maiden-lane/internal/ports"
)

const maxJSONBody = 1 << 20

type createExecutionRequest struct {
	InputID    model.InputID          `json:"input_id"`
	PlanID     model.PlanID           `json:"plan_id"`
	Executor   model.ExecutorIdentity `json:"executor"`
	Provenance model.ProvenancePolicy `json:"provenance"`
}

type createExecutionResponse struct {
	SemanticRunID model.SemanticRunID   `json:"semantic_run_id"`
	ExecutionID   model.ExecutionID     `json:"execution_id"`
	Status        model.ExecutionStatus `json:"status"`
}

func (h Handler) createExecution(w http.ResponseWriter, r *http.Request) {
	scope, ok := requestScope(r)
	if !ok {
		writeProblem(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}

	var body createExecutionRequest
	if err := decodeJSON(w, r, &body); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_request", "request body is invalid")
		return
	}
	if body.InputID == "" || body.PlanID == "" || body.Executor == "" {
		writeProblem(w, http.StatusBadRequest, "invalid_request", "input_id, plan_id, and executor are required")
		return
	}
	if body.Provenance != model.ProvenanceSummary &&
		body.Provenance != model.ProvenanceChanges &&
		body.Provenance != model.ProvenanceFull {
		writeProblem(w, http.StatusBadRequest, "invalid_provenance", "provenance must be summary, changes, or full")
		return
	}

	result, err := h.App.SubmitExecution(r.Context(), app.SubmitExecution{
		Scope:      scope,
		InputID:    body.InputID,
		PlanID:     body.PlanID,
		Executor:   body.Executor,
		Provenance: body.Provenance,
	})
	if err != nil {
		writeApplicationError(w, err)
		return
	}

	status := http.StatusAccepted
	if !result.Created {
		status = http.StatusOK
	}
	writeJSON(w, status, createExecutionResponse{
		SemanticRunID: result.SemanticRunID,
		ExecutionID:   result.ExecutionID,
		Status:        result.Status,
	})
}

func (h Handler) getExecution(w http.ResponseWriter, r *http.Request) {
	scope, ok := requestScope(r)
	if !ok {
		writeProblem(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}
	record, err := h.App.GetExecution(
		r.Context(),
		scope,
		model.ExecutionID(chi.URLParam(r, "executionID")),
	)
	if err != nil {
		writeApplicationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, record)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON value")
	}
	return nil
}
```

### 16.2 Problem responses

```go
// internal/httpapi/problem.go
package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/optimaldynamics/maiden-lane/internal/ports"
)

type problem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Code   string `json:"code"`
	Detail string `json:"detail,omitempty"`
}

func writeProblem(w http.ResponseWriter, status int, code, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(problem{
		Type:   "urn:maiden-lane:problem:" + code,
		Title:  http.StatusText(status),
		Status: status,
		Code:   code,
		Detail: detail,
	})
}

func writeApplicationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ports.ErrNotFound):
		writeProblem(w, http.StatusNotFound, "not_found", "resource not found")
	case errors.Is(err, ports.ErrConflict):
		writeProblem(w, http.StatusConflict, "conflict", "resource version changed")
	default:
		writeProblem(w, http.StatusInternalServerError, "internal_error", "request failed")
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
```

The URN is an illustrative stable RFC 9457 type namespace. A deployment may
instead configure a durable documentation origin while preserving the stable
`code` field.

### 16.3 Guarded publication handler

The wire request carries an expected pointer version. It cannot request a
semantic override.

```go
// internal/httpapi/handlers.go, continued
package httpapi

import (
	"net/http"

	"github.com/optimaldynamics/maiden-lane/internal/model"
)

type publishRequest struct {
	ExecutionID     model.ExecutionID `json:"execution_id"`
	ExpectedVersion uint64            `json:"expected_version"`
}

func (h Handler) publish(w http.ResponseWriter, r *http.Request) {
	scope, ok := requestScope(r)
	if !ok {
		writeProblem(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}

	var body publishRequest
	if err := decodeJSON(w, r, &body); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_request", "request body is invalid")
		return
	}
	if body.ExecutionID == "" {
		writeProblem(w, http.StatusBadRequest, "invalid_request", "execution_id is required")
		return
	}

	publication, err := h.App.Publish(
		r.Context(), scope, body.ExecutionID, body.ExpectedVersion,
	)
	if err != nil {
		writeApplicationError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, publication)
}
```

The application implementation of `Publish` must reload the execution inside
the same authorization scope, require `ExecutionSucceeded` and `GatePass`,
verify immutable artifact references, then call `CompareAndSwap`.

## 17. Illustrative HTTP exchange

Submission:

```http
POST /v1/executions HTTP/1.1
Authorization: Bearer <credential>
Content-Type: application/json

{
  "input_id": "sha256:input...",
  "plan_id": "sha256:plan...",
  "executor": "go@sha256:image...",
  "provenance": "changes"
}
```

```http
HTTP/1.1 202 Accepted
Content-Type: application/json

{
  "semantic_run_id": "sha256:semantic...",
  "execution_id": "sha256:execution...",
  "status": "pending"
}
```

Submitting the same request again returns `200 OK` and the same
`execution_id`. Changing only `executor` or `provenance` retains the same
`semantic_run_id` and returns a different `execution_id`.

Publication:

```http
POST /v1/publications HTTP/1.1
Authorization: Bearer <credential>
Content-Type: application/json

{
  "execution_id": "sha256:execution...",
  "expected_version": 17
}
```

A protected invariant failure returns a non-publishable execution record. It
does not turn `POST /v1/publications` into an override endpoint.

## 18. AWS Batch adapter seam

The application owns dispatch intent; the adapter owns AWS mechanics.

```go
// internal/adapters/batch/dispatcher.go
package batch

import (
	"context"

	"github.com/optimaldynamics/maiden-lane/internal/model"
)

type Submitter interface {
	Submit(
		ctx context.Context,
		jobName string,
		command []string,
		tags map[string]string,
	) (jobID string, err error)
}

type SafeTagger interface {
	TenantTag(model.Scope) string
	ExecutionTag(model.ExecutionID) string
}

type Dispatcher struct {
	Submitter Submitter
	Tags      SafeTagger
}

func (d Dispatcher) Enqueue(
	ctx context.Context,
	scope model.Scope,
	execution model.ExecutionID,
) error {
	_, err := d.Submitter.Submit(
		ctx,
		"maiden-lane-execution",
		[]string{"worker", "--execution-id", string(execution)},
		map[string]string{
			"tenant_digest":    d.Tags.TenantTag(scope),
			"execution_digest": d.Tags.ExecutionTag(execution),
		},
	)
	return err
}
```

`SafeTagger` produces bounded display digests; raw tenant and execution IDs
should not become unbounded CloudWatch metric dimensions. The production
adapter will use the AWS SDK v2, an approved job definition, private networking,
and a task role. None of those choices enter semantic identity.

The worker mode accepts only `ExecutionID`, loads the immutable execution
record, acquires a lease, creates a fresh operational `AttemptID`, and then
loads the pinned plan, input, and world. AWS retry classification maps process
exit codes from explicit failure classes; invariant failures are permanent.

## 19. Command boundary

One image can expose API and worker modes without sharing their lifecycle:

```go
// cmd/maiden-lane/main.go
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: maiden-lane <serve|worker>")
		os.Exit(2)
	}

	var err error
	switch os.Args[1] {
	case "serve":
		err = runServer(os.Args[2:])
	case "worker":
		err = runWorker(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "unknown mode %q\n", os.Args[1])
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "maiden-lane failed")
		os.Exit(1)
	}
}
```

`runServer` and `runWorker` are composition roots. They may read environment
configuration and construct AWS/PostgreSQL adapters. Semantic packages may
not.

### 19.1 Observability placement

OpenTelemetry belongs at HTTP, application-use-case, worker, and adapter
boundaries. It does not belong in canonical encoders, identity builders,
compiler decisions, patch application, or invariant predicates. Traces may
carry `SemanticRunID`, `ExecutionID`, `PlanID`, and `AttemptID`; metrics must
not use those values, customer IDs, or entity IDs as dimensions. Logs contain
stable failure codes and digests, never state fields, evidence bodies, journal
payloads, authored rules, or generated SQL.

The initial code does not need a domain-level telemetry interface. Ordinary
OpenTelemetry spans can wrap application methods and adapters without making
observability part of semantic output. If worker progress events later become
a product requirement, they should be operational records explicitly excluded
from state and journal digests.

## 20. Tests that validate the shape

These examples are not a commitment to the first production milestone. They
show the kinds of tests needed before the interfaces deserve to solidify.

The identity examples use a deterministic test implementation of the hashing
port:

```go
type testHasher struct{}

func (testHasher) HashCanonical(data []byte) model.Digest {
	sum := sha256.Sum256(data)
	return model.Digest("sha256:" + hex.EncodeToString(sum[:]))
}
```

### 20.1 Semantic identity is executor-independent

```go
func TestSemanticRunIdentityIgnoresExecutorAndProvenance(t *testing.T) {
	b := identity.Builder{Hasher: testHasher{}}
	semantic := b.SemanticRun("input-1", "plan-1")

	goExecution := b.Execution(semantic, "go@v1", model.ProvenanceChanges)
	sqlExecution := b.Execution(semantic, "sql@v1", model.ProvenanceChanges)
	fullGoExecution := b.Execution(semantic, "go@v1", model.ProvenanceFull)

	if goExecution == sqlExecution || goExecution == fullGoExecution {
		t.Fatal("physical execution choices must change ExecutionID")
	}
	if semantic != b.SemanticRun("input-1", "plan-1") {
		t.Fatal("semantic identity must be stable")
	}
}
```

### 20.2 Synthetic entity identity ignores input order

```go
func TestSyntheticEntityIDUsesCanonicalProgenitors(t *testing.T) {
	b := identity.Builder{Hasher: testHasher{}}
	left := identity.SyntheticEntityInput{
		Lineage: "challenger",
		Kind:    "team",
		RuleID:  "team_collapse",
		Progenitors: []identity.Progenitor{
			{Role: "driver_2", Ref: model.EntityRef{Kind: "driver", ID: "B"}},
			{Role: "driver_1", Ref: model.EntityRef{Kind: "driver", ID: "A"}},
		},
		OutputKey: "active-team",
	}
	right := left
	right.Progenitors = slices.Clone(left.Progenitors)
	slices.Reverse(right.Progenitors)

	a, err := b.SyntheticEntity(left)
	if err != nil {
		t.Fatal(err)
	}
	z, err := b.SyntheticEntity(right)
	if err != nil {
		t.Fatal(err)
	}
	if a != z {
		t.Fatalf("identity changed with input order: %q != %q", a, z)
	}
}
```

### 20.3 Publication refuses a failed gate

```go
func TestGateRejectsProtectedInvariantFailure(t *testing.T) {
	decision := promotion.Evaluate(promotion.Facts{
		ExecutionStatus:    model.ExecutionSucceeded,
		Provenance:         model.ProvenanceChanges,
		ProtectedFailures:  1,
		ComparisonComplete: true,
		DigestsConsistent:  true,
		BackendCertified:   true,
	})
	if decision.Verdict != model.GateFail {
		t.Fatalf("verdict = %q, want fail", decision.Verdict)
	}
}
```

### 20.4 API submission is semantically idempotent

```go
func TestCreateExecutionReturnsExistingIdentity(t *testing.T) {
	request := `{
	  "input_id":"sha256:input",
	  "plan_id":"sha256:plan",
	  "executor":"go@sha256:image",
	  "provenance":"changes"
	}`

	first := httptest.NewRecorder()
	router.ServeHTTP(first, httptest.NewRequest(http.MethodPost, "/v1/executions", strings.NewReader(request)))
	if first.Code != http.StatusAccepted {
		t.Fatalf("first status = %d", first.Code)
	}

	second := httptest.NewRecorder()
	router.ServeHTTP(second, httptest.NewRequest(http.MethodPost, "/v1/executions", strings.NewReader(request)))
	if second.Code != http.StatusOK {
		t.Fatalf("second status = %d", second.Code)
	}
	if !bytes.Equal(first.Body.Bytes(), second.Body.Bytes()) {
		t.Fatal("idempotent submission returned a different execution")
	}
}
```

The handler test will need an authenticated-scope middleware and in-memory
application fake. Those are test wiring, not semantics.

Before any of these types become stable, the same exploratory suite should
also contain canonical-format test vectors; apply/undo property tests for every
structural operation; compiler permutation, conflict, and cycle tests; journal
gap and crash-resume tests; randomized map-insertion determinism tests; tenant
isolation tests; and differential state/journal/invariant digest tests for every
candidate backend.

## 21. Expected change hotspots

The following choices are intentionally provisional and should be expected to
move as mapper code and real extracts are studied:

| Area | Stable requirement | Replaceable sketch choice |
|---|---|---|
| State | Immutable semantic value with deterministic digest | In-memory maps |
| Values | Closed, typed, backend-neutral | Current small tagged union |
| Rule authoring | Closed and statically analyzable | Exact AST and external syntax |
| Planning | One canonical semantic plan | Current plan fields and package split |
| Patch storage | Structural before-images remain authoritative | Embedded entities versus digest references |
| Provenance | Required executor capability | Segment size and physical manifest format |
| Comparison | Same pinned corpus and protected regression policy | Exact metric schema and stochflow adapter |
| API | Async execution, scoped reads, guarded publication | DTO details and route additions |
| Persistence | Atomic outbox, leases, immutable artifacts, CAS publication | PostgreSQL schema and S3 key layout |
| AWS | ECS/Fargate API plus Batch/Fargate workers | Initial resource sizing and later EC2 capacity |

Package boundaries should be collapsed when experience proves they add no
independent policy. Conversely, large packages should split when real operators
or adapters acquire distinct reasons to change. This document should evolve by
preserving the stable requirements in the middle column, not by preserving the
sample code verbatim.

## 22. What a later implementation plan must decide

A later, separately reviewed implementation plan should be written only after
the mapper constraints, sample source data, and operating environment are
available. It must select one independently testable slice and then specify:

- The exact Go module and dependency versions.
- The first supported entity schema and rule operators.
- The canonical format test vectors.
- The first immutable input/world manifest.
- The storage adapters required for that slice.
- The exact API endpoints implemented in that slice.
- Red-green test steps and verification commands.
- Deployment and rollback boundaries, if deployment is in scope.

This sketch deliberately makes none of those sequencing commitments.

## 23. References

- [Maiden Lane High-Level Design](../superpowers/specs/2026-08-11-maiden-lane-high-level-design.md)
- [chi v5 package documentation](https://pkg.go.dev/github.com/go-chi/chi/v5)
- [chi official repository](https://github.com/go-chi/chi)
- [Stochflow local architecture](../../../stochflow/README.md)
