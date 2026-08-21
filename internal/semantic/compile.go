package semantic

import (
	"bytes"
	"fmt"
	"slices"
	"sort"
	"strings"
)

// CompilationDiagnosticCode is the ratified closed compiler failure set.
type CompilationDiagnosticCode string

const (
	UnknownField            CompilationDiagnosticCode = "UNKNOWN_FIELD"
	UnsupportedOperator     CompilationDiagnosticCode = "UNSUPPORTED_OPERATOR"
	DeclaredAccessMismatch  CompilationDiagnosticCode = "DECLARED_ACCESS_MISMATCH"
	WriteConflictUnresolved CompilationDiagnosticCode = "WRITE_CONFLICT_UNRESOLVED"
	DependencyCycle         CompilationDiagnosticCode = "DEPENDENCY_CYCLE"
	ProfileOrderUnprovable  CompilationDiagnosticCode = "PROFILE_ORDER_UNPROVABLE"
)

// FailureKind distinguishes invalid compilation from later protected or
// artifact-integrity rejection without collapsing their typed payloads.
type FailureKind string

const (
	InvalidPlan              FailureKind = "INVALID_PLAN"
	ProtectedInvariantFailed FailureKind = "PROTECTED_INVARIANT_FAILED"
	ArtifactIntegrityFailed  FailureKind = "ARTIFACT_INTEGRITY_FAILED"
)

// CompilationDiagnostic identifies a stable code and bounded semantic
// declaration keys. Human prose is deliberately excluded from canonical form.
type CompilationDiagnostic struct {
	code    CompilationDiagnosticCode
	subject string
	detail  string
}

// Code returns the stable closed diagnostic code.
func (d CompilationDiagnostic) Code() CompilationDiagnosticCode { return d.code }

// Subject returns the canonical rule, profile, or checkpoint key implicated.
func (d CompilationDiagnostic) Subject() string { return d.subject }

// Detail returns a bounded typed declaration key such as a field path.
func (d CompilationDiagnostic) Detail() string { return d.detail }

// Compilation is either one accepted plan and its complete compiled profile
// set, or one canonical failure. It never exposes partial accepted artifacts.
type Compilation struct {
	input       CompilationInput
	inputDigest CompilationInputDigest
	plan        *Plan
	profiles    []CompiledProfile
	failure     *CompilationFailure
}

// InputDigest identifies the complete canonicalizable compiler request.
func (c Compilation) InputDigest() CompilationInputDigest { return c.inputDigest }

// Input returns the immutable input that produced this compilation.
//
// It exists so a durable store can retain what is needed to reproduce a
// compilation. A Compilation itself cannot be persisted: its fields are
// private and Compile is the only way to obtain one, and the canonical
// encoders here are deliberately one-way, so there is no decoder to rehydrate
// with. An adapter therefore persists this input in whatever form it likes,
// recompiles on read, and requires the resulting identities to equal the ones
// it stored. A corrupted or silently re-encoded row cannot satisfy that, so
// storage is structurally unable to lie about semantic identity.
//
// A bare CompileRequest deliberately is not used for that purpose: it is an
// ordinary authoring structure of exported slices and pointers, so holding one
// hands every caller a mutable alias. This value's interior is unreachable.
func (c Compilation) Input() CompilationInput { return c.input }

// CompilationInput is the immutable, persistable input of one compilation.
type CompilationInput struct {
	request   CompileRequest
	digest    CompilationInputDigest
	canonical []byte
}

// Request returns a deep copy. The copy is made here, in the package that
// defines the declarations, so a field added to one is copied where it is
// declared rather than in an adapter that cannot see it.
func (i CompilationInput) Request() CompileRequest { return cloneCompileRequest(i.request) }

// Digest returns the canonical identity of this input. It equals the
// InputDigest of the compilation that produced it.
func (i CompilationInput) Digest() CompilationInputDigest { return i.digest }

// CanonicalBytes returns a defensive copy of the canonical input bytes.
func (i CompilationInput) CanonicalBytes() []byte { return bytes.Clone(i.canonical) }

// cloneCompileRequest deep-copies every part of an authoring request whose
// interior is reachable through an exported field.
func cloneCompileRequest(input CompileRequest) CompileRequest {
	transformations := make([]TransformationDeclaration, len(input.Rules.Transformations))
	for i, transformation := range input.Rules.Transformations {
		transformations[i] = cloneTransformation(transformation)
	}
	profiles := make([]ProfileDeclaration, len(input.Profiles))
	for i, profile := range input.Profiles {
		profiles[i] = cloneProfile(profile)
	}
	return CompileRequest{
		Schema: SchemaDeclaration{
			entities:  cloneEntityDeclarations(input.Schema.entities),
			relations: slices.Clone(input.Schema.relations),
		},
		Rules: RulesetDeclaration{
			Transformations: transformations,
			Checkpoints:     slices.Clone(input.Rules.Checkpoints),
		},
		Profiles:                 profiles,
		CompilerSemanticsVersion: input.CompilerSemanticsVersion,
	}
}

// Plan returns the immutable accepted plan, if compilation succeeded.
func (c Compilation) Plan() (Plan, bool) {
	if c.plan == nil {
		return Plan{}, false
	}
	return clonePlan(*c.plan), true
}

// Profiles returns defensive copies only for a fully accepted compilation.
func (c Compilation) Profiles() []CompiledProfile {
	if c.plan == nil {
		return nil
	}
	return cloneCompiledProfiles(c.profiles)
}

// Failure returns the canonical failure, if compilation rejected the request.
func (c Compilation) Failure() (CompilationFailure, bool) {
	if c.failure == nil {
		return CompilationFailure{}, false
	}
	return cloneCompilationFailure(*c.failure), true
}

// Plan is the immutable backend-independent execution contract.
type Plan struct {
	schemaDigest    SchemaDigest
	rulesetDigest   RulesetDigest
	compilerVersion CompilerSemanticsVersion
	transformations []CompiledTransformation
	checkpoints     []CheckpointDeclaration
	canonical       []byte
	id              PlanID
}

// ID returns the content identity of the canonical compiled plan.
func (p Plan) ID() PlanID { return p.id }

// SchemaDigest returns the schema identity pinned by this plan.
func (p Plan) SchemaDigest() SchemaDigest { return p.schemaDigest }

// RulesetDigest returns the normalized source-rule identity pinned by this plan.
func (p Plan) RulesetDigest() RulesetDigest { return p.rulesetDigest }

// CompilerVersion returns the exact semantic compiler version.
func (p Plan) CompilerVersion() CompilerSemanticsVersion { return p.compilerVersion }

// CanonicalBytes returns a defensive copy of the v1 plan bytes.
func (p Plan) CanonicalBytes() []byte { return bytes.Clone(p.canonical) }

// Transformations returns deep copies in canonical dependency order.
func (p Plan) Transformations() []CompiledTransformation {
	result := make([]CompiledTransformation, len(p.transformations))
	for i, transformation := range p.transformations {
		result[i] = cloneCompiledTransformation(transformation)
	}
	return result
}

// Checkpoints returns declarations ordered by prefix and then semantic key.
func (p Plan) Checkpoints() []CheckpointDeclaration { return slices.Clone(p.checkpoints) }

// CheckpointID derives the identity of a checkpoint this plan declares, and reports
// whether the plan declares it at all.
//
// It is a forward derivation, not a lookup table: the identity is recomputed from this
// plan's identity and the declared key every time. That keeps the kernel's one-way rule
// intact, because nothing here turns a CheckpointID back into anything.
//
// The undeclared case is reported rather than derived. A CheckpointID can be computed
// for any (plan, key) pair, including a key the plan never declares, and such an
// identity would look entirely well formed while naming a checkpoint that cannot be
// realized. Refusing to produce one is what makes "this plan declares that checkpoint"
// a statement a caller can obtain rather than assume.
func (p Plan) CheckpointID(key CheckpointKey) (CheckpointID, bool) {
	if !slices.ContainsFunc(p.checkpoints, func(declared CheckpointDeclaration) bool {
		return declared.Key == key
	}) {
		return "", false
	}
	identity, err := checkpointIdentity(p.id, key)
	if err != nil {
		// Unreachable for a declared key: compilation already accepted it, and the
		// encoder refuses only what compilation would have refused first.
		return "", false
	}
	return identity, true
}

