// Package openapi embeds the API's OpenAPI contract so it can be loaded
// without any filesystem-path assumption -- CI, tests, and the codegen tool
// all read the same bytes. This package is framework-agnostic and imports
// nothing beyond the standard library's embed support.
package openapi

import _ "embed"

//go:embed openapi.yaml
var Raw []byte
