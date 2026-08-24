// Package validator validates requests and responses against the OpenAPI
// contract in openapi/openapi.yaml -- the CLAUDE.md-named http/validator
// component. It is wired into tests only, never into the production request
// path: FEATURES.md item 05 asks for validation "wired into tests," and CI's
// contract check is spec-drift detection plus these tests, not a live
// middleware layer.
package validator

import (
	"context"
	"fmt"
	"net/http"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"

	// routers/legacy is the router-agnostic OpenAPI path matcher: given a
	// spec and an *http.Request it resolves the matching operation and path
	// parameters without pulling in a routing framework. That's the right
	// fit here because this package only ever runs inside tests -- the
	// production router (internal/http/router.go) stays stdlib
	// net/http.ServeMux, untouched by this import.
	legacyrouter "github.com/getkin/kin-openapi/routers/legacy"

	"github.com/Uzama/krane-event-management-platform/openapi"
)

// LoadSpec parses and validates the embedded OpenAPI contract.
func LoadSpec() (*openapi3.T, error) {
	loader := openapi3.NewLoader()

	spec, err := loader.LoadFromData(openapi.Raw)
	if err != nil {
		return nil, fmt.Errorf("parsing openapi spec: %w", err)
	}

	if err := spec.Validate(loader.Context); err != nil {
		return nil, fmt.Errorf("validating openapi spec: %w", err)
	}

	return spec, nil
}

// ValidateRequest reports every way r violates the operation it matches in
// spec. A non-nil error means the request does not conform to the contract.
func ValidateRequest(ctx context.Context, spec *openapi3.T, r *http.Request) error {
	input, err := requestValidationInput(spec, r)
	if err != nil {
		return err
	}

	return openapi3filter.ValidateRequest(ctx, input)
}

// ValidateResponse reports every way a recorded response for r violates the
// contract. A non-nil error means the response does not conform to the
// operation r matches in spec.
func ValidateResponse(ctx context.Context, spec *openapi3.T, r *http.Request, status int, header http.Header, body []byte) error {
	reqInput, err := requestValidationInput(spec, r)
	if err != nil {
		return err
	}

	respInput := &openapi3filter.ResponseValidationInput{
		RequestValidationInput: reqInput,
		Status:                 status,
		Header:                 header,
	}
	respInput.SetBodyBytes(body)

	return openapi3filter.ValidateResponse(ctx, respInput)
}

func requestValidationInput(spec *openapi3.T, r *http.Request) (*openapi3filter.RequestValidationInput, error) {
	router, err := legacyrouter.NewRouter(spec)
	if err != nil {
		return nil, fmt.Errorf("building spec router: %w", err)
	}

	route, pathParams, err := router.FindRoute(r)
	if err != nil {
		return nil, fmt.Errorf("no matching route in spec for %s %s: %w", r.Method, r.URL.Path, err)
	}

	return &openapi3filter.RequestValidationInput{
		Request:    r,
		PathParams: pathParams,
		Route:      route,
	}, nil
}
