// Package api holds tervi's API contracts.
package api

import _ "embed"

// OpenAPI is the HTTP contract in openapi.yaml.
//
//go:embed openapi.yaml
var OpenAPI []byte