// MustTransformation returns an accepted transformation or panics when the
// caller names a rule outside this immutable plan.
func (p Plan) MustTransformation(id RuleID) CompiledTransformation {
	for _, transformation := range p.transformations {
		if transformation.declaration.ID == id {
			return cloneCompiledTransformation(transformation)
		}
	}
	panic(fmt.Sprintf("semantic plan has no transformation %q", id))
}

// CompiledTransformation contains normalized source meaning plus every
// compiler-derived access, dependency, invariant, and stable execution level.
type CompiledTransformation struct {
	declaration TransformationDeclaration
	// selector is the compiled form of a SelectAssign payload's selector, and is the zero
	// value for every other operator. It is compiled HERE rather than at execution because
	// Select refuses a selector it was not handed compiled, and re-checking at execution
	// would make the executor a second compiler -- the shape decision 3 refuses for read/
	// write derivation, for the same reason.
	selector     CompiledSelector
	reads        []FieldPath
	writes       []FieldPath
	accesses     []SemanticAccess
	dependencies []RuleID
	invariants   []InvariantDeclaration
	level        uint64
}

// Declaration returns a deep copy of the normalized closed declaration.
func (t CompiledTransformation) Declaration() TransformationDeclaration {
	return cloneTransformation(t.declaration)
}

// Operator returns the closed operator tag.
func (t CompiledTransformation) Operator() OperatorKind { return t.declaration.Operator }

// ReadSet returns the canonical field-read set.
func (t CompiledTransformation) ReadSet() []FieldPath { return slices.Clone(t.reads) }

// WriteSet returns the canonical field-write set.
func (t CompiledTransformation) WriteSet() []FieldPath { return slices.Clone(t.writes) }

// Reads reports whether the compiler derived one exact field dependency.
func (t CompiledTransformation) Reads(path FieldPath) bool {
	_, ok := slices.BinarySearch(t.reads, path)
	return ok
}

// Writes reports whether the compiler derived one exact field output.
func (t CompiledTransformation) Writes(path FieldPath) bool {
	_, ok := slices.BinarySearch(t.writes, path)
	return ok
}

// Accesses returns canonical entity/relation/field accesses.
func (t CompiledTransformation) Accesses() []SemanticAccess { return slices.Clone(t.accesses) }

// Dependencies returns canonical immediate predecessor rule IDs.
func (t CompiledTransformation) Dependencies() []RuleID { return slices.Clone(t.dependencies) }

// Invariants returns defensive copies of derived protected obligations.
func (t CompiledTransformation) Invariants() []InvariantDeclaration {
	result := make([]InvariantDeclaration, len(t.invariants))
	for i, invariant := range t.invariants {
		result[i] = cloneInvariant(invariant)
	}
	return result
}

// Level returns the stable zero-based topological execution level.
func (t CompiledTransformation) Level() uint64 { return t.level }

// ProfileProofKind is the only implication proof admitted by this slice.
type ProfileProofKind uint8

const ProfileProofRequirementSetContainment ProfileProofKind = 1

// ProfileImplication records one compiler-proved readiness implication.
type ProfileImplication struct {
	target ProfileKey
	kind   ProfileProofKind
}

// Target returns the implied profile's canonical declaration key.
func (i ProfileImplication) Target() ProfileKey { return i.target }

// Kind returns the closed mechanical proof kind.
func (i ProfileImplication) Kind() ProfileProofKind { return i.kind }

// CompiledProfile is one immutable, schema-pinned readiness contract.
type CompiledProfile struct {
	declaration     ProfileDeclaration
	schemaDigest    SchemaDigest
	compilerVersion CompilerSemanticsVersion
	proofs          []ProfileImplication
	canonical       []byte
	id              ProfileID
}

// Key returns the profile declaration key.
func (p CompiledProfile) Key() ProfileKey { return p.declaration.Key }

// ID returns the content identity of the compiled profile.
func (p CompiledProfile) ID() ProfileID { return p.id }

// Declaration returns a defensive copy of the normalized profile declaration.
func (p CompiledProfile) Declaration() ProfileDeclaration { return cloneProfile(p.declaration) }

// CanonicalBytes returns a defensive copy of the v1 compiled-profile bytes.
func (p CompiledProfile) CanonicalBytes() []byte { return bytes.Clone(p.canonical) }

// Proofs returns the canonical implication proofs.
func (p CompiledProfile) Proofs() []ProfileImplication { return slices.Clone(p.proofs) }

// CompilationFailure is the immutable diagnostic answer for one invalid
// canonicalizable compiler request.
type CompilationFailure struct {
	inputDigest CompilationInputDigest
	diagnostics []CompilationDiagnostic
	canonical   []byte
	digest      CompilationFailureDigest
}

// Kind returns INVALID_PLAN for every compilation-failure value.
func (f CompilationFailure) Kind() FailureKind { return InvalidPlan }

// InputDigest returns the identity of the rejected compiler request.
func (f CompilationFailure) InputDigest() CompilationInputDigest { return f.inputDigest }

// Diagnostics returns the complete canonical diagnostic set.
func (f CompilationFailure) Diagnostics() []CompilationDiagnostic { return slices.Clone(f.diagnostics) }

// CanonicalBytes returns a defensive copy of the failure bytes.
func (f CompilationFailure) CanonicalBytes() []byte { return bytes.Clone(f.canonical) }

// Digest returns the content identity of this diagnostic answer.
func (f CompilationFailure) Digest() CompilationFailureDigest { return f.digest }

type normalizedRuleset struct {
	transformations []TransformationDeclaration
	checkpoints     []CheckpointDeclaration
}

// Compile canonicalizes the full request before semantic validation, then
// returns either one complete accepted program or one canonical failure.
func Compile(request CompileRequest) (Compilation, error) {
	if !validSemanticName(string(request.CompilerSemanticsVersion)) {
		return Compilation{}, fmt.Errorf("compiler semantics version is empty or invalid UTF-8")
	}
	schema, err := NewSchema(request.Schema.EntityDeclarations(), request.Schema.RelationDeclarations())
	if err != nil {
		return Compilation{}, fmt.Errorf("compile schema: %w", err)
	}
	rules, err := normalizeRuleset(request.Rules)
	if err != nil {
		return Compilation{}, fmt.Errorf("canonicalize ruleset: %w", err)
	}
	profiles, err := normalizeProfiles(request.Profiles)
	if err != nil {
		return Compilation{}, fmt.Errorf("canonicalize profiles: %w", err)
	}
	rulesBytes, err := encodeRuleset(rules)
	if err != nil {
		return Compilation{}, fmt.Errorf("canonicalize ruleset: %w", err)
	}
	rulesDigest := RulesetDigest(canonicalDigest(rulesBytes))
	inputBytes, err := encodeCompilationInput(schema.Digest(), rulesDigest, profiles, request.CompilerSemanticsVersion)
	if err != nil {
		return Compilation{}, fmt.Errorf("canonicalize compiler input: %w", err)
	}
	inputDigest := CompilationInputDigest(canonicalDigest(inputBytes))
	// The retained input is cloned on the way in as well as on the way out, so
	// a caller mutating the request it passed cannot reach the compilation.
	retained := CompilationInput{request: cloneCompileRequest(request), digest: inputDigest,
		canonical: bytes.Clone(inputBytes)}

	diagnostics := make([]CompilationDiagnostic, 0)
	compiledByID := make(map[RuleID]CompiledTransformation, len(rules.transformations))
	for _, declaration := range rules.transformations {
		compiled, derivedDiagnostics := deriveTransformation(schema, request.CompilerSemanticsVersion, declaration)
		diagnostics = append(diagnostics, derivedDiagnostics...)
		compiledByID[declaration.ID] = compiled
	}
	diagnostics = append(diagnostics, resolveDependencies(schema, rules.transformations, compiledByID)...)
	diagnostics = append(diagnostics, validateCheckpointBoundaries(rules.checkpoints, compiledByID)...)
	ordered, cycle := topologicalOrder(compiledByID)
	if cycle {
		diagnostics = append(diagnostics, diagnostic(DependencyCycle, "ruleset", "dependency_graph"))
	}
	compiledProfiles, profileDiagnostics := compileProfiles(schema, profiles, request.CompilerSemanticsVersion)
	diagnostics = append(diagnostics, profileDiagnostics...)
	diagnostics = canonicalDiagnostics(diagnostics)
	if len(diagnostics) > 0 {
		failureBytes, encodeErr := encodeCompilationFailure(inputDigest, diagnostics)
		if encodeErr != nil {
			return Compilation{}, fmt.Errorf("canonicalize compilation failure: %w", encodeErr)
		}
		failure := CompilationFailure{inputDigest: inputDigest, diagnostics: diagnostics, canonical: failureBytes,
			digest: CompilationFailureDigest(canonicalDigest(failureBytes))}
		return Compilation{input: retained, inputDigest: inputDigest, failure: &failure}, nil
	}

	checkpoints := orderCheckpoints(rules.checkpoints, ordered)
	planBytes, err := encodePlan(schema.Digest(), rulesDigest, request.CompilerSemanticsVersion, ordered, checkpoints)
	if err != nil {
		return Compilation{}, fmt.Errorf("canonicalize plan: %w", err)
	}
	plan := Plan{schemaDigest: schema.Digest(), rulesetDigest: rulesDigest, compilerVersion: request.CompilerSemanticsVersion,
		transformations: ordered, checkpoints: checkpoints, canonical: planBytes, id: PlanID(canonicalDigest(planBytes))}
	return Compilation{input: retained, inputDigest: inputDigest, plan: &plan, profiles: compiledProfiles}, nil
}

