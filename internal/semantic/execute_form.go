package semantic

import (
	"fmt"
	"slices"
	"sort"
)

func executeFormRelatedEntity(binding RunBinding, transformation CompiledTransformation, state State, journal Journal) (TransitionOutcome, error) {
	base := TransitionOutcome{state: state, journal: Journal{entries: cloneJournalEntries(journal.entries)}}
	declaration := transformation.Declaration()
	form := declaration.Form
	sources := make([]Entity, 0, len(form.Sources))
	refs := make([]EntityRef, 0, len(form.Sources))
	for _, source := range form.Sources {
		ref := EntityRef{Kind: source.Kind, ID: SourceEntityID(state.InputLineageID(), source.Kind, source.CanonicalSourceKey)}
		entity, ok := state.Entity(ref)
		if !ok {
			return rejectInvariant(binding, declaration.ID, state, journal, transformation.invariants, DeclaredSourceNotFound, append(refs, ref), nil, nil)
		}
		if entity.Ref().Kind != form.SourceKind {
			return rejectInvariant(binding, declaration.ID, state, journal, transformation.invariants, DeclaredSourceKindInvalid, append(refs, ref), nil, nil)
		}
		sources, refs = append(sources, entity), append(refs, ref)
	}
	if uint64(len(sources)) != form.SourceCount || len(sources) != 2 || refs[0] == refs[1] {
		return rejectInvariant(binding, declaration.ID, state, journal, transformation.invariants, TeamMemberCardinalityInvalid, refs, nil, nil)
	}
	_, groupingName := splitFieldPath(form.GroupingField)
	grouping := make([]Value, len(sources))
	facts := make([]FactRef, len(sources))
	for i, source := range sources {
		value, present := source.Field(groupingName)
		text, isText := value.String()
		if !present || !isText || text == "" {
			return rejectInvariant(binding, declaration.ID, state, journal, transformation.invariants, TeamAssignmentKeyInvalid, refs, facts[:i], nil)
		}
		grouping[i] = value
		facts[i] = FactRef{entity: source.Ref(), field: groupingName}
	}
	if !grouping[0].Equal(grouping[1]) {
		return rejectInvariant(binding, declaration.ID, state, journal, transformation.invariants, TeamAssignmentKeyMismatch, refs, facts, nil)
	}
	teamID, err := syntheticEntityID(state.InputLineageID(), form.OutputKind, declaration.ID, refs, grouping[0])
	if err != nil {
		return base, err
	}
	teamFields := make(map[FieldName]Value, len(form.CopiedFields))
	for _, copied := range form.CopiedFields {
		_, sourceName := splitFieldPath(copied.Source)
		_, destination := splitFieldPath(copied.Destination)
		value, present := sources[0].Field(sourceName)
		if !present {
			return rejectInvariant(binding, declaration.ID, state, journal, transformation.invariants, TeamAssignmentKeyInvalid, refs, facts, nil)
		}
		for _, source := range sources[1:] {
			other, otherPresent := source.Field(sourceName)
			if !otherPresent || !value.Equal(other) {
				return rejectInvariant(binding, declaration.ID, state, journal, transformation.invariants, TeamAssignmentKeyMismatch, refs, facts, nil)
			}
		}
		for _, source := range sources {
			facts = append(facts, FactRef{entity: source.Ref(), field: sourceName})
		}
		teamFields[destination] = value
	}
	facts = canonicalFactRefs(facts)
	team, err := NewEntity(EntityRef{Kind: form.OutputKind, ID: teamID}, teamFields)
	if err != nil {
		return base, fmt.Errorf("execute form team entity: %w", err)
	}
	operations := []Operation{InsertOperation(team)}
	for _, ref := range refs {
		operations = append(operations, RelateOperation(Relation{Kind: form.RelationKind, From: team.Ref(), To: ref}))
	}
	patch, err := NewPatch(state.Schema(), operations)
	if err != nil {
		return base, fmt.Errorf("execute form patch: %w", err)
	}
	application, err := ApplyPatch(state, patch)
	if err != nil {
		return base, err
	}
	if operationFailure := application.Failure(); operationFailure != nil {
		return rejectOperation(binding, declaration.ID, state, journal, transformation.invariants, operationFailure.Code(), refs, facts, patch)
	}
	candidate := application.State()
	if !hasExactMembers(candidate, team.Ref(), form.RelationKind, refs) {
		return rejectInvariant(binding, declaration.ID, state, journal, transformation.invariants, TeamMemberCardinalityInvalid, refs, facts, &patch)
	}
	results := passingResults(transformation.invariants, refs, facts)
	entry, err := newJournalEntry(declaration.ID, state, candidate, patch, facts, results)
	if err != nil {
		return base, err
	}
	acceptedJournal := journal.AppendAccepted(entry)
	return TransitionOutcome{state: candidate, patch: &patch, journal: acceptedJournal, results: journalInvariantResults(acceptedJournal)}, nil
}

func hasExactMembers(state State, team EntityRef, relationKind RelationKind, members []EntityRef) bool {
	want := slices.Clone(members)
	sort.Slice(want, func(i, j int) bool { return compareEntityRefs(want[i], want[j]) < 0 })
	got := make([]EntityRef, 0)
	for _, relation := range state.relations {
		if relation.Kind == relationKind && relation.From == team {
			got = append(got, relation.To)
		}
	}
	sort.Slice(got, func(i, j int) bool { return compareEntityRefs(got[i], got[j]) < 0 })
	return slices.Equal(got, want)
}
