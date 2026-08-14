package semantic

import "slices"

// FieldPath is a statically declared entity-kind and field pair encoded as
// "kind.field". Compilation resolves both components against the pinned schema.
type FieldPath string

// ProfileKey and OutputSlotKey identify declarations inside a compiler request;
// they are semantic keys, not content identities.
type (
	ProfileKey    string
	OutputSlotKey string
)

// OperatorKind is the complete certified transformation union for v1.
type OperatorKind uint8

const (
	OperatorFormRelatedEntity OperatorKind = iota + 1
	OperatorAggregateRelatedFields
)

// OutputKeyKind is the closed typed output-key expression supported by the
// related-entity operator.
type OutputKeyKind uint8

const OutputKeyCommonSourceField OutputKeyKind = 1

// AggregatePredicateKind is the complete v1 predicate vocabulary. Emitted
// anchor/reduction equality is derived from FieldCopy and FieldReduction rather
// than being author-selectable predicate variants.
type AggregatePredicateKind uint8

const (
	CompleteTuple AggregatePredicateKind = iota + 1
	NonNegativeInt
	EqualFieldAcrossSources
	LessOrEqualFields
)

// ReductionKind is the complete v1 reduction vocabulary.
type ReductionKind uint8

const ReduceInt64Max ReductionKind = 1

// SourceReference is an exact typed reference resolved within the input
// lineage at execution time. Canonical source keys remain declaration content;
// they are never operational telemetry.
type SourceReference struct {
	Kind               EntityKind
	CanonicalSourceKey string
}

// FieldCopy declares one source/destination field correspondence.
type FieldCopy struct {
	Source      FieldPath
	Destination FieldPath
}

// OutputKeyExpression derives a synthetic output key from the common value of
// one declared source field.
type OutputKeyExpression struct {
	Kind  OutputKeyKind
	Field FieldPath
}

// OutputSlotReference identifies one declared output of an earlier rule.
type OutputSlotReference struct {
	Rule RuleID
	Slot OutputSlotKey
}

// AggregatePredicate contains only statically resolvable field references.
type AggregatePredicate struct {
	Kind   AggregatePredicateKind
	Fields []FieldPath
}

// FieldReduction declares one typed source-to-destination reduction.
type FieldReduction struct {
	Kind        ReductionKind
	Source      FieldPath
	Destination FieldPath
}

// FormRelatedEntityDeclaration is the first closed transformation payload.
type FormRelatedEntityDeclaration struct {
	SourceKind    EntityKind
	Sources       []SourceReference
	OutputKind    EntityKind
	OutputSlot    OutputSlotKey
	GroupingField FieldPath
	SourceCount   uint64
	CopiedFields  []FieldCopy
	RelationKind  RelationKind
	OutputKey     *OutputKeyExpression
}

// AggregateRelatedFieldsDeclaration is the second closed transformation
// payload. It contains field references and closed tags, never callbacks.
type AggregateRelatedFieldsDeclaration struct {
	Target              OutputSlotReference
	RelationKind        RelationKind
	SourceKind          EntityKind
	RequiredSourceTuple []FieldPath
	Predicates          []AggregatePredicate
	Anchor              FieldCopy
	Reductions          []FieldReduction
	ResultPredicates    []AggregatePredicate
}

// TransformationDeclaration is a closed tagged union. Exactly one payload
// must be present and it must agree with Operator.
type TransformationDeclaration struct {
	ID             RuleID
	Operator       OperatorKind
	DeclaredReads  []FieldPath
	DeclaredWrites []FieldPath
	After          []RuleID
	Form           *FormRelatedEntityDeclaration
	Aggregate      *AggregateRelatedFieldsDeclaration
}

// CheckpointDeclaration names a complete transformation prefix.
type CheckpointDeclaration struct {
	Key   CheckpointKey
	After RuleID
}

// RulesetDeclaration contains source declarations whose authored order is not
// semantic.
type RulesetDeclaration struct {
	Transformations []TransformationDeclaration
	Checkpoints     []CheckpointDeclaration
}

// ProfileScopeKind is the complete v1 readiness-scope vocabulary.
type ProfileScopeKind uint8

const AllEntitiesOfKind ProfileScopeKind = 1

// ProfileAggregationKind is the complete v1 aggregation vocabulary.
type ProfileAggregationKind uint8

const AllSelected ProfileAggregationKind = 1

// RequirementAtomKind is the complete v1 readiness-atom vocabulary.
type RequirementAtomKind uint8

const FieldPresent RequirementAtomKind = 1

// RequirementCode is a stable, closed readiness code distinct from invariant
// and compiler-diagnostic codes.
type RequirementCode string

const (
	TeamAssignmentKeyRequired     RequirementCode = "TEAM_ASSIGNMENT_KEY_REQUIRED"
	TeamAggregationAnchorRequired RequirementCode = "TEAM_AGGREGATION_ANCHOR_REQUIRED"
	TeamElapsedDurationRequired   RequirementCode = "TEAM_ELAPSED_DURATION_REQUIRED"
	TeamDrivingDurationRequired   RequirementCode = "TEAM_DRIVING_DURATION_REQUIRED"
)

// ProfileScope explicitly selects every entity of one semantic kind.
type ProfileScope struct {
	Kind       ProfileScopeKind
	EntityKind EntityKind
}

// RequirementAtom is one closed field-presence requirement.
type RequirementAtom struct {
	Code  RequirementCode
	Kind  RequirementAtomKind
	Field FieldPath
}

// ProfileDeclaration is a closed consumer-completeness declaration.
type ProfileDeclaration struct {
	Key          ProfileKey
	Scope        ProfileScope
	Aggregation  ProfileAggregationKind
	Requirements []RequirementAtom
	Implies      []ProfileKey
}

