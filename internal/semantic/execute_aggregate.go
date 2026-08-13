package semantic

import (
	"fmt"
	"sort"
)

func executeAggregateRelatedFields(binding RunBinding, transformation CompiledTransformation, state State, journal Journal) (TransitionOutcome, error) {
	base := TransitionOutcome{state: state, journal: Journal{entries: cloneJournalEntries(journal.entries)}}
	declaration := transformation.Declaration()
	aggregate := declaration.Aggregate
	memberInvariant, err := requiredInvariant(transformation.invariants, string(declaration.ID)+"/01-member-cardinality")
	if err != nil {
		return base, err
	}
	tupleInvariant, err := requiredInvariant(transformation.invariants, string(declaration.ID)+"/02-tuple-complete")
	if err != nil {
		return base, err
	}
	emittedInvariant, err := requiredInvariant(transformation.invariants, string(declaration.ID)+"/04-emitted-values")
	if err != nil {
		return base, err
	}
	target, err := resolveOutputSlot(binding.plan, journal, aggregate.Target)
	if err != nil {
		return base, err
	}
	members := relatedMembers(state, target, aggregate.RelationKind)
	evaluated := make([]InvariantResult, 0, len(transformation.invariants))
	memberRefs := make([]EntityRef, len(members))
	for i := range members {
		memberRefs[i] = members[i].Ref()
	}
	if len(members) != 2 || memberRefs[0] == memberRefs[1] {
		return rejectInvariantEvaluated(binding, declaration.ID, state, journal, TeamMemberCardinalityInvalid, append(evaluated, invariantResult(memberInvariant, false, memberRefs, nil)), memberRefs, nil, nil)
	}
	for _, member := range members {
		if member.Ref().Kind != aggregate.SourceKind {
			return rejectInvariantEvaluated(binding, declaration.ID, state, journal, TeamMemberCardinalityInvalid, append(evaluated, invariantResult(memberInvariant, false, memberRefs, nil)), memberRefs, nil, nil)
		}
	}
	evaluated = append(evaluated, invariantResult(memberInvariant, true, memberRefs, nil))

	facts := make([]FactRef, 0, len(members)*len(aggregate.RequiredSourceTuple))
	for _, member := range members {
		for _, path := range aggregate.RequiredSourceTuple {
			_, name := splitFieldPath(path)
			if _, present := member.Field(name); !present {
				failed := invariantResult(tupleInvariant, false, memberRefs, facts)
				return rejectInvariantEvaluated(binding, declaration.ID, state, journal, HOSTupleIncomplete, append(evaluated, failed), memberRefs, facts, nil)
			}
			facts = append(facts, FactRef{entity: member.Ref(), field: name})
		}
	}
	evaluated = append(evaluated, invariantResult(tupleInvariant, true, memberRefs, facts))

	// Declaration order is canonical content order, not evaluation order. The
	// protected boundary requires every scalar-duration check before anchor
	// equality so a tuple cannot hide an unlawful duration behind a mismatch.
	for _, requiredKind := range []AggregatePredicateKind{CompleteTuple, NonNegativeInt, LessOrEqualFields, EqualFieldAcrossSources} {
		for predicateIndex, predicate := range aggregate.Predicates {
			if predicate.Kind != requiredKind {
				continue
			}
			predicateKey := fmt.Sprintf("%s/03-source-%02d", declaration.ID, predicateIndex)
			predicateInvariant, err := requiredInvariant(transformation.invariants, predicateKey)
			if err != nil {
				return base, err
			}
			switch predicate.Kind {
			case CompleteTuple:
				for _, member := range members {
					for _, path := range predicate.Fields {
						_, name := splitFieldPath(path)
						if _, present := member.Field(name); !present {
							failed := invariantResult(predicateInvariant, false, memberRefs, facts)
							return rejectInvariantEvaluated(binding, declaration.ID, state, journal, HOSTupleIncomplete, append(evaluated, failed), memberRefs, facts, nil)
						}
						facts = append(facts, FactRef{entity: member.Ref(), field: name})
					}
				}
				facts = canonicalFactRefs(facts)
			case NonNegativeInt:
				for _, member := range members {
					for _, path := range predicate.Fields {
						_, name := splitFieldPath(path)
						value, _ := member.Field(name)
						integer, ok := value.Int64()
						if !ok || integer < 0 {
							failed := invariantResult(predicateInvariant, false, memberRefs, facts)
							return rejectInvariantEvaluated(binding, declaration.ID, state, journal, HOSDurationInvalid, append(evaluated, failed), memberRefs, facts, nil)
						}
					}
				}
			case LessOrEqualFields:
				leftName, rightName, ok := orderedPredicateFields(predicate)
				if !ok {
					return base, fmt.Errorf("execute aggregate: malformed compiled less-or-equal predicate")
				}
				for _, member := range members {
					leftValue, _ := member.Field(leftName)
					rightValue, _ := member.Field(rightName)
					left, leftOK := leftValue.Int64()
					right, rightOK := rightValue.Int64()
					if !leftOK || !rightOK || left > right {
						failed := invariantResult(predicateInvariant, false, memberRefs, facts)
						return rejectInvariantEvaluated(binding, declaration.ID, state, journal, HOSDurationInvalid, append(evaluated, failed), memberRefs, facts, nil)
					}
				}
			case EqualFieldAcrossSources:
				for _, path := range predicate.Fields {
					_, name := splitFieldPath(path)
					first, _ := members[0].Field(name)
					for _, member := range members[1:] {
						other, _ := member.Field(name)
						if !first.Equal(other) {
							failed := invariantResult(predicateInvariant, false, memberRefs, facts)
							return rejectInvariantEvaluated(binding, declaration.ID, state, journal, HOSAnchorMismatch, append(evaluated, failed), memberRefs, facts, nil)
						}
					}
				}
			default:
				return base, fmt.Errorf("execute aggregate: unsupported compiled predicate %d", predicate.Kind)
			}
			evaluated = append(evaluated, invariantResult(predicateInvariant, true, memberRefs, facts))
		}
	}

	team, ok := state.Entity(target)
	if !ok {
		return base, fmt.Errorf("execute aggregate: accepted output slot target is absent")
	}
	destinationNames := make([]FieldName, 0, 1+len(aggregate.Reductions))
	_, anchorDestination := splitFieldPath(aggregate.Anchor.Destination)
	destinationNames = append(destinationNames, anchorDestination)
	for _, reduction := range aggregate.Reductions {
		_, destination := splitFieldPath(reduction.Destination)
		destinationNames = append(destinationNames, destination)
	}
	for _, name := range destinationNames {
		if _, present := team.Field(name); present {
			failed := invariantResult(emittedInvariant, false, []EntityRef{target}, facts)
			return rejectInvariantEvaluated(binding, declaration.ID, state, journal, HOSAggregateInvalid, append(evaluated, failed), []EntityRef{target}, facts, nil)
		}
	}

	_, anchorSource := splitFieldPath(aggregate.Anchor.Source)
	anchor, _ := members[0].Field(anchorSource)
	updates := []FieldUpdate{{Name: anchorDestination, Before: AbsentField(), After: anchor}}
	expected := map[FieldName]Value{anchorDestination: anchor}
	for _, reduction := range aggregate.Reductions {
		if reduction.Kind != ReduceInt64Max {
			return base, fmt.Errorf("execute aggregate: unsupported compiled reduction %d", reduction.Kind)
		}
		_, source := splitFieldPath(reduction.Source)
		_, destination := splitFieldPath(reduction.Destination)
		maximum, ok := maximumInt64(members, source)
		if !ok {
			return base, fmt.Errorf("execute aggregate: compiled max source is not int64")
		}
		value := NewInt64Value(maximum)
		updates = append(updates, FieldUpdate{Name: destination, Before: AbsentField(), After: value})
		expected[destination] = value
	}
	patch, err := NewPatch(state.Schema(), []Operation{UpdateOperation(target, updates)})
	if err != nil {
		return base, fmt.Errorf("execute aggregate patch: %w", err)
	}
	application, err := ApplyPatch(state, patch)
	if err != nil {
		return base, err
	}
	if operationFailure := application.Failure(); operationFailure != nil {
		return rejectOperation(binding, declaration.ID, state, journal, transformation.invariants, operationFailure.Code(), []EntityRef{target}, facts, patch)
	}
	candidate := application.State()
	if !validateAggregateCandidate(candidate, target, expected, aggregate.ResultPredicates) {
		failed := invariantResult(emittedInvariant, false, []EntityRef{target}, facts)
		return rejectInvariantEvaluated(binding, declaration.ID, state, journal, HOSAggregateInvalid, append(evaluated, failed), []EntityRef{target}, facts, &patch)
	}
	evaluated = append(evaluated, invariantResult(emittedInvariant, true, append(memberRefs, target), facts))
	results := evaluated
	entry, err := newJournalEntry(declaration.ID, state, candidate, patch, facts, results)
	if err != nil {
		return base, err
	}
	acceptedJournal := journal.AppendAccepted(entry)
	return TransitionOutcome{state: candidate, patch: &patch, journal: acceptedJournal, results: journalInvariantResults(acceptedJournal)}, nil
}

