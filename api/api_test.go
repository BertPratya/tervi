package api_test

import (
	"context"
	"testing"

	"github.com/bertpratya/tervi/api"
	"github.com/getkin/kin-openapi/openapi3"
)

func TestOpenAPIValid(t *testing.T) {
	doc, err := openapi3.NewLoader().LoadFromData(api.OpenAPI)
	if err != nil {
		t.Fatalf("load api.OpenAPI: %v", err)
	}
	if err := doc.Validate(context.Background()); err != nil {
		t.Fatalf("validate api.OpenAPI: %v", err)
	}
}
