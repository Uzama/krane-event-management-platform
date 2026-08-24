// Package gen holds oapi-codegen's generated output: the ServerInterface and
// model types matching openapi/openapi.yaml, targeting stdlib
// net/http.ServeMux (the std-http-server generator target). Nothing in this
// package is hand-edited -- run `make generate` after changing the spec.
package gen

//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen -config cfg.yaml ../../../openapi/openapi.yaml
