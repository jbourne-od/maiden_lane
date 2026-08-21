package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/optimaldynamics/maiden-lane/internal/adapters/memory"
	"github.com/optimaldynamics/maiden-lane/internal/fixtures/teamhos"
	openapiv1 "github.com/optimaldynamics/maiden-lane/internal/httpapi/openapiv1"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

func TestCorpusEndpointsLifecycle(t *testing.T) {
	store := memory.NewStore()
	router := NewRouter(Dependencies{
		Corpora: store,
	})

	inputsPassing, _ := teamhos.New(teamhos.Passing)
	inputsAnchor, _ := teamhos.New(teamhos.AnchorMismatch)

	schemaWire := schemaToWire(inputsPassing.InitialState.Schema())
	statePassingWire := stateToWireInput(t, inputsPassing.InitialState)
	stateAnchorWire := stateToWireInput(t, inputsAnchor.InitialState)

	corpusReq := openapiv1.CreateCorpusRequest{
		Cases: []openapiv1.CorpusCaseInput{
			{
				Schema: schemaWire,
				State:  statePassingWire,
			},
			{
				Schema: schemaWire,
				State:  stateAnchorWire,
			},
		},
	}

	// 1. Create corpus -> 201 Created
	reqBody, _ := json.Marshal(corpusReq)
	req := httptest.NewRequest(http.MethodPost, "/v1/corpora", bytes.NewReader(reqBody))
	req.Header.Set("X-Maiden-Lane-Tenant", "acme")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("POST /v1/corpora code = %d, want 201; body = %s", w.Code, w.Body.String())
	}
	var createdCorpus openapiv1.Corpus
	if err := json.Unmarshal(w.Body.Bytes(), &createdCorpus); err != nil {
		t.Fatalf("unmarshal created corpus: %v", err)
	}
	if createdCorpus.CorpusID == "" || createdCorpus.CaseCount != 2 || len(createdCorpus.Cases) != 2 {
		t.Fatalf("unexpected created corpus: %+v", createdCorpus)
	}

	// 2. GET corpus -> 200 OK
	getReq := httptest.NewRequest(http.MethodGet, "/v1/corpora/"+string(createdCorpus.CorpusID), nil)
	getReq.Header.Set("X-Maiden-Lane-Tenant", "acme")
	getW := httptest.NewRecorder()
	router.ServeHTTP(getW, getReq)

	if getW.Code != http.StatusOK {
		t.Fatalf("GET /v1/corpora/{id} code = %d, want 200", getW.Code)
	}
	var retrievedCorpus openapiv1.Corpus
	if err := json.Unmarshal(getW.Body.Bytes(), &retrievedCorpus); err != nil {
		t.Fatalf("unmarshal retrieved corpus: %v", err)
	}
	if retrievedCorpus.CorpusID != createdCorpus.CorpusID || retrievedCorpus.CaseCount != 2 {
		t.Fatalf("unexpected retrieved corpus: %+v", retrievedCorpus)
	}

	// 3. Cross-tenant GET -> 404 Not Found
	getReqCross := httptest.NewRequest(http.MethodGet, "/v1/corpora/"+string(createdCorpus.CorpusID), nil)
	getReqCross.Header.Set("X-Maiden-Lane-Tenant", "other-tenant")
	getWCross := httptest.NewRecorder()
	router.ServeHTTP(getWCross, getReqCross)

	if getWCross.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant GET code = %d, want 404", getWCross.Code)
	}

	// 4. Non-existent corpus -> 404 Not Found
	getReqMissing := httptest.NewRequest(http.MethodGet, "/v1/corpora/sha256:0000000000000000000000000000000000000000000000000000000000000000", nil)
	getReqMissing.Header.Set("X-Maiden-Lane-Tenant", "acme")
	getWMissing := httptest.NewRecorder()
	router.ServeHTTP(getWMissing, getReqMissing)

	if getWMissing.Code != http.StatusNotFound {
		t.Fatalf("missing corpus GET code = %d, want 404", getWMissing.Code)
	}
}

func stateToWireInput(t *testing.T, state semantic.State) openapiv1.StateInput {
	t.Helper()
	lineage := state.InputLineageID()
	entities := make([]openapiv1.EntityInput, 0, len(state.Entities()))
	for _, entity := range state.Entities() {
		key := ""
		for _, candidate := range []string{"A", "B"} {
			if semantic.SourceEntityID(lineage, entity.Ref().Kind, candidate) == entity.Ref().ID {
				key = candidate
			}
		}
		if key == "" {
			key = "A"
		}
		fields := map[string]openapiv1.Value{}
		for name, value := range entity.Fields() {
			fields[string(name)] = valueToWire(value)
		}
		entities = append(entities, openapiv1.EntityInput{
			Kind:               string(entity.Ref().Kind),
			CanonicalSourceKey: key,
			Fields:             fields,
		})
	}
	return openapiv1.StateInput{
		Lineage: openapiv1.InputLineage{
			Namespace: "maiden-lane.sanitized-fixture",
			RootKey:   "team-hos-team-ab",
		},
		Entities: entities,
	}
}
