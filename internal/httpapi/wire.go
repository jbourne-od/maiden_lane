package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"

	openapiv1 "github.com/optimaldynamics/maiden-lane/internal/httpapi/openapiv1"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

// This file translates between the published wire contract and the semantic
// kernel. It is the most dangerous file in the package, because translation is
// where a wire format quietly becomes a second source of meaning.
//
// Two rules keep that from happening, and both are asserted by test:
//
//  1. Translation runs one way, through the kernel's own constructors. This
//     package never hashes a document, never assembles an identity from parts,
//     and never re-derives a digest. Identities in responses are the kernel's,
//     copied verbatim (Inviolate 4).
//
//  2. Unknown or malformed input is rejected, never coerced or dropped. A
//     dropped field produces a valid artifact that omits data the client
//     believed it sent, which is worse than an error because nothing reports it.
//
// Client-supplied identities are deliberately not accepted anywhere. A client
// supplies a canonical source key and an input lineage; the kernel derives the
// entity identity from them, so a client cannot forge or collide one.

// errTranslation marks input this package refused. Handlers map it to an
// invalid-request problem; the message never reaches the client, because
// echoing rejected input into a response body is how a diagnostic aid becomes
// an injection vector.
var errTranslation = errors.New("httpapi: request could not be translated")

func translationError(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errTranslation, fmt.Sprintf(format, args...))
}

// valueFromWire translates one typed scalar. Exactly one payload must be
// present and it must agree with the declared kind: a mismatch is a client
// type error, and coercing it would turn that error into a different but
// silently valid artifact.
func valueFromWire(value openapiv1.Value) (semantic.Value, error) {
	present := 0
	if value.String != nil {
		present++
	}
	if value.Atom != nil {
		present++
	}
	if value.Int64 != nil {
		present++
	}
	if present != 1 {
		return semantic.Value{}, translationError("value carries %d payloads, want exactly 1", present)
	}

	switch value.Kind {
	case openapiv1.ValueKindString:
		if value.String == nil {
			return semantic.Value{}, translationError("string value carries a different payload")
		}
		return semantic.NewStringValue(*value.String)
	case openapiv1.ValueKindAtom:
		if value.Atom == nil {
			return semantic.Value{}, translationError("atom value carries a different payload")
		}
		return semantic.NewAtomValue(*value.Atom)
	case openapiv1.ValueKindInt64:
		if value.Int64 == nil {
			return semantic.Value{}, translationError("int64 value carries a different payload")
		}
		return semantic.NewInt64Value(*value.Int64), nil
	default:
		return semantic.Value{}, translationError("unknown value kind")
	}
}

// valueToWire projects a kernel value outward.
func valueToWire(value semantic.Value) openapiv1.Value {
	switch value.Kind() {
	case semantic.ValueString:
		text, _ := value.String()
		return openapiv1.Value{Kind: openapiv1.ValueKindString, String: &text}
	case semantic.ValueAtom:
		// The kernel's String accessor returns the exact bytes for both the
		// string and atom variants; the kind is what distinguishes them.
		atom, _ := value.String()
		return openapiv1.Value{Kind: openapiv1.ValueKindAtom, Atom: &atom}
	case semantic.ValueInt64:
		number, _ := value.Int64()
		return openapiv1.Value{Kind: openapiv1.ValueKindInt64, Int64: &number}
	default:
		return openapiv1.Value{}
	}
}

// schemaFromWire builds the pinned schema. The kernel validates and
// canonicalizes; this function only maps closed tokens onto closed types.
func schemaFromWire(declaration openapiv1.SchemaDeclaration) (semantic.Schema, error) {
	entities := make([]semantic.EntityDeclaration, 0, len(declaration.Entities))
	for _, entity := range declaration.Entities {
		fields := make([]semantic.FieldDeclaration, 0, len(entity.Fields))
		for _, field := range entity.Fields {
			kind, err := valueKindFromWire(field.Kind)
			if err != nil {
				return semantic.Schema{}, err
			}
			required := false
			if field.RequiredAtConstruction != nil {
				required = *field.RequiredAtConstruction
			}
			fields = append(fields, semantic.FieldDeclaration{
				Name:                   semantic.FieldName(field.Name),
				Kind:                   kind,
				RequiredAtConstruction: required,
			})
		}
		entities = append(entities, semantic.EntityDeclaration{
			Kind:   semantic.EntityKind(entity.Kind),
			Fields: fields,
		})
	}

	relations := make([]semantic.RelationDeclaration, 0)
	if declaration.Relations != nil {
		for _, relation := range *declaration.Relations {
			relations = append(relations, semantic.RelationDeclaration{
				Kind:     semantic.RelationKind(relation.Kind),
				FromKind: semantic.EntityKind(relation.FromKind),
				ToKind:   semantic.EntityKind(relation.ToKind),
			})
		}
	}
	return semantic.NewSchema(entities, relations)
}

