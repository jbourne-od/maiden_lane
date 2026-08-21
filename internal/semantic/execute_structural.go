package semantic

import (
	"fmt"
)

func entityRefs(entities []Entity) []EntityRef {
	refs := make([]EntityRef, len(entities))
	for i, e := range entities {
		refs[i] = e.Ref()
	}
	return refs
}

// executeInsertEntity runs an insert-entity transformation rule.
func executeInsertEntity(
	binding RunBinding, transformation CompiledTransformation, state State, journal Journal,
) (TransitionOutcome, error) {
	base := TransitionOutcome{state: state, journal: Journal{entries: cloneJournalEntries(journal.entries)}}
	declaration := transformation.Declaration()
	if declaration.InsertEntity == nil {
		return base, fmt.Errorf("execute insert: missing insert payload")
	}
	payload := declaration.InsertEntity
	selection, err := transformation.selector.Select(state)
	if err != nil {
		if fault, isData := err.(selectionDataFault); isData {
			refs, facts := selectorFaultEvidence(state, fault, transformation.selector)
			return rejectInvariantAtKey(binding, declaration.ID, state, journal, transformation.invariants,
				SelectionExpressionUnavailable, invariantKey(declaration.ID, selectorEvaluableSuffix),
				refs, facts, nil)
		}
		return base, fmt.Errorf("execute insert: %w", err)
	}
	if !selection.Ran() {
		return base, fmt.Errorf("execute insert: selection did not run")
	}
	schema := state.Schema()
	guardFields := fieldNames(readFieldPaths(payload.Guard))

	if violations := selection.Violations(); len(violations) > 0 {
		refs, facts := groupEvidence(violations, guardFields)
		return rejectInvariantAtKey(binding, declaration.ID, state, journal, transformation.invariants,
			SelectionCardinalityInvalid, invariantKey(declaration.ID, cardinalitySuffix), refs, facts, nil)
	}
	groups := selection.Groups()
	if len(groups) == 0 {
		return rejectInvariantAtKey(binding, declaration.ID, state, journal, transformation.invariants,
			SelectionEmpty, invariantKey(declaration.ID, selectionNonEmptySuffix), nil, nil, nil)
	}

	operations := make([]Operation, 0)
	facts := make([]FactRef, 0)
	refs := make([]EntityRef, 0)
	qualified := 0

	for _, group := range groups {
		members := group.Members()
		facts = append(facts, memberFacts(members, guardFields)...)
		held, guardErr := evaluateGroupExpr(schema, payload.Guard, members)
		if guardErr != nil {
			gRefs, evidence := groupEvidence([]Group{group}, guardFields)
			return rejectInvariantAtKey(binding, declaration.ID, state, journal, transformation.invariants,
				SelectionExpressionUnavailable, invariantKey(declaration.ID, groupEvaluableSuffix),
				gRefs, canonicalFactRefs(append(facts, evidence...)), nil)
		}
		if !held {
			continue
		}
		qualified++
		if transformation.selector.Grouped() {
			mRefs := entityRefs(members)
			discVal, discErr := evaluateGroupValue(schema, payload.Discriminator, members)
			if discErr != nil {
				sources := memberFacts(members, fieldNames(readFieldPaths(payload.Discriminator)))
				return rejectInvariantAtKey(binding, declaration.ID, state, journal, transformation.invariants,
					SelectionExpressionUnavailable, invariantKey(declaration.ID, groupEvaluableSuffix),
					mRefs, canonicalFactRefs(append(facts, sources...)), nil)
			}
			facts = append(facts, memberFacts(members, fieldNames(readFieldPaths(payload.Discriminator)))...)

			syntheticID := SyntheticEntityID(state.InputLineageID(), payload.TargetKind, declaration.ID, mRefs, discVal)
			fields := make(map[FieldName]Value, len(payload.Assignments))
			for _, assignment := range payload.Assignments {
				val, valErr := evaluateGroupValue(schema, assignment.Value, members)
				if valErr != nil {
					sources := memberFacts(members, fieldNames(readFieldPaths(assignment.Value)))
					return rejectInvariantAtKey(binding, declaration.ID, state, journal, transformation.invariants,
						SelectionExpressionUnavailable, invariantKey(declaration.ID, groupEvaluableSuffix),
						mRefs, canonicalFactRefs(append(facts, sources...)), nil)
				}
				facts = append(facts, memberFacts(members, fieldNames(readFieldPaths(assignment.Value)))...)
				_, fieldName := splitFieldPath(assignment.Target)
				fields[fieldName] = val
			}

			newEntity, err := NewEntity(EntityRef{Kind: payload.TargetKind, ID: syntheticID}, fields)
			if err != nil {
				return base, fmt.Errorf("execute insert entity: %w", err)
			}
			operations = append(operations, InsertOperation(newEntity))
			refs = append(refs, append(mRefs, newEntity.Ref())...)
		} else {
			for _, member := range members {
				discVal, discErr := evaluateAssignmentValue(schema, payload.Discriminator, members, member)
				if discErr != nil {
					sources := memberFacts(members, fieldNames(readFieldPaths(payload.Discriminator)))
					return rejectInvariantAtKey(binding, declaration.ID, state, journal, transformation.invariants,
						SelectionExpressionUnavailable, invariantKey(declaration.ID, groupEvaluableSuffix),
						[]EntityRef{member.Ref()}, canonicalFactRefs(append(facts, sources...)), nil)
				}
				facts = append(facts, memberFacts([]Entity{member}, fieldNames(readFieldPaths(payload.Discriminator)))...)

				syntheticID := SyntheticEntityID(state.InputLineageID(), payload.TargetKind, declaration.ID, []EntityRef{member.Ref()}, discVal)
				fields := make(map[FieldName]Value, len(payload.Assignments))
				for _, assignment := range payload.Assignments {
					val, valErr := evaluateAssignmentValue(schema, assignment.Value, members, member)
					if valErr != nil {
						sources := memberFacts(members, fieldNames(readFieldPaths(assignment.Value)))
						return rejectInvariantAtKey(binding, declaration.ID, state, journal, transformation.invariants,
							SelectionExpressionUnavailable, invariantKey(declaration.ID, groupEvaluableSuffix),
							[]EntityRef{member.Ref()}, canonicalFactRefs(append(facts, sources...)), nil)
					}
					facts = append(facts, memberFacts([]Entity{member}, fieldNames(readFieldPaths(assignment.Value)))...)
					_, fieldName := splitFieldPath(assignment.Target)
					fields[fieldName] = val
				}

				newEntity, err := NewEntity(EntityRef{Kind: payload.TargetKind, ID: syntheticID}, fields)
				if err != nil {
					return base, fmt.Errorf("execute insert entity: %w", err)
				}
				operations = append(operations, InsertOperation(newEntity))
				refs = append(refs, member.Ref(), newEntity.Ref())
			}
		}
	}

	if qualified == 0 {
		gRefs, evidence := groupEvidence(groups, guardFields)
		return rejectInvariantAtKey(binding, declaration.ID, state, journal, transformation.invariants,
			SelectionGuardUnsatisfied, invariantKey(declaration.ID, guardSuffix), gRefs, evidence, nil)
	}

	return commitStructuralOperations(binding, declaration.ID, state, journal, transformation.invariants, operations, refs, facts)
}

