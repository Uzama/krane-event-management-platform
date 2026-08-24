# AI-WORKFLOW.md

How AI was used to build this repository, what was driven by the human versus delegated to the model, the planning process behind each feature, and one specific case where the AI produced wrong code — how it was caught, and what was done about it.

---

## Tools

Claude Code (Sonnet), operating directly in this repository via its file, shell, and git tools, on every commit in this repo's history. No other AI tool touched the code. There is no separate "AI-generated draft, human-cleaned-up" pass — the model wrote every line, under the constraints below.

## What was driven vs. delegated

The **human role** was direction and gatekeeping, not typing:

- Wrote `CLAUDE.md` and its correctness invariants (no check-then-act, `EXCLUDE` constraints over application locking, `Optional[T]` for PATCH, keyset-only pagination, append-only audit in the same transaction, etc.) before any feature work started. Every plan is judged against this document, not against general Go practice.
- Set the workflow the model must follow for every feature: plan → **stop for review** → branch → TDD → **stop for review** → log → **stop for commit message approval** → commit. Nine of `FEATURES.md`'s items and every bug fix went through all three human gates; the model never merged its own plan, never declared a red test green, and never wrote a commit message that shipped without a shown-and-approved message first.
- Made every genuinely ambiguous product decision via `AskUserQuestion` rather than letting the model guess: which base branch to build docs on, whether item 28's fresh-clone verification should actually run or just be described, the email-visibility boundary between contributor and attendee (`docs/requirements.md` §7.3), and — mid-build — reversing the agent's model-client dependency choice (ADR 0002) after the human's review surfaced the transitive dependency tree.
- Caught the specific defect described below, and it was a **human review step** — not a passing test — that caught it.

The **AI's role** was everything downstream of a signed-off plan: reading the invariants and `FAILURES.md` before starting, writing the failing test first, implementing the minimum to turn it green, running `make test`/`make lint` before ever claiming success, writing the self-audit table mapping each applicable invariant to the line that enforces it and the test that proves it, and logging `AUDIT.md`/`FEATURES.md`/`ISSUE.md`/`FAILURES.md` entries in the same commit as the work they describe.

## Planning process

Every item in `FEATURES.md` ran through the same loop, enforced by `CLAUDE.md` itself (so the model could not skip a step without contradicting its own operating instructions):

1. **Plan.** Read the relevant requirement, the correctness invariants, and `FAILURES.md`. Resolve every ambiguity with `AskUserQuestion` rather than assuming.
2. **Plan review — stop.** Present the plan as a table (`What | Before → After | Why | How`), naming which invariants apply and how the plan satisfies each. Wait for approval.
3. **Branch, then TDD.** Cut the feature branch off `main`. Write the failing test first, watch it fail for the right reason, then implement.
4. **Green is mandatory.** `make test` must pass for real — no skipped, commented-out, or weakened tests to reach green.
5. **Implementation review — stop.** Present the diff as a table (`File | Change | Why | Requirement it satisfies`), with a self-audit against every applicable invariant. Wait for review.
6. **Log, then commit — stop.** `AUDIT.md`/`FEATURES.md` land in the same commit as the code. The commit message is shown and approved before `git commit` ever runs.

This loop is why `AUDIT.md` reads as a narrative rather than a changelog: each entry records not just what shipped but what was verified and how, because the verification step is itself part of what the human is reviewing at each gate.

---

## A specific case where the AI produced wrong code

**Item 09 — event membership's last-admin protection**, and the race test written to prove it. Full detail in `ISSUE.md`'s 2026-08-24 entry; summarized here for the "how it was caught, what was done" question specifically.

**What the AI produced first:** an application-level guard (`SELECT ... FOR UPDATE` before a role change or removal that could drop an event to zero admins) plus a race test for it — two goroutines released together on a `sync.WaitGroup` barrier, each removing one of an event's two admins concurrently, asserting exactly one `409` and one success. The test read correctly and **passed**. Nothing about a single green run suggested a problem, and this is the shape a race test is supposed to have.

**How it was caught:** not by the test, and not by the model reviewing its own output — by a human instruction at the plan-review gate to "confirm it actually races" before trusting a green run, per `CLAUDE.md`'s own concurrency-test rule ("write the failing test first and confirm it actually races... not sequential calls that can't catch the bug"). Following that instruction, the model removed the `FOR UPDATE` lock from the implementation and re-ran the exact same test: **it still passed, 20/20 runs, under `-race`.** A test that cannot fail when the bug it claims to guard against is deliberately reintroduced is not proving anything — it was asserting a shape, not a property. The goroutine/network scheduling jitter between "both released" and "each statement executes" was reliably larger than the actual vulnerable window (a single fast indexed `DELETE`), so the two deletes almost never landed inside it by accident.

**Root cause:** a bare goroutine barrier guarantees both goroutines *start* together, not that they land inside the specific microsecond-scale window between one transaction's existence check and its commit. The AI wrote a test whose control flow looked like a race test without verifying it actually forced the overlap it needed to prove anything.

**What was done:** the implementation gained a test-only synchronization seam (`AfterEventLockForTest`, gated behind `testing.Testing()` so it can never fire in a production binary) called immediately after the lock is acquired, letting the test force two goroutines to be genuinely inside the critical section at once and fail outright if that ever happens correctly (i.e., serialized) versus incorrectly (both inside simultaneously). Proven in both directions before being trusted: with the lock present, the new test passes repeatedly; with the lock removed again, it fails deterministically every time — the property the original barrier-only version never actually had. With the lock removed and the seam in place, the underlying bug was also confirmed real, not just a test gap: both concurrent deletes succeeded and left the event with zero admins, the exact orphan state the guard exists to prevent.

**What changed as a result:**
- `FAILURES.md` gained a permanent rule: a bare goroutine barrier around a fast, single-statement Postgres write does not reliably prove a race test catches a missing lock; force the overlap deterministically and verify the test actually fails with the lock removed before trusting it.
- Every later concurrency test in this repo (item 16's room/speaker `EXCLUDE` race, item 17's lost-update tests) was written against this standard from the start — and where the mechanism differs (item 16's constraint is enforced entirely inside one atomic `INSERT`'s index insertion, with no read-then-write window in Go), `AUDIT.md` records *why* a bare barrier is trusted there instead of restating the rule blindly.
- `CLAUDE.md`'s own workflow now names this as one of its concurrency-test requirements explicitly, rather than leaving "confirm it actually races" as an unwritten review habit.

This is the clearest instance in the project of AI output that looked correct, passed its own test, and was wrong — caught only because a human review gate required proving the negative, not just observing the positive.