func valueKindFromWire(kind openapiv1.ValueKind) (semantic.ValueKind, error) {
	switch kind {
	case openapiv1.ValueKindString:
		return semantic.ValueString, nil
	case openapiv1.ValueKindAtom:
		return semantic.ValueAtom, nil
	case openapiv1.ValueKindInt64:
		return semantic.ValueInt64, nil
	default:
		return 0, translationError("unknown value kind")
	}
}

func valueKindToWire(kind semantic.ValueKind) openapiv1.ValueKind {
	switch kind {
	case semantic.ValueString:
		return openapiv1.ValueKindString
	case semantic.ValueAtom:
		return openapiv1.ValueKindAtom
	default:
		return openapiv1.ValueKindInt64
	}
}

// stateFromWire builds the pinned initial state.
//
// Entity identities are derived here rather than accepted: SourceEntityID
// combines the lineage with the client's canonical source key, so the same
// observation load always yields the same identity and a client cannot supply
// one that the lineage does not actually produce.
func stateFromWire(schema semantic.Schema, input openapiv1.StateInput) (semantic.State, error) {
	lineage, err := semantic.NewInputLineageID(input.Lineage.Namespace, input.Lineage.RootKey)
	if err != nil {
		return semantic.State{}, translationError("invalid input lineage")
	}

	entities := make([]semantic.Entity, 0, len(input.Entities))
	for _, entity := range input.Entities {
		fields := make(map[semantic.FieldName]semantic.Value, len(entity.Fields))
		for name, value := range entity.Fields {
			translated, err := valueFromWire(value)
			if err != nil {
				return semantic.State{}, err
			}
			fields[semantic.FieldName(name)] = translated
		}
		kind := semantic.EntityKind(entity.Kind)
		built, err := semantic.NewEntity(semantic.EntityRef{
			Kind: kind,
			ID:   semantic.SourceEntityID(lineage, kind, entity.CanonicalSourceKey),
		}, fields)
		if err != nil {
			// The kernel rejects an undeclared or wrongly typed field here.
			// That rejection is preserved rather than softened: a dropped field
			// would produce an artifact missing data the client believed it sent.
			return semantic.State{}, translationError("invalid entity")
		}
		entities = append(entities, built)
	}

	relations := make([]semantic.Relation, 0)
	if input.Relations != nil {
		for _, relation := range *input.Relations {
			fromKind := semantic.EntityKind(relation.FromKind)
			toKind := semantic.EntityKind(relation.ToKind)
			relations = append(relations, semantic.Relation{
				Kind: semantic.RelationKind(relation.Kind),
				From: semantic.EntityRef{Kind: fromKind, ID: semantic.SourceEntityID(lineage, fromKind, relation.FromSourceKey)},
				To:   semantic.EntityRef{Kind: toKind, ID: semantic.SourceEntityID(lineage, toKind, relation.ToSourceKey)},
			})
		}
	}

	state, err := semantic.NewState(schema, lineage, entities, relations)
	if err != nil {
		return semantic.State{}, translationError("invalid initial state")
	}
	return state, nil
}

// worldFromWire builds the pinned world. An absent or empty reference list is
// a real, versioned empty world rather than a missing value.
func worldFromWire(input *openapiv1.WorldInput) (semantic.World, error) {
	if input == nil || input.References == nil {
		return semantic.NewWorld(nil)
	}
	references := make([]semantic.WorldReference, 0, len(*input.References))
	for _, reference := range *input.References {
		kind, err := worldReferenceKindFromWire(reference.Kind)
		if err != nil {
			return semantic.World{}, err
		}
		built, err := semantic.NewWorldReference(kind, semantic.Digest(reference.ContentDigest))
		if err != nil {
			return semantic.World{}, translationError("invalid world reference")
		}
		references = append(references, built)
	}
	world, err := semantic.NewWorld(references)
	if err != nil {
		return semantic.World{}, translationError("invalid world")
	}
	return world, nil
}

