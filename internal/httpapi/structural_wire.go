package httpapi

import (
	"github.com/optimaldynamics/maiden-lane/internal/httpapi/openapiv1"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

func selectorFromWire(s openapiv1.Selector) (semantic.Selector, error) {
	members, err := cardinalityFromWire(s.Members)
	if err != nil {
		return semantic.Selector{}, err
	}
	selector := semantic.Selector{Kind: semantic.EntityKind(s.Kind), Members: members}
	if s.Where != nil {
		where, err := exprFromWire(*s.Where)
		if err != nil {
			return semantic.Selector{}, err
		}
		selector.Where = &where
	}
	if s.GroupBy != nil {
		groupBy, err := exprFromWire(*s.GroupBy)
		if err != nil {
			return semantic.Selector{}, err
		}
		selector.GroupBy = &groupBy
	}
	return selector, nil
}

func selectorToWire(s semantic.Selector) (openapiv1.Selector, error) {
	members, err := cardinalityToWire(s.Members)
	if err != nil {
		return openapiv1.Selector{}, err
	}
	selector := openapiv1.Selector{Kind: string(s.Kind), Members: members}
	if s.Where != nil {
		where, err := exprToWire(*s.Where)
		if err != nil {
			return openapiv1.Selector{}, err
		}
		selector.Where = &where
	}
	if s.GroupBy != nil {
		groupBy, err := exprToWire(*s.GroupBy)
		if err != nil {
			return openapiv1.Selector{}, err
		}
		selector.GroupBy = &groupBy
	}
	return selector, nil
}

func fieldAssignmentsFromWire(assignments []openapiv1.FieldAssignment) ([]semantic.FieldAssignment, error) {
	result := make([]semantic.FieldAssignment, 0, len(assignments))
	for _, assignment := range assignments {
		value, err := exprFromWire(assignment.Value)
		if err != nil {
			return nil, err
		}
		result = append(result, semantic.FieldAssignment{
			Target: semantic.FieldPath(assignment.Target), Value: value,
		})
	}
	return result, nil
}

func fieldAssignmentsToWire(assignments []semantic.FieldAssignment) ([]openapiv1.FieldAssignment, error) {
	result := make([]openapiv1.FieldAssignment, 0, len(assignments))
	for _, assignment := range assignments {
		value, err := exprToWire(assignment.Value)
		if err != nil {
			return nil, err
		}
		result = append(result, openapiv1.FieldAssignment{
			Target: string(assignment.Target), Value: value,
		})
	}
	return result, nil
}

func insertEntityFromWire(payload openapiv1.InsertEntityDeclaration) (semantic.InsertEntityDeclaration, error) {
	selector, err := selectorFromWire(payload.Selector)
	if err != nil {
		return semantic.InsertEntityDeclaration{}, err
	}
	discriminator, err := exprFromWire(payload.Discriminator)
	if err != nil {
		return semantic.InsertEntityDeclaration{}, err
	}
	guard, err := exprFromWire(payload.Guard)
	if err != nil {
		return semantic.InsertEntityDeclaration{}, err
	}
	assignments, err := fieldAssignmentsFromWire(payload.Assignments)
	if err != nil {
		return semantic.InsertEntityDeclaration{}, err
	}
	return semantic.InsertEntityDeclaration{
		Selector:      selector,
		TargetKind:    semantic.EntityKind(payload.TargetKind),
		Discriminator: discriminator,
		Guard:         guard,
		Assignments:   assignments,
	}, nil
}

func insertEntityToWire(payload semantic.InsertEntityDeclaration) (openapiv1.InsertEntityDeclaration, error) {
	selector, err := selectorToWire(payload.Selector)
	if err != nil {
		return openapiv1.InsertEntityDeclaration{}, err
	}
	discriminator, err := exprToWire(payload.Discriminator)
	if err != nil {
		return openapiv1.InsertEntityDeclaration{}, err
	}
	guard, err := exprToWire(payload.Guard)
	if err != nil {
		return openapiv1.InsertEntityDeclaration{}, err
	}
	assignments, err := fieldAssignmentsToWire(payload.Assignments)
	if err != nil {
		return openapiv1.InsertEntityDeclaration{}, err
	}
	return openapiv1.InsertEntityDeclaration{
		Selector:      selector,
		TargetKind:    string(payload.TargetKind),
		Discriminator: discriminator,
		Guard:         guard,
		Assignments:   assignments,
	}, nil
}

func deleteEntityFromWire(payload openapiv1.DeleteEntityDeclaration) (semantic.DeleteEntityDeclaration, error) {
	selector, err := selectorFromWire(payload.Selector)
	if err != nil {
		return semantic.DeleteEntityDeclaration{}, err
	}
	guard, err := exprFromWire(payload.Guard)
	if err != nil {
		return semantic.DeleteEntityDeclaration{}, err
	}
	return semantic.DeleteEntityDeclaration{
		Selector: selector,
		Guard:    guard,
	}, nil
}

func deleteEntityToWire(payload semantic.DeleteEntityDeclaration) (openapiv1.DeleteEntityDeclaration, error) {
	selector, err := selectorToWire(payload.Selector)
	if err != nil {
		return openapiv1.DeleteEntityDeclaration{}, err
	}
	guard, err := exprToWire(payload.Guard)
	if err != nil {
		return openapiv1.DeleteEntityDeclaration{}, err
	}
	return openapiv1.DeleteEntityDeclaration{
		Selector: selector,
		Guard:    guard,
	}, nil
}

func relateEntitiesFromWire(payload openapiv1.RelateEntitiesDeclaration) (semantic.RelateEntitiesDeclaration, error) {
	fromSelector, err := selectorFromWire(payload.FromSelector)
	if err != nil {
		return semantic.RelateEntitiesDeclaration{}, err
	}
	toSelector, err := selectorFromWire(payload.ToSelector)
	if err != nil {
		return semantic.RelateEntitiesDeclaration{}, err
	}
	guard, err := exprFromWire(payload.Guard)
	if err != nil {
		return semantic.RelateEntitiesDeclaration{}, err
	}
	return semantic.RelateEntitiesDeclaration{
		FromSelector: fromSelector,
		ToSelector:   toSelector,
		RelationKind: semantic.RelationKind(payload.RelationKind),
		Guard:        guard,
	}, nil
}

func relateEntitiesToWire(payload semantic.RelateEntitiesDeclaration) (openapiv1.RelateEntitiesDeclaration, error) {
	fromSelector, err := selectorToWire(payload.FromSelector)
	if err != nil {
		return openapiv1.RelateEntitiesDeclaration{}, err
	}
	toSelector, err := selectorToWire(payload.ToSelector)
	if err != nil {
		return openapiv1.RelateEntitiesDeclaration{}, err
	}
	guard, err := exprToWire(payload.Guard)
	if err != nil {
		return openapiv1.RelateEntitiesDeclaration{}, err
	}
	return openapiv1.RelateEntitiesDeclaration{
		FromSelector: fromSelector,
		ToSelector:   toSelector,
		RelationKind: string(payload.RelationKind),
		Guard:        guard,
	}, nil
}

func unrelateEntitiesFromWire(payload openapiv1.UnrelateEntitiesDeclaration) (semantic.UnrelateEntitiesDeclaration, error) {
	fromSelector, err := selectorFromWire(payload.FromSelector)
	if err != nil {
		return semantic.UnrelateEntitiesDeclaration{}, err
	}
	toSelector, err := selectorFromWire(payload.ToSelector)
	if err != nil {
		return semantic.UnrelateEntitiesDeclaration{}, err
	}
	guard, err := exprFromWire(payload.Guard)
	if err != nil {
		return semantic.UnrelateEntitiesDeclaration{}, err
	}
	return semantic.UnrelateEntitiesDeclaration{
		FromSelector: fromSelector,
		ToSelector:   toSelector,
		RelationKind: semantic.RelationKind(payload.RelationKind),
		Guard:        guard,
	}, nil
}

func unrelateEntitiesToWire(payload semantic.UnrelateEntitiesDeclaration) (openapiv1.UnrelateEntitiesDeclaration, error) {
	fromSelector, err := selectorToWire(payload.FromSelector)
	if err != nil {
		return openapiv1.UnrelateEntitiesDeclaration{}, err
	}
	toSelector, err := selectorToWire(payload.ToSelector)
	if err != nil {
		return openapiv1.UnrelateEntitiesDeclaration{}, err
	}
	guard, err := exprToWire(payload.Guard)
	if err != nil {
		return openapiv1.UnrelateEntitiesDeclaration{}, err
	}
	return openapiv1.UnrelateEntitiesDeclaration{
		FromSelector: fromSelector,
		ToSelector:   toSelector,
		RelationKind: string(payload.RelationKind),
		Guard:        guard,
	}, nil
}

func mergeEntitiesFromWire(payload openapiv1.MergeEntitiesDeclaration) (semantic.MergeEntitiesDeclaration, error) {
	selector, err := selectorFromWire(payload.Selector)
	if err != nil {
		return semantic.MergeEntitiesDeclaration{}, err
	}
	discriminator, err := exprFromWire(payload.Discriminator)
	if err != nil {
		return semantic.MergeEntitiesDeclaration{}, err
	}
	guard, err := exprFromWire(payload.Guard)
	if err != nil {
		return semantic.MergeEntitiesDeclaration{}, err
	}
	assignments, err := fieldAssignmentsFromWire(payload.Assignments)
	if err != nil {
		return semantic.MergeEntitiesDeclaration{}, err
	}
	return semantic.MergeEntitiesDeclaration{
		Selector:          selector,
		TargetKind:        semantic.EntityKind(payload.TargetKind),
		Discriminator:     discriminator,
		Guard:             guard,
		RetainSources:     payload.RetainSources,
		ReanchorRelations: payload.ReanchorRelations,
		Assignments:       assignments,
	}, nil
}

func mergeEntitiesToWire(payload semantic.MergeEntitiesDeclaration) (openapiv1.MergeEntitiesDeclaration, error) {
	selector, err := selectorToWire(payload.Selector)
	if err != nil {
		return openapiv1.MergeEntitiesDeclaration{}, err
	}
	discriminator, err := exprToWire(payload.Discriminator)
	if err != nil {
		return openapiv1.MergeEntitiesDeclaration{}, err
	}
	guard, err := exprToWire(payload.Guard)
	if err != nil {
		return openapiv1.MergeEntitiesDeclaration{}, err
	}
	assignments, err := fieldAssignmentsToWire(payload.Assignments)
	if err != nil {
		return openapiv1.MergeEntitiesDeclaration{}, err
	}
	return openapiv1.MergeEntitiesDeclaration{
		Selector:          selector,
		TargetKind:        string(payload.TargetKind),
		Discriminator:     discriminator,
		Guard:             guard,
		RetainSources:     payload.RetainSources,
		ReanchorRelations: payload.ReanchorRelations,
		Assignments:       assignments,
	}, nil
}

func splitEntityFromWire(payload openapiv1.SplitEntityDeclaration) (semantic.SplitEntityDeclaration, error) {
	selector, err := selectorFromWire(payload.Selector)
	if err != nil {
		return semantic.SplitEntityDeclaration{}, err
	}
	guard, err := exprFromWire(payload.Guard)
	if err != nil {
		return semantic.SplitEntityDeclaration{}, err
	}
	partitions := make([]semantic.PartitionDeclaration, 0, len(payload.Partitions))
	for _, p := range payload.Partitions {
		disc, err := exprFromWire(p.Discriminator)
		if err != nil {
			return semantic.SplitEntityDeclaration{}, err
		}
		assignments, err := fieldAssignmentsFromWire(p.Assignments)
		if err != nil {
			return semantic.SplitEntityDeclaration{}, err
		}
		partitions = append(partitions, semantic.PartitionDeclaration{
			Discriminator: disc,
			Assignments:   assignments,
		})
	}
	return semantic.SplitEntityDeclaration{
		Selector:     selector,
		TargetKind:   semantic.EntityKind(payload.TargetKind),
		Guard:        guard,
		RetainSource: payload.RetainSource,
		Partitions:   partitions,
	}, nil
}

func splitEntityToWire(payload semantic.SplitEntityDeclaration) (openapiv1.SplitEntityDeclaration, error) {
	selector, err := selectorToWire(payload.Selector)
	if err != nil {
		return openapiv1.SplitEntityDeclaration{}, err
	}
	guard, err := exprToWire(payload.Guard)
	if err != nil {
		return openapiv1.SplitEntityDeclaration{}, err
	}
	partitions := make([]openapiv1.PartitionDeclaration, 0, len(payload.Partitions))
	for _, p := range payload.Partitions {
		disc, err := exprToWire(p.Discriminator)
		if err != nil {
			return openapiv1.SplitEntityDeclaration{}, err
		}
		assignments, err := fieldAssignmentsToWire(p.Assignments)
		if err != nil {
			return openapiv1.SplitEntityDeclaration{}, err
		}
		partitions = append(partitions, openapiv1.PartitionDeclaration{
			Discriminator: disc,
			Assignments:   assignments,
		})
	}
	return openapiv1.SplitEntityDeclaration{
		Selector:     selector,
		TargetKind:   string(payload.TargetKind),
		Guard:        guard,
		RetainSource: payload.RetainSource,
		Partitions:   partitions,
	}, nil
}
