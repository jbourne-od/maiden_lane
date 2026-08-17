package postgres

import (
	"encoding/json"
	"fmt"

	"github.com/optimaldynamics/maiden-lane/internal/ports"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

// This file is the adapter's encoding of an execution.
//
// The stored request is self-describing: it carries the schema declarations
// alongside the pinned state, so a worker can rehydrate it from one row without
// consulting the plans table. That costs a little redundancy and buys a queue
// whose work items stand alone, which is what makes a claim a complete unit of
// work rather than a pointer into other state that may have moved.
//
// As with plans, this encoding requires no trust of its own. The kernel's
// constructors validate everything on the way back in, so a row that decodes
// into something the kernel refuses fails closed rather than producing a
// plausible-looking execution.

// requestDocument is the stored form of a pinned execution input.
type requestDocument struct {
	// The complete lineage identity is stored, not its parts. Re-deriving it
	// from a namespace and root key would risk producing a different one and
	// silently executing an input other than the one that was accepted.
	Lineage    string                         `json:"lineage"`
	Entities   []entityDocument               `json:"entities"`
	Relations  []relationDocument             `json:"relations"`
	Schema     []semantic.EntityDeclaration   `json:"schema_entities"`
	Relations2 []semantic.RelationDeclaration `json:"schema_relations"`
	World      []worldReferenceDocument       `json:"world"`
	Backend    string                         `json:"executor_backend"`
	Version    string                         `json:"executor_version"`
	Policy     uint8                          `json:"provenance_policy"`
}

type entityDocument struct {
	Kind   string                   `json:"kind"`
	ID     string                   `json:"id"`
	Fields map[string]valueDocument `json:"fields"`
}

type relationDocument struct {
	Kind     string `json:"kind"`
	FromKind string `json:"from_kind"`
	FromID   string `json:"from_id"`
	ToKind   string `json:"to_kind"`
	ToID     string `json:"to_id"`
}

// valueDocument stores a typed scalar. The kind is stored beside the payload so
// a string and an atom holding identical bytes stay distinguishable, which they
// must, because they are different values with different identities.
type valueDocument struct {
	Kind  uint8  `json:"kind"`
	Text  string `json:"text,omitempty"`
	Int64 int64  `json:"int64,omitempty"`
}

type worldReferenceDocument struct {
	Kind   uint8  `json:"kind"`
	Digest string `json:"digest"`
}

func encodeExecutionRequest(request ports.ExecutionRequest) ([]byte, error) {
	state := request.Input.InitialState
	declaration := state.Schema().Declaration()

	document := requestDocument{
		Lineage:    string(state.InputLineageID()),
		Schema:     declaration.EntityDeclarations(),
		Relations2: declaration.RelationDeclarations(),
		Backend:    request.Input.ExecutorIdentity.Backend(),
		Version:    string(request.Input.ExecutorIdentity.Version()),
		Policy:     uint8(request.Input.Policy),
	}

	for _, entity := range state.Entities() {
		fields := map[string]valueDocument{}
		for name, value := range entity.Fields() {
			fields[string(name)] = encodeValue(value)
		}
		document.Entities = append(document.Entities, entityDocument{
			Kind:   string(entity.Ref().Kind),
			ID:     string(entity.Ref().ID),
			Fields: fields,
		})
	}
	for _, relation := range state.Relations() {
		document.Relations = append(document.Relations, relationDocument{
			Kind:     string(relation.Kind),
			FromKind: string(relation.From.Kind),
			FromID:   string(relation.From.ID),
			ToKind:   string(relation.To.Kind),
			ToID:     string(relation.To.ID),
		})
	}
	for _, reference := range request.Input.World.References() {
		document.World = append(document.World, worldReferenceDocument{
			Kind:   uint8(reference.Kind()),
			Digest: string(reference.ContentDigest()),
		})
	}

	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("postgres: execution request could not be encoded: %w", err)
	}
	return encoded, nil
}

