package semantic

import (
	"fmt"
	"sort"
)

// FieldDiff records the change to a single field on an entity between baseline and candidate states.
type FieldDiff struct {
	Name      FieldName
	Baseline  Value // Zero Value if absent in baseline
	Candidate Value // Zero Value if absent in candidate
}

// EntityModification records all field changes between baseline and candidate for an entity.
type EntityModification struct {
	Ref        EntityRef
	FieldDiffs []FieldDiff
}

// DiffMetrics summarizes the structural changes between baseline and candidate states.
type DiffMetrics struct {
	CreatedEntitiesCount   uint64
	DeletedEntitiesCount   uint64
	ModifiedEntitiesCount  uint64
	FieldChangesCount      uint64
	AddedRelationsCount    uint64
	RemovedRelationsCount  uint64
	IdenticalEntitiesCount uint64
}

// StateDiff contains the complete structural difference between two semantic States.
type StateDiff struct {
	BaselineDigest   StateDigest
	CandidateDigest  StateDigest
	CreatedEntities  []EntityRef
	DeletedEntities  []EntityRef
	ModifiedEntities []EntityModification
	AddedRelations   []Relation
	RemovedRelations []Relation
	Metrics          DiffMetrics
}

// Identical reports whether baseline and candidate states are identical.
func (d StateDiff) Identical() bool {
	return d.BaselineDigest == d.CandidateDigest
}

// DiffStates computes the exact, deterministic structural difference between baseline and candidate states.
func DiffStates(baseline, candidate State) (StateDiff, error) {
	if baseline.Digest() == "" || candidate.Digest() == "" {
		return StateDiff{}, fmt.Errorf("diff requires initialized semantic states")
	}

	diff := StateDiff{
		BaselineDigest:  baseline.Digest(),
		CandidateDigest: candidate.Digest(),
	}

	if baseline.Digest() == candidate.Digest() {
		diff.Metrics.IdenticalEntitiesCount = uint64(len(baseline.Entities()))
		return diff, nil
	}

	baselineEntities := make(map[EntityRef]Entity, len(baseline.Entities()))
	for _, e := range baseline.Entities() {
		baselineEntities[e.Ref()] = e
	}

	candidateEntities := make(map[EntityRef]Entity, len(candidate.Entities()))
	for _, e := range candidate.Entities() {
		candidateEntities[e.Ref()] = e
	}

	for _, cEntity := range candidate.Entities() {
		ref := cEntity.Ref()
		bEntity, existsInBaseline := baselineEntities[ref]
		if !existsInBaseline {
			diff.CreatedEntities = append(diff.CreatedEntities, ref)
			continue
		}

		// Entity exists in both; compare fields.
		allFieldNames := make(map[FieldName]struct{})
		for _, name := range sortedFieldNames(cEntity.fields) {
			allFieldNames[name] = struct{}{}
		}
		for _, name := range sortedFieldNames(bEntity.fields) {
			allFieldNames[name] = struct{}{}
		}

		sortedNames := make([]FieldName, 0, len(allFieldNames))
		for name := range allFieldNames {
			sortedNames = append(sortedNames, name)
		}
		sort.Slice(sortedNames, func(i, j int) bool {
			return sortedNames[i] < sortedNames[j]
		})

		var fieldDiffs []FieldDiff
		for _, name := range sortedNames {
			bVal, bOk := bEntity.Field(name)
			cVal, cOk := cEntity.Field(name)
			if bOk != cOk || !bVal.Equal(cVal) {
				fieldDiffs = append(fieldDiffs, FieldDiff{
					Name:      name,
					Baseline:  bVal,
					Candidate: cVal,
				})
			}
		}

		if len(fieldDiffs) > 0 {
			diff.ModifiedEntities = append(diff.ModifiedEntities, EntityModification{
				Ref:        ref,
				FieldDiffs: fieldDiffs,
			})
			diff.Metrics.FieldChangesCount += uint64(len(fieldDiffs))
		} else {
			diff.Metrics.IdenticalEntitiesCount++
		}
	}

	for _, bEntity := range baseline.Entities() {
		ref := bEntity.Ref()
		if _, existsInCandidate := candidateEntities[ref]; !existsInCandidate {
			diff.DeletedEntities = append(diff.DeletedEntities, ref)
		}
	}

	// Canonical sorting of entities
	sort.Slice(diff.CreatedEntities, func(i, j int) bool {
		return compareEntityRefs(diff.CreatedEntities[i], diff.CreatedEntities[j]) < 0
	})
	sort.Slice(diff.DeletedEntities, func(i, j int) bool {
		return compareEntityRefs(diff.DeletedEntities[i], diff.DeletedEntities[j]) < 0
	})
	sort.Slice(diff.ModifiedEntities, func(i, j int) bool {
		return compareEntityRefs(diff.ModifiedEntities[i].Ref, diff.ModifiedEntities[j].Ref) < 0
	})

	// Compare relations
	baselineRelations := make(map[Relation]struct{}, len(baseline.Relations()))
	for _, r := range baseline.Relations() {
		baselineRelations[r] = struct{}{}
	}

	candidateRelations := make(map[Relation]struct{}, len(candidate.Relations()))
	for _, r := range candidate.Relations() {
		candidateRelations[r] = struct{}{}
	}

	for _, r := range candidate.Relations() {
		if _, existsInBaseline := baselineRelations[r]; !existsInBaseline {
			diff.AddedRelations = append(diff.AddedRelations, r)
		}
	}

	for _, r := range baseline.Relations() {
		if _, existsInCandidate := candidateRelations[r]; !existsInCandidate {
			diff.RemovedRelations = append(diff.RemovedRelations, r)
		}
	}

	sort.Slice(diff.AddedRelations, func(i, j int) bool {
		return compareRelations(diff.AddedRelations[i], diff.AddedRelations[j]) < 0
	})
	sort.Slice(diff.RemovedRelations, func(i, j int) bool {
		return compareRelations(diff.RemovedRelations[i], diff.RemovedRelations[j]) < 0
	})

	diff.Metrics.CreatedEntitiesCount = uint64(len(diff.CreatedEntities))
	diff.Metrics.DeletedEntitiesCount = uint64(len(diff.DeletedEntities))
	diff.Metrics.ModifiedEntitiesCount = uint64(len(diff.ModifiedEntities))
	diff.Metrics.AddedRelationsCount = uint64(len(diff.AddedRelations))
	diff.Metrics.RemovedRelationsCount = uint64(len(diff.RemovedRelations))

	return diff, nil
}