// CompileRequest contains every input capable of affecting compilation.
type CompileRequest struct {
	Schema                   SchemaDeclaration
	Rules                    RulesetDeclaration
	Profiles                 []ProfileDeclaration
	CompilerSemanticsVersion CompilerSemanticsVersion
}

// AccessKind distinguishes statically derived entity, relation, and field
// facts. AccessMode distinguishes reads from writes.
type AccessKind uint8
type AccessMode uint8

const (
	AccessEntity AccessKind = iota + 1
	AccessRelation
	AccessField
)

const (
	AccessRead AccessMode = iota + 1
	AccessWrite
)

// SemanticAccess is one compiler-derived access. Only the member selected by
// Kind contributes to its canonical key.
type SemanticAccess struct {
	Kind         AccessKind
	Mode         AccessMode
	EntityKind   EntityKind
	RelationKind RelationKind
	Field        FieldPath
}

// InvariantCode is the ratified closed protected rule-invariant vocabulary.
type InvariantCode string

const (
	DeclaredSourceNotFound       InvariantCode = "DECLARED_SOURCE_NOT_FOUND"
	DeclaredSourceKindInvalid    InvariantCode = "DECLARED_SOURCE_KIND_INVALID"
	TeamAssignmentKeyInvalid     InvariantCode = "TEAM_ASSIGNMENT_KEY_INVALID"
	TeamAssignmentKeyMismatch    InvariantCode = "TEAM_ASSIGNMENT_KEY_MISMATCH"
	TeamMemberCardinalityInvalid InvariantCode = "TEAM_MEMBER_CARDINALITY_INVALID"
	HOSTupleIncomplete           InvariantCode = "HOS_TUPLE_INCOMPLETE"
	HOSDurationInvalid           InvariantCode = "HOS_DURATION_INVALID"
	HOSAnchorMismatch            InvariantCode = "HOS_ANCHOR_MISMATCH"
	HOSAggregateInvalid          InvariantCode = "HOS_AGGREGATE_INVALID"
)

// InvariantScope is the complete scope vocabulary for derived obligations.
type InvariantScope uint8

const (
	InvariantRulePrecondition InvariantScope = iota + 1
	InvariantCandidatePostcondition
	InvariantOperation
	InvariantCheckpointPrefix
)

// InvariantDeclaration is one immutable compiler-derived obligation.
type InvariantDeclaration struct {
	key          string
	code         InvariantCode
	scope        InvariantScope
	reads        []FieldPath
	appliesAfter RuleID
}

// Key returns the operator-relative canonical declaration key.
func (d InvariantDeclaration) Key() string { return d.key }

// Code returns the stable protected invariant code.
func (d InvariantDeclaration) Code() InvariantCode { return d.code }

// Scope returns the closed evaluation boundary.
func (d InvariantDeclaration) Scope() InvariantScope { return d.scope }

// ReadSet returns a defensive copy of the fields used by this obligation.
func (d InvariantDeclaration) ReadSet() []FieldPath { return slices.Clone(d.reads) }

// AppliesAfter returns the transformation boundary where the obligation first
// applies.
func (d InvariantDeclaration) AppliesAfter() RuleID { return d.appliesAfter }

func cloneOutputKey(input *OutputKeyExpression) *OutputKeyExpression {
	if input == nil {
		return nil
	}
	result := *input
	return &result
}

func cloneForm(input *FormRelatedEntityDeclaration) *FormRelatedEntityDeclaration {
	if input == nil {
		return nil
	}
	return &FormRelatedEntityDeclaration{
		SourceKind: input.SourceKind, Sources: slices.Clone(input.Sources), OutputKind: input.OutputKind,
		OutputSlot: input.OutputSlot, GroupingField: input.GroupingField, SourceCount: input.SourceCount,
		CopiedFields: slices.Clone(input.CopiedFields), RelationKind: input.RelationKind, OutputKey: cloneOutputKey(input.OutputKey),
	}
}

func clonePredicates(input []AggregatePredicate) []AggregatePredicate {
	result := make([]AggregatePredicate, len(input))
	for i, predicate := range input {
		result[i] = AggregatePredicate{Kind: predicate.Kind, Fields: slices.Clone(predicate.Fields)}
	}
	return result
}

func cloneAggregate(input *AggregateRelatedFieldsDeclaration) *AggregateRelatedFieldsDeclaration {
	if input == nil {
		return nil
	}
	return &AggregateRelatedFieldsDeclaration{
		Target: input.Target, RelationKind: input.RelationKind, SourceKind: input.SourceKind,
		RequiredSourceTuple: slices.Clone(input.RequiredSourceTuple), Predicates: clonePredicates(input.Predicates),
		Anchor: input.Anchor, Reductions: slices.Clone(input.Reductions), ResultPredicates: clonePredicates(input.ResultPredicates),
	}
}

func cloneTransformation(input TransformationDeclaration) TransformationDeclaration {
	return TransformationDeclaration{
		ID: input.ID, Operator: input.Operator, DeclaredReads: slices.Clone(input.DeclaredReads),
		DeclaredWrites: slices.Clone(input.DeclaredWrites), After: slices.Clone(input.After),
		Form: cloneForm(input.Form), Aggregate: cloneAggregate(input.Aggregate),
	}
}

func cloneProfile(input ProfileDeclaration) ProfileDeclaration {
	return ProfileDeclaration{Key: input.Key, Scope: input.Scope, Aggregation: input.Aggregation,
		Requirements: slices.Clone(input.Requirements), Implies: slices.Clone(input.Implies)}
}