func resolveOutputSlot(plan Plan, journal Journal, target OutputSlotReference) (EntityRef, error) {
	producer, ok := findCompiledTransformation(plan, target.Rule)
	if !ok || producer.declaration.Form == nil || producer.declaration.Form.OutputSlot != target.Slot {
		return EntityRef{}, fmt.Errorf("resolve output slot: compiled producer is unavailable")
	}
	for _, entry := range journal.entries {
		if entry.rule != target.Rule {
			continue
		}
		var result *EntityRef
		for _, operation := range entry.patch.operations {
			if operation.kind != OperationInsert || operation.insert.entity.ref.Kind != producer.declaration.Form.OutputKind {
				continue
			}
			ref := operation.insert.entity.ref
			if result != nil {
				return EntityRef{}, fmt.Errorf("resolve output slot: accepted producer has multiple outputs")
			}
			result = &ref
		}
		if result == nil {
			return EntityRef{}, fmt.Errorf("resolve output slot: accepted producer output is absent")
		}
		return *result, nil
	}
	return EntityRef{}, fmt.Errorf("resolve output slot: producer has no accepted entry")
}

func relatedMembers(state State, target EntityRef, relationKind RelationKind) []Entity {
	members := make([]Entity, 0)
	for _, relation := range state.relations {
		if relation.Kind != relationKind || relation.From != target {
			continue
		}
		if entity, ok := state.Entity(relation.To); ok {
			members = append(members, entity)
		}
	}
	sort.Slice(members, func(i, j int) bool { return compareEntityRefs(members[i].ref, members[j].ref) < 0 })
	return members
}