func worldReferenceKindFromWire(kind openapiv1.WorldReferenceKind) (semantic.WorldReferenceKind, error) {
	switch kind {
	case openapiv1.WorldReferenceKindSnapshot:
		return semantic.WorldReferenceSnapshot, nil
	case openapiv1.WorldReferenceKindConfiguration:
		return semantic.WorldReferenceConfiguration, nil
	default:
		return 0, translationError("unknown world reference kind")
	}
}

// executorIdentityFromWire builds the closed executor identity. It affects
// only ExecutionID and never enters checkpoint or journal identity.
func executorIdentityFromWire(input openapiv1.ExecutorIdentity) (semantic.ExecutorIdentity, error) {
	identity, err := semantic.NewExecutorIdentity(input.Backend, semantic.Digest(input.Version))
	if err != nil {
		return semantic.ExecutorIdentity{}, translationError("invalid executor identity")
	}
	return identity, nil
}

func provenancePolicyFromWire(policy openapiv1.CreateExecutionRequestProvenancePolicy) (semantic.ProvenancePolicy, error) {
	if policy == openapiv1.CreateExecutionRequestProvenancePolicyChangesV1 {
		return semantic.ChangesProvenance, nil
	}
	return 0, translationError("unknown provenance policy")
}

// compileRequestFromWire builds the compiler request from declarations. The
// compiler owns all validation; this only maps closed tokens.
func compileRequestFromWire(declarations openapiv1.PlanDeclarations) (semantic.CompileRequest, error) {
	schema, err := schemaFromWire(declarations.Schema)
	if err != nil {
		return semantic.CompileRequest{}, err
	}
	rules, err := rulesetFromWire(declarations.Rules)
	if err != nil {
		return semantic.CompileRequest{}, err
	}
	profiles, err := profilesFromWire(declarations.Profiles)
	if err != nil {
		return semantic.CompileRequest{}, err
	}
	return semantic.CompileRequest{
		Schema:                   schema.Declaration(),
		Rules:                    rules,
		Profiles:                 profiles,
		CompilerSemanticsVersion: semantic.CompilerSemanticsVersion(declarations.CompilerSemanticsVersion),
	}, nil
}

func rulesetFromWire(declaration openapiv1.RulesetDeclaration) (semantic.RulesetDeclaration, error) {
	transformations := make([]semantic.TransformationDeclaration, 0, len(declaration.Transformations))
	for _, transformation := range declaration.Transformations {
		translated, err := transformationFromWire(transformation)
		if err != nil {
			return semantic.RulesetDeclaration{}, err
		}
		transformations = append(transformations, translated)
	}

	checkpoints := make([]semantic.CheckpointDeclaration, 0)
	if declaration.Checkpoints != nil {
		for _, checkpoint := range *declaration.Checkpoints {
			checkpoints = append(checkpoints, semantic.CheckpointDeclaration{
				Key:   semantic.CheckpointKey(checkpoint.Key),
				After: semantic.RuleID(checkpoint.After),
			})
		}
	}
	return semantic.RulesetDeclaration{Transformations: transformations, Checkpoints: checkpoints}, nil
}

func transformationFromWire(declaration openapiv1.TransformationDeclaration) (semantic.TransformationDeclaration, error) {
	translated := semantic.TransformationDeclaration{
		ID:             semantic.RuleID(declaration.Id),
		DeclaredReads:  fieldPathsFromWire(declaration.DeclaredReads),
		DeclaredWrites: fieldPathsFromWire(declaration.DeclaredWrites),
	}
	if declaration.After != nil {
		for _, rule := range *declaration.After {
			translated.After = append(translated.After, semantic.RuleID(rule))
		}
	}

	// The union is closed: exactly one payload, and it must agree with the
	// operator tag. The compiler enforces the agreement; this rejects a request
	// that carries neither or both before it gets there.
	switch declaration.Operator {
	case openapiv1.TransformationDeclarationOperatorFormRelatedEntity:
		if declaration.Form == nil || declaration.Aggregate != nil {
			return semantic.TransformationDeclaration{}, translationError("form operator without exactly its own payload")
		}
		translated.Operator = semantic.OperatorFormRelatedEntity
		form, err := formFromWire(*declaration.Form)
		if err != nil {
			return semantic.TransformationDeclaration{}, err
		}
		translated.Form = &form
	case openapiv1.TransformationDeclarationOperatorAggregateRelatedFields:
		if declaration.Aggregate == nil || declaration.Form != nil {
			return semantic.TransformationDeclaration{}, translationError("aggregate operator without exactly its own payload")
		}
		translated.Operator = semantic.OperatorAggregateRelatedFields
		aggregate, err := aggregateFromWire(*declaration.Aggregate)
		if err != nil {
			return semantic.TransformationDeclaration{}, err
		}
		translated.Aggregate = &aggregate
	default:
		return semantic.TransformationDeclaration{}, translationError("unknown operator")
	}
	return translated, nil
}

