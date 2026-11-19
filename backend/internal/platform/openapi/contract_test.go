package openapi_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/hosama-adem/twofold/backend/internal/platform/openapi"
)

func TestOpenAPIDocumentValid(t *testing.T) {
	ctx := context.Background()
	doc, err := openapi.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Info == nil || doc.Info.Title != "Twofold Customer API" {
		t.Fatalf("unexpected info: %+v", doc.Info)
	}
	if doc.Paths == nil || doc.Paths.Find("/v1/transfers") == nil {
		t.Fatal("missing /v1/transfers path")
	}
	if _, err := openapi.NewValidator(doc); err != nil {
		t.Fatal(err)
	}
}

func TestResponseDriftIsDetected(t *testing.T) {
	ctx := context.Background()
	doc, err := openapi.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	v, err := openapi.NewValidator(doc)
	if err != nil {
		t.Fatal(err)
	}

	// Spec requires status + service. Dropping service must fail validation —
	// this is the CI tripwire for "changed a response field without the spec".
	body := []byte(`{"status":"ok"}`)
	err = v.CheckResponse(ctx, http.MethodGet, "/health", http.StatusOK,
		http.Header{"Content-Type": []string{"application/json"}}, body)
	if err == nil {
		t.Fatal("expected validation error for drifted /health body")
	}
}