// executeDeleteEntity runs a delete-entity transformation rule.
func executeDeleteEntity(
	binding RunBinding, transformation CompiledTransformation, state State, journal Journal,
) (TransitionOutcome, error) {
	base := TransitionOutcome{state: state, journal: Journal{entries: cloneJournalEntries(journal.entries)}}
	declaration := transformation.Declaration()
	payload := declaration.DeleteEntity
	if payload == nil {
		return base, fmt.Errorf("execute delete: operator carries no delete-entity payload")
	}
	selection, err := transformation.selector.Select(state)
	if err != nil {
		if fault, isData := err.(selectionDataFault); isData {
			refs, facts := selectorFaultEvidence(state, fault, transformation.selector)
			return rejectInvariantAtKey(binding, declaration.ID, state, journal, transformation.invariants,
				SelectionExpressionUnavailable, invariantKey(declaration.ID, selectorEvaluableSuffix),
				refs, facts, nil)
		}
		return base, fmt.Errorf("execute delete: %w", err)
	}
	if !selection.Ran() {
		return base, fmt.Errorf("execute delete: selection did not run")
	}
	schema := state.Schema()
	guardFields := fieldNames(readFieldPaths(payload.Guard))

	if violations := selection.Violations(); len(violations) > 0 {
		refs, facts := groupEvidence(violations, guardFields)
		return rejectInvariantAtKey(binding, declaration.ID, state, journal, transformation.invariants,
			SelectionCardinalityInvalid, invariantKey(declaration.ID, cardinalitySuffix), refs, facts, nil)
	}
	groups := selection.Groups()
	if len(groups) == 0 {
		return rejectInvariantAtKey(binding, declaration.ID, state, journal, transformation.invariants,
			SelectionEmpty, invariantKey(declaration.ID, selectionNonEmptySuffix), nil, nil, nil)
	}

	operations := make([]Operation, 0)
	facts := make([]FactRef, 0)
	refs := make([]EntityRef, 0)
	qualified := 0

	for _, group := range groups {
		members := group.Members()
		facts = append(facts, memberFacts(members, guardFields)...)
		held, guardErr := evaluateGroupExpr(schema, payload.Guard, members)
		if guardErr != nil {
			gRefs, evidence := groupEvidence([]Group{group}, guardFields)
			return rejectInvariantAtKey(binding, declaration.ID, state, journal, transformation.invariants,
				SelectionExpressionUnavailable, invariantKey(declaration.ID, groupEvaluableSuffix),
				gRefs, canonicalFactRefs(append(facts, evidence...)), nil)
		}
		if !held {
			continue
		}
		qualified++
		for _, member := range members {
			operations = append(operations, DeleteOperation(member))
			refs = append(refs, member.Ref())
		}
	}

	if qualified == 0 {
		gRefs, evidence := groupEvidence(groups, guardFields)
		return rejectInvariantAtKey(binding, declaration.ID, state, journal, transformation.invariants,
			SelectionGuardUnsatisfied, invariantKey(declaration.ID, guardSuffix), gRefs, evidence, nil)
	}

	return commitStructuralOperations(binding, declaration.ID, state, journal, transformation.invariants, operations, refs, facts)
}

