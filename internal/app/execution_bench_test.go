package app

import (
	"fmt"
	"testing"

	"github.com/optimaldynamics/maiden-lane/internal/fixtures/teamhos"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

// BenchmarkExecutionByStateSize measures a whole spine run as the state grows, holding the
// rule count fixed at two.
//
// READ THE RESULT THE RIGHT WAY ROUND. The two rules resolve exactly two named drivers, so
// every additional entity is state they never read. If cost were confined to the work the
// rules do, this would be FLAT in the number of entities. It is not: it is linear, at roughly
// 15µs per entity. Linear is therefore the finding, not the reassurance — an earlier version
// of this comment treated superlinear growth as the threshold for concern, which set the bar
// in the wrong place and read the measurement as evidence against the hazard it demonstrates.
//
// Where the slope comes from, all of it proportional to total state and none of it to the two
// rules: verifyState re-encodes and re-hashes the whole state on every transition
// (semantic/binding.go), Seal canonicalizes it, evaluateProfileOverState SCANS every entity in
// the state to filter by scope kind, and replayVerifiedJournal re-applies every prior patch
// from the initial state on every transition.
//
// The profile term is stated as a scan on purpose. An earlier version said it "walks every
// entity of the scope kind", which is O(1) under this fixture — the scope kind is team and
// there is exactly one team at every size, so that walk contributes no slope at all. The
// state-proportional cost is the skip-scan that reaches it.
//
// That last one is the reason this benchmark cannot answer the question the programme actually
// needs answered. Transition k replays k-1 entries, so a run of R transitions is Θ(R²·E) and
// this fixture pins R=2. Under one rule pair per team, R and E both grow with the fleet, which
// makes a run Θ(N³). That figure is derived from reading the code, not measured, and measuring
// it needs a multi-rule plan that compiles — which today's operators cannot express (see
// TestMultiInstanceRulesetBaseline). It is blocked on the set-scoped selector.
//
// WHAT IS TIMED includes compilation. Request.Compilation is a semantic.CompileRequest, not a
// compiled plan, so Run calls semantic.Compile inside the measured region along with the two
// profile compilations and their implication proof. At entities=2 that fixed cost dominates;
// the per-entity slope is the part that is about state.
//
// Run with: go test ./internal/app/ -bench BenchmarkExecutionByStateSize -run '^$'
func BenchmarkExecutionByStateSize(b *testing.B) {
	for _, entities := range []int{2, 50, 200, 1000, 2000} {
		b.Run(fmt.Sprintf("entities=%d", entities), func(b *testing.B) {
			request, compilation := benchmarkExecutionInputs(b, entities)

			b.ReportAllocs()
			for b.Loop() {
				result, err := Run(b.Context(), Request{
					Compilation:      compilation,
					InitialState:     request.InitialState,
					World:            request.World,
					ExecutorIdentity: request.ExecutorIdentity,
					Policy:           request.Policy,
				}, nil)
				if err != nil {
					b.Fatalf("Run: %v", err)
				}
				// A nil error is NOT success here. Run reports every deterministic semantic
				// rejection as a completed call with a non-succeeded status, so without this
				// the benchmark would happily time a compile-and-refuse path and report it as
				// the cost of a full spine — and nothing in the output would say so.
				if result.Status() != SpineSucceeded {
					b.Fatalf("spine status = %v, want SpineSucceeded: the benchmark is timing "+
						"a truncated run", result.Status())
				}
			}
		})
	}
}

// benchmarkExecutionInputs returns the ratified inputs with the initial state padded out to
// the requested number of driver entities.
//
// The padding drivers carry complete, self-consistent observations rather than partial ones,
// so they are ordinary state rather than a shape the kernel might reject or short-circuit on.
// They share no assignment key with the two the rules name, so they never enter a team.
func benchmarkExecutionInputs(
	b *testing.B, entities int,
) (teamhos.Inputs, semantic.CompileRequest) {
	b.Helper()

	inputs, err := teamhos.New(teamhos.Passing)
	if err != nil {
		b.Fatalf("teamhos.New: %v", err)
	}
	if entities < 2 {
		b.Fatalf("the ratified fixture already carries two drivers, got entities=%d", entities)
	}

	state := inputs.InitialState
	padded := state.Entities()
	anchor, err := semantic.NewAtomValue("T0")
	if err != nil {
		b.Fatalf("NewAtomValue: %v", err)
	}
	for i := len(padded); i < entities; i++ {
		key := fmt.Sprintf("bench-driver-%d", i)
		assignment, err := semantic.NewStringValue("bench-assignment-" + key)
		if err != nil {
			b.Fatalf("NewStringValue: %v", err)
		}
		entity, err := semantic.NewEntity(
			semantic.EntityRef{
				Kind: "driver",
				ID:   semantic.SourceEntityID(state.InputLineageID(), "driver", key),
			},
			map[semantic.FieldName]semantic.Value{
				"assignment_key":    assignment,
				"hos_anchor":        anchor,
				"hos_elapsed_hours": semantic.NewInt64Value(10),
				"hos_driving_hours": semantic.NewInt64Value(8),
			})
		if err != nil {
			b.Fatalf("NewEntity: %v", err)
		}
		padded = append(padded, entity)
	}

	grown, err := semantic.NewState(state.Schema(), state.InputLineageID(), padded, nil)
	if err != nil {
		b.Fatalf("NewState: %v", err)
	}
	inputs.InitialState = grown
	return inputs, inputs.Compilation
}