func formFromWire(form openapiv1.FormRelatedEntity) (semantic.FormRelatedEntityDeclaration, error) {
	sources := make([]semantic.SourceReference, 0, len(form.Sources))
	for _, source := range form.Sources {
		sources = append(sources, semantic.SourceReference{
			Kind:               semantic.EntityKind(source.Kind),
			CanonicalSourceKey: source.CanonicalSourceKey,
		})
	}
	translated := semantic.FormRelatedEntityDeclaration{
		SourceKind:    semantic.EntityKind(form.SourceKind),
		Sources:       sources,
		OutputKind:    semantic.EntityKind(form.OutputKind),
		OutputSlot:    semantic.OutputSlotKey(form.OutputSlot),
		GroupingField: semantic.FieldPath(form.GroupingField),
		SourceCount:   uint64(form.SourceCount),
		CopiedFields:  fieldCopiesFromWire(form.CopiedFields),
		RelationKind:  semantic.RelationKind(form.RelationKind),
	}
	if form.OutputKey != nil {
		if form.OutputKey.Kind != openapiv1.OutputKeyExpressionKindCommonSourceField {
			return semantic.FormRelatedEntityDeclaration{}, translationError("unknown output key kind")
		}
		translated.OutputKey = &semantic.OutputKeyExpression{
			Kind:  semantic.OutputKeyCommonSourceField,
			Field: semantic.FieldPath(form.OutputKey.Field),
		}
	}
	return translated, nil
}

func aggregateFromWire(aggregate openapiv1.AggregateRelatedFields) (semantic.AggregateRelatedFieldsDeclaration, error) {
	predicates, err := predicatesFromWire(aggregate.Predicates)
	if err != nil {
		return semantic.AggregateRelatedFieldsDeclaration{}, err
	}
	resultPredicates, err := predicatesFromWire(aggregate.ResultPredicates)
	if err != nil {
		return semantic.AggregateRelatedFieldsDeclaration{}, err
	}
	reductions, err := reductionsFromWire(aggregate.Reductions)
	if err != nil {
		return semantic.AggregateRelatedFieldsDeclaration{}, err
	}
	return semantic.AggregateRelatedFieldsDeclaration{
		Target: semantic.OutputSlotReference{
			Rule: semantic.RuleID(aggregate.Target.Rule),
			Slot: semantic.OutputSlotKey(aggregate.Target.Slot),
		},
		RelationKind:        semantic.RelationKind(aggregate.RelationKind),
		SourceKind:          semantic.EntityKind(aggregate.SourceKind),
		RequiredSourceTuple: fieldPathsFromWire(&aggregate.RequiredSourceTuple),
		Predicates:          predicates,
		Anchor: semantic.FieldCopy{
			Source:      semantic.FieldPath(aggregate.Anchor.Source),
			Destination: semantic.FieldPath(aggregate.Anchor.Destination),
		},
		Reductions:       reductions,
		ResultPredicates: resultPredicates,
	}, nil
}

func predicatesFromWire(predicates *[]openapiv1.AggregatePredicate) ([]semantic.AggregatePredicate, error) {
	if predicates == nil {
		return nil, nil
	}
	translated := make([]semantic.AggregatePredicate, 0, len(*predicates))
	for _, predicate := range *predicates {
		kind, err := predicateKindFromWire(predicate.Kind)
		if err != nil {
			return nil, err
		}
		translated = append(translated, semantic.AggregatePredicate{
			Kind:   kind,
			Fields: fieldPathsFromWire(&predicate.Fields),
		})
	}
	return translated, nil
}

