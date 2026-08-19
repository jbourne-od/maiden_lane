package semantic

import (
	"bytes"
	"fmt"
	"sort"
)

// CardinalityKind is the closed vocabulary for how many members a group may hold.
type CardinalityKind uint8

const (
	// CardinalityInvalid is the zero value and refuses. A selector that forgot to state its
	// cardinality must not silently mean "any", because "any" is a real choice an author
	// makes and forgetting is not.
	CardinalityInvalid CardinalityKind = iota
	CardinalityAny
	CardinalityExactly
	CardinalityAtLeast
)

// Cardinality declares how many members a group must hold to be selected.
//
// This is where `len(sources) != 2` goes. The old form was a hard-coded pair inside an
// executor; here the shape a rule expects is authored, checked at compile time for internal
// consistency, and applied to every group uniformly.
type Cardinality struct {
	Kind  CardinalityKind
	Count uint64
}

// Selector declares what a rule applies to.
//
// Ungrouped, it applies once per matching entity. Grouped, it applies once per distinct group
// key, with the transform seeing that key's members. This is the thing whose absence made a
// fleet inexpressible: the two frozen operators name their inputs by canonical source key, so
// one rule handles one team.
type Selector struct {
	// Kind is the entity kind selected from. It is the only kind the predicate and the
	// grouping expression may name, because it is the only kind the selection binds.
	Kind EntityKind

	// Where filters candidates. Nil selects every entity of Kind.
	Where *Expr

	// GroupBy partitions the matches. Nil leaves them ungrouped, which is one member per
	// group rather than one group of everything -- an ungrouped rule applies per entity.
	GroupBy *Expr

	// Members constrains group size. Required, including when ungrouped, because a
	// cardinality nobody stated is not the same as one stated as Any.
	Members Cardinality
}

// CompiledSelector is a type-checked selector with its canonical bytes.
type CompiledSelector struct {
	kind      EntityKind
	schema    SchemaDigest
	where     *CompiledExpression
	groupBy   *CompiledExpression
	members   Cardinality
	canonical []byte
}

// Kind returns the entity kind this selector binds.
func (c CompiledSelector) Kind() EntityKind { return c.kind }

// Grouped reports whether this selector partitions its matches.
func (c CompiledSelector) Grouped() bool { return c.groupBy != nil }

// CanonicalBytes returns a copy of the v1 selector bytes.
func (c CompiledSelector) CanonicalBytes() []byte { return bytes.Clone(c.canonical) }

// CompileSelector validates a selector against a schema and identifies it.
func CompileSelector(
	schema Schema, version CompilerSemanticsVersion, selector Selector,
) (CompiledSelector, error) {
	// UNCONDITIONALLY, at this function's own door. An earlier version validated the version
	// only inside CompileExpression, which is reached only when Where or GroupBy is non-nil,
	// so a bare selector compiled with an empty version written straight into its identity --
	// and only the EMPTY case, since encoder.string still refuses invalid UTF-8. That is a
	// third sibling entry point relying on a check that lives in one of the other two, which
	// is the hole CompileExpression's own comment warns about, opened one function over.
	if !validSemanticName(string(version)) {
		return CompiledSelector{}, fmt.Errorf("selector has no usable compiler semantics version")
	}
	if !validSemanticName(string(selector.Kind)) {
		return CompiledSelector{}, fmt.Errorf("selector has no entity kind")
	}
	if _, declared := schema.entityDeclaration(selector.Kind); !declared {
		return CompiledSelector{}, fmt.Errorf(
			"selector names undeclared entity kind %q", selector.Kind)
	}
	if err := checkCardinality(selector); err != nil {
		return CompiledSelector{}, err
	}

	compiled := CompiledSelector{
		kind: selector.Kind, schema: schema.Digest(), members: selector.Members}
	if selector.Where != nil {
		where, err := compileSelectorExpr(schema, version, selector.Kind, *selector.Where)
		if err != nil {
			return CompiledSelector{}, fmt.Errorf("selector predicate: %w", err)
		}
		if where.Type() != TypeBool {
			return CompiledSelector{}, fmt.Errorf(
				"selector predicate is %s, not bool", where.Type())
		}
		compiled.where = &where
	}
	if selector.GroupBy != nil {
		groupBy, err := compileSelectorExpr(schema, version, selector.Kind, *selector.GroupBy)
		if err != nil {
			return CompiledSelector{}, fmt.Errorf("selector grouping: %w", err)
		}
		// A bool group key partitions into at most two groups and reads as a filter somebody
		// wrote in the wrong field. Refused because it is far more likely to be a mistake
		// than an intent, and permitting it costs a real diagnostic.
		if groupBy.Type() == TypeBool {
			return CompiledSelector{}, fmt.Errorf("selector groups by a bool")
		}
		compiled.groupBy = &groupBy
	}

	canonical, err := encodeSelector(version, compiled)
	if err != nil {
		return CompiledSelector{}, fmt.Errorf("canonicalize selector: %w", err)
	}
	compiled.canonical = canonical
	return compiled, nil
}