// executeRelateEntities runs a relate-entities transformation rule.
func executeRelateEntities(
	binding RunBinding, transformation CompiledTransformation, state State, journal Journal,
) (TransitionOutcome, error) {
	return executeRelationMutation(binding, transformation, state, journal, true)
}

// executeUnrelateEntities runs an unrelate-entities transformation rule.
func executeUnrelateEntities(
	binding RunBinding, transformation CompiledTransformation, state State, journal Journal,
) (TransitionOutcome, error) {
	return executeRelationMutation(binding, transformation, state, journal, false)
}

func executeRelationMutation(
	binding RunBinding, transformation CompiledTransformation, state State, journal Journal, isRelate bool,
) (TransitionOutcome, error) {
	base := TransitionOutcome{state: state, journal: Journal{entries: cloneJournalEntries(journal.entries)}}
	declaration := transformation.Declaration()
	var relKind RelationKind
	var guard Expr
	if isRelate {
		if declaration.RelateEntities == nil {
			return base, fmt.Errorf("execute relate: missing relate-entities payload")
		}
		relKind = declaration.RelateEntities.RelationKind
		guard = declaration.RelateEntities.Guard
	} else {
		if declaration.UnrelateEntities == nil {
			return base, fmt.Errorf("execute unrelate: missing unrelate-entities payload")
		}
		relKind = declaration.UnrelateEntities.RelationKind
		guard = declaration.UnrelateEntities.Guard
	}

	fromSelection, err := transformation.fromSelector.Select(state)
	if err != nil {
		if fault, isData := err.(selectionDataFault); isData {
			refs, facts := selectorFaultEvidence(state, fault, transformation.fromSelector)
			return rejectInvariantAtKey(binding, declaration.ID, state, journal, transformation.invariants,
				SelectionExpressionUnavailable, invariantKey(declaration.ID, selectorEvaluableSuffix),
				refs, facts, nil)
		}
		return base, fmt.Errorf("execute relation from_selector: %w", err)
	}
	if !fromSelection.Ran() {
		return base, fmt.Errorf("execute relation from_selector: selection did not run")
	}
	if violations := fromSelection.Violations(); len(violations) > 0 {
		refs, facts := groupEvidence(violations, fieldNames(readFieldPaths(guard)))
		return rejectInvariantAtKey(binding, declaration.ID, state, journal, transformation.invariants,
			SelectionCardinalityInvalid, invariantKey(declaration.ID, cardinalitySuffix), refs, facts, nil)
	}

	toSelection, err := transformation.toSelector.Select(state)
	if err != nil {
		if fault, isData := err.(selectionDataFault); isData {
			refs, facts := selectorFaultEvidence(state, fault, transformation.toSelector)
			return rejectInvariantAtKey(binding, declaration.ID, state, journal, transformation.invariants,
				SelectionExpressionUnavailable, invariantKey(declaration.ID, selectorEvaluableSuffix),
				refs, facts, nil)
		}
		return base, fmt.Errorf("execute relation to_selector: %w", err)
	}
	if !toSelection.Ran() {
		return base, fmt.Errorf("execute relation to_selector: selection did not run")
	}
	if violations := toSelection.Violations(); len(violations) > 0 {
		refs, facts := groupEvidence(violations, fieldNames(readFieldPaths(guard)))
		return rejectInvariantAtKey(binding, declaration.ID, state, journal, transformation.invariants,
			SelectionCardinalityInvalid, invariantKey(declaration.ID, cardinalitySuffix), refs, facts, nil)
	}

	fromGroups := fromSelection.Groups()
	toGroups := toSelection.Groups()
	if len(fromGroups) == 0 || len(toGroups) == 0 {
		return rejectInvariantAtKey(binding, declaration.ID, state, journal, transformation.invariants,
			SelectionEmpty, invariantKey(declaration.ID, selectionNonEmptySuffix), nil, nil, nil)
	}

	schema := state.Schema()
	guardFields := fieldNames(readFieldPaths(guard))
	operations := make([]Operation, 0)
	facts := make([]FactRef, 0)
	refs := make([]EntityRef, 0)
	qualified := 0

	for _, fg := range fromGroups {
		for _, fMember := range fg.Members() {
			for _, tg := range toGroups {
				for _, tMember := range tg.Members() {
					pair := []Entity{fMember, tMember}
					facts = append(facts, memberFacts(pair, guardFields)...)
					held, guardErr := evaluateBool(schema, guard, fMember, tMember)
					if guardErr != nil {
						return rejectInvariantAtKey(binding, declaration.ID, state, journal, transformation.invariants,
							SelectionExpressionUnavailable, invariantKey(declaration.ID, groupEvaluableSuffix),
							[]EntityRef{fMember.Ref(), tMember.Ref()}, canonicalFactRefs(facts), nil)
					}
					if !held {
						continue
					}
					qualified++
					rel := Relation{Kind: relKind, From: fMember.Ref(), To: tMember.Ref()}
					if isRelate {
						operations = append(operations, RelateOperation(rel))
					} else {
						operations = append(operations, UnrelateOperation(rel))
					}
					refs = append(refs, fMember.Ref(), tMember.Ref())
				}
			}
		}
	}

	if qualified == 0 {
		var candidateRefs []EntityRef
		for _, fg := range fromGroups {
			for _, fMember := range fg.Members() {
				candidateRefs = append(candidateRefs, fMember.Ref())
			}
		}
		for _, tg := range toGroups {
			for _, tMember := range tg.Members() {
				candidateRefs = append(candidateRefs, tMember.Ref())
			}
		}
		return rejectInvariantAtKey(binding, declaration.ID, state, journal, transformation.invariants,
			SelectionGuardUnsatisfied, invariantKey(declaration.ID, guardSuffix),
			canonicalEntityRefs(candidateRefs), canonicalFactRefs(facts), nil)
	}

	return commitStructuralOperations(binding, declaration.ID, state, journal, transformation.invariants, operations, refs, facts)
}

