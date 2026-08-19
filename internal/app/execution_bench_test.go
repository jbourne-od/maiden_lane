package app

import (
	"fmt"
	"testing"

	"github.com/optimaldynamics/maiden-lane/internal/fixtures/teamhos"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

// BenchmarkExecutionByStateSize measures what one execution costs as the state grows,
// holding the plan fixed.
//
// It exists because the closed-rule-language programme is motivated by a claim about where
// the engine's cost lives, and that claim was originally made from a throwaway probe that was
// deleted. A number nobody can reproduce is not evidence, and this programme's whole posture
// is that a stated fact must be checkable — so the measurement is committed before the work it
// justifies.
//
// What it isolates: the plan has two rules and names two drivers, so every additional driver
// is state the rules do not touch. The benchmark therefore measures what a transition pays for
// state it never reads, which is the term that decides whether a real fleet is tractable.
// Growth that is worse than linear here would mean per-rule cost scales with total state, and
// a ruleset with one rule pair per team would be quadratic overall.
//
// Run with: go test ./internal/app/ -bench BenchmarkExecutionByStateSize -run '^$'
func BenchmarkExecutionByStateSize(b *testing.B) {
	for _, entities := range []int{2, 50, 200, 1000, 2000} {
		b.Run(fmt.Sprintf("entities=%d", entities), func(b *testing.B) {
			request, compilation := benchmarkExecutionInputs(b, entities)

			b.ReportAllocs()
			for b.Loop() {
				if _, err := Run(b.Context(), Request{
					Compilation:      compilation,
					InitialState:     request.InitialState,
					World:            request.World,
					ExecutorIdentity: request.ExecutorIdentity,
					Policy:           request.Policy,
				}, nil); err != nil {
					b.Fatalf("Run: %v", err)
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
