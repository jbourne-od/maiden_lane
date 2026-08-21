package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/optimaldynamics/maiden-lane/internal/adapters/memory"
	openapiv1 "github.com/optimaldynamics/maiden-lane/internal/httpapi/openapiv1"
)

func TestPolicyEndpointsLifecycleAndCAS(t *testing.T) {
	store := memory.NewStore()
	router := NewRouter(Dependencies{
		Policies: store,
	})

	prof1 := "sha256:0000000000000000000000000000000000000000000000000000000000000001"
	prof2 := "sha256:0000000000000000000000000000000000000000000000000000000000000002"

	// 1. Initial PUT version 1 -> 201 Created
	reqBody1, _ := json.Marshal(openapiv1.PutPolicyRequest{
		Version:           1,
		RequiredProfileID: openapiv1.Digest(prof1),
	})
	req1 := httptest.NewRequest(http.MethodPut, "/v1/policies/cust1/target1", bytes.NewReader(reqBody1))
	req1.Header.Set("X-Maiden-Lane-Tenant", "acme")
	req1.Header.Set("Content-Type", "application/json")
	w1 := httptest.NewRecorder()
	router.ServeHTTP(w1, req1)

	if w1.Code != http.StatusCreated {
		t.Fatalf("PUT version 1 code = %d, want 201; body = %s", w1.Code, w1.Body.String())
	}
	var created1 openapiv1.TargetPolicy
	if err := json.Unmarshal(w1.Body.Bytes(), &created1); err != nil {
		t.Fatalf("unmarshal created policy: %v", err)
	}
	if created1.Version != 1 || created1.RequiredProfileID != openapiv1.Digest(prof1) {
		t.Fatalf("unexpected created policy: %+v", created1)
	}

	// 2. Idempotent PUT version 1 identical content -> 200 OK
	req1Retry := httptest.NewRequest(http.MethodPut, "/v1/policies/cust1/target1", bytes.NewReader(reqBody1))
	req1Retry.Header.Set("X-Maiden-Lane-Tenant", "acme")
	req1Retry.Header.Set("Content-Type", "application/json")
	w1Retry := httptest.NewRecorder()
	router.ServeHTTP(w1Retry, req1Retry)
	if w1Retry.Code != http.StatusOK {
		t.Fatalf("PUT retry code = %d, want 200; body = %s", w1Retry.Code, w1Retry.Body.String())
	}

	// 3. Advancing PUT version 2 -> 201 Created
	reqBody2, _ := json.Marshal(openapiv1.PutPolicyRequest{
		Version:           2,
		RequiredProfileID: openapiv1.Digest(prof2),
	})
	req2 := httptest.NewRequest(http.MethodPut, "/v1/policies/cust1/target1", bytes.NewReader(reqBody2))
	req2.Header.Set("X-Maiden-Lane-Tenant", "acme")
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)

	if w2.Code != http.StatusCreated {
		t.Fatalf("PUT version 2 code = %d, want 201; body = %s", w2.Code, w2.Body.String())
	}

	// 4. GET active policy -> version 2
	getReqActive := httptest.NewRequest(http.MethodGet, "/v1/policies/cust1/target1", nil)
	getReqActive.Header.Set("X-Maiden-Lane-Tenant", "acme")
	getWActive := httptest.NewRecorder()
	router.ServeHTTP(getWActive, getReqActive)

	if getWActive.Code != http.StatusOK {
		t.Fatalf("GET active code = %d, want 200", getWActive.Code)
	}
	var activePolicy openapiv1.TargetPolicy
	if err := json.Unmarshal(getWActive.Body.Bytes(), &activePolicy); err != nil {
		t.Fatalf("unmarshal active policy: %v", err)
	}
	if activePolicy.Version != 2 || activePolicy.RequiredProfileID != openapiv1.Digest(prof2) {
		t.Fatalf("unexpected active policy: %+v", activePolicy)
	}

	// 5. GET historical policy at version 1 -> version 1
	getReqV1 := httptest.NewRequest(http.MethodGet, "/v1/policies/cust1/target1?version=1", nil)
	getReqV1.Header.Set("X-Maiden-Lane-Tenant", "acme")
	getWV1 := httptest.NewRecorder()
	router.ServeHTTP(getWV1, getReqV1)

	if getWV1.Code != http.StatusOK {
		t.Fatalf("GET v1 code = %d, want 200", getWV1.Code)
	}
	var v1Policy openapiv1.TargetPolicy
	if err := json.Unmarshal(getWV1.Body.Bytes(), &v1Policy); err != nil {
		t.Fatalf("unmarshal v1 policy: %v", err)
	}
	if v1Policy.Version != 1 || v1Policy.RequiredProfileID != openapiv1.Digest(prof1) {
		t.Fatalf("unexpected v1 policy: %+v", v1Policy)
	}

	// 6. Conflicting PUT (skip version: version 5 when current is 2) -> 409 Conflict
	reqBodySkip, _ := json.Marshal(openapiv1.PutPolicyRequest{
		Version:           5,
		RequiredProfileID: openapiv1.Digest(prof1),
	})
	reqSkip := httptest.NewRequest(http.MethodPut, "/v1/policies/cust1/target1", bytes.NewReader(reqBodySkip))
	reqSkip.Header.Set("X-Maiden-Lane-Tenant", "acme")
	reqSkip.Header.Set("Content-Type", "application/json")
	wSkip := httptest.NewRecorder()
	router.ServeHTTP(wSkip, reqSkip)

	if wSkip.Code != http.StatusConflict {
		t.Fatalf("PUT version skip code = %d, want 409", wSkip.Code)
	}

	// 7. Cross-tenant GET -> 404 Not Found
	getReqCross := httptest.NewRequest(http.MethodGet, "/v1/policies/cust1/target1", nil)
	getReqCross.Header.Set("X-Maiden-Lane-Tenant", "other-tenant")
	getWCross := httptest.NewRecorder()
	router.ServeHTTP(getWCross, getReqCross)

	if getWCross.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant GET code = %d, want 404", getWCross.Code)
	}
}
