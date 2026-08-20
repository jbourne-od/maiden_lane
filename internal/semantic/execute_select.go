package semantic

import "fmt"

// executeSelectAndAssign runs a selector-scoped rule.
//
// This is the first executor that does not know its inputs by name. The two frozen operators
// resolve each source by canonical source key, so one rule handles one team and a fleet needs
// one rule per team; this one is handed a population and iterates it.
//
// EVERY REFUSAL BELOW IS AN INVARIANT REJECTION, not a returned error, whenever the cause is
// the data. A returned error aborts the transition with no attributable code, which is right
// for a broken artifact and wrong for a driver with a missing field.
func executeSelectAndAssign(
	binding RunBinding, transformation CompiledTransformation, state State, journal Journal,
) (TransitionOutcome, error) {
	base := TransitionOutcome{state: state, journal: Journal{entries: cloneJournalEntries(journal.entries)}}
	declaration := transformation.Declaration()
	payload := declaration.SelectAssign
	if payload == nil {
		return base, fmt.Errorf("execute select: operator carries no select-assign payload")
	}
	selection, err := transformation.selector.Select(state)
	if err != nil {
		// The selector was not compiled, or was compiled against another schema. Both are
		// artifact faults rather than data faults.
		return base, fmt.Errorf("execute select: %w", err)
	}
	if !selection.Ran() {
		// Unreachable while Select returns a ran Selection on every non-error path, and
		// checked anyway because this is the exact value Selection.Ran exists to guard: a
		// Selection that did not run reports no groups AND no violations, so reading it as
		// data turns a selection that never happened into a population that was empty.
		return base, fmt.Errorf("execute select: selection did not run")
	}
	schema := state.Schema()
	guardFields := fieldNames(readFieldPaths(payload.Guard))

	if violations := selection.Violations(); len(violations) > 0 {
		// The policy Selection deferred to its consumer, decided here: a group that matched
		// the predicate and the grouping but not the declared cardinality refuses the
		// transition. Silently dropping it means a team with three drivers never forms and
		// nothing records why.
		refs, facts := groupEvidence(violations, guardFields)
		return rejectInvariant(binding, declaration.ID, state, journal, transformation.invariants,
			SelectionCardinalityInvalid, refs, facts, nil)
	}
	groups := selection.Groups()
	if len(groups) == 0 {
		return rejectInvariant(binding, declaration.ID, state, journal, transformation.invariants,
			SelectionEmpty, nil, nil, nil)
	}

	operations := make([]Operation, 0, len(groups))
	facts := make([]FactRef, 0)
	qualified := 0
	for _, group := range groups {
		members := group.Members()
		facts = append(facts, memberFacts(members, guardFields)...)
		held, guardErr := evaluateGroupExpr(schema, payload.Guard, members)
		if guardErr != nil {
			refs, evidence := groupEvidence([]Group{group}, guardFields)
			return rejectInvariant(binding, declaration.ID, state, journal, transformation.invariants,
				SelectionExpressionUnavailable, refs, canonicalFactRefs(append(facts, evidence...)), nil)
		}
		if !held {
			continue
		}
		qualified++
		for _, member := range members {
			updates := make([]FieldUpdate, 0, len(payload.Assignments))
			for _, assignment := range payload.Assignments {
				value, valueErr := evaluateValue(schema, assignment.Value, member)
				if valueErr != nil {
					sources := memberFacts([]Entity{member}, fieldNames(readFieldPaths(assignment.Value)))
					return rejectInvariant(binding, declaration.ID, state, journal, transformation.invariants,
						SelectionExpressionUnavailable, []EntityRef{member.Ref()},
						canonicalFactRefs(append(facts, sources...)), nil)
				}
				facts = append(facts, memberFacts([]Entity{member}, fieldNames(readFieldPaths(assignment.Value)))...)
				_, target := splitFieldPath(assignment.Target)
				// The before-image must be exact: ApplyPatch compares it against the state
				// and rejects the operation when it disagrees, which is the mechanism that
				// makes a patch valid against one predecessor only.
				before := AbsentField()
				if current, present := member.Field(target); present {
					before = PresentField(current)
				}
				updates = append(updates, FieldUpdate{Name: target, Before: before, After: value})
			}
			operations = append(operations, UpdateOperation(member.Ref(), updates))
		}
	}
	if qualified == 0 {
		refs, evidence := groupEvidence(groups, guardFields)
		return rejectInvariant(binding, declaration.ID, state, journal, transformation.invariants,
			SelectionGuardUnsatisfied, refs, evidence, nil)
	}

	facts = canonicalFactRefs(facts)
	refs := make([]EntityRef, 0, len(operations))
	for _, operation := range operations {
		update, _ := operation.Update()
		refs = append(refs, update.Target())
	}
	refs = canonicalEntityRefs(refs)
	patch, err := NewPatch(state.Schema(), operations)
	if err != nil {
		return base, fmt.Errorf("execute select patch: %w", err)
	}
	application, err := ApplyPatch(state, patch)
	if err != nil {
		return base, err
	}
	if operationFailure := application.Failure(); operationFailure != nil {
		return rejectOperation(binding, declaration.ID, state, journal, transformation.invariants,
			operationFailure.Code(), refs, facts, patch)
	}
	candidate := application.State()
	results := passingResults(transformation.invariants, refs, facts)
	entry, err := newJournalEntry(declaration.ID, state, candidate, patch, facts, results)
	if err != nil {
		return base, err
	}
	acceptedJournal := journal.AppendAccepted(entry)
	return TransitionOutcome{state: candidate, patch: &patch, journal: acceptedJournal,
		results: journalInvariantResults(acceptedJournal)}, nil
}

// fieldNames reduces qualified paths to the names a FactRef carries.
//
// A FactRef names an entity and a field, so the entity kind in the path is already implied by
// the ref. Deduplicated, because one path read twice in a tree is one fact.
func fieldNames(paths []FieldPath) []FieldName {
	names := make([]FieldName, 0, len(paths))
	seen := make(map[FieldName]struct{}, len(paths))
	for _, path := range paths {
		_, name := splitFieldPath(path)
		if _, duplicate := seen[name]; duplicate {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	return names
}

// memberFacts pairs each member with each field, as the evidence a refusal cites.
func memberFacts(members []Entity, names []FieldName) []FactRef {
	facts := make([]FactRef, 0, len(members)*len(names))
	for _, member := range members {
		for _, name := range names {
			facts = append(facts, FactRef{entity: member.Ref(), field: name})
		}
	}
	return facts
}

// groupEvidence returns the entities and facts attributable to a set of groups.
func groupEvidence(groups []Group, names []FieldName) ([]EntityRef, []FactRef) {
	refs := make([]EntityRef, 0)
	facts := make([]FactRef, 0)
	for _, group := range groups {
		members := group.Members()
		for _, member := range members {
			refs = append(refs, member.Ref())
		}
		facts = append(facts, memberFacts(members, names)...)
	}
	return canonicalEntityRefs(refs), canonicalFactRefs(facts)
}
