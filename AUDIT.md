# AUDIT.md

Append-only activity log. One timestamped entry per completed feature or fix, newest last. Format:

```
## YYYY-MM-DD HH:MM — <feature or fix name>
<what was done, in a sentence or two>
```

---

## 2026-08-23 20:17 — Feature 00 · git-ai + repo skeleton

Completed feature 00. Added `go.mod` (module `github.com/Uzama/krane-event-management-platform`, Go 1.23.0); the rest of the skeleton — `git init`, `.gitignore`, `CLAUDE.md`, `FAILURES.md` — already existed, and git-ai capture was verified live rather than set up fresh.

Bundled the preceding `/init` review of the instruction files: trimmed `FAILURES.md` from 36 lines to 7 (14 of its 16 rules restated `CLAUDE.md` invariants, so both files were charging every session twice for the same rules), folding the two net-new rules into `CLAUDE.md`; pinned Go 1.23; documented the single-test command; added a pre-code note. Created `AUDIT.md` and `ISSUE.md`.

Audited `FEATURES.md` against the assignment brief and applied 8 amendments closing 6 gaps and 2 inconsistencies — CI had no owner, the diagram covered only the data model, the contributor and attendee role boundaries were unencoded and untested, DST had no test, audit recorded no actor, and item 18's dependency contradicted the file's own ordering notes. Added Phase 4 to track the four graded deliverables. Queue is now 29 items.

Commit `5e9bf10`.

## 2026-08-23 21:47 — Feature 01 · Requirement analysis + design diagram

Completed feature 01. Produced both halves of the required design diagram plus the written analysis behind them: `docs/requirements.md` (nine entities with full column tables, relationships, the per-event role matrix expressed as the `role_permissions` rows item 07 will seed, an invariant→column map, eight recorded decisions, and the remaining open question), `docs/er-diagram.md` (Mermaid `erDiagram`, constraint table, legend), and `docs/components.md` (layers, a request traced end to end through the middleware chain and back out the error path, and the compose topology). Moved the assignment brief from `doc/` to `docs/` so there is one doc directory.

Eight decisions recorded so items 02–23 don't re-litigate them. Two are scope cuts made deliberately and logged for `TRADEOFFS.md`: the invitation acceptance lifecycle (invitations stay an independent record; membership is populated directly, keeping every read path behind the per-event authz chokepoint), and cross-event physical-room conflicts (rooms are per-event, so item 16's `EXCLUDE` covers exactly the conflicts the model claims to have — the known limitation is that two events in the same building can both book Hall A). Both carry a "what two more weeks would buy" line.

The remaining open question — whether the email-visibility ban extends below attendee to contributors — has a drafted but unsent email at `docs/requirements.md` §7.3. It does not block item 02 and does not weaken the item 10 `must:` test, which asserts the attendee case the brief names.

Verification: all four Mermaid blocks parse under mermaid@11 driven by a jsdom DOM. Bare Node fails *every* flowchart with a missing-DOM error regardless of content, so the checker asserts a trivial flowchart parses first — without that self-check the run would have reported false failures. Scripted checks confirmed all nine tables item 02 names appear in both the ER diagram and the analysis, and that no invitation `status`/`responded_at` column survives anywhere.

No code, and no TDD step: this feature produces documents, and `make test` does not exist until item 03. Flagged at the review gate rather than skipped silently.