func predicateKindFromWire(kind openapiv1.AggregatePredicateKind) (semantic.AggregatePredicateKind, error) {
	switch kind {
	case openapiv1.AggregatePredicateKindCompleteTuple:
		return semantic.CompleteTuple, nil
	case openapiv1.AggregatePredicateKindNonNegativeInt:
		return semantic.NonNegativeInt, nil
	case openapiv1.AggregatePredicateKindEqualFieldAcrossSources:
		return semantic.EqualFieldAcrossSources, nil
	case openapiv1.AggregatePredicateKindLessOrEqualFields:
		return semantic.LessOrEqualFields, nil
	default:
		return 0, translationError("unknown aggregate predicate kind")
	}
}

func reductionsFromWire(reductions *[]openapiv1.FieldReduction) ([]semantic.FieldReduction, error) {
	if reductions == nil {
		return nil, nil
	}
	translated := make([]semantic.FieldReduction, 0, len(*reductions))
	for _, reduction := range *reductions {
		if reduction.Kind != openapiv1.FieldReductionKindReduceInt64Max {
			return nil, translationError("unknown reduction kind")
		}
		translated = append(translated, semantic.FieldReduction{
			Kind:        semantic.ReduceInt64Max,
			Source:      semantic.FieldPath(reduction.Source),
			Destination: semantic.FieldPath(reduction.Destination),
		})
	}
	return translated, nil
}

func profilesFromWire(profiles *[]openapiv1.ProfileDeclaration) ([]semantic.ProfileDeclaration, error) {
	if profiles == nil {
		return nil, nil
	}
	translated := make([]semantic.ProfileDeclaration, 0, len(*profiles))
	for _, profile := range *profiles {
		if profile.Scope.Kind != openapiv1.ProfileDeclarationScopeKindAllEntitiesOfKind {
			return nil, translationError("unknown profile scope kind")
		}
		if profile.Aggregation != openapiv1.ProfileDeclarationAggregationAllSelected {
			return nil, translationError("unknown profile aggregation")
		}
		requirements := make([]semantic.RequirementAtom, 0, len(profile.Requirements))
		for _, atom := range profile.Requirements {
			if atom.Kind != openapiv1.RequirementAtomKindFieldPresent {
				return nil, translationError("unknown requirement atom kind")
			}
			requirements = append(requirements, semantic.RequirementAtom{
				Code:  semantic.RequirementCode(atom.Code),
				Kind:  semantic.FieldPresent,
				Field: semantic.FieldPath(atom.Field),
			})
		}
		declaration := semantic.ProfileDeclaration{
			Key: semantic.ProfileKey(profile.Key),
			Scope: semantic.ProfileScope{
				Kind:       semantic.AllEntitiesOfKind,
				EntityKind: semantic.EntityKind(profile.Scope.EntityKind),
			},
			Aggregation:  semantic.AllSelected,
			Requirements: requirements,
		}
		if profile.Implies != nil {
			for _, implied := range *profile.Implies {
				declaration.Implies = append(declaration.Implies, semantic.ProfileKey(implied))
			}
		}
		translated = append(translated, declaration)
	}
	return translated, nil
}

func fieldPathsFromWire(paths *[]string) []semantic.FieldPath {
	if paths == nil {
		return nil
	}
	translated := make([]semantic.FieldPath, 0, len(*paths))
	for _, path := range *paths {
		translated = append(translated, semantic.FieldPath(path))
	}
	return translated
}

func fieldCopiesFromWire(copies *[]openapiv1.FieldCopy) []semantic.FieldCopy {
	if copies == nil {
		return nil
	}
	translated := make([]semantic.FieldCopy, 0, len(*copies))
	for _, copied := range *copies {
		translated = append(translated, semantic.FieldCopy{
			Source:      semantic.FieldPath(copied.Source),
			Destination: semantic.FieldPath(copied.Destination),
		})
	}
	return translated
}