func validateCheckpointBoundaries(checkpoints []CheckpointDeclaration, compiled map[RuleID]CompiledTransformation) []CompilationDiagnostic {
	diagnostics := make([]CompilationDiagnostic, 0)
	for _, checkpoint := range checkpoints {
		if _, ok := compiled[checkpoint.After]; !ok {
			diagnostics = append(diagnostics, diagnostic(UnsupportedOperator, string(checkpoint.Key), "checkpoint_after:"+string(checkpoint.After)))
		}
	}
	return diagnostics
}

func normalizeRuleset(input RulesetDeclaration) (normalizedRuleset, error) {
	transformations := make([]TransformationDeclaration, len(input.Transformations))
	for i, raw := range input.Transformations {
		if !validSemanticName(string(raw.ID)) {
			return normalizedRuleset{}, fmt.Errorf("rule ID is empty or invalid UTF-8")
		}
		// BEFORE cloneTransformation, which recurses over the authored expression trees.
		if err := checkAuthoredPayloadBounds(raw); err != nil {
			return normalizedRuleset{}, fmt.Errorf("rule %q: %w", raw.ID, err)
		}
		transformation := cloneTransformation(raw)
		var err error
		if transformation.DeclaredReads, err = normalizeFieldSet(transformation.DeclaredReads); err != nil {
			return normalizedRuleset{}, fmt.Errorf("rule %q declared reads: %w", raw.ID, err)
		}
		if transformation.DeclaredWrites, err = normalizeFieldSet(transformation.DeclaredWrites); err != nil {
			return normalizedRuleset{}, fmt.Errorf("rule %q declared writes: %w", raw.ID, err)
		}
		if transformation.After, err = normalizeRuleSet(transformation.After); err != nil {
			return normalizedRuleset{}, fmt.Errorf("rule %q dependencies: %w", raw.ID, err)
		}
		if err := normalizeTransformationPayload(&transformation); err != nil {
			return normalizedRuleset{}, fmt.Errorf("rule %q: %w", raw.ID, err)
		}
		transformations[i] = transformation
	}
	sort.Slice(transformations, func(i, j int) bool { return transformations[i].ID < transformations[j].ID })
	for i := 1; i < len(transformations); i++ {
		if transformations[i-1].ID == transformations[i].ID {
			return normalizedRuleset{}, fmt.Errorf("duplicate rule ID %q", transformations[i].ID)
		}
	}
	checkpoints := slices.Clone(input.Checkpoints)
	for _, checkpoint := range checkpoints {
		if !validSemanticName(string(checkpoint.Key)) || !validSemanticName(string(checkpoint.After)) {
			return normalizedRuleset{}, fmt.Errorf("checkpoint contains an empty or invalid UTF-8 key")
		}
	}
	sort.Slice(checkpoints, func(i, j int) bool {
		if checkpoints[i].Key != checkpoints[j].Key {
			return checkpoints[i].Key < checkpoints[j].Key
		}
		return checkpoints[i].After < checkpoints[j].After
	})
	for i := 1; i < len(checkpoints); i++ {
		if checkpoints[i-1].Key == checkpoints[i].Key {
			return normalizedRuleset{}, fmt.Errorf("duplicate checkpoint key %q", checkpoints[i].Key)
		}
	}
	return normalizedRuleset{transformations: transformations, checkpoints: checkpoints}, nil
}

// checkAuthoredPayloadBounds bounds every authored expression tree a declaration carries,
// before anything walks one recursively.
//
// It reads the caller's declaration rather than a clone, deliberately: cloning is the first
// recursion, so a check that ran after it would already have overflowed.
func checkAuthoredPayloadBounds(raw TransformationDeclaration) error {
	payload := raw.SelectAssign
	if payload == nil {
		// The frozen operators carry no expressions. When one gains them, it comes here.
		return nil
	}
	trees := make([]Expr, 0, 3+len(payload.Assignments))
	if payload.Selector.Where != nil {
		trees = append(trees, *payload.Selector.Where)
	}
	if payload.Selector.GroupBy != nil {
		trees = append(trees, *payload.Selector.GroupBy)
	}
	trees = append(trees, payload.Guard)
	for _, assignment := range payload.Assignments {
		trees = append(trees, assignment.Value)
	}
	for _, tree := range trees {
		if err := checkAuthoredExprBound(tree); err != nil {
			return err
		}
	}
	return nil
}

func normalizeTransformationPayload(transformation *TransformationDeclaration) error {
	if payload := transformation.SelectAssign; payload != nil {
		// AUTHORED ORDER IS NOT SEMANTIC HERE, and leaving it unnormalized put it in the
		// ruleset identity. Every value is evaluated against the same pre-state member, no
		// assignment observes another's result, duplicate targets are refused, and NewPatch
		// sorts an update's fields by name -- so two rulesets listing the same assignments in
		// different order produce byte-identical patches, journals and states, and differed
		// only in their PlanID and therefore their SemanticRunID.
		//
		// This is the arm that was missing: every other authored payload list in this
		// function is sorted, and RulesetDeclaration's own doc says authored order is not
		// semantic. Duplicate detection stays in deriveTransformation, where it is an
		// attributable diagnostic rather than an error.
		sort.Slice(payload.Assignments, func(i, j int) bool {
			return payload.Assignments[i].Target < payload.Assignments[j].Target
		})
	}
	if form := transformation.Form; form != nil {
		sort.Slice(form.Sources, func(i, j int) bool {
			if form.Sources[i].Kind != form.Sources[j].Kind {
				return form.Sources[i].Kind < form.Sources[j].Kind
			}
			return form.Sources[i].CanonicalSourceKey < form.Sources[j].CanonicalSourceKey
		})
		for i, source := range form.Sources {
			if !validSemanticName(string(source.Kind)) || !validSemanticName(source.CanonicalSourceKey) {
				return fmt.Errorf("source reference is empty or invalid UTF-8")
			}
			if i > 0 && source == form.Sources[i-1] {
				return fmt.Errorf("duplicate source reference")
			}
		}
		sort.Slice(form.CopiedFields, func(i, j int) bool { return compareFieldCopy(form.CopiedFields[i], form.CopiedFields[j]) < 0 })
		copyDestinations := make(map[FieldPath]struct{}, len(form.CopiedFields))
		for i := 1; i < len(form.CopiedFields); i++ {
			if form.CopiedFields[i] == form.CopiedFields[i-1] {
				return fmt.Errorf("duplicate copied field")
			}
		}
		for _, copied := range form.CopiedFields {
			if _, exists := copyDestinations[copied.Destination]; exists {
				return fmt.Errorf("multiple copied fields write destination %q", copied.Destination)
			}
			copyDestinations[copied.Destination] = struct{}{}
		}
	}
	if aggregate := transformation.Aggregate; aggregate != nil {
		var err error
		if aggregate.RequiredSourceTuple, err = normalizeFieldSet(aggregate.RequiredSourceTuple); err != nil {
			return fmt.Errorf("required source tuple: %w", err)
		}
		if err := normalizePredicates(aggregate.Predicates); err != nil {
			return err
		}
		if err := normalizePredicates(aggregate.ResultPredicates); err != nil {
			return err
		}
		sort.Slice(aggregate.Predicates, func(i, j int) bool {
			return predicateKey(aggregate.Predicates[i]) < predicateKey(aggregate.Predicates[j])
		})
		sort.Slice(aggregate.ResultPredicates, func(i, j int) bool {
			return predicateKey(aggregate.ResultPredicates[i]) < predicateKey(aggregate.ResultPredicates[j])
		})
		if duplicatePredicate(aggregate.Predicates) || duplicatePredicate(aggregate.ResultPredicates) {
			return fmt.Errorf("duplicate normalized aggregate predicate")
		}
		sort.Slice(aggregate.Reductions, func(i, j int) bool {
			return reductionKey(aggregate.Reductions[i]) < reductionKey(aggregate.Reductions[j])
		})
		reductionDestinations := map[FieldPath]struct{}{aggregate.Anchor.Destination: {}}
		for i, reduction := range aggregate.Reductions {
			if i > 0 && reductionKey(reduction) == reductionKey(aggregate.Reductions[i-1]) {
				return fmt.Errorf("duplicate normalized reduction")
			}
			if _, exists := reductionDestinations[reduction.Destination]; exists {
				return fmt.Errorf("multiple aggregate writers target destination %q", reduction.Destination)
			}
			reductionDestinations[reduction.Destination] = struct{}{}
		}
	}
	return nil
}