// compileSelectorExpr compiles one of the selector's expressions and requires every field it
// reads to belong to the selected kind.
//
// THIS IS THE ANSWER TO A QUESTION SLICE 1 COULD NOT ASK. An expression there type-checks
// `equal(driver.x, team.y)` because ExprType carries only the scalar kind. Under a selector
// the question becomes answerable: the selection binds one entity of one kind, so a path
// naming any other kind has no referent, and admitting it would leave evaluation to invent
// one -- the same silent reinterpretation the no-ambient-scope decision exists to prevent.
func compileSelectorExpr(
	schema Schema, version CompilerSemanticsVersion, kind EntityKind, expr Expr,
) (CompiledExpression, error) {
	compiled, err := CompileExpression(schema, version, expr)
	if err != nil {
		return CompiledExpression{}, err
	}
	for _, path := range readFieldPaths(expr) {
		named, _ := splitFieldPath(path)
		if named != kind {
			return CompiledExpression{}, fmt.Errorf(
				"reads %q, but this selector binds only %q", path, kind)
		}
	}
	return compiled, nil
}

// readFieldPaths collects every field path an expression names, in tree order.
func readFieldPaths(expr Expr) []FieldPath {
	var paths []FieldPath
	var walk func(Expr)
	walk = func(node Expr) {
		switch node.Kind {
		case ExprField, ExprExists:
			paths = append(paths, node.Field)
		default:
			for i := range node.Args {
				walk(node.Args[i])
			}
		}
	}
	walk(expr)
	return paths
}

// checkCardinality refuses a declaration that cannot describe any group.
func checkCardinality(selector Selector) error {
	switch selector.Members.Kind {
	case CardinalityAny:
		if selector.Members.Count != 0 {
			return fmt.Errorf("cardinality any carries a count")
		}
	case CardinalityExactly, CardinalityAtLeast:
		if selector.Members.Count == 0 {
			// Exactly zero selects nothing and at-least zero is Any spelled differently.
			// Both are almost certainly an unset field rather than an intent.
			return fmt.Errorf("cardinality %d requires a positive count", selector.Members.Kind)
		}
	default:
		return fmt.Errorf("selector has no cardinality")
	}
	// An ungrouped selector yields one member per group, so any constraint other than
	// exactly-one is unsatisfiable and would select nothing at all, silently.
	if selector.GroupBy == nil {
		if selector.Members.Kind == CardinalityExactly && selector.Members.Count != 1 {
			return fmt.Errorf("an ungrouped selector yields one member per group, so " +
				"exactly-n is unsatisfiable for n other than 1")
		}
		if selector.Members.Kind == CardinalityAtLeast && selector.Members.Count > 1 {
			return fmt.Errorf("an ungrouped selector yields one member per group, so " +
				"at-least-n is unsatisfiable for n above 1")
		}
	}
	return nil
}

// Group is one selected group: a key and its members, in canonical order.
type Group struct {
	key     Value
	members []Entity
}

// Key returns the group key. It is the zero Value for an ungrouped selection.
func (g Group) Key() Value { return g.key }

// Members returns copies of the group's entities in canonical order.
func (g Group) Members() []Entity { return cloneEntities(g.members) }