// planToWire projects a compiled plan outward.
//
// withDeclarations governs the debug projection. It is derived from the
// compiled plan rather than from a stored copy of the request, so it shows what
// the compiler accepted, in canonical order. Where that differs from what a
// client sent, the difference is the compiler's canonicalization, which is
// usually the thing an operator is trying to see. Operational telemetry cannot
// carry declarations at all, so this is the only way to resolve a PlanID
// observed in a trace back to what the plan actually says.
func planToWire(
	plan semantic.Plan,
	profiles []semantic.CompiledProfile,
	schema semantic.Schema,
	compilerVersion semantic.CompilerSemanticsVersion,
	withDeclarations bool,
) (openapiv1.Plan, error) {
	rules := make([]string, 0, len(plan.Transformations()))
	for _, transformation := range plan.Transformations() {
		rules = append(rules, string(transformation.Declaration().ID))
	}

	checkpoints := make([]openapiv1.CheckpointDeclaration, 0, len(plan.Checkpoints()))
	for _, checkpoint := range plan.Checkpoints() {
		checkpoints = append(checkpoints, openapiv1.CheckpointDeclaration{
			Key:   string(checkpoint.Key),
			After: string(checkpoint.After),
		})
	}

	compiled := make([]openapiv1.CompiledProfile, 0, len(profiles))
	for _, profile := range profiles {
		compiled = append(compiled, openapiv1.CompiledProfile{
			Key:       string(profile.Key()),
			ProfileID: openapiv1.Digest(profile.ID()),
		})
	}

	projected := openapiv1.Plan{
		PlanID:      openapiv1.Digest(plan.ID()),
		Rules:       rules,
		Checkpoints: checkpoints,
		Profiles:    compiled,
	}
	if withDeclarations {
		declarations, err := declarationsToWire(plan, profiles, schema, compilerVersion)
		if err != nil {
			return openapiv1.Plan{}, err
		}
		projected.Declarations = &declarations
	}
	return projected, nil
}

func declarationsToWire(
	plan semantic.Plan,
	profiles []semantic.CompiledProfile,
	schema semantic.Schema,
	compilerVersion semantic.CompilerSemanticsVersion,
) (openapiv1.PlanDeclarations, error) {
	transformations := make([]openapiv1.TransformationDeclaration, 0, len(plan.Transformations()))
	for _, transformation := range plan.Transformations() {
		projected, err := transformationToWire(transformation.Declaration())
		if err != nil {
			return openapiv1.PlanDeclarations{}, err
		}
		transformations = append(transformations, projected)
	}
	checkpoints := make([]openapiv1.CheckpointDeclaration, 0, len(plan.Checkpoints()))
	for _, checkpoint := range plan.Checkpoints() {
		checkpoints = append(checkpoints, openapiv1.CheckpointDeclaration{
			Key:   string(checkpoint.Key),
			After: string(checkpoint.After),
		})
	}
	profileDeclarations := make([]openapiv1.ProfileDeclaration, 0, len(profiles))
	for _, profile := range profiles {
		profileDeclarations = append(profileDeclarations, profileToWire(profile.Declaration()))
	}

	return openapiv1.PlanDeclarations{
		CompilerSemanticsVersion: string(compilerVersion),
		Schema:                   schemaToWire(schema),
		Rules: openapiv1.RulesetDeclaration{
			Transformations: transformations,
			Checkpoints:     &checkpoints,
		},
		Profiles: &profileDeclarations,
	}, nil
}

func schemaToWire(schema semantic.Schema) openapiv1.SchemaDeclaration {
	declaration := schema.Declaration()
	entities := make([]openapiv1.EntityDeclaration, 0)
	for _, entity := range declaration.EntityDeclarations() {
		fields := make([]openapiv1.FieldDeclaration, 0, len(entity.Fields))
		for _, field := range entity.Fields {
			required := field.RequiredAtConstruction
			fields = append(fields, openapiv1.FieldDeclaration{
				Name:                   string(field.Name),
				Kind:                   valueKindToWire(field.Kind),
				RequiredAtConstruction: &required,
			})
		}
		entities = append(entities, openapiv1.EntityDeclaration{
			Kind:   string(entity.Kind),
			Fields: fields,
		})
	}
	relations := make([]openapiv1.RelationDeclaration, 0)
	for _, relation := range declaration.RelationDeclarations() {
		relations = append(relations, openapiv1.RelationDeclaration{
			Kind:     string(relation.Kind),
			FromKind: string(relation.FromKind),
			ToKind:   string(relation.ToKind),
		})
	}
	return openapiv1.SchemaDeclaration{Entities: entities, Relations: &relations}
}