func duplicatePredicate(predicates []AggregatePredicate) bool {
	for i := 1; i < len(predicates); i++ {
		if predicateKey(predicates[i]) == predicateKey(predicates[i-1]) {
			return true
		}
	}
	return false
}

func normalizePredicates(predicates []AggregatePredicate) error {
	for i := range predicates {
		if predicates[i].Kind == LessOrEqualFields {
			fields := slices.Clone(predicates[i].Fields)
			seen := make(map[FieldPath]struct{}, len(fields))
			for _, field := range fields {
				kind, name := splitFieldPath(field)
				if !validSemanticName(string(kind)) || !validSemanticName(string(name)) {
					return fmt.Errorf("predicate fields: invalid field path %q", field)
				}
				if _, duplicate := seen[field]; duplicate {
					return fmt.Errorf("predicate fields: duplicate field path %q", field)
				}
				seen[field] = struct{}{}
			}
			predicates[i].Fields = fields
			continue
		}
		fields, err := normalizeFieldSet(predicates[i].Fields)
		if err != nil {
			return fmt.Errorf("predicate fields: %w", err)
		}
		predicates[i].Fields = fields
	}
	return nil
}

func normalizeProfiles(input []ProfileDeclaration) ([]ProfileDeclaration, error) {
	profiles := make([]ProfileDeclaration, len(input))
	for i, raw := range input {
		if !validSemanticName(string(raw.Key)) {
			return nil, fmt.Errorf("profile key is empty or invalid UTF-8")
		}
		profile := cloneProfile(raw)
		sort.Slice(profile.Requirements, func(i, j int) bool {
			return requirementKey(profile.Requirements[i]) < requirementKey(profile.Requirements[j])
		})
		for j := 1; j < len(profile.Requirements); j++ {
			if profile.Requirements[j] == profile.Requirements[j-1] {
				return nil, fmt.Errorf("profile %q has duplicate requirement", profile.Key)
			}
		}
		var err error
		if profile.Implies, err = normalizeProfileSet(profile.Implies); err != nil {
			return nil, fmt.Errorf("profile %q implications: %w", profile.Key, err)
		}
		profiles[i] = profile
	}
	sort.Slice(profiles, func(i, j int) bool { return profiles[i].Key < profiles[j].Key })
	for i := 1; i < len(profiles); i++ {
		if profiles[i-1].Key == profiles[i].Key {
			return nil, fmt.Errorf("duplicate profile key %q", profiles[i].Key)
		}
	}
	return profiles, nil
}