// Select applies the selector to a state.
//
// ORDER IS AN IDENTITY PROBLEM HERE, not a presentation one. A set-scoped rule iterates, and
// any nondeterminism in that iteration changes patch order, the journal, and therefore every
// downstream identity. Two guarantees make it deterministic:
//
//   - Members inherit the state's canonical (kind, EntityID) order. NewState sorts by
//     compareEntityRefs and refuses duplicates, so filtering in that order is already
//     canonical and this code adds no sort of its own.
//   - Groups are ordered by the canonical bytes of their key, not by encounter order and not
//     by map iteration. A map is used to accumulate members, and its iteration order is
//     never observed.
//
// Selecting nothing is a successful selection over an empty result, not an error: a rule whose
// predicate matches no entity has run and found nothing, which is a different fact from a rule
// that could not run.
func (c CompiledSelector) Select(state State) ([]Group, error) {
	if c.canonical == nil {
		return nil, fmt.Errorf("selector was not compiled")
	}
	if state.Schema().Digest() != c.schema {
		// The selector was type-checked against a different schema, so its field paths carry
		// no guarantee here. Refused rather than re-checked, because re-checking would make
		// this a second compiler.
		return nil, fmt.Errorf("selector was compiled against a different schema")
	}

	type accumulator struct {
		key     Value
		encoded string
		members []Entity
	}
	byKey := map[string]*accumulator{}
	var ordered []*accumulator

	for _, entity := range state.Entities() {
		if entity.Ref().Kind != c.kind {
			continue
		}
		if c.where != nil {
			matched, err := evaluateBool(c.where.Expression(), entity)
			if err != nil {
				return nil, fmt.Errorf("selector predicate: %w", err)
			}
			if !matched {
				continue
			}
		}

		if c.groupBy == nil {
			// Ungrouped: one group per entity, with no key.
			ordered = append(ordered, &accumulator{members: []Entity{entity}})
			continue
		}
		key, err := evaluateValue(c.groupBy.Expression(), entity)
		if err != nil {
			return nil, fmt.Errorf("selector grouping: %w", err)
		}
		encoded, err := encodeGroupKey(key)
		if err != nil {
			return nil, err
		}
		existing, present := byKey[encoded]
		if !present {
			existing = &accumulator{key: key, encoded: encoded}
			byKey[encoded] = existing
			ordered = append(ordered, existing)
		}
		existing.members = append(existing.members, entity)
	}

	if c.groupBy != nil {
		// By key bytes, so the result does not depend on which entity was seen first and
		// certainly not on map iteration.
		sort.Slice(ordered, func(i, j int) bool { return ordered[i].encoded < ordered[j].encoded })
	}

	groups := make([]Group, 0, len(ordered))
	for _, accumulated := range ordered {
		if !c.members.admits(uint64(len(accumulated.members))) {
			continue
		}
		groups = append(groups, Group{key: accumulated.key, members: accumulated.members})
	}
	return groups, nil
}

// admits reports whether a group of this size satisfies the declared cardinality.
func (c Cardinality) admits(size uint64) bool {
	switch c.Kind {
	case CardinalityAny:
		return true
	case CardinalityExactly:
		return size == c.Count
	case CardinalityAtLeast:
		return size >= c.Count
	default:
		return false
	}
}

// encodeGroupKey renders a key as canonical bytes, which is what groups are ordered by.
func encodeGroupKey(key Value) (string, error) {
	var encoder canonicalEncoder
	encoder.value(key)
	encoded, err := encoder.bytes()
	if err != nil {
		return "", fmt.Errorf("canonicalize group key: %w", err)
	}
	return string(encoded), nil
}

// encodeSelector writes the v1 selector tuple.
func encodeSelector(
	version CompilerSemanticsVersion, selector CompiledSelector,
) ([]byte, error) {
	var encoder canonicalEncoder
	encoder.tag(selectorDomainTag)
	encoder.string(string(version))
	encoder.digest(string(selector.schema))
	encoder.string(string(selector.kind))
	encoder.byte(byte(selector.members.Kind))
	encoder.uint64(selector.members.Count)
	// Presence is encoded explicitly, so a selector with no predicate cannot encode the same
	// as one whose predicate happens to be absent for another reason.
	encoder.optional(selector.where != nil, func() {
		encodeExpr(&encoder, selector.where.Expression())
	})
	encoder.optional(selector.groupBy != nil, func() {
		encodeExpr(&encoder, selector.groupBy.Expression())
	})
	return encoder.bytes()
}
