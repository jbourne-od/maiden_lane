package postgres

import (
	"encoding/json"
	"fmt"

	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

// corpusDocument is the adapter's own encoding of a replay corpus.
//
// The schema is stored once because the kernel requires every case to share one, and each
// case is stored as its lineage plus its entities and relations — the same decomposition
// the execution codec uses, and for the same reason: a State cannot be serialized, only
// rebuilt through the kernel's constructors from the parts it was built from.
type corpusDocument struct {
	Schema          []semantic.EntityDeclaration   `json:"schema_entities"`
	SchemaRelations []semantic.RelationDeclaration `json:"schema_relations"`
	Cases           []caseDocument                 `json:"cases"`
}

type caseDocument struct {
	// The complete lineage identity is stored, not its parts, for the reason the
	// execution codec gives: re-deriving it from a namespace and root key risks producing
	// a different one and silently naming a case other than the one that was stored.
	Lineage   string             `json:"lineage"`
	Entities  []entityDocument   `json:"entities"`
	Relations []relationDocument `json:"relations"`
}

func encodeCorpus(corpus semantic.Corpus) ([]byte, error) {
	first, ok := corpus.Case(0)
	if !ok {
		return nil, fmt.Errorf("postgres: corpus has no cases")
	}
	declaration := first.Schema().Declaration()

	document := corpusDocument{
		Schema:          declaration.EntityDeclarations(),
		SchemaRelations: declaration.RelationDeclarations(),
		Cases:           make([]caseDocument, 0, corpus.Len()),
	}
	for index := 0; index < corpus.Len(); index++ {
		state, ok := corpus.Case(index)
		if !ok {
			return nil, fmt.Errorf("postgres: corpus case %d disappeared", index)
		}
		encoded := caseDocument{
			Lineage:   string(state.InputLineageID()),
			Entities:  make([]entityDocument, 0, len(state.Entities())),
			Relations: make([]relationDocument, 0, len(state.Relations())),
		}
		for _, entity := range state.Entities() {
			fields := make(map[string]valueDocument, len(entity.Fields()))
			for name, value := range entity.Fields() {
				fields[string(name)] = encodeValue(value)
			}
			encoded.Entities = append(encoded.Entities, entityDocument{
				Kind:   string(entity.Ref().Kind),
				ID:     string(entity.Ref().ID),
				Fields: fields,
			})
		}
		for _, relation := range state.Relations() {
			encoded.Relations = append(encoded.Relations, relationDocument{
				Kind:     string(relation.Kind),
				FromKind: string(relation.From.Kind),
				FromID:   string(relation.From.ID),
				ToKind:   string(relation.To.Kind),
				ToID:     string(relation.To.ID),
			})
		}
		document.Cases = append(document.Cases, encoded)
	}
	return json.Marshal(document)
}

// decodeCorpus rebuilds a corpus through the kernel's constructors.
//
// It does NOT return the identity it was given. The caller re-derives one from the
// rebuilt cases and requires it to match, which is what makes a stored corpus verifiable
// rather than merely well formed: a row whose document no longer produces its own
// corpus_id is refused instead of being returned under a name the kernel never assigned
// to those cases.
func decodeCorpus(encoded []byte) (semantic.Corpus, error) {
	var document corpusDocument
	if err := json.Unmarshal(encoded, &document); err != nil {
		return semantic.Corpus{}, fmt.Errorf("%w: stored corpus is unreadable", ErrIntegrity)
	}
	schema, err := semantic.NewSchema(document.Schema, document.SchemaRelations)
	if err != nil {
		return semantic.Corpus{}, fmt.Errorf("%w: stored corpus schema is invalid", ErrIntegrity)
	}

	cases := make([]semantic.State, 0, len(document.Cases))
	for _, stored := range document.Cases {
		entities := make([]semantic.Entity, 0, len(stored.Entities))
		for _, storedEntity := range stored.Entities {
			fields := make(map[semantic.FieldName]semantic.Value, len(storedEntity.Fields))
			for name, value := range storedEntity.Fields {
				decoded, err := decodeValue(value)
				if err != nil {
					return semantic.Corpus{}, fmt.Errorf("%w: stored corpus value is invalid", ErrIntegrity)
				}
				fields[semantic.FieldName(name)] = decoded
			}
			entity, err := semantic.NewEntity(semantic.EntityRef{
				Kind: semantic.EntityKind(storedEntity.Kind),
				ID:   semantic.EntityID(storedEntity.ID),
			}, fields)
			if err != nil {
				return semantic.Corpus{}, fmt.Errorf("%w: stored corpus entity is invalid", ErrIntegrity)
			}
			entities = append(entities, entity)
		}

		relations := make([]semantic.Relation, 0, len(stored.Relations))
		for _, storedRelation := range stored.Relations {
			relations = append(relations, semantic.Relation{
				Kind: semantic.RelationKind(storedRelation.Kind),
				From: semantic.EntityRef{
					Kind: semantic.EntityKind(storedRelation.FromKind),
					ID:   semantic.EntityID(storedRelation.FromID),
				},
				To: semantic.EntityRef{
					Kind: semantic.EntityKind(storedRelation.ToKind),
					ID:   semantic.EntityID(storedRelation.ToID),
				},
			})
		}

		state, err := semantic.NewState(
			schema, semantic.InputLineageID(stored.Lineage), entities, relations)
		if err != nil {
			return semantic.Corpus{}, fmt.Errorf("%w: stored corpus case is invalid", ErrIntegrity)
		}
		cases = append(cases, state)
	}

	corpus, err := semantic.NewCorpus(cases)
	if err != nil {
		return semantic.Corpus{}, fmt.Errorf("%w: stored corpus cases are invalid", ErrIntegrity)
	}
	return corpus, nil
}