// executeMergeEntities runs a merge-entities transformation rule.
func executeMergeEntities(
	binding RunBinding, transformation CompiledTransformation, state State, journal Journal,
) (TransitionOutcome, error) {
	base := TransitionOutcome{state: state, journal: Journal{entries: cloneJournalEntries(journal.entries)}}
	declaration := transformation.Declaration()
	payload := declaration.MergeEntities
	if payload == nil {
		return base, fmt.Errorf("execute merge: operator carries no merge-entities payload")
	}
	selection, err := transformation.selector.Select(state)
	if err != nil {
		if fault, isData := err.(selectionDataFault); isData {
			refs, facts := selectorFaultEvidence(state, fault, transformation.selector)
			return rejectInvariantAtKey(binding, declaration.ID, state, journal, transformation.invariants,
				SelectionExpressionUnavailable, invariantKey(declaration.ID, selectorEvaluableSuffix),
				refs, facts, nil)
		}
		return base, fmt.Errorf("execute merge: %w", err)
	}
	if !selection.Ran() {
		return base, fmt.Errorf("execute merge: selection did not run")
	}
	schema := state.Schema()
	guardFields := fieldNames(readFieldPaths(payload.Guard))

	if violations := selection.Violations(); len(violations) > 0 {
		refs, facts := groupEvidence(violations, guardFields)
		return rejectInvariantAtKey(binding, declaration.ID, state, journal, transformation.invariants,
			SelectionCardinalityInvalid, invariantKey(declaration.ID, cardinalitySuffix), refs, facts, nil)
	}
	groups := selection.Groups()
	if len(groups) == 0 {
		return rejectInvariantAtKey(binding, declaration.ID, state, journal, transformation.invariants,
			SelectionEmpty, invariantKey(declaration.ID, selectionNonEmptySuffix), nil, nil, nil)
	}

	var operations []Operation
	var refs []EntityRef
	var facts []FactRef
	var qualified int

	type mergedGroupData struct {
		mergedEntity Entity
		members      []Entity
	}
	var mergedGroups []mergedGroupData
	mergedTargetBySource := make(map[EntityRef]EntityRef)

	for _, group := range groups {
		members := group.Members()
		facts = append(facts, memberFacts(members, guardFields)...)
		held, guardErr := evaluateGroupExpr(schema, payload.Guard, members)
		if guardErr != nil {
			gRefs, evidence := groupEvidence([]Group{group}, guardFields)
			return rejectInvariantAtKey(binding, declaration.ID, state, journal, transformation.invariants,
				SelectionExpressionUnavailable, invariantKey(declaration.ID, groupEvaluableSuffix),
				gRefs, canonicalFactRefs(append(facts, evidence...)), nil)
		}
		if !held {
			continue
		}
		qualified++

		discVal, discErr := evaluateGroupValue(schema, payload.Discriminator, members)
		if discErr != nil {
			sources := memberFacts(members, fieldNames(readFieldPaths(payload.Discriminator)))
			return rejectInvariantAtKey(binding, declaration.ID, state, journal, transformation.invariants,
				SelectionExpressionUnavailable, invariantKey(declaration.ID, groupEvaluableSuffix),
				nil, canonicalFactRefs(append(facts, sources...)), nil)
		}
		facts = append(facts, memberFacts(members, fieldNames(readFieldPaths(payload.Discriminator)))...)

		memberRefs := make([]EntityRef, len(members))
		for i, m := range members {
			memberRefs[i] = m.Ref()
			refs = append(refs, m.Ref())
		}
		syntheticID := SyntheticEntityID(state.InputLineageID(), payload.TargetKind, declaration.ID, memberRefs, discVal)

		targetFields := make(map[FieldName]Value, len(payload.Assignments))
		for _, assignment := range payload.Assignments {
			val, valErr := evaluateGroupValue(schema, assignment.Value, members)
			if valErr != nil {
				sources := memberFacts(members, fieldNames(readFieldPaths(assignment.Value)))
				return rejectInvariantAtKey(binding, declaration.ID, state, journal, transformation.invariants,
					SelectionExpressionUnavailable, invariantKey(declaration.ID, groupEvaluableSuffix),
					nil, canonicalFactRefs(append(facts, sources...)), nil)
			}
			facts = append(facts, memberFacts(members, fieldNames(readFieldPaths(assignment.Value)))...)
			_, targetFieldName := splitFieldPath(assignment.Target)
			targetFields[targetFieldName] = val
		}

		mergedEntity, err := NewEntity(EntityRef{Kind: payload.TargetKind, ID: syntheticID}, targetFields)
		if err != nil {
			return base, fmt.Errorf("execute merge entity: %w", err)
		}
		operations = append(operations, InsertOperation(mergedEntity))
		refs = append(refs, mergedEntity.Ref())

		mergedGroups = append(mergedGroups, mergedGroupData{mergedEntity: mergedEntity, members: members})
		for _, m := range members {
			mergedTargetBySource[m.Ref()] = mergedEntity.Ref()
		}
	}

	if qualified == 0 {
		gRefs, evidence := groupEvidence(groups, guardFields)
		return rejectInvariantAtKey(binding, declaration.ID, state, journal, transformation.invariants,
			SelectionGuardUnsatisfied, invariantKey(declaration.ID, guardSuffix), gRefs, evidence, nil)
	}

	if !payload.RetainSources {
		seenUnrelated := make(map[Relation]struct{})
		seenReanchored := make(map[Relation]struct{})
		for _, r := range state.Relations() {
			fromTarget, fromDeleted := mergedTargetBySource[r.From]
			toTarget, toDeleted := mergedTargetBySource[r.To]
			if fromDeleted || toDeleted {
				if _, seen := seenUnrelated[r]; !seen {
					seenUnrelated[r] = struct{}{}
					operations = append(operations, UnrelateOperation(r))
				}
				if payload.ReanchorRelations {
					newFrom := r.From
					if fromDeleted {
						newFrom = fromTarget
					}
					newTo := r.To
					if toDeleted {
						newTo = toTarget
					}
					if newFrom != newTo {
						if relDecl, ok := schema.relationDeclaration(r.Kind); ok && relDecl.FromKind == newFrom.Kind && relDecl.ToKind == newTo.Kind {
							newRel := Relation{Kind: r.Kind, From: newFrom, To: newTo}
							if _, seen := seenReanchored[newRel]; !seen {
								seenReanchored[newRel] = struct{}{}
								operations = append(operations, RelateOperation(newRel))
							}
						}
					}
				}
			}
		}
		for _, g := range mergedGroups {
			for _, m := range g.members {
				operations = append(operations, DeleteOperation(m))
			}
		}
	}

	return commitStructuralOperations(binding, declaration.ID, state, journal, transformation.invariants, operations, refs, facts)
}