func orderedPredicateFields(predicate AggregatePredicate) (FieldName, FieldName, bool) {
	if len(predicate.Fields) != 2 {
		return "", "", false
	}
	_, left := splitFieldPath(predicate.Fields[0])
	_, right := splitFieldPath(predicate.Fields[1])
	return left, right, left != "" && right != ""
}

func maximumInt64(entities []Entity, field FieldName) (int64, bool) {
	if len(entities) == 0 {
		return 0, false
	}
	first, present := entities[0].Field(field)
	maximum, ok := first.Int64()
	if !present || !ok {
		return 0, false
	}
	for _, entity := range entities[1:] {
		value, present := entity.Field(field)
		integer, ok := value.Int64()
		if !present || !ok {
			return 0, false
		}
		if integer > maximum {
			maximum = integer
		}
	}
	return maximum, true
}

func validateAggregateCandidate(state State, target EntityRef, expected map[FieldName]Value, predicates []AggregatePredicate) bool {
	entity, ok := state.Entity(target)
	if !ok {
		return false
	}
	for name, want := range expected {
		got, present := entity.Field(name)
		if !present || !got.Equal(want) {
			return false
		}
	}
	for _, predicate := range predicates {
		switch predicate.Kind {
		case CompleteTuple:
			for _, path := range predicate.Fields {
				_, name := splitFieldPath(path)
				if _, present := entity.Field(name); !present {
					return false
				}
			}
		case NonNegativeInt:
			for _, path := range predicate.Fields {
				_, name := splitFieldPath(path)
				value, present := entity.Field(name)
				integer, valid := value.Int64()
				if !present || !valid || integer < 0 {
					return false
				}
			}
		case LessOrEqualFields:
			leftName, rightName, valid := orderedPredicateFields(predicate)
			if !valid {
				return false
			}
			leftValue, leftPresent := entity.Field(leftName)
			rightValue, rightPresent := entity.Field(rightName)
			left, leftOK := leftValue.Int64()
			right, rightOK := rightValue.Int64()
			if !leftPresent || !rightPresent || !leftOK || !rightOK || left > right {
				return false
			}
		default:
			return false
		}
	}
	return true
}
