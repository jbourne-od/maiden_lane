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
	OperatorSelectAndAssign
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

// FieldAssignment is one field written on every member of a qualifying group.
//
// Value is evaluated in MEMBER scope -- once per member, with that member bound -- so two
// members of one group may receive different values. Target names a field on the selector's
// own kind, because a member is the only entity the assignment binds.
type FieldAssignment struct {
	Target FieldPath
	Value  Expr
}

// SelectAssignDeclaration is a rule that applies to a selected population rather than to
// entities named one by one.
//
// This is the payload the two frozen operators could not express. They resolve their inputs
// by canonical source key, so one rule handles one team; this one declares a selector and
// applies to every group it finds.
type SelectAssignDeclaration struct {
	// Selector is what the rule applies to. It must be GROUPED. An ungrouped selector is
	// refused, not silently treated as one member per group: Guard is a group-scoped
	// expression, and the three group node kinds are refused outside group scope, so an
	// ungrouped rule could only ever carry a guard that this operator would then reject
	// at evaluation rather than at compile time.
	Selector Selector

	// Guard is a group-scoped FILTER: groups satisfying it receive the assignments, groups
	// that do not are skipped.
	//
	// THE ALTERNATIVE WAS AN OBLIGATION -- every selected group must satisfy the guard or
	// the transition refuses -- and it is recorded here because it is the reading this
	// engine's fail-closed habits pull towards, and because reinterpreting Guard later
	// would change what already encoded rules do without changing their bytes.
	//
	// Filter won on two grounds. It is the plain meaning of an authored condition: Where
	// already filters candidates one entity at a time and nobody calls that silent, and a
	// group-level condition that could only ever refuse would leave "apply to the teams
	// where every driver shares a domicile" inexpressible -- which is a rule the actual
	// project has. And an obligation cannot distinguish which groups a predicate describes,
	// so altering the predicate could only ever flip the whole transition between accepted
	// and refused, never change WHICH entities a patch touches.
	//
	// What filtering does NOT get is silence. A guard that cannot be evaluated against a
	// group -- an absent field, an overflowing sum -- refuses the transition rather than
	// quietly excluding that group, because "this group does not qualify" and "this group
	// could not be assessed" are different facts and only one of them is the author's
	// intent. And if no group qualifies, the rule refuses rather than proposing an empty
	// patch. Skipping is a decision the guard makes about a group; it is never a decision
	// the executor makes about an error.
	Guard Expr

	// Assignments are the writes applied to every member of every qualifying group. At
	// least one is required: a rule that writes nothing produces no patch, and the journal
	// has no representation for an accepted transition without one.
	Assignments []FieldAssignment
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
	SelectAssign   *SelectAssignDeclaration
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

	// SelectionCardinalityInvalid is the policy Selection deliberately left to its consumer:
	// a group that matched the predicate and the grouping but not the declared cardinality
	// is an attributable refusal, not a group quietly dropped from the result.
	SelectionCardinalityInvalid InvariantCode = "SELECTION_CARDINALITY_INVALID"

	// SelectionEmpty refuses a rule that selected no group at all.
	//
	// This is forced by the journal model rather than chosen: an accepted entry carries a
	// patch, NewPatch refuses an empty operation list, and replay re-applies every entry, so
	// there is no representation for an accepted transition that did nothing. Recorded as a
	// limitation because it is one -- a rule that legitimately applies to nothing cannot be
	// written today -- and it fails closed rather than open.
	SelectionEmpty InvariantCode = "SELECTION_EMPTY"

	// SelectionGuardUnsatisfied refuses a rule whose selector found groups but whose guard
	// admitted none of them. The rule has nothing to write, and an empty patch is not a
	// thing this engine can accept.
	SelectionGuardUnsatisfied InvariantCode = "SELECTION_GUARD_UNSATISFIED"

	// SelectionExpressionUnavailable is an expression the rule needed and the data could not
	// answer: an absent field, an overflowing arithmetic node. It covers the guard and the
	// assignment values alike, because in both places the fact is the same -- the rule could
	// not be evaluated -- and the recorded evidence names which fields were consulted.
	//
	// It is emphatically NOT the guard returning false. A guard that answers "no" has been
	// evaluated; one that raises has not, and treating the second as the first would exclude
	// a group for a reason the author never wrote.
	SelectionExpressionUnavailable InvariantCode = "SELECTION_EXPRESSION_UNAVAILABLE"
)

// AllInvariantCodes is the complete v1 vocabulary, in declaration order.
//
// It exists so the boundary mappings can be tested against the vocabulary instead of against
// a hand-copied list. Three walkers over these codes live outside this package -- the
// observation code mapping, its string rendering, and the telemetry dimension -- and nothing
// but a test forces them to reach every one.
func AllInvariantCodes() []InvariantCode {
	return []InvariantCode{
		DeclaredSourceNotFound, DeclaredSourceKindInvalid,
		TeamAssignmentKeyInvalid, TeamAssignmentKeyMismatch, TeamMemberCardinalityInvalid,
		HOSTupleIncomplete, HOSDurationInvalid, HOSAnchorMismatch, HOSAggregateInvalid,
		SelectionCardinalityInvalid, SelectionEmpty, SelectionGuardUnsatisfied,
		SelectionExpressionUnavailable,
	}
}

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

func cloneSelectAssign(input *SelectAssignDeclaration) *SelectAssignDeclaration {
	if input == nil {
		return nil
	}
	clone := *input
	clone.Selector = cloneSelector(input.Selector)
	clone.Guard = cloneExpr(input.Guard)
	clone.Assignments = make([]FieldAssignment, len(input.Assignments))
	for i, assignment := range input.Assignments {
		clone.Assignments[i] = FieldAssignment{Target: assignment.Target, Value: cloneExpr(assignment.Value)}
	}
	return &clone
}

func cloneTransformation(input TransformationDeclaration) TransformationDeclaration {
	return TransformationDeclaration{
		ID: input.ID, Operator: input.Operator, DeclaredReads: slices.Clone(input.DeclaredReads),
		DeclaredWrites: slices.Clone(input.DeclaredWrites), After: slices.Clone(input.After),
		Form: cloneForm(input.Form), Aggregate: cloneAggregate(input.Aggregate),
		SelectAssign: cloneSelectAssign(input.SelectAssign),
	}
}

func cloneProfile(input ProfileDeclaration) ProfileDeclaration {
	return ProfileDeclaration{Key: input.Key, Scope: input.Scope, Aggregation: input.Aggregation,
		Requirements: slices.Clone(input.Requirements), Implies: slices.Clone(input.Implies)}
}
