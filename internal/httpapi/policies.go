package httpapi

import (
	"errors"
	"net/http"

	openapiv1 "github.com/optimaldynamics/maiden-lane/internal/httpapi/openapiv1"
	"github.com/optimaldynamics/maiden-lane/internal/ports"
	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

// PutPolicy appends or confirms a target promotion policy.
func (s *server) PutPolicy(
	w http.ResponseWriter, r *http.Request, customerID string, target string, params openapiv1.PutPolicyParams,
) {
	tenant, ok := s.scope(w, params.XMaidenLaneTenant)
	if !ok {
		return
	}
	if s.deps.Policies == nil {
		writeProblem(w, problemDependencyUnavailable, nil)
		return
	}
	if customerID == "" || target == "" {
		writeProblem(w, problemInvalidRequest, nil)
		return
	}

	var body openapiv1.PutPolicyJSONRequestBody
	if err := decodeJSON(r, &body); err != nil {
		writeDecodeProblem(w, err)
		return
	}

	if body.Version <= 0 || body.RequiredProfileID == "" {
		writeProblem(w, problemInvalidRequest, nil)
		return
	}

	// Check if already recorded at this version with identical content (idempotency check)
	current, exists, err := s.deps.Policies.PolicyAtVersion(
		r.Context(), tenant, ports.CustomerID(customerID), ports.TargetKey(target), ports.PolicyVersion(body.Version),
	)
	if err != nil {
		writeStorageProblem(w, err)
		return
	}
	if exists && current.RequiredProfileID == semantic.ProfileID(body.RequiredProfileID) {
		writeJSON(w, http.StatusOK, policyToWire(current))
		return
	}

	policy := ports.TargetPolicy{
		TenantID:          tenant,
		CustomerID:        ports.CustomerID(customerID),
		Target:            ports.TargetKey(target),
		Version:           ports.PolicyVersion(body.Version),
		RequiredProfileID: semantic.ProfileID(body.RequiredProfileID),
	}

	if err := s.deps.Policies.PutPolicy(r.Context(), policy); err != nil {
		if errors.Is(err, ports.ErrPolicyConflict) {
			writeProblem(w, problemPolicyConflict, nil)
			return
		}
		writeStorageProblem(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, policyToWire(policy))
}

// GetPolicy retrieves the active target policy or historical version.
func (s *server) GetPolicy(
	w http.ResponseWriter, r *http.Request, customerID string, target string, params openapiv1.GetPolicyParams,
) {
	tenant, ok := s.scope(w, params.XMaidenLaneTenant)
	if !ok {
		return
	}
	if s.deps.Policies == nil {
		writeProblem(w, problemDependencyUnavailable, nil)
		return
	}
	if customerID == "" || target == "" {
		writeProblem(w, problemInvalidRequest, nil)
		return
	}

	var policy ports.TargetPolicy
	var found bool
	var err error

	if params.Version != nil {
		if *params.Version <= 0 {
			writeProblem(w, problemInvalidRequest, nil)
			return
		}
		policy, found, err = s.deps.Policies.PolicyAtVersion(
			r.Context(), tenant, ports.CustomerID(customerID), ports.TargetKey(target), ports.PolicyVersion(*params.Version),
		)
	} else {
		policy, found, err = s.deps.Policies.ActivePolicy(
			r.Context(), tenant, ports.CustomerID(customerID), ports.TargetKey(target),
		)
	}

	if err != nil {
		writeStorageProblem(w, err)
		return
	}
	if !found {
		writeProblem(w, problemNotFound, nil)
		return
	}

	writeJSON(w, http.StatusOK, policyToWire(policy))
}

func policyToWire(policy ports.TargetPolicy) openapiv1.TargetPolicy {
	return openapiv1.TargetPolicy{
		CustomerID:        string(policy.CustomerID),
		Target:            string(policy.Target),
		Version:           int(policy.Version),
		RequiredProfileID: openapiv1.Digest(policy.RequiredProfileID),
	}
}
