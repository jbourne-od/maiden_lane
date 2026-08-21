package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"

	openapiv1 "github.com/optimaldynamics/maiden-lane/internal/httpapi/openapiv1"
	"github.com/optimaldynamics/maiden-lane/internal/ports"
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
	if value.Timestamp != nil {
		present++
	}
	if value.Duration != nil {
		present++
	}
	if value.Decimal != nil {
		present++
	}
	if value.Date != nil {
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
	case openapiv1.ValueKindTimestamp:
		if value.Timestamp == nil {
			return semantic.Value{}, translationError("timestamp value carries a different payload")
		}
		return semantic.NewTimestampValue(*value.Timestamp)
	case openapiv1.ValueKindDuration:
		if value.Duration == nil {
			return semantic.Value{}, translationError("duration value carries a different payload")
		}
		return semantic.NewDurationValue(*value.Duration), nil
	case openapiv1.ValueKindDecimal:
		if value.Decimal == nil {
			return semantic.Value{}, translationError("decimal value carries a different payload")
		}
		return semantic.NewDecimalValue(*value.Decimal)
	case openapiv1.ValueKindDate:
		if value.Date == nil {
			return semantic.Value{}, translationError("date value carries a different payload")
		}
		return semantic.NewDateValue(*value.Date)
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
	case semantic.ValueTimestamp:
		ts, _ := value.Timestamp()
		return openapiv1.Value{Kind: openapiv1.ValueKindTimestamp, Timestamp: &ts}
	case semantic.ValueDuration:
		dur, _ := value.Duration()
		return openapiv1.Value{Kind: openapiv1.ValueKindDuration, Duration: &dur}
	case semantic.ValueDecimal:
		dec, _ := value.Decimal()
		return openapiv1.Value{Kind: openapiv1.ValueKindDecimal, Decimal: &dec}
	case semantic.ValueDate:
		dt, _ := value.Date()
		return openapiv1.Value{Kind: openapiv1.ValueKindDate, Date: &dt}
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
	case openapiv1.ValueKindTimestamp:
		return semantic.ValueTimestamp, nil
	case openapiv1.ValueKindDuration:
		return semantic.ValueDuration, nil
	case openapiv1.ValueKindDecimal:
		return semantic.ValueDecimal, nil
	case openapiv1.ValueKindDate:
		return semantic.ValueDate, nil
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
	case semantic.ValueInt64:
		return openapiv1.ValueKindInt64
	case semantic.ValueTimestamp:
		return openapiv1.ValueKindTimestamp
	case semantic.ValueDuration:
		return openapiv1.ValueKindDuration
	case semantic.ValueDecimal:
		return openapiv1.ValueKindDecimal
	case semantic.ValueDate:
		return openapiv1.ValueKindDate
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
	case openapiv1.TransformationDeclarationOperatorSelectAndAssign:
		if declaration.SelectAssign == nil {
			return semantic.TransformationDeclaration{}, translationError("select-and-assign operator without exactly its own payload")
		}
		translated.Operator = semantic.OperatorSelectAndAssign
		payload, err := selectAssignFromWire(*declaration.SelectAssign)
		if err != nil {
			return semantic.TransformationDeclaration{}, err
		}
		translated.SelectAssign = &payload
	default:
		return semantic.TransformationDeclaration{}, translationError("unknown operator")
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
// The contract has since gained OperatorSelectAndAssign, so the arm that would have been the
// silent one is now written. The default remains, and remains load-bearing: the next operator
// added to the kernel and not to this contract lands there rather than in a response.
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
	case declaration.SelectAssign != nil:
		projected.Operator = openapiv1.TransformationDeclarationOperatorSelectAndAssign
		payload, err := selectAssignToWire(*declaration.SelectAssign)
		if err != nil {
			return openapiv1.TransformationDeclaration{}, err
		}
		projected.SelectAssign = &payload
	default:
		return openapiv1.TransformationDeclaration{}, translationError(
			"compiled operator has no representation in this contract version")
	}
	return projected, nil
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

func comparisonToWire(record ports.ComparisonRecord) openapiv1.Comparison {
	correspondences := make([]openapiv1.ComparisonCorrespondence, 0, len(record.Correspondences))
	for _, c := range record.Correspondences {
		correspondences = append(correspondences, openapiv1.ComparisonCorrespondence{
			Baseline:  string(c.Baseline),
			Candidate: string(c.Candidate),
		})
	}
	return openapiv1.Comparison{
		ComparisonID:          string(record.ComparisonID),
		BaselinePlanID:        string(record.BaselinePlan),
		CandidatePlanID:       string(record.CandidatePlan),
		BaselineCheckpointID:  string(record.Baseline),
		CandidateCheckpointID: string(record.Candidate),
		ProfileID:             string(record.Profile),
		WorldID:               string(record.World),
		CorpusID:              string(record.Corpus),
		PolicyID:              string(record.PolicyID),
		Correspondences:       correspondences,
	}
}

func checkpointPairsFromWire(pairs []openapiv1.CheckpointPair) ([]semantic.CheckpointPair, error) {
	if len(pairs) == 0 {
		return nil, translationError("comparison request has no checkpoint correspondences")
	}
	out := make([]semantic.CheckpointPair, 0, len(pairs))
	for _, p := range pairs {
		if p.Baseline == "" || p.Candidate == "" {
			return nil, translationError("checkpoint pair carries empty key")
		}
		out = append(out, semantic.CheckpointPair{
			Baseline:  semantic.CheckpointKey(p.Baseline),
			Candidate: semantic.CheckpointKey(p.Candidate),
		})
	}
	return out, nil
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