func decodeExecutionRequest(tenant ports.TenantID, executionID semantic.ExecutionID,
	runID semantic.SemanticRunID, planID semantic.PlanID, encoded []byte) (ports.ExecutionRequest, error) {
	var document requestDocument
	if err := json.Unmarshal(encoded, &document); err != nil {
		return ports.ExecutionRequest{}, fmt.Errorf("%w: stored execution request could not be decoded", ErrIntegrity)
	}

	schema, err := semantic.NewSchema(document.Schema, document.Relations2)
	if err != nil {
		return ports.ExecutionRequest{}, fmt.Errorf("%w: stored schema is invalid", ErrIntegrity)
	}

	entities := make([]semantic.Entity, 0, len(document.Entities))
	for _, stored := range document.Entities {
		fields := make(map[semantic.FieldName]semantic.Value, len(stored.Fields))
		for name, value := range stored.Fields {
			decoded, err := decodeValue(value)
			if err != nil {
				return ports.ExecutionRequest{}, err
			}
			fields[semantic.FieldName(name)] = decoded
		}
		// The identity is stored rather than re-derived from a source key,
		// because the identity is what the run was bound to. Re-deriving would
		// risk producing a different one and silently executing a different
		// input than the one that was accepted.
		entity, err := semantic.NewEntity(semantic.EntityRef{
			Kind: semantic.EntityKind(stored.Kind),
			ID:   semantic.EntityID(stored.ID),
		}, fields)
		if err != nil {
			return ports.ExecutionRequest{}, fmt.Errorf("%w: stored entity is invalid", ErrIntegrity)
		}
		entities = append(entities, entity)
	}

	relations := make([]semantic.Relation, 0, len(document.Relations))
	for _, stored := range document.Relations {
		relations = append(relations, semantic.Relation{
			Kind: semantic.RelationKind(stored.Kind),
			From: semantic.EntityRef{Kind: semantic.EntityKind(stored.FromKind), ID: semantic.EntityID(stored.FromID)},
			To:   semantic.EntityRef{Kind: semantic.EntityKind(stored.ToKind), ID: semantic.EntityID(stored.ToID)},
		})
	}

	state, err := semantic.NewState(schema, semantic.InputLineageID(document.Lineage), entities, relations)
	if err != nil {
		return ports.ExecutionRequest{}, fmt.Errorf("%w: stored state is invalid", ErrIntegrity)
	}

	references := make([]semantic.WorldReference, 0, len(document.World))
	for _, stored := range document.World {
		reference, err := semantic.NewWorldReference(
			semantic.WorldReferenceKind(stored.Kind), semantic.Digest(stored.Digest))
		if err != nil {
			return ports.ExecutionRequest{}, fmt.Errorf("%w: stored world reference is invalid", ErrIntegrity)
		}
		references = append(references, reference)
	}
	world, err := semantic.NewWorld(references)
	if err != nil {
		return ports.ExecutionRequest{}, fmt.Errorf("%w: stored world is invalid", ErrIntegrity)
	}

	executor, err := semantic.NewExecutorIdentity(document.Backend, semantic.Digest(document.Version))
	if err != nil {
		return ports.ExecutionRequest{}, fmt.Errorf("%w: stored executor identity is invalid", ErrIntegrity)
	}

	return ports.ExecutionRequest{
		TenantID:    tenant,
		ExecutionID: executionID,
		RunID:       runID,
		PlanID:      planID,
		Input: ports.ExecutionInput{
			InitialState:     state,
			World:            world,
			ExecutorIdentity: executor,
			Policy:           semantic.ProvenancePolicy(document.Policy),
		},
	}, nil
}

func encodeValue(value semantic.Value) valueDocument {
	document := valueDocument{Kind: uint8(value.Kind())}
	switch value.Kind() {
	case semantic.ValueString, semantic.ValueAtom:
		text, _ := value.String()
		document.Text = text
	case semantic.ValueInt64:
		number, _ := value.Int64()
		document.Int64 = number
	}
	return document
}

func decodeValue(document valueDocument) (semantic.Value, error) {
	switch semantic.ValueKind(document.Kind) {
	case semantic.ValueString:
		return semantic.NewStringValue(document.Text)
	case semantic.ValueAtom:
		return semantic.NewAtomValue(document.Text)
	case semantic.ValueInt64:
		return semantic.NewInt64Value(document.Int64), nil
	default:
		return semantic.Value{}, fmt.Errorf("%w: stored value has an unknown kind", ErrIntegrity)
	}
}

// resultDocument is the stored form of a completed projection. Byte slices are
// carried by encoding/json as base64, so the sealed artifacts round-trip exactly.
type resultDocument struct {
	SpineStatus         string               `json:"spine_status"`
	FinalStateDigest    string               `json:"final_state_digest"`
	JournalPrefixDigest string               `json:"journal_prefix_digest"`
	InputID             string               `json:"input_id"`
	WorldID             string               `json:"world_id"`
	AcceptedRules       []string             `json:"accepted_rules"`
	Checkpoints         []checkpointDocument `json:"checkpoints"`
	Assessments         []assessmentDocument `json:"assessments"`
	FailureKind         string               `json:"failure_kind,omitempty"`
	FailureCode         string               `json:"failure_code,omitempty"`
	HasFailure          bool                 `json:"has_failure"`
}

type checkpointDocument struct {
	CheckpointKey        string `json:"checkpoint_key"`
	CheckpointID         string `json:"checkpoint_id"`
	CheckpointArtifactID string `json:"checkpoint_artifact_id"`
	Digest               string `json:"digest"`
	StateDigest          string `json:"state_digest"`
	CanonicalBytes       []byte `json:"canonical_bytes"`
}

