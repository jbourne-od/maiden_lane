package semantic

import (
	"bytes"
	"fmt"
)

// ProvenancePolicy is the closed v1 provenance contract.
type ProvenancePolicy uint8

const ChangesProvenance ProvenancePolicy = 1

// RunBindingRequest supplies every semantic and execution identity input.
type RunBindingRequest struct {
	Plan             Plan
	InitialState     State
	World            World
	ExecutorIdentity ExecutorIdentity
	Policy           ProvenancePolicy
}

// RunBinding is a verified immutable execution contract.
type RunBinding struct {
	plan               Plan
	initialState       State
	world              World
	initialStateDigest StateDigest
	worldID            WorldID
	inputID            InputID
	semanticRunID      SemanticRunID
	policy             ProvenancePolicy
	policyID           ProvenancePolicyID
	executor           ExecutorIdentity
	executionID        ExecutionID
}

func (b RunBinding) Plan() Plan                             { return clonePlan(b.plan) }
func (b RunBinding) InitialStateDigest() StateDigest        { return b.initialStateDigest }
func (b RunBinding) WorldID() WorldID                       { return b.worldID }
func (b RunBinding) InputID() InputID                       { return b.inputID }
func (b RunBinding) SemanticRunID() SemanticRunID           { return b.semanticRunID }
func (b RunBinding) ProvenancePolicy() ProvenancePolicy     { return b.policy }
func (b RunBinding) ProvenancePolicyID() ProvenancePolicyID { return b.policyID }
func (b RunBinding) ExecutorIdentity() ExecutorIdentity     { return b.executor }
func (b RunBinding) ExecutionID() ExecutionID               { return b.executionID }

// BindRun verifies every supplied artifact before deriving any run identity.
func BindRun(request RunBindingRequest) (RunBinding, error) {
	if err := verifyPlan(request.Plan); err != nil {
		return RunBinding{}, fmt.Errorf("bind plan: %w", err)
	}
	if err := verifyState(request.InitialState); err != nil {
		return RunBinding{}, fmt.Errorf("bind initial state: %w", err)
	}
	if err := verifyWorld(request.World); err != nil {
		return RunBinding{}, fmt.Errorf("bind world: %w", err)
	}
	if request.InitialState.Schema().Digest() != request.Plan.SchemaDigest() {
		return RunBinding{}, fmt.Errorf("bind run: plan and initial state schema differ")
	}
	if !validExecutorIdentity(request.ExecutorIdentity) {
		return RunBinding{}, fmt.Errorf("bind run: unsupported executor identity")
	}
	if request.Policy != ChangesProvenance {
		return RunBinding{}, fmt.Errorf("bind run: unsupported provenance policy %d", request.Policy)
	}
	policyBytes, err := encodeProvenancePolicy(request.Policy)
	if err != nil {
		return RunBinding{}, err
	}
	policyID := ProvenancePolicyID(canonicalDigest(policyBytes))
	inputBytes, err := encodeInputIdentity(request.InitialState.Digest(), request.World.ID())
	if err != nil {
		return RunBinding{}, err
	}
	inputID := InputID(canonicalDigest(inputBytes))
	runBytes, err := encodeSemanticRunIdentity(inputID, request.Plan.ID())
	if err != nil {
		return RunBinding{}, err
	}
	runID := SemanticRunID(canonicalDigest(runBytes))
	executionBytes, err := encodeExecutionIdentity(runID, request.ExecutorIdentity, policyID)
	if err != nil {
		return RunBinding{}, err
	}
	return RunBinding{plan: clonePlan(request.Plan), initialState: request.InitialState, world: request.World,
		initialStateDigest: request.InitialState.Digest(), worldID: request.World.ID(), inputID: inputID,
		semanticRunID: runID, policy: request.Policy, policyID: policyID, executor: request.ExecutorIdentity,
		executionID: ExecutionID(canonicalDigest(executionBytes))}, nil
}

func validExecutorIdentity(identity ExecutorIdentity) bool {
	if !validExecutorBackend(identity.backend) {
		return false
	}
	_, err := decodeDigest(string(identity.version))
	return err == nil
}

func verifyPlan(plan Plan) error {
	if len(plan.canonical) == 0 {
		return fmt.Errorf("plan is not initialized")
	}
	canonical, err := encodePlan(plan.schemaDigest, plan.rulesetDigest, plan.compilerVersion, plan.transformations, plan.checkpoints)
	if err != nil {
		return err
	}
	if !bytes.Equal(canonical, plan.canonical) || PlanID(canonicalDigest(canonical)) != plan.id {
		return fmt.Errorf("plan canonical identity mismatch")
	}
	return nil
}

func verifyState(state State) error {
	if len(state.canonical) == 0 {
		return fmt.Errorf("state is not initialized")
	}
	if err := verifySchema(state.schema); err != nil {
		return fmt.Errorf("state schema: %w", err)
	}
	canonical, err := encodeState(state)
	if err != nil {
		return err
	}
	if !bytes.Equal(canonical, state.canonical) || StateDigest(canonicalDigest(canonical)) != state.digest {
		return fmt.Errorf("state canonical identity mismatch")
	}
	return nil
}

func verifySchema(schema Schema) error {
	if len(schema.canonical) == 0 {
		return fmt.Errorf("schema is not initialized")
	}
	canonical, err := encodeSchema(schema.declaration)
	if err != nil {
		return err
	}
	if !bytes.Equal(canonical, schema.canonical) || SchemaDigest(canonicalDigest(canonical)) != schema.digest {
		return fmt.Errorf("schema canonical identity mismatch")
	}
	return nil
}

func verifyWorld(world World) error {
	rebuilt, err := NewWorld(world.references)
	if err != nil {
		return err
	}
	if !bytes.Equal(rebuilt.canonical, world.canonical) || rebuilt.id != world.id {
		return fmt.Errorf("world canonical identity mismatch")
	}
	return nil
}

func verifyBinding(binding RunBinding) error {
	if !validExecutorIdentity(binding.executor) || binding.policy != ChangesProvenance {
		return fmt.Errorf("run binding is not initialized")
	}
	if err := verifyPlan(binding.plan); err != nil {
		return err
	}
	if err := verifyState(binding.initialState); err != nil {
		return err
	}
	if err := verifyWorld(binding.world); err != nil {
		return err
	}
	if binding.initialState.Digest() != binding.initialStateDigest || binding.world.ID() != binding.worldID || binding.initialState.Schema().Digest() != binding.plan.SchemaDigest() {
		return fmt.Errorf("run binding artifact links are inconsistent")
	}
	policyBytes, err := encodeProvenancePolicy(binding.policy)
	if err != nil {
		return err
	}
	policyID := ProvenancePolicyID(canonicalDigest(policyBytes))
	inputBytes, err := encodeInputIdentity(binding.initialStateDigest, binding.worldID)
	if err != nil {
		return err
	}
	inputID := InputID(canonicalDigest(inputBytes))
	runBytes, err := encodeSemanticRunIdentity(inputID, binding.plan.ID())
	if err != nil {
		return err
	}
	runID := SemanticRunID(canonicalDigest(runBytes))
	executionBytes, err := encodeExecutionIdentity(runID, binding.executor, policyID)
	if err != nil {
		return err
	}
	if binding.policyID != policyID || binding.inputID != inputID || binding.semanticRunID != runID || binding.executionID != ExecutionID(canonicalDigest(executionBytes)) {
		return fmt.Errorf("run binding layered identity mismatch")
	}
	return nil
}