func deriveTransformation(
	schema Schema, version CompilerSemanticsVersion, declaration TransformationDeclaration,
) (CompiledTransformation, []CompilationDiagnostic) {
	compiled := CompiledTransformation{declaration: cloneTransformation(declaration)}
	diagnostics := make([]CompilationDiagnostic, 0)
	for _, path := range declaration.DeclaredReads {
		validateFieldPath(schema, path, "", 0, declaration.ID, &diagnostics)
	}
	for _, path := range declaration.DeclaredWrites {
		validateFieldPath(schema, path, "", 0, declaration.ID, &diagnostics)
	}
	active := 0
	if declaration.Form != nil {
		active++
	}
	if declaration.Aggregate != nil {
		active++
	}
	if declaration.SelectAssign != nil {
		active++
	}
	if active != 1 || (declaration.Operator == OperatorFormRelatedEntity && declaration.Form == nil) ||
		(declaration.Operator == OperatorAggregateRelatedFields && declaration.Aggregate == nil) ||
		(declaration.Operator == OperatorSelectAndAssign && declaration.SelectAssign == nil) ||
		(declaration.Operator != OperatorFormRelatedEntity && declaration.Operator != OperatorAggregateRelatedFields &&
			declaration.Operator != OperatorSelectAndAssign) {
		diagnostics = append(diagnostics, diagnostic(UnsupportedOperator, string(declaration.ID), "operator_union"))
	}
	reads := make([]FieldPath, 0)
	writes := make([]FieldPath, 0)
	accesses := make([]SemanticAccess, 0)
	invariants := make([]InvariantDeclaration, 0)
	switch declaration.Operator {
	case OperatorFormRelatedEntity:
		if declaration.Form != nil {
			form := declaration.Form
			if form.OutputKey == nil || form.OutputKey.Kind != OutputKeyCommonSourceField || form.OutputSlot == "" || form.SourceCount == 0 || uint64(len(form.Sources)) != form.SourceCount {
				diagnostics = append(diagnostics, diagnostic(UnsupportedOperator, string(declaration.ID), "form_shape"))
			}
			if !schemaHasEntity(schema, form.SourceKind) || !schemaHasEntity(schema, form.OutputKind) || !schemaHasRelation(schema, form.RelationKind, form.OutputKind, form.SourceKind) {
				diagnostics = append(diagnostics, diagnostic(UnsupportedOperator, string(declaration.ID), "form_schema_shape"))
			}
			for _, source := range form.Sources {
				if source.Kind != form.SourceKind {
					diagnostics = append(diagnostics, diagnostic(UnsupportedOperator, string(declaration.ID), "source_kind"))
				}
			}
			reads = append(reads, form.GroupingField)
			validateFieldPath(schema, form.GroupingField, form.SourceKind, 0, declaration.ID, &diagnostics)
			if form.OutputKey != nil {
				reads = append(reads, form.OutputKey.Field)
				validateFieldPath(schema, form.OutputKey.Field, form.SourceKind, 0, declaration.ID, &diagnostics)
			}
			for _, copied := range form.CopiedFields {
				reads = append(reads, copied.Source)
				writes = append(writes, copied.Destination)
				sourceKind := validateFieldPath(schema, copied.Source, form.SourceKind, 0, declaration.ID, &diagnostics)
				destinationKind := validateFieldPath(schema, copied.Destination, form.OutputKind, 0, declaration.ID, &diagnostics)
				if sourceKind != 0 && destinationKind != 0 && sourceKind != destinationKind {
					diagnostics = append(diagnostics, diagnostic(UnsupportedOperator, string(declaration.ID), "copy_type"))
				}
			}
			accesses = append(accesses,
				SemanticAccess{Kind: AccessEntity, Mode: AccessRead, EntityKind: form.SourceKind},
				SemanticAccess{Kind: AccessEntity, Mode: AccessWrite, EntityKind: form.OutputKind},
				SemanticAccess{Kind: AccessRelation, Mode: AccessWrite, RelationKind: form.RelationKind},
			)
			invariants = append(invariants, formInvariants(declaration.ID, form.GroupingField)...)
		}
	case OperatorSelectAndAssign:
		if declaration.SelectAssign != nil {
			payload := declaration.SelectAssign
			selector, err := CompileSelector(schema, version, payload.Selector)
			if err != nil {
				diagnostics = append(diagnostics, diagnostic(UnsupportedOperator, string(declaration.ID), "selector"))
			} else if !selector.Grouped() {
				// Refused at compile time rather than left to fail at evaluation. An
				// ungrouped selector cannot carry a group-scoped guard, and the guard is
				// this operator's whole point.
				diagnostics = append(diagnostics, diagnostic(UnsupportedOperator, string(declaration.ID), "selector_ungrouped"))
			} else {
				compiled.selector = selector
			}
			if len(payload.Assignments) == 0 {
				diagnostics = append(diagnostics, diagnostic(UnsupportedOperator, string(declaration.ID), "assignments_empty"))
			}
			kind := payload.Selector.Kind
			guardType, guardErr := checkGroupExpr(schema, kind, payload.Guard, 0)
			if guardErr != nil || guardType != TypeBool {
				diagnostics = append(diagnostics, diagnostic(UnsupportedOperator, string(declaration.ID), "guard"))
			}
			// READS ARE DERIVED FROM THE EXPRESSION TREES, which is the half decision 3
			// records as missing. Every path the rule can read must appear, or the write-
			// conflict analysis and DECLARED_ACCESS_MISMATCH both reason over a read set
			// smaller than the truth -- and under-derivation is the fail-OPEN direction.
			if payload.Selector.Where != nil {
				reads = append(reads, readFieldPaths(*payload.Selector.Where)...)
			}
			if payload.Selector.GroupBy != nil {
				reads = append(reads, readFieldPaths(*payload.Selector.GroupBy)...)
			}
			reads = append(reads, readFieldPaths(payload.Guard)...)
			seenTargets := make(map[FieldPath]struct{}, len(payload.Assignments))
			for _, assignment := range payload.Assignments {
				// THE TARGET, NOT THE INDEX. normalizeTransformationPayload sorts the
				// assignments, so an index names a position in the sorted slice and not the
				// one the author wrote -- an author who duplicated their first two
				// assignments was told the diagnostic concerned their third. A target path
				// is a bounded typed declaration key, which is what a detail is for, and it
				// is stable under the sort.
				detail := string(assignment.Target)
				if _, duplicate := seenTargets[assignment.Target]; duplicate {
					// Two assignments to one field would produce two FieldUpdates with the
					// same name in one Update, which NewPatch refuses as an error rather
					// than as a diagnostic -- so the whole compilation would fail with no
					// attributable subject instead of refusing this rule.
					diagnostics = append(diagnostics, diagnostic(UnsupportedOperator, string(declaration.ID), detail))
				}
				seenTargets[assignment.Target] = struct{}{}
				writes = append(writes, assignment.Target)
				reads = append(reads, readFieldPaths(assignment.Value)...)
				targetKind := validateFieldPath(schema, assignment.Target, kind, 0, declaration.ID, &diagnostics)
				valueType, valueErr := checkExprInScope(schema, kind, assignment.Value, memberInGroupScope, 0)
				if valueErr != nil {
					diagnostics = append(diagnostics, diagnostic(UnsupportedOperator, string(declaration.ID), detail))
				} else if pathErr := checkPathsBindKind(assignment.Value, kind); pathErr != nil {
					diagnostics = append(diagnostics, diagnostic(UnsupportedOperator, string(declaration.ID), detail))
				} else if targetKind != 0 {
					declaredType, typeErr := valueKindType(targetKind)
					if typeErr != nil || declaredType != valueType {
						// The assignment writes a value of one type into a field declared as
						// another. Caught here because ApplyPatch does not re-derive types.
						diagnostics = append(diagnostics, diagnostic(UnsupportedOperator, string(declaration.ID), detail))
					}
				}
			}
			accesses = append(accesses, SemanticAccess{Kind: AccessEntity, Mode: AccessRead, EntityKind: kind},
				SemanticAccess{Kind: AccessEntity, Mode: AccessWrite, EntityKind: kind})
			invariants = append(invariants, selectAssignInvariants(declaration.ID, payload)...)
		}
	case OperatorAggregateRelatedFields:
		if declaration.Aggregate != nil {
			aggregate := declaration.Aggregate
			if aggregate.Target.Rule == "" || aggregate.Target.Slot == "" || aggregate.RelationKind == "" || aggregate.SourceKind == "" {
				diagnostics = append(diagnostics, diagnostic(UnsupportedOperator, string(declaration.ID), "aggregate_shape"))
			}
			reads = append(reads, aggregate.RequiredSourceTuple...)
			for _, path := range aggregate.RequiredSourceTuple {
				validateFieldPath(schema, path, aggregate.SourceKind, 0, declaration.ID, &diagnostics)
			}
			for _, predicate := range aggregate.Predicates {
				reads = append(reads, predicate.Fields...)
				validatePredicate(schema, declaration.ID, aggregate.SourceKind, predicate, &diagnostics)
			}
			reads = append(reads, aggregate.Anchor.Source, aggregate.Anchor.Destination)
			anchorSource := validateFieldPath(schema, aggregate.Anchor.Source, aggregate.SourceKind, 0, declaration.ID, &diagnostics)
			anchorDestination := validateFieldPath(schema, aggregate.Anchor.Destination, "", 0, declaration.ID, &diagnostics)
			if anchorSource != 0 && anchorDestination != 0 && anchorSource != anchorDestination {
				diagnostics = append(diagnostics, diagnostic(UnsupportedOperator, string(declaration.ID), "anchor_type"))
			}
			writes = append(writes, aggregate.Anchor.Destination)
			for _, reduction := range aggregate.Reductions {
				reads = append(reads, reduction.Source, reduction.Destination)
				writes = append(writes, reduction.Destination)
				if reduction.Kind != ReduceInt64Max {
					diagnostics = append(diagnostics, diagnostic(UnsupportedOperator, string(declaration.ID), "reduction_tag"))
				}
				validateFieldPath(schema, reduction.Source, aggregate.SourceKind, ValueInt64, declaration.ID, &diagnostics)
				validateFieldPath(schema, reduction.Destination, "", ValueInt64, declaration.ID, &diagnostics)
			}
			for _, predicate := range aggregate.ResultPredicates {
				reads = append(reads, predicate.Fields...)
				validatePredicate(schema, declaration.ID, "", predicate, &diagnostics)
			}
			accesses = append(accesses,
				SemanticAccess{Kind: AccessEntity, Mode: AccessRead, EntityKind: aggregate.SourceKind},
				SemanticAccess{Kind: AccessRelation, Mode: AccessRead, RelationKind: aggregate.RelationKind},
			)
			invariants = append(invariants, aggregateInvariants(declaration.ID, aggregate)...)
		}
	}
	reads = normalizeDerivedFields(reads)
	writes = normalizeDerivedFields(writes)
	for _, path := range reads {
		accesses = append(accesses, SemanticAccess{Kind: AccessField, Mode: AccessRead, Field: path})
	}
	for _, path := range writes {
		accesses = append(accesses, SemanticAccess{Kind: AccessField, Mode: AccessWrite, Field: path})
	}
	sort.Slice(accesses, func(i, j int) bool { return accessKey(accesses[i]) < accessKey(accesses[j]) })
	compiled.reads, compiled.writes, compiled.accesses = reads, writes, accesses
	sort.Slice(invariants, func(i, j int) bool { return invariants[i].key < invariants[j].key })
	compiled.invariants = invariants
	if !slices.Equal(declaration.DeclaredReads, reads) {
		diagnostics = append(diagnostics, diagnostic(DeclaredAccessMismatch, string(declaration.ID), "reads"))
	}
	if !slices.Equal(declaration.DeclaredWrites, writes) {
		diagnostics = append(diagnostics, diagnostic(DeclaredAccessMismatch, string(declaration.ID), "writes"))
	}
	return compiled, diagnostics
}