type assessmentDocument struct {
	AssessmentID         string   `json:"assessment_id"`
	Digest               string   `json:"digest"`
	CheckpointArtifactID string   `json:"checkpoint_artifact_id"`
	ProfileID            string   `json:"profile_id"`
	ProfileKey           string   `json:"profile_key"`
	Verdict              string   `json:"verdict"`
	MissingRequirements  []string `json:"missing_requirements"`
	CanonicalBytes       []byte   `json:"canonical_bytes"`
}

func storedResult(result ports.ExecutionResult) resultDocument {
	document := resultDocument{
		SpineStatus:         result.SpineStatus,
		FinalStateDigest:    string(result.FinalStateDigest),
		JournalPrefixDigest: string(result.JournalPrefixDigest),
		InputID:             string(result.InputID),
		WorldID:             string(result.WorldID),
	}
	for _, rule := range result.AcceptedRules {
		document.AcceptedRules = append(document.AcceptedRules, string(rule))
	}
	for _, checkpoint := range result.Checkpoints {
		document.Checkpoints = append(document.Checkpoints, checkpointDocument{
			CheckpointKey:        string(checkpoint.CheckpointKey),
			CheckpointID:         string(checkpoint.CheckpointID),
			CheckpointArtifactID: string(checkpoint.CheckpointArtifactID),
			Digest:               string(checkpoint.Digest),
			StateDigest:          string(checkpoint.StateDigest),
			CanonicalBytes:       checkpoint.CanonicalBytes,
		})
	}
	for _, assessment := range result.Assessments {
		stored := assessmentDocument{
			AssessmentID:         string(assessment.AssessmentID),
			Digest:               string(assessment.Digest),
			CheckpointArtifactID: string(assessment.CheckpointArtifactID),
			ProfileID:            string(assessment.ProfileID),
			ProfileKey:           string(assessment.ProfileKey),
			Verdict:              string(assessment.Verdict),
			CanonicalBytes:       assessment.CanonicalBytes,
		}
		for _, code := range assessment.MissingRequirements {
			stored.MissingRequirements = append(stored.MissingRequirements, string(code))
		}
		document.Assessments = append(document.Assessments, stored)
	}
	if result.Failure != nil {
		document.HasFailure = true
		document.FailureKind = string(result.Failure.Kind)
		document.FailureCode = result.Failure.Code
	}
	return document
}

func (d resultDocument) toResult(tenant ports.TenantID, executionID semantic.ExecutionID, status ports.ExecutionStatus) ports.ExecutionResult {
	result := ports.ExecutionResult{
		TenantID:            tenant,
		ExecutionID:         executionID,
		Status:              status,
		SpineStatus:         d.SpineStatus,
		FinalStateDigest:    semantic.StateDigest(d.FinalStateDigest),
		JournalPrefixDigest: semantic.JournalPrefixDigest(d.JournalPrefixDigest),
		InputID:             semantic.InputID(d.InputID),
		WorldID:             semantic.WorldID(d.WorldID),
	}
	for _, rule := range d.AcceptedRules {
		result.AcceptedRules = append(result.AcceptedRules, semantic.RuleID(rule))
	}
	for _, stored := range d.Checkpoints {
		result.Checkpoints = append(result.Checkpoints, ports.SealedCheckpoint{
			CheckpointKey:        semantic.CheckpointKey(stored.CheckpointKey),
			CheckpointID:         semantic.CheckpointID(stored.CheckpointID),
			CheckpointArtifactID: semantic.CheckpointArtifactID(stored.CheckpointArtifactID),
			Digest:               semantic.CheckpointArtifactDigest(stored.Digest),
			StateDigest:          semantic.StateDigest(stored.StateDigest),
			CanonicalBytes:       stored.CanonicalBytes,
		})
	}
	for _, stored := range d.Assessments {
		assessment := ports.StoredAssessment{
			AssessmentID:         semantic.AssessmentID(stored.AssessmentID),
			Digest:               semantic.AssessmentDigest(stored.Digest),
			CheckpointArtifactID: semantic.CheckpointArtifactID(stored.CheckpointArtifactID),
			ProfileID:            semantic.ProfileID(stored.ProfileID),
			ProfileKey:           semantic.ProfileKey(stored.ProfileKey),
			Verdict:              semantic.ReadinessVerdict(stored.Verdict),
			CanonicalBytes:       stored.CanonicalBytes,
		}
		for _, code := range stored.MissingRequirements {
			assessment.MissingRequirements = append(assessment.MissingRequirements, semantic.RequirementCode(code))
		}
		result.Assessments = append(result.Assessments, assessment)
	}
	if d.HasFailure {
		result.Failure = &ports.StoredFailure{
			Kind: semantic.FailureKind(d.FailureKind),
			Code: d.FailureCode,
		}
	}
	return result
}
