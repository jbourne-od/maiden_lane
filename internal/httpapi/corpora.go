package httpapi

import (
	"net/http"

	openapiv1 "github.com/optimaldynamics/maiden-lane/internal/httpapi/openapiv1"
	"github.com/optimaldynamics/maiden-lane/internal/ports"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

// CreateCorpus registers an immutable, content-addressed replay corpus.
func (s *server) CreateCorpus(w http.ResponseWriter, r *http.Request, params openapiv1.CreateCorpusParams) {
	tenant, ok := s.scope(w, params.XMaidenLaneTenant)
	if !ok {
		return
	}
	if s.deps.Corpora == nil {
		writeProblem(w, problemDependencyUnavailable, nil)
		return
	}

	var body openapiv1.CreateCorpusRequest
	if err := decodeJSON(r, &body); err != nil {
		writeDecodeProblem(w, err)
		return
	}
	if len(body.Cases) == 0 {
		writeProblem(w, problemInvalidRequest, nil)
		return
	}

	states := make([]semantic.State, 0, len(body.Cases))
	for _, caseInput := range body.Cases {
		schema, err := schemaFromWire(caseInput.Schema)
		if err != nil {
			writeProblem(w, problemInvalidSemanticInput, nil)
			return
		}
		state, err := stateFromWire(schema, caseInput.State)
		if err != nil {
			writeProblem(w, problemInvalidSemanticInput, nil)
			return
		}
		states = append(states, state)
	}

	corpus, err := semantic.NewCorpus(states)
	if err != nil {
		writeProblem(w, problemInvalidSemanticInput, nil)
		return
	}

	record := ports.CorpusRecord{
		TenantID: tenant,
		CorpusID: corpus.ID(),
		Corpus:   corpus,
	}

	if err := s.deps.Corpora.PutCorpus(r.Context(), record); err != nil {
		writeStorageProblem(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, corpusToWire(corpus))
}

// GetCorpus retrieves a stored replay corpus.
func (s *server) GetCorpus(
	w http.ResponseWriter, r *http.Request, corpusID openapiv1.Digest, params openapiv1.GetCorpusParams,
) {
	tenant, ok := s.scope(w, params.XMaidenLaneTenant)
	if !ok {
		return
	}
	if s.deps.Corpora == nil {
		writeProblem(w, problemDependencyUnavailable, nil)
		return
	}
	if corpusID == "" {
		writeProblem(w, problemInvalidRequest, nil)
		return
	}

	record, found, err := s.deps.Corpora.GetCorpus(r.Context(), tenant, semantic.CorpusID(corpusID))
	if err != nil {
		writeStorageProblem(w, err)
		return
	}
	if !found {
		writeProblem(w, problemNotFound, nil)
		return
	}

	writeJSON(w, http.StatusOK, corpusToWire(record.Corpus))
}

func corpusToWire(corpus semantic.Corpus) openapiv1.Corpus {
	caseDigests := corpus.Digests()
	cases := make([]openapiv1.Digest, 0, len(caseDigests))
	for _, d := range caseDigests {
		cases = append(cases, openapiv1.Digest(d))
	}
	return openapiv1.Corpus{
		CorpusID:  openapiv1.Digest(corpus.ID()),
		CaseCount: len(cases),
		Cases:     cases,
	}
}
