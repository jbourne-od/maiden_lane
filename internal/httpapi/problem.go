package httpapi

import (
	"encoding/json"
	"net/http"

	openapiv1 "github.com/optimaldynamics/maiden-lane/internal/httpapi/openapiv1"
)

// This file owns the only way this package reports a failure to a client.
//
// Every problem is drawn from a closed catalog: a call site selects a ratified
// kind, it cannot compose a new one. That is deliberate. A problem document is
// written on the failure path, which is exactly where the tempting thing to do
// is attach the cause, the offending value, or a wrapped error string. None of
// those is representable here, so the temptation has nowhere to land
// (Inviolate 17, AGENTS.md section 18).
//
// A deterministic semantic outcome is NOT a problem. A failed protected
// invariant and a needs_input readiness verdict are answers the computation
// produced, and they are returned as successful responses carrying a typed
// result. Only the application's inability to reach an answer appears here.

const problemBaseURI = "https://maiden-lane.optimaldynamics.com/problems/"

// problemKind is the closed problem vocabulary. Its values are the slugs that
// complete the ratified type URIs.
type problemKind string

const (
	problemInvalidRequest              problemKind = "invalid-request"
	problemTenantRequired              problemKind = "tenant-required"
	problemNotFound                    problemKind = "not-found"
	problemMethodNotAllowed            problemKind = "method-not-allowed"
	problemUnsupportedMediaType        problemKind = "unsupported-media-type"
	problemInvalidPlan                 problemKind = "invalid-plan"
	problemInvalidSemanticInput        problemKind = "invalid-semantic-input"
	problemInternalError               problemKind = "internal-error"
	problemDependencyUnavailable       problemKind = "dependency-unavailable"
	problemPublicationConflict         problemKind = "publication-conflict"
	problemPolicyConflict              problemKind = "policy-conflict"
	problemExecutionConflict           problemKind = "execution-conflict"
	problemStoredArtifactsUnverifiable problemKind = "stored-artifacts-unverifiable"
)

// problemDefinition is the fixed status and text for one kind. Titles and
// details are constants: nothing here is derived from a request.
type problemDefinition struct {
	status int
	title  string
	detail string
}

var problemCatalog = map[problemKind]problemDefinition{
	problemInvalidRequest: {
		status: http.StatusBadRequest,
		title:  "Invalid request",
		detail: "The request body could not be read as a valid document for this operation.",
	},
	problemTenantRequired: {
		status: http.StatusBadRequest,
		title:  "Tenant required",
		detail: "Every versioned operation requires a well-formed tenant identifier header.",
	},
	problemPublicationConflict: {
		status: http.StatusConflict,
		title:  "Publication conflict",
		detail: "The target is not at the expected version, so this decision was formed " +
			"against a state that no longer holds. Read the target again and decide afresh.",
	},
	problemPolicyConflict: {
		status: http.StatusConflict,
		title:  "Policy conflict",
		detail: "The target policy version conflicts with recorded history. Either the version is not the immediate successor or it attempts to rewrite an existing version.",
	},
	problemExecutionConflict: {
		status: http.StatusConflict,
		title:  "Execution conflict",
		detail: "The execution cannot be reattempted because it is not in a failed state.",
	},
	problemStoredArtifactsUnverifiable: {
		// 500 rather than 422: the request was well formed and every dependency
		// answered. What failed is the agreement between what a store recorded and
		// what the kernel derives from the inputs that store also holds, which is
		// this service's fault to own rather than the caller's to correct.
		status: http.StatusInternalServerError,
		title:  "Stored artifacts could not be verified",
		detail: "The stored execution could not be reproduced from its recorded inputs, " +
			"so no authenticated evidence exists to evaluate.",
	},
	problemNotFound: {
		status: http.StatusNotFound,
		title:  "Not found",
		detail: "No such artifact exists for this tenant.",
	},
	problemMethodNotAllowed: {
		status: http.StatusMethodNotAllowed,
		title:  "Method not allowed",
		detail: "This resource does not support the requested method.",
	},
	problemUnsupportedMediaType: {
		status: http.StatusUnsupportedMediaType,
		title:  "Unsupported media type",
		detail: "This operation accepts application/json request bodies.",
	},
	problemInvalidPlan: {
		status: http.StatusUnprocessableEntity,
		title:  "Invalid plan",
		detail: "The declarations did not compile. No plan was created.",
	},
	problemInvalidSemanticInput: {
		status: http.StatusUnprocessableEntity,
		title:  "Invalid semantic input",
		detail: "The canonical inputs were incomplete or unsupported at the request boundary.",
	},
	problemInternalError: {
		status: http.StatusInternalServerError,
		title:  "Internal error",
		detail: "The request could not be completed because of an internal inconsistency.",
	},
	problemDependencyUnavailable: {
		status: http.StatusServiceUnavailable,
		title:  "Dependency unavailable",
		detail: "A required dependency was unavailable. The request may be retried.",
	},
}

// ratifiedDiagnostics is the closed compiler-diagnostic vocabulary admitted to
// a problem document. It is repeated here rather than taken from the semantic
// package so that widening the compiler's vocabulary cannot widen the published
// wire contract without a deliberate edit at this boundary.
var ratifiedDiagnostics = map[string]bool{
	"UNKNOWN_FIELD":             true,
	"UNSUPPORTED_OPERATOR":      true,
	"DECLARED_ACCESS_MISMATCH":  true,
	"WRITE_CONFLICT_UNRESOLVED": true,
	"DEPENDENCY_CYCLE":          true,
	"PROFILE_ORDER_UNPROVABLE":  true,
}

// writeProblem renders one closed problem as RFC 9457 application/problem+json.
//
// diagnostics is honored only for a compilation failure and only for codes in
// the ratified vocabulary; anything else is dropped rather than published.
func writeProblem(w http.ResponseWriter, kind problemKind, diagnostics []string) {
	definition, ok := problemCatalog[kind]
	if !ok {
		// An unrecognized kind is a defect in this package, never client input.
		// Degrade to the internal-error problem without echoing the unknown
		// value, which would put an unbounded string into a response.
		kind = problemInternalError
		definition = problemCatalog[problemInternalError]
	}

	document := openapiv1.Problem{
		Type:   problemBaseURI + string(kind),
		Title:  definition.title,
		Status: int32(definition.status),
		Detail: &definition.detail,
	}
	if kind == problemInvalidPlan {
		if admitted := admittedDiagnostics(diagnostics); len(admitted) > 0 {
			document.Diagnostics = &admitted
		}
	}

	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(definition.status)
	// The document is built from constants and validated codes, so encoding
	// cannot fail on unsupported values. A write failure means the client is
	// gone, which nothing here can act on.
	_ = json.NewEncoder(w).Encode(document)
}

func admittedDiagnostics(codes []string) []openapiv1.CompilerDiagnostic {
	admitted := make([]openapiv1.CompilerDiagnostic, 0, len(codes))
	for _, code := range codes {
		if !ratifiedDiagnostics[code] {
			continue
		}
		admitted = append(admitted, openapiv1.CompilerDiagnostic{
			Code: openapiv1.CompilerDiagnosticCode(code),
		})
	}
	return admitted
}
