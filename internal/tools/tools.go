//go:build tools
// +build tools

// Package tools pins build-time-only tool dependencies into go.mod/go.sum by
// blank-importing their main packages. This is the Go-1.23-era idiom (Go
// 1.24 added a native `tool` directive in go.mod for the same purpose) --
// don't "modernize" this file to the tool directive without also bumping the
// module's go directive off 1.23, which is a deliberate pin (see
// FAILURES.md).
//
// The `tools` build tag keeps this file out of every ordinary build, vet,
// and lint run; it is only ever reached by `go mod tidy` (for pinning) and
// by `go run <path>` (for actually invoking the tool, e.g. via go:generate).
package tools

import (
	// oapi-codegen generates internal/http/gen from openapi/openapi.yaml.
	_ "github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen"
)