func resolveDependencies(_ Schema, declarations []TransformationDeclaration, compiled map[RuleID]CompiledTransformation) []CompilationDiagnostic {
	diagnostics := make([]CompilationDiagnostic, 0)
	edges := make(map[RuleID]map[RuleID]struct{}, len(declarations))
	for _, declaration := range declarations {
		edges[declaration.ID] = make(map[RuleID]struct{})
	}
	for _, declaration := range declarations {
		for _, dependency := range declaration.After {
			if dependency == declaration.ID || compiled[dependency].declaration.ID == "" {
				if dependency != declaration.ID {
					diagnostics = append(diagnostics, diagnostic(UnsupportedOperator, string(declaration.ID), "dependency:"+string(dependency)))
				}
			}
			edges[declaration.ID][dependency] = struct{}{}
		}
		if declaration.Aggregate != nil {
			target := declaration.Aggregate.Target
			producer, ok := compiled[target.Rule]
			if !ok || producer.declaration.Form == nil || producer.declaration.Form.OutputSlot != target.Slot {
				diagnostics = append(diagnostics, diagnostic(UnsupportedOperator, string(declaration.ID), "output_slot:"+string(target.Rule)+":"+string(target.Slot)))
			} else {
				edges[declaration.ID][target.Rule] = struct{}{}
				consumer := compiled[declaration.ID]
				consumer.accesses = append(consumer.accesses,
					SemanticAccess{Kind: AccessEntity, Mode: AccessRead, EntityKind: producer.declaration.Form.OutputKind},
					SemanticAccess{Kind: AccessEntity, Mode: AccessWrite, EntityKind: producer.declaration.Form.OutputKind},
				)
				sort.Slice(consumer.accesses, func(i, j int) bool { return accessKey(consumer.accesses[i]) < accessKey(consumer.accesses[j]) })
				consumer.accesses = slices.Compact(consumer.accesses)
				compiled[declaration.ID] = consumer
				targetKind, _ := splitFieldPath(declaration.Aggregate.Anchor.Destination)
				validTarget := targetKind == producer.declaration.Form.OutputKind
				for _, reduction := range declaration.Aggregate.Reductions {
					kind, _ := splitFieldPath(reduction.Destination)
					validTarget = validTarget && kind == producer.declaration.Form.OutputKind
				}
				for _, predicate := range declaration.Aggregate.ResultPredicates {
					for _, field := range predicate.Fields {
						kind, _ := splitFieldPath(field)
						validTarget = validTarget && kind == producer.declaration.Form.OutputKind
					}
				}
				if !validTarget || !schemaRelationMatches(producer, declaration) {
					diagnostics = append(diagnostics, diagnostic(UnsupportedOperator, string(declaration.ID), "output_slot_shape"))
				}
			}
		}
	}
	for readerID, reader := range compiled {
		for writerID, writer := range compiled {
			if readerID == writerID {
				continue
			}
			if intersects(writer.writes, reader.reads) {
				edges[readerID][writerID] = struct{}{}
			}
		}
	}
	ruleIDs := make([]RuleID, 0, len(compiled))
	for id := range compiled {
		ruleIDs = append(ruleIDs, id)
	}
	slices.Sort(ruleIDs)
	for i, leftID := range ruleIDs {
		for _, rightID := range ruleIDs[i+1:] {
			overlap := fieldIntersection(compiled[leftID].writes, compiled[rightID].writes)
			if len(overlap) == 0 || dependencyPathExists(edges, leftID, rightID) || dependencyPathExists(edges, rightID, leftID) {
				continue
			}
			for _, field := range overlap {
				diagnostics = append(diagnostics, diagnostic(WriteConflictUnresolved, string(leftID)+"|"+string(rightID), string(field)))
			}
		}
	}
	for id, dependencies := range edges {
		transformation := compiled[id]
		transformation.dependencies = make([]RuleID, 0, len(dependencies))
		for dependency := range dependencies {
			if _, ok := compiled[dependency]; ok {
				transformation.dependencies = append(transformation.dependencies, dependency)
			}
		}
		slices.Sort(transformation.dependencies)
		compiled[id] = transformation
	}
	return diagnostics
}

func dependencyPathExists(edges map[RuleID]map[RuleID]struct{}, from, target RuleID) bool {
	visited := make(map[RuleID]struct{}, len(edges))
	frontier := []RuleID{from}
	for len(frontier) > 0 {
		current := frontier[len(frontier)-1]
		frontier = frontier[:len(frontier)-1]
		if _, seen := visited[current]; seen {
			continue
		}
		visited[current] = struct{}{}
		for dependency := range edges[current] {
			if dependency == target {
				return true
			}
			frontier = append(frontier, dependency)
		}
	}
	return false
}

func fieldIntersection(left, right []FieldPath) []FieldPath {
	result := make([]FieldPath, 0)
	for _, field := range left {
		if _, ok := slices.BinarySearch(right, field); ok {
			result = append(result, field)
		}
	}
	return result
}

func topologicalOrder(compiled map[RuleID]CompiledTransformation) ([]CompiledTransformation, bool) {
	remaining := make(map[RuleID]int, len(compiled))
	dependents := make(map[RuleID][]RuleID, len(compiled))
	for id, transformation := range compiled {
		remaining[id] = len(transformation.dependencies)
		for _, dependency := range transformation.dependencies {
			dependents[dependency] = append(dependents[dependency], id)
		}
	}
	ready := make([]RuleID, 0)
	for id, count := range remaining {
		if count == 0 {
			ready = append(ready, id)
		}
	}
	slices.Sort(ready)
	ordered := make([]CompiledTransformation, 0, len(compiled))
	levels := make(map[RuleID]uint64, len(compiled))
	for len(ready) > 0 {
		id := ready[0]
		ready = ready[1:]
		transformation := compiled[id]
		transformation.level = levels[id]
		compiled[id] = transformation
		ordered = append(ordered, transformation)
		for _, dependent := range dependents[id] {
			if levels[dependent] < levels[id]+1 {
				levels[dependent] = levels[id] + 1
			}
			remaining[dependent]--
			if remaining[dependent] == 0 {
				ready = insertRuleID(ready, dependent)
			}
		}
	}
	return ordered, len(ordered) != len(compiled)
}

func compileProfiles(schema Schema, declarations []ProfileDeclaration, version CompilerSemanticsVersion) ([]CompiledProfile, []CompilationDiagnostic) {
	diagnostics := make([]CompilationDiagnostic, 0)
	byKey := make(map[ProfileKey]ProfileDeclaration, len(declarations))
	for _, profile := range declarations {
		byKey[profile.Key] = profile
	}
	result := make([]CompiledProfile, 0, len(declarations))
	for _, profile := range declarations {
		valid := profile.Scope.Kind == AllEntitiesOfKind && profile.Aggregation == AllSelected && schemaHasEntity(schema, profile.Scope.EntityKind)
		if !valid {
			diagnostics = append(diagnostics, diagnostic(ProfileOrderUnprovable, string(profile.Key), "profile_shape"))
		}
		for _, requirement := range profile.Requirements {
			if requirement.Kind != FieldPresent || !validRequirementCode(requirement.Code) {
				diagnostics = append(diagnostics, diagnostic(ProfileOrderUnprovable, string(profile.Key), "requirement_tag"))
			}
			validateFieldPath(schema, requirement.Field, profile.Scope.EntityKind, 0, RuleID(profile.Key), &diagnostics)
		}
		proofs := make([]ProfileImplication, 0, len(profile.Implies))
		for _, targetKey := range profile.Implies {
			target, ok := byKey[targetKey]
			if !ok || !profileImplies(profile, target) {
				diagnostics = append(diagnostics, diagnostic(ProfileOrderUnprovable, string(profile.Key), string(targetKey)))
				continue
			}
			proofs = append(proofs, ProfileImplication{target: targetKey, kind: ProfileProofRequirementSetContainment})
		}
		compiled := CompiledProfile{declaration: cloneProfile(profile), schemaDigest: schema.Digest(), compilerVersion: version, proofs: proofs}
		canonical, err := encodeCompiledProfile(compiled)
		if err != nil {
			diagnostics = append(diagnostics, diagnostic(ProfileOrderUnprovable, string(profile.Key), "canonical"))
		} else {
			compiled.canonical = canonical
			compiled.id = ProfileID(canonicalDigest(canonical))
		}
		result = append(result, compiled)
	}
	return result, diagnostics
}