// executeSplitEntity runs a split-entity transformation rule.
func executeSplitEntity(
	binding RunBinding, transformation CompiledTransformation, state State, journal Journal,
) (TransitionOutcome, error) {
	base := TransitionOutcome{state: state, journal: Journal{entries: cloneJournalEntries(journal.entries)}}
	declaration := transformation.Declaration()
	payload := declaration.SplitEntity
	if payload == nil {
		return base, fmt.Errorf("execute split: operator carries no split-entity payload")
	}
	selection, err := transformation.selector.Select(state)
	if err != nil {
		if fault, isData := err.(selectionDataFault); isData {
			refs, facts := selectorFaultEvidence(state, fault, transformation.selector)
			return rejectInvariantAtKey(binding, declaration.ID, state, journal, transformation.invariants,
				SelectionExpressionUnavailable, invariantKey(declaration.ID, selectorEvaluableSuffix),
				refs, facts, nil)
		}
		return base, fmt.Errorf("execute split: %w", err)
	}
	if !selection.Ran() {
		return base, fmt.Errorf("execute split: selection did not run")
	}
	schema := state.Schema()
	guardFields := fieldNames(readFieldPaths(payload.Guard))

	if violations := selection.Violations(); len(violations) > 0 {
		refs, facts := groupEvidence(violations, guardFields)
		return rejectInvariantAtKey(binding, declaration.ID, state, journal, transformation.invariants,
			SelectionCardinalityInvalid, invariantKey(declaration.ID, cardinalitySuffix), refs, facts, nil)
	}
	groups := selection.Groups()
	if len(groups) == 0 {
		return rejectInvariantAtKey(binding, declaration.ID, state, journal, transformation.invariants,
			SelectionEmpty, invariantKey(declaration.ID, selectionNonEmptySuffix), nil, nil, nil)
	}

	var operations []Operation
	var refs []EntityRef
	var facts []FactRef
	var qualified int
	var deletedSources []Entity
	deletedSourceRefs := make(map[EntityRef]struct{})

	for _, group := range groups {
		members := group.Members()
		guardVal, guardErr := evaluateGroupExpr(state.Schema(), payload.Guard, members)
		if guardErr != nil {
			gRefs, evidence := groupEvidence([]Group{group}, guardFields)
			return rejectInvariantAtKey(binding, declaration.ID, state, journal, transformation.invariants,
				SelectionExpressionUnavailable, invariantKey(declaration.ID, groupEvaluableSuffix),
				gRefs, canonicalFactRefs(append(facts, evidence...)), nil)
		}
		if !guardVal {
			continue
		}
		qualified++

		for _, member := range members {
			refs = append(refs, member.Ref())
			for _, part := range payload.Partitions {
				discVal, discErr := evaluateAssignmentValue(schema, part.Discriminator, members, member)
				if discErr != nil {
					sources := memberFacts(members, fieldNames(readFieldPaths(part.Discriminator)))
					return rejectInvariantAtKey(binding, declaration.ID, state, journal, transformation.invariants,
						SelectionExpressionUnavailable, invariantKey(declaration.ID, groupEvaluableSuffix),
						[]EntityRef{member.Ref()}, canonicalFactRefs(append(facts, sources...)), nil)
				}
				facts = append(facts, memberFacts([]Entity{member}, fieldNames(readFieldPaths(part.Discriminator)))...)

				childID := SyntheticEntityID(state.InputLineageID(), payload.TargetKind, declaration.ID, []EntityRef{member.Ref()}, discVal)
				childFields := make(map[FieldName]Value, len(part.Assignments))
				for _, assignment := range part.Assignments {
					val, valErr := evaluateAssignmentValue(schema, assignment.Value, members, member)
					if valErr != nil {
						sources := memberFacts(members, fieldNames(readFieldPaths(assignment.Value)))
						return rejectInvariantAtKey(binding, declaration.ID, state, journal, transformation.invariants,
							SelectionExpressionUnavailable, invariantKey(declaration.ID, groupEvaluableSuffix),
							[]EntityRef{member.Ref()}, canonicalFactRefs(append(facts, sources...)), nil)
					}
					facts = append(facts, memberFacts([]Entity{member}, fieldNames(readFieldPaths(assignment.Value)))...)
					_, targetFieldName := splitFieldPath(assignment.Target)
					childFields[targetFieldName] = val
				}

				childEntity, err := NewEntity(EntityRef{Kind: payload.TargetKind, ID: childID}, childFields)
				if err != nil {
					return base, fmt.Errorf("execute split child entity: %w", err)
				}
				operations = append(operations, InsertOperation(childEntity))
				refs = append(refs, childEntity.Ref())
			}

			if !payload.RetainSource {
				deletedSources = append(deletedSources, member)
				deletedSourceRefs[member.Ref()] = struct{}{}
			}
		}
	}

	if qualified == 0 {
		gRefs, evidence := groupEvidence(groups, guardFields)
		return rejectInvariantAtKey(binding, declaration.ID, state, journal, transformation.invariants,
			SelectionGuardUnsatisfied, invariantKey(declaration.ID, guardSuffix), gRefs, evidence, nil)
	}

	if !payload.RetainSource {
		seenUnrelated := make(map[Relation]struct{})
		for _, r := range state.Relations() {
			_, fromDeleted := deletedSourceRefs[r.From]
			_, toDeleted := deletedSourceRefs[r.To]
			if fromDeleted || toDeleted {
				if _, seen := seenUnrelated[r]; !seen {
					seenUnrelated[r] = struct{}{}
					operations = append(operations, UnrelateOperation(r))
				}
			}
		}
		for _, member := range deletedSources {
			operations = append(operations, DeleteOperation(member))
		}
	}

	return commitStructuralOperations(binding, declaration.ID, state, journal, transformation.invariants, operations, refs, facts)
}

