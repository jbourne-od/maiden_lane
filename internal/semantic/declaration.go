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
	OperatorSelectAndAssign OperatorKind = 1
)

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
type SelectAssignDeclaration struct {
	Selector    Selector
	Guard       Expr
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

// RequirementCode is an author-supplied readiness requirement code.
type RequirementCode string

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
func AllInvariantCodes() []InvariantCode {
	return []InvariantCode{
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
		SelectAssign: cloneSelectAssign(input.SelectAssign),
	}
}

func cloneProfile(input ProfileDeclaration) ProfileDeclaration {
	return ProfileDeclaration{Key: input.Key, Scope: input.Scope, Aggregation: input.Aggregation,
		Requirements: slices.Clone(input.Requirements), Implies: slices.Clone(input.Implies)}
}