func profileImplies(source, target ProfileDeclaration) bool {
	if source.Scope != target.Scope || source.Aggregation != target.Aggregation {
		return false
	}
	for _, required := range target.Requirements {
		if _, ok := slices.BinarySearchFunc(source.Requirements, required, func(a, b RequirementAtom) int { return compare(requirementKey(a), requirementKey(b)) }); !ok {
			return false
		}
	}
	return true
}

func orderCheckpoints(input []CheckpointDeclaration, ordered []CompiledTransformation) []CheckpointDeclaration {
	position := make(map[RuleID]int, len(ordered))
	for i, transformation := range ordered {
		position[transformation.declaration.ID] = i
	}
	result := slices.Clone(input)
	sort.Slice(result, func(i, j int) bool {
		if position[result[i].After] != position[result[j].After] {
			return position[result[i].After] < position[result[j].After]
		}
		return result[i].Key < result[j].Key
	})
	return result
}

func validateFieldPath(schema Schema, path FieldPath, expectedEntity EntityKind, expectedKind ValueKind, subject RuleID, diagnostics *[]CompilationDiagnostic) ValueKind {
	entityKind, fieldName := splitFieldPath(path)
	declaration, ok := schema.entityDeclaration(entityKind)
	if !ok || (expectedEntity != "" && entityKind != expectedEntity) {
		*diagnostics = append(*diagnostics, diagnostic(UnknownField, string(subject), string(path)))
		return 0
	}
	field, ok := findFieldDeclaration(declaration.Fields, fieldName)
	if !ok || (expectedKind != 0 && field.Kind != expectedKind) {
		*diagnostics = append(*diagnostics, diagnostic(UnknownField, string(subject), string(path)))
		return 0
	}
	return field.Kind
}

func validatePredicate(schema Schema, subject RuleID, expectedEntity EntityKind, predicate AggregatePredicate, diagnostics *[]CompilationDiagnostic) {
	validShape := false
	switch predicate.Kind {
	case CompleteTuple:
		validShape = len(predicate.Fields) > 0
	case NonNegativeInt:
		validShape = len(predicate.Fields) == 1
	case EqualFieldAcrossSources:
		validShape = len(predicate.Fields) == 1 && expectedEntity != ""
	case LessOrEqualFields:
		validShape = len(predicate.Fields) == 2
	default:
		validShape = false
	}
	if !validShape {
		*diagnostics = append(*diagnostics, diagnostic(UnsupportedOperator, string(subject), "predicate_tag"))
	}
	for _, path := range predicate.Fields {
		expectedKind := ValueKind(0)
		if predicate.Kind == NonNegativeInt || predicate.Kind == LessOrEqualFields {
			expectedKind = ValueInt64
		}
		validateFieldPath(schema, path, expectedEntity, expectedKind, subject, diagnostics)
	}
}

func formInvariants(rule RuleID, grouping FieldPath) []InvariantDeclaration {
	return []InvariantDeclaration{
		newInvariant(rule, "01-source-found", DeclaredSourceNotFound, InvariantRulePrecondition, nil),
		newInvariant(rule, "02-source-kind", DeclaredSourceKindInvalid, InvariantRulePrecondition, nil),
		newInvariant(rule, "03-grouping-valid", TeamAssignmentKeyInvalid, InvariantRulePrecondition, []FieldPath{grouping}),
		newInvariant(rule, "04-grouping-equal", TeamAssignmentKeyMismatch, InvariantRulePrecondition, []FieldPath{grouping}),
		newInvariant(rule, "05-member-cardinality", TeamMemberCardinalityInvalid, InvariantCandidatePostcondition, nil),
	}
}

// selectAssignInvariants derives the obligations of a selector-scoped rule.
//
// The reads recorded on each obligation are the paths the CHECK reads, not the rule's whole
// read set: an evidence set wider than the fact that failed makes an attribution that names
// fields the refusal did not consult.
// The obligation suffixes of a selector-scoped rule, in the order executeSelectAndAssign
// checks them. Constants because the compiler derives them and the executor names them, and
// a string literal in two places is a proposition nothing forces to agree.
const (
	selectorEvaluableSuffix = "01-selector-evaluable"
	cardinalitySuffix       = "02-cardinality"
	selectionNonEmptySuffix = "03-selection-nonempty"
	groupEvaluableSuffix    = "04-evaluable"
	guardSuffix             = "05-guard"
)

func invariantKey(rule RuleID, suffix string) string { return string(rule) + "/" + suffix }

func selectAssignInvariants(rule RuleID, payload *SelectAssignDeclaration) []InvariantDeclaration {
	grouping := make([]FieldPath, 0)
	if payload.Selector.GroupBy != nil {
		grouping = readFieldPaths(*payload.Selector.GroupBy)
	}
	values := make([]FieldPath, 0)
	for _, assignment := range payload.Assignments {
		values = append(values, readFieldPaths(assignment.Value)...)
	}
	// NUMBERED IN THE ORDER executeSelectAndAssign CHECKS THEM, and that is a correctness
	// constraint rather than a tidiness one. Obligations are sorted by key, and
	// evaluatedFailureResults marks every declaration BEFORE the failing key as passed --
	// so a key ordered ahead of the check that actually runs first produces a sealed,
	// digested failure report attesting to an obligation nothing established.
	//
	// SELECTION_EXPRESSION_UNAVAILABLE APPEARS TWICE, and that is why. It is raised in two
	// places that cannot be adjacent: once by the SELECTOR, whose filter and grouping
	// expressions are evaluated before any group exists, and once by the GUARD and the
	// assignment values, which are evaluated per group after cardinality and emptiness have
	// been established. A single obligation carrying both would have to sit either before
	// the cardinality check or after it, and would lie in whichever case it did not sit
	// beside -- an earlier version put it fourth, so a selector fault sealed a report
	// claiming the cardinality obligation and the non-empty obligation had both passed when
	// nothing had been grouped at all. Two obligations, one code, distinct keys, each
	// carrying the reads ITS check consults.
	selectorPaths := make([]FieldPath, 0)
	if payload.Selector.Where != nil {
		selectorPaths = append(selectorPaths, readFieldPaths(*payload.Selector.Where)...)
	}
	selectorPaths = append(selectorPaths, grouping...)
	guard := readFieldPaths(payload.Guard)
	return []InvariantDeclaration{
		newInvariant(rule, selectorEvaluableSuffix, SelectionExpressionUnavailable, InvariantRulePrecondition, selectorPaths),
		newInvariant(rule, cardinalitySuffix, SelectionCardinalityInvalid, InvariantRulePrecondition, grouping),
		newInvariant(rule, selectionNonEmptySuffix, SelectionEmpty, InvariantRulePrecondition, grouping),
		newInvariant(rule, groupEvaluableSuffix, SelectionExpressionUnavailable, InvariantRulePrecondition, append(slices.Clone(guard), values...)),
		newInvariant(rule, guardSuffix, SelectionGuardUnsatisfied, InvariantRulePrecondition, guard),
	}
}

func aggregateInvariants(rule RuleID, aggregate *AggregateRelatedFieldsDeclaration) []InvariantDeclaration {
	result := []InvariantDeclaration{
		newInvariant(rule, "01-member-cardinality", TeamMemberCardinalityInvalid, InvariantRulePrecondition, nil),
		newInvariant(rule, "02-tuple-complete", HOSTupleIncomplete, InvariantRulePrecondition, aggregate.RequiredSourceTuple),
	}
	for i, predicate := range aggregate.Predicates {
		code := HOSAggregateInvalid
		switch predicate.Kind {
		case CompleteTuple:
			code = HOSTupleIncomplete
		case NonNegativeInt, LessOrEqualFields:
			code = HOSDurationInvalid
		case EqualFieldAcrossSources:
			code = HOSAnchorMismatch
		}
		result = append(result, newInvariant(rule, fmt.Sprintf("03-source-%02d", i), code, InvariantRulePrecondition, predicate.Fields))
	}
	resultReads := []FieldPath{aggregate.Anchor.Source, aggregate.Anchor.Destination}
	for _, reduction := range aggregate.Reductions {
		resultReads = append(resultReads, reduction.Source, reduction.Destination)
	}
	for _, predicate := range aggregate.ResultPredicates {
		resultReads = append(resultReads, predicate.Fields...)
	}
	result = append(result, newInvariant(rule, "04-emitted-values", HOSAggregateInvalid, InvariantCandidatePostcondition, normalizeDerivedFields(resultReads)))
	return result
}

