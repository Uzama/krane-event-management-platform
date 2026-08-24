# TRADEOFFS.md

Cuts and scope decisions, recorded as they're made. Item 26 (Phase 4) expands this into the full track-level writeup; entries below are added by the feature that made the cut, not retrofitted later.

---

## Item 23 — Recurring sessions (go-further pick)

Chosen as the go-further pick over the caching/two-versions-live and webhooks alternatives: it exercises the same correctness machinery (items 16/17/22) under a new write shape, rather than adding an orthogonal concern.

**Shipped:** `POST /v1/events/{eventId}/sessions/series` eagerly materializes every occurrence as an ordinary `sessions` row at creation time (tagged `series_id`), through the exact same `Create` a single-session POST uses — so each occurrence is independently subject to the room/speaker `EXCLUDE` (item 16), version-gating (item 17), and audit trail (item 22), with zero new code in any of those paths. A conflicting occurrence is a per-item `"conflict"` result (matching item 21's `BulkCreate` precedent), not a whole-series failure. Editing or cancelling one occurrence afterward is the ordinary session `PATCH`/`DELETE` — unchanged — with a `session_exceptions` row recorded alongside it for history (`status`: `modified`/`cancelled`, `original_starts_at`).

**Cut, deliberately, to keep this small:**
- **Lazy materialize-on-read.** A series only ever exists as N already-created `sessions` rows; there is no "materialize the next occurrence when a client asks for a date range" path, and no un-materializing a far-future occurrence to save space. At real scale (a series running for years) this would need revisiting — capped at 52 occurrences per series for exactly this reason.
- **RRULE-shaped recurrence.** Only `daily`/`weekly` × a fixed interval count are supported. No monthly-by-weekday ("second Tuesday of the month"), no `BYDAY` lists, no explicit exception dates supplied at creation time (an exception is only ever recorded after the fact, via editing/cancelling a materialized occurrence).
- **No series-level operations.** No "cancel the whole series," no "shift all future occurrences by an hour," no regenerate-from-here. Each occurrence is independent once materialized; a caller wanting to change many occurrences edits them one at a time through the existing session endpoints.
- **No read endpoint for a series or its exceptions.** `GET /v1/events/{eventId}/sessions/series/{seriesId}` and a way to list `session_exceptions` don't exist — the only way to see a series' occurrences today is the ordinary session list, filtered client-side by matching `series_id` isn't even exposed on `SessionResponse` (deliberately, to keep the response contract for `GET`/`POST /sessions` unchanged — `series_id` exists on the domain `Session` and is used internally, but was never added to the wire response for the plain session endpoints).
- **Get/List's `series_id` visibility is asymmetric with intent.** `Get` scans `series_id` (needed for the exception-writing check on the write paths' RETURNING clause reuse) but it's not surfaced in `SessionResponse`; `List`'s JOIN-based query was left untouched (item 18's query-count concern), so it doesn't select `series_id` at all. A client cannot currently discover which sessions belong to a series through the API — only through direct DB access or by holding onto a series-create response's occurrence ids.

**What two more weeks would buy:** a `GET .../sessions/series/{seriesId}` endpoint returning the rule plus every occurrence's current state (joining `sessions` by `series_id`); `series_id`/`is_exception` surfaced on `SessionResponse`; monthly-by-weekday recurrence; a "regenerate remaining occurrences" operation that respects already-recorded exceptions instead of blindly re-materializing.
