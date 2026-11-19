package openapi

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/gorillamux"
)

// Validator checks live responses against the loaded customer contract.
type Validator struct {
	doc    *openapi3.T
	router routers.Router
}

// NewValidator builds a response validator from a validated document.
func NewValidator(doc *openapi3.T) (*Validator, error) {
	r, err := gorillamux.NewRouter(doc)
	if err != nil {
		return nil, fmt.Errorf("openapi: router: %w", err)
	}
	return &Validator{doc: doc, router: r}, nil
}

// CheckResponse validates that status + body match the operation for method/path.
// path is the client path (e.g. "/v1/me"); the document's first server URL is
// used as the request base so gorillamux can match OpenAPI servers.
func (v *Validator) CheckResponse(ctx context.Context, method, path string, status int, header http.Header, body []byte) error {
	base := "http://localhost:8080"
	if v.doc != nil && len(v.doc.Servers) > 0 && v.doc.Servers[0].URL != "" {
		base = v.doc.Servers[0].URL
	}
	httpReq, err := http.NewRequest(method, base+path, nil)
	if err != nil {
		return err
	}
	route, pathParams, err := v.router.FindRoute(httpReq)
	if err != nil {
		return fmt.Errorf("openapi: find route %s %s: %w", method, path, err)
	}

	reqInput := &openapi3filter.RequestValidationInput{
		Request:    httpReq,
		PathParams: pathParams,
		Route:      route,
		Options: &openapi3filter.Options{
			AuthenticationFunc: openapi3filter.NoopAuthenticationFunc,
		},
	}

	respHeader := header.Clone()
	if respHeader == nil {
		respHeader = make(http.Header)
	}
	if status != http.StatusNoContent && respHeader.Get("Content-Type") == "" && len(body) > 0 {
		respHeader.Set("Content-Type", "application/json")
	}

	respInput := &openapi3filter.ResponseValidationInput{
		RequestValidationInput: reqInput,
		Status:                 status,
		Header:                 respHeader,
		Options: &openapi3filter.Options{
			IncludeResponseStatus: true,
		},
	}
	respInput.SetBodyBytes(body)
	if err := openapi3filter.ValidateResponse(ctx, respInput); err != nil {
		return fmt.Errorf("openapi: response %s %s -> %d: %w", method, path, status, err)
	}
	return nil
}

// CheckRecorder validates an httptest response for the given method and path.
func (v *Validator) CheckRecorder(ctx context.Context, method, path string, rec *httptest.ResponseRecorder) error {
	return v.CheckResponse(ctx, method, path, rec.Code, rec.Header(), bytes.Clone(rec.Body.Bytes()))
}