func commitStructuralOperations(
	binding RunBinding, rule RuleID, state State, journal Journal,
	invariants []InvariantDeclaration, operations []Operation, refs []EntityRef, facts []FactRef,
) (TransitionOutcome, error) {
	base := TransitionOutcome{state: state, journal: Journal{entries: cloneJournalEntries(journal.entries)}}
	facts = canonicalFactRefs(facts)
	refs = canonicalEntityRefs(refs)

	patch, err := NewPatch(state.Schema(), operations)
	if err != nil {
		return base, fmt.Errorf("execute structural patch: %w", err)
	}
	application, err := ApplyPatch(state, patch)
	if err != nil {
		return base, err
	}
	if operationFailure := application.Failure(); operationFailure != nil {
		return rejectOperation(binding, rule, state, journal, invariants,
			operationFailure.Code(), refs, facts, patch)
	}
	candidate := application.State()
	results := passingResults(invariants, refs, facts)
	entry, err := newJournalEntry(rule, state, candidate, patch, facts, results)
	if err != nil {
		return base, err
	}
	acceptedJournal := journal.AppendAccepted(entry)
	return TransitionOutcome{
		state: candidate, patch: &patch, journal: acceptedJournal,
		results: journalInvariantResults(acceptedJournal),
	}, nil
}
