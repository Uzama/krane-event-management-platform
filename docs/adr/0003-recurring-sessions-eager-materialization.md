# ADR 0003 — Recurring sessions: eager materialization, not on-read

**Status:** Accepted (item 23, go-further pick)

## Decision

A recurring session series (`POST /v1/events/{eventId}/sessions/series`) is materialized **eagerly, in full, at creation time**: every occurrence is written as an ordinary `sessions` row (tagged with a `series_id`), through the exact same `Create` path a single-session `POST` uses. Editing or cancelling one occurrence afterward is the ordinary session `PATCH`/`DELETE`, with a `session_exceptions` row recorded alongside it for history. Series are capped at 52 occurrences.

## Alternatives rejected

1. **Lazy materialize-on-read** — store only the rule (`daily`/`weekly` × interval × count) and exceptions; expand into concrete occurrences when a client requests a date range.
2. **Caching / two-versions-live**, and **webhooks** — the other two "go-further" options offered alongside recurring sessions.

## Why

- Recurring sessions was chosen over caching and webhooks because it exercises the *same* correctness machinery items 16/17/22 already built (the room/speaker `EXCLUDE` constraint, version-gating, same-transaction audit) under a new write shape, rather than adding an orthogonal concern with its own new failure modes late in the project.
- Eager over lazy: every occurrence needs independent conflict detection against the room/speaker `EXCLUDE` constraints and independent version-gating for later edits — both are properties of a concrete `sessions` row, not of an abstract rule. Materializing eagerly means zero new code in the constraint, version-gating, or audit paths; a lazy expander would need to re-implement conflict checking and versioning for occurrences that don't exist as rows yet, doubling the surface those three depth items already proved.
- A per-occurrence conflict is a defined per-item result (mirroring item 21's bulk-invite precedent: one item's failure doesn't fail the whole series), not a single all-or-nothing series creation.

## Consequence

The cut is deliberate and recorded in full in [`TRADEOFFS.md`](../../TRADEOFFS.md#item-23--recurring-sessions-go-further-pick): no RRULE-shaped recurrence (only fixed daily/weekly × interval), no series-level operations (cancel-the-whole-series, shift-all-future-occurrences), no read endpoint for a series or its exceptions, and a 52-occurrence cap standing in for what lazy materialization would otherwise need to handle at real scale (a series running for years). `series_id` exists on the domain `Session` and is used internally but isn't exposed on `SessionResponse`, keeping the response contract for `GET`/`POST /sessions` unchanged.