// transformationToWire projects one compiled declaration onto the wire contract.
//
// IT RETURNS AN ERROR BECAUSE THE PROJECTION IS PARTIAL, and an earlier version hid that. The
// switch below had no default, so a declaration whose operator the contract cannot express
// projected to a wire object carrying the ZERO operator -- a token outside the closed enum --
// and no payload at all. That is a boundary inventing a declaration nobody holds, and it
// fails open: the response looks well-formed and describes a rule that does not exist.
//
// OperatorSelectAndAssign is exactly that case today. rulesetFromWire refuses it inbound
// (its default arm errors), so no such plan can currently be stored through the API and this
// path is unreachable -- but "unreachable" was also true of the group node kinds until a
// consumer arrived, and the contract gaining the operator is what makes this live.
func transformationToWire(declaration semantic.TransformationDeclaration) (openapiv1.TransformationDeclaration, error) {
	reads := fieldPathsToWire(declaration.DeclaredReads)
	writes := fieldPathsToWire(declaration.DeclaredWrites)
	after := make([]string, 0, len(declaration.After))
	for _, rule := range declaration.After {
		after = append(after, string(rule))
	}

	projected := openapiv1.TransformationDeclaration{
		Id:             string(declaration.ID),
		DeclaredReads:  &reads,
		DeclaredWrites: &writes,
		After:          &after,
	}
	switch {
	case declaration.Form != nil:
		projected.Operator = openapiv1.TransformationDeclarationOperatorFormRelatedEntity
		form := formToWire(*declaration.Form)
		projected.Form = &form
	case declaration.Aggregate != nil:
		projected.Operator = openapiv1.TransformationDeclarationOperatorAggregateRelatedFields
		aggregate := aggregateToWire(*declaration.Aggregate)
		projected.Aggregate = &aggregate
	default:
		return openapiv1.TransformationDeclaration{}, translationError(
			"compiled operator has no representation in this contract version")
	}
	return projected, nil
}

func formToWire(form semantic.FormRelatedEntityDeclaration) openapiv1.FormRelatedEntity {
	sources := make([]openapiv1.SourceReference, 0, len(form.Sources))
	for _, source := range form.Sources {
		sources = append(sources, openapiv1.SourceReference{
			Kind:               string(source.Kind),
			CanonicalSourceKey: source.CanonicalSourceKey,
		})
	}
	copies := fieldCopiesToWire(form.CopiedFields)
	projected := openapiv1.FormRelatedEntity{
		SourceKind:    string(form.SourceKind),
		Sources:       sources,
		OutputKind:    string(form.OutputKind),
		OutputSlot:    string(form.OutputSlot),
		GroupingField: string(form.GroupingField),
		SourceCount:   int64(form.SourceCount),
		CopiedFields:  &copies,
		RelationKind:  string(form.RelationKind),
	}
	if form.OutputKey != nil {
		projected.OutputKey = &openapiv1.OutputKeyExpression{
			Kind:  openapiv1.OutputKeyExpressionKindCommonSourceField,
			Field: string(form.OutputKey.Field),
		}
	}
	return projected
}

func aggregateToWire(aggregate semantic.AggregateRelatedFieldsDeclaration) openapiv1.AggregateRelatedFields {
	predicates := predicatesToWire(aggregate.Predicates)
	resultPredicates := predicatesToWire(aggregate.ResultPredicates)
	reductions := make([]openapiv1.FieldReduction, 0, len(aggregate.Reductions))
	for _, reduction := range aggregate.Reductions {
		reductions = append(reductions, openapiv1.FieldReduction{
			Kind:        openapiv1.FieldReductionKindReduceInt64Max,
			Source:      string(reduction.Source),
			Destination: string(reduction.Destination),
		})
	}
	return openapiv1.AggregateRelatedFields{
		Target: openapiv1.OutputSlotReference{
			Rule: string(aggregate.Target.Rule),
			Slot: string(aggregate.Target.Slot),
		},
		RelationKind:        string(aggregate.RelationKind),
		SourceKind:          string(aggregate.SourceKind),
		RequiredSourceTuple: fieldPathsToWire(aggregate.RequiredSourceTuple),
		Predicates:          &predicates,
		Anchor: openapiv1.FieldCopy{
			Source:      string(aggregate.Anchor.Source),
			Destination: string(aggregate.Anchor.Destination),
		},
		Reductions:       &reductions,
		ResultPredicates: &resultPredicates,
	}
}

