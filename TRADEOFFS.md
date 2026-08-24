# TRADEOFFS.md

Cuts and scope decisions, recorded as they're made. Item 26 (Phase 4) expands this into the full track-level writeup; entries below are added by the feature that made the cut, not retrofitted later.

---

## Track and overall scope

**Track chosen: 1 — Backend (data correctness under load).** The alternative offered was a frontend/product-breadth track; this one was picked because the assignment's own invariants — no check-then-act, `EXCLUDE` constraints over application locking, lost-update protection, keyset pagination at scale, idempotent bulk writes, append-only audit — are exactly the kind of thing that's easy to *claim* and hard to *prove*, and the brief rewards proof (a race test, a query-count test, a 50k-row pagination test) over surface area. Phase 3 (items 16–23) is this track's payload; everything in Phase 1–2 exists to give it somewhere to attach.

### What Track 1 forced

Choosing correctness-under-load as the axis of depth had consequences that a breadth-first track wouldn't have faced:

- **Every mutation needed a version column and a version-gated `WHERE`** (item 17), not just the ones an examiner might think to test — retrofitting this after the fact across events/rooms/sessions/event_members would have been far more invasive than introducing `opt.Optional[T]` once (item 08, pulled forward from its original item 12/20 slot) and reusing it everywhere.
- **The seed generator (item 14) had to be a first-class piece of engineering, not a fixture script** — 50k invitations and 5k users exist specifically so items 18 (query-count) and 19 (keyset pagination) have something real to measure against, and item 14's own `noSpeakerOrRoomOverlap` guard had to independently satisfy the room/speaker `EXCLUDE` constraints item 16 adds two items later.
- **Concurrency tests had to be trusted, not just green** — the admin-count race (item 09) and the room/speaker `EXCLUDE` race (item 16) both required proving the test fails when the guard is removed, not just that it passes when the guard is present. See `AI-WORKFLOW.md`'s worked example and `FAILURES.md`'s rule on bare goroutine barriers.
- **Authorization had to be data, not code**, because response-shaping (item 10) and repository-level defense-in-depth (item 13's fail-closed escalation guard) both need to reason about roles independently of the HTTP layer — a hardcoded `if role == "admin"` chain would have made at least three of the depth items harder to prove in isolation.
- **Less room for product breadth.** There is no session check-in flow, no notification system, no calendar export, no search — the track's own name says where the two weeks went.

### What was cut, project-wide

Beyond item 23's recurring-sessions cuts (below), two decisions from item 01's requirement analysis narrowed scope before any code existed:

- **Invitation acceptance lifecycle.** Invitations stay an independent record with no `status`/`responded_at` column and no accept/decline endpoint; membership is populated directly by an admin/contributor invite call, keeping every read path behind the existing per-event authz chokepoint rather than adding a second, unauthenticated acceptance flow.
- **Cross-event physical-room conflicts.** Rooms are per-event (not a shared physical inventory), so item 16's `EXCLUDE` constraint prevents exactly the conflicts the data model claims to represent. The known gap: two different events in the same real building can each book a room named "Hall A" without the database ever knowing they're the same physical space.
- **No update/delete on a sent invitation** (item 13) — a wrong-role invite to an already-invited email is unfixable via the API; the only recourse is a fresh invite after the email itself changes, or direct DB access. Recorded here because it surfaces as a real rough edge, not just an implementation detail.

### What two more weeks would buy

In priority order, if this track continued: a session check-in/attendance flow (the most product-visible gap); the invitation acceptance lifecycle with a real status column and a public accept/decline link; cross-event room-conflict awareness (shared physical inventory, or at minimum a warning); the recurring-sessions read endpoint and series-level operations listed under item 23 below; and ETag/If-Match as an additive alternative to the body/query-param version scheme (item 17 kept the latter as the sole mechanism, judged sufficient, not because the former was rejected on merits).

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