func newInvariant(rule RuleID, suffix string, code InvariantCode, scope InvariantScope, reads []FieldPath) InvariantDeclaration {
	return InvariantDeclaration{key: string(rule) + "/" + suffix, code: code, scope: scope, reads: normalizeDerivedFields(reads), appliesAfter: rule}
}

func canonicalDiagnostics(input []CompilationDiagnostic) []CompilationDiagnostic {
	result := slices.Clone(input)
	sort.Slice(result, func(i, j int) bool {
		ri, rj := diagnosticRank(result[i].code), diagnosticRank(result[j].code)
		if ri != rj {
			return ri < rj
		}
		if result[i].subject != result[j].subject {
			return result[i].subject < result[j].subject
		}
		return result[i].detail < result[j].detail
	})
	return slices.Compact(result)
}

func diagnostic(code CompilationDiagnosticCode, subject, detail string) CompilationDiagnostic {
	return CompilationDiagnostic{code: code, subject: subject, detail: detail}
}

func diagnosticRank(code CompilationDiagnosticCode) int {
	switch code {
	case UnknownField:
		return 1
	case UnsupportedOperator:
		return 2
	case DeclaredAccessMismatch:
		return 3
	case WriteConflictUnresolved:
		return 4
	case DependencyCycle:
		return 5
	case ProfileOrderUnprovable:
		return 6
	default:
		return 99
	}
}

func splitFieldPath(path FieldPath) (EntityKind, FieldName) {
	value := string(path)
	index := strings.IndexByte(value, '.')
	if index <= 0 || index == len(value)-1 || strings.IndexByte(value[index+1:], '.') >= 0 {
		return "", ""
	}
	return EntityKind(value[:index]), FieldName(value[index+1:])
}

func normalizeFieldSet(input []FieldPath) ([]FieldPath, error) {
	result := slices.Clone(input)
	for _, path := range result {
		kind, name := splitFieldPath(path)
		if !validSemanticName(string(kind)) || !validSemanticName(string(name)) {
			return nil, fmt.Errorf("invalid field path %q", path)
		}
	}
	slices.Sort(result)
	for i := 1; i < len(result); i++ {
		if result[i] == result[i-1] {
			return nil, fmt.Errorf("duplicate field path %q", result[i])
		}
	}
	return result, nil
}

func normalizeDerivedFields(input []FieldPath) []FieldPath {
	result := slices.Clone(input)
	slices.Sort(result)
	return slices.Compact(result)
}

func normalizeRuleSet(input []RuleID) ([]RuleID, error) {
	result := slices.Clone(input)
	for _, id := range result {
		if !validSemanticName(string(id)) {
			return nil, fmt.Errorf("invalid rule ID")
		}
	}
	slices.Sort(result)
	for i := 1; i < len(result); i++ {
		if result[i] == result[i-1] {
			return nil, fmt.Errorf("duplicate rule ID %q", result[i])
		}
	}
	return result, nil
}

func normalizeProfileSet(input []ProfileKey) ([]ProfileKey, error) {
	result := slices.Clone(input)
	for _, key := range result {
		if !validSemanticName(string(key)) {
			return nil, fmt.Errorf("invalid profile key")
		}
	}
	slices.Sort(result)
	for i := 1; i < len(result); i++ {
		if result[i] == result[i-1] {
			return nil, fmt.Errorf("duplicate profile key %q", result[i])
		}
	}
	return result, nil
}

func schemaHasEntity(schema Schema, kind EntityKind) bool {
	_, ok := schema.entityDeclaration(kind)
	return ok
}

func schemaHasRelation(schema Schema, relation RelationKind, from, to EntityKind) bool {
	declaration, ok := schema.relationDeclaration(relation)
	return ok && declaration.FromKind == from && declaration.ToKind == to
}

func schemaRelationMatches(producer CompiledTransformation, consumer TransformationDeclaration) bool {
	return producer.declaration.Form != nil && consumer.Aggregate != nil &&
		producer.declaration.Form.RelationKind == consumer.Aggregate.RelationKind &&
		producer.declaration.Form.SourceKind == consumer.Aggregate.SourceKind
}

func validRequirementCode(code RequirementCode) bool {
	return code == TeamAssignmentKeyRequired || code == TeamAggregationAnchorRequired ||
		code == TeamElapsedDurationRequired || code == TeamDrivingDurationRequired
}

func intersects(a, b []FieldPath) bool {
	for _, value := range a {
		if _, ok := slices.BinarySearch(b, value); ok {
			return true
		}
	}
	return false
}

func compareFieldCopy(a, b FieldCopy) int {
	if a.Source != b.Source {
		return compare(string(a.Source), string(b.Source))
	}
	return compare(string(a.Destination), string(b.Destination))
}

func predicateKey(predicate AggregatePredicate) string {
	parts := make([]string, len(predicate.Fields))
	for i, field := range predicate.Fields {
		parts[i] = string(field)
	}
	return fmt.Sprintf("%03d:%s", predicate.Kind, strings.Join(parts, "\x00"))
}

func reductionKey(reduction FieldReduction) string {
	return fmt.Sprintf("%03d:%s:%s", reduction.Kind, reduction.Source, reduction.Destination)
}

func requirementKey(requirement RequirementAtom) string {
	return fmt.Sprintf("%s:%03d:%s", requirement.Code, requirement.Kind, requirement.Field)
}

func accessKey(access SemanticAccess) string {
	return fmt.Sprintf("%03d:%03d:%s:%s:%s", access.Kind, access.Mode, access.EntityKind, access.RelationKind, access.Field)
}

func insertRuleID(sorted []RuleID, id RuleID) []RuleID {
	index, _ := slices.BinarySearch(sorted, id)
	sorted = append(sorted, "")
	copy(sorted[index+1:], sorted[index:])
	sorted[index] = id
	return sorted
}

func cloneInvariant(input InvariantDeclaration) InvariantDeclaration {
	return InvariantDeclaration{key: input.key, code: input.code, scope: input.scope, reads: slices.Clone(input.reads), appliesAfter: input.appliesAfter}
}

func cloneCompiledTransformation(input CompiledTransformation) CompiledTransformation {
	result := CompiledTransformation{declaration: cloneTransformation(input.declaration), reads: slices.Clone(input.reads), writes: slices.Clone(input.writes),
		accesses: slices.Clone(input.accesses), dependencies: slices.Clone(input.dependencies), level: input.level,
		selector: cloneCompiledSelector(input.selector)}
	result.invariants = make([]InvariantDeclaration, len(input.invariants))
	for i, invariant := range input.invariants {
		result.invariants[i] = cloneInvariant(invariant)
	}
	return result
}

func clonePlan(input Plan) Plan {
	return Plan{schemaDigest: input.schemaDigest, rulesetDigest: input.rulesetDigest, compilerVersion: input.compilerVersion,
		transformations: input.Transformations(), checkpoints: slices.Clone(input.checkpoints), canonical: bytes.Clone(input.canonical), id: input.id}
}

func cloneCompiledProfile(input CompiledProfile) CompiledProfile {
	return CompiledProfile{declaration: cloneProfile(input.declaration), schemaDigest: input.schemaDigest, compilerVersion: input.compilerVersion,
		proofs: slices.Clone(input.proofs), canonical: bytes.Clone(input.canonical), id: input.id}
}

func cloneCompiledProfiles(input []CompiledProfile) []CompiledProfile {
	result := make([]CompiledProfile, len(input))
	for i, profile := range input {
		result[i] = cloneCompiledProfile(profile)
	}
	return result
}

func cloneCompilationFailure(input CompilationFailure) CompilationFailure {
	return CompilationFailure{inputDigest: input.inputDigest, diagnostics: slices.Clone(input.diagnostics), canonical: bytes.Clone(input.canonical), digest: input.digest}
}