func predicatesToWire(predicates []semantic.AggregatePredicate) []openapiv1.AggregatePredicate {
	projected := make([]openapiv1.AggregatePredicate, 0, len(predicates))
	for _, predicate := range predicates {
		projected = append(projected, openapiv1.AggregatePredicate{
			Kind:   predicateKindToWire(predicate.Kind),
			Fields: fieldPathsToWire(predicate.Fields),
		})
	}
	return projected
}

func predicateKindToWire(kind semantic.AggregatePredicateKind) openapiv1.AggregatePredicateKind {
	switch kind {
	case semantic.CompleteTuple:
		return openapiv1.AggregatePredicateKindCompleteTuple
	case semantic.NonNegativeInt:
		return openapiv1.AggregatePredicateKindNonNegativeInt
	case semantic.EqualFieldAcrossSources:
		return openapiv1.AggregatePredicateKindEqualFieldAcrossSources
	default:
		return openapiv1.AggregatePredicateKindLessOrEqualFields
	}
}

func profileToWire(declaration semantic.ProfileDeclaration) openapiv1.ProfileDeclaration {
	requirements := make([]openapiv1.RequirementAtom, 0, len(declaration.Requirements))
	for _, atom := range declaration.Requirements {
		requirements = append(requirements, openapiv1.RequirementAtom{
			Code:  string(atom.Code),
			Kind:  openapiv1.RequirementAtomKindFieldPresent,
			Field: string(atom.Field),
		})
	}
	implies := make([]string, 0, len(declaration.Implies))
	for _, implied := range declaration.Implies {
		implies = append(implies, string(implied))
	}
	return openapiv1.ProfileDeclaration{
		Key: string(declaration.Key),
		Scope: struct {
			EntityKind string                                `json:"entityKind"`
			Kind       openapiv1.ProfileDeclarationScopeKind `json:"kind"`
		}{
			EntityKind: string(declaration.Scope.EntityKind),
			Kind:       openapiv1.ProfileDeclarationScopeKindAllEntitiesOfKind,
		},
		Aggregation:  openapiv1.ProfileDeclarationAggregationAllSelected,
		Requirements: requirements,
		Implies:      &implies,
	}
}

func fieldPathsToWire(paths []semantic.FieldPath) []string {
	projected := make([]string, 0, len(paths))
	for _, path := range paths {
		projected = append(projected, string(path))
	}
	return projected
}

func fieldCopiesToWire(copies []semantic.FieldCopy) []openapiv1.FieldCopy {
	projected := make([]openapiv1.FieldCopy, 0, len(copies))
	for _, copied := range copies {
		projected = append(projected, openapiv1.FieldCopy{
			Source:      string(copied.Source),
			Destination: string(copied.Destination),
		})
	}
	return projected
}

// decodeJSON reads exactly one JSON document of type T from a request body.
//
// Unknown members are rejected rather than ignored (owner decision,
// 2026-08-16). Ignoring them is the friendlier default and the wrong one here:
// a misspelled field would be silently dropped and the run would proceed over
// inputs the client did not intend, producing a valid artifact that is
// quietly wrong. The contract declares additionalProperties: false, so this
// enforces what the contract already promises.
//
// The tradeoff is deliberate and should be understood before adding a field:
// with strict decoding, a client that sends a member this build does not know
// is refused, so new optional members must be introduced in the contract
// before any client may send them.
func decodeJSON[T any](r *http.Request, target *T) error {
	if mediaType := r.Header.Get("Content-Type"); mediaType != "" {
		parsed, _, err := mime.ParseMediaType(mediaType)
		if err != nil || parsed != "application/json" {
			return errUnsupportedMediaType
		}
	}

	decoder := json.NewDecoder(io.LimitReader(r.Body, maxRequestBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return translationError("request body is not a valid document for this operation")
	}
	// A second document in the same body is ambiguous: nothing says which one
	// the client meant, so neither is used.
	if decoder.More() {
		return translationError("request body carries more than one document")
	}
	return nil
}

// maxRequestBytes bounds a request body. Declarations are small; an unbounded
// body would let one request exhaust process memory.
const maxRequestBytes = 8 << 20

// errUnsupportedMediaType is distinguished from a translation failure so a
// handler can answer 415 rather than 400.
var errUnsupportedMediaType = errors.New("httpapi: unsupported media type")
