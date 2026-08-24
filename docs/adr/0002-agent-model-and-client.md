# ADR 0002 — Agent model client: hand-rolled `net/http` adapter, not `anthropic-sdk-go`

**Status:** Accepted (item 15)

## Decision

`cmd/agent` talks to the Claude Messages API through a ~200-line hand-written `net/http` adapter (`anthropic.go`), not the official `anthropic-sdk-go` Go SDK. The model used by default is `claude-sonnet-5`, overridable via `-model`.

## Alternative rejected

The official `anthropic-sdk-go`. It was the first choice, and code was started against it, before this ADR's decision reversed course.

## Why

- `go.mod` pins Go 1.23.0. The newest `anthropic-sdk-go` version still compatible with that (`v1.46.0`) — anything from `v1.47.0` on requires Go 1.24 — pulls a large transitive dependency tree for capabilities this agent never touches: AWS SDK v2 and Google Cloud SDK (Bedrock/Vertex model routing), gRPC, OpenTelemetry, an MCP SDK.
- The agent needs exactly one capability: send messages, receive tool-use blocks, send tool results back. That's a handful of JSON structs and one `POST /v1/messages` call — well inside `CLAUDE.md`'s "prefer the standard library" rule, and inside `FAILURES.md`'s rule to check a dependency's own `go` directive before pinning it (this is precisely the check that caught the Go 1.24 mismatch).
- Caught during planning, not after code was written into a corner: an `AskUserQuestion` explicitly re-opened the choice once the dependency tree was inspected, rather than accepting the SDK because it was the "official" answer.

## Consequence

`cmd/agent/anthropic.go` owns its own request/response types for exactly the fields used (messages, tool definitions, tool_use/tool_result content blocks) and will need manual updates if the Messages API's tool-use shape changes — an accepted maintenance cost against zero new `go.mod` entries and a dependency tree unrelated to what the agent actually does. The tool-call loop itself (`agent.go`) is proven with a scripted fake `ModelClient`, so this choice adds no risk to `make test`, which never makes a live API call.
