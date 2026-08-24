# ISSUE.md

Bug ledger. One narrative entry per runtime bug — what broke, why, and how it was fixed. A lesson that should change future behaviour also gets a one-line rule in `FAILURES.md`. Format:

```
## YYYY-MM-DD — <short bug title>
**Symptom:** what was observed.
**Root cause:** the actual cause, not the symptom.
**Solution:** the fix, and the test that reproduces the bug.
```

---

## 2026-08-23 — `make up` failed: Postgres 18 refused to start on the volume path every older Postgres uses

**Symptom:** The very first `make up` pulled cleanly, created the volume, started the container — and then `krane-postgres-1 exited (1)` while compose was still waiting on its healthcheck. `make` reported `Error 1` with nothing useful; the cause was only visible in `docker compose logs postgres`.

**Root cause:** `docker-compose.yml` mounted the named volume at `/var/lib/postgresql/data`. That is the correct, universally documented path for Postgres 9.x through 17, and it is what a decade of tutorials, StackOverflow answers, and generated compose files all say — so it is what got written. It is wrong for Postgres 18. The `postgres:18+` images store the cluster in a major-version subdirectory of `/var/lib/postgresql` so that `pg_upgrade --link` can work without crossing a mount-point boundary (docker-library/postgres#1259). Mounting the legacy path makes the entrypoint detect data in an "unused mount/volume" and abort rather than risk silently ignoring it.

This is precisely the trap the assignment brief warns about: *"Postgres 18 is required, not a floor; some problems here have cleaner solutions in 18 than in older versions, so it's worth checking what's changed before reaching for a familiar pattern."* The familiar pattern was reached for, and 18 rejected it.

**How it was caught:** Immediately, by `make up` itself failing loudly on its first run — the container's non-zero exit propagated through `docker compose up --wait` and out through `make`. It could not have shipped silently. Had the entrypoint chosen to ignore the mount instead of aborting, the failure mode would have been far worse: a database that appeared to work and lost every row on restart.

**Solution:** Mount at `/var/lib/postgresql`. The compose file now carries the reason and the upstream issue number inline, so the next person to "fix" the path back has to read why first. `FAILURES.md` carries the one-line rule. There is no regression test for this specific line because the whole pipeline is the test: if the volume path is wrong, Postgres does not start, and `make test` cannot reach green.

## 2026-08-24 — A "race test" that would have shipped without ever proving the thing it claimed to prove

**Symptom:** Item 09's last-admin protection (`MemberRepository.AssignRole`/`Delete` in `internal/adapter/postgres/member_repo.go`) needed a race test per CLAUDE.md's rule: "write the failing test first and confirm it actually races... not sequential calls that can't catch the bug." The first version written — two goroutines released together on a `sync.WaitGroup` barrier, each deleting one of an event's two admins concurrently, asserting exactly one success and one `409` — looked correct, read correctly, and **passed**. Nothing about running it once suggested a problem.

**Root cause:** The test only proves what it claims if it actually forces the two transactions to overlap inside the vulnerable window — the moment after each transaction's `EXISTS(SELECT 1 FROM event_members other WHERE ...)` snapshot is taken but before either has committed. A bare goroutine barrier does not guarantee that: it only guarantees both goroutines *start* at roughly the same time, not that they land inside a window that, for a single fast indexed `DELETE`, is on the order of microseconds. Verified directly: with `SELECT ... FOR UPDATE` deliberately removed from `Delete`, the barrier-only test passed **20/20** runs with `-race` — the goroutine/network scheduling jitter between "both released" and "each statement executes" was consistently larger than the actual race window, so the two deletes almost always landed sequentially by accident, never actually colliding. A test that cannot fail is not a test; it is a passing assertion wearing a race test's shape.

Forcing the window open conclusively proved the underlying bug was real, not just the test's blind spot: with the lock removed and a `pg_sleep()` embedded inside the guard's own `WHERE` clause (so the delay happens *after* the statement's snapshot is already established, not before the statement even starts — an earlier attempt using a leading `SELECT pg_sleep(...)` statement, and then `EXISTS(SELECT pg_sleep(...))`, both failed to actually delay anything, for two different silent reasons: a leading statement's sleep finishes long before either goroutine's fast `DELETE` executes, and Postgres's planner skips evaluating an `EXISTS` subquery's target list entirely when the subquery has no `FROM` clause, since existence doesn't depend on the value), both concurrent deletes succeeded and the event was left with **zero admins** — the exact orphan bug the lock exists to prevent.

**How it was caught:** Not by the test itself — by a mandated verification step during plan review (a human reviewer's explicit instruction to "confirm it actually races" before trusting the test, per CLAUDE.md's concurrency-test rule, rather than accepting a green run at face value). Removing the lock and rerunning was the only way to find that the test's green run meant nothing.

**Solution:** Replaced reliance on timing luck with a deterministic mechanism. `member_repo.go` gained an exported test-only synchronization seam, `AfterEventLockForTest` (same "NOT FOR PRODUCTION USE" pattern as `middleware.ContextWithUser`), called immediately after the `SELECT ... FOR UPDATE` lock is acquired in both `AssignRole` and `Delete`. The race test sets it to a function that increments a shared counter, sleeps briefly while "inside," then decrements — if two goroutines are ever both inside concurrently, an `atomic.Bool` flag is set and the test fails outright, independent of the eventual outcome. This lets the test force genuine overlap on every run rather than hoping for it. Proven in both directions before trusting it: with the lock present, the new test passes (12/12 across repeated runs, both `AssignRole` and `Delete`); with the lock deliberately removed again, it fails deterministically (6/6, both paths) — exactly the property a race test needs and the barrier-only version never had.

The seam itself needed a second safeguard, caught immediately by a self-inflicted stack overflow while wiring it: the call site is gated through an internal `afterEventLock()` helper that checks `testing.Testing()` (Go 1.21+) before ever invoking the exported hook, so a misfire in a production binary — a stray import, a copy-pasted assignment — can never pause a real request while holding a live row lock; `testing.Testing()` is hardwired `false` outside a `go test` binary regardless of what the exported var is set to.

**Tests:** `internal/adapter/postgres/member_repo_race_test.go` — `TestMemberRepository_Delete_ConcurrentSelfRemovalOfBothAdmins_ExactlyOneSucceeds` / `TestMemberRepository_AssignRole_ConcurrentSelfDemotionOfBothAdmins_ExactlyOneSucceeds` (outcome-based, kept for readability but not trusted alone) plus `TestMemberRepository_Delete_EventLockSerializesAdminCountAffectingWrites` / `TestMemberRepository_AssignRole_EventLockSerializesAdminCountAffectingWrites` (the deterministic proof, one per code path since each has its own lock call site).

## 2026-08-24 — `make seed` failed on host Go < 1.23, violating the "clean machine with Docker" contract

**Symptom:** Running `make seed` on a machine whose host Go toolchain was older than 1.23 failed immediately with `scripts/require-go.sh`'s version-mismatch message, before Postgres or the mock OIDC issuer were ever touched. This contradicts CLAUDE.md's stated graded contract: `make up && make seed && make test` must pass "on a clean machine with Docker" — no mention of a host Go prerequisite.

**Root cause:** `make seed` and `make test` ran `go run ./cmd/seed` / `go test ./... -race -count=1` directly on the **host** (`Makefile`), gated by `scripts/require-go.sh`. Only `make up` (Postgres, migrations, mock OIDC) was actually containerized; the two targets that follow it silently assumed a host Go 1.23+ install existed. A "clean machine with Docker" — the literal, stated prerequisite — has no such guarantee.

**Solution:** Added a `gotools` service to `docker-compose.yml` (`golang:1.23.12-alpine3.22`, the same patch/variant as the `Dockerfile` builder stage; `apk add build-base` for `-race`'s cgo dependency; bind-mounted repo; named volumes for the module and build cache so repeat runs don't re-download). `seed`/`test` in the `Makefile` now run via `docker compose run --rm gotools ...` with in-network DSNs/OIDC URL (compose service hostnames, not `localhost`) instead of calling `go` on the host; `require-go.sh` now only gates `lint`/`generate`/`contract-check`, which aren't part of the 3-target graded contract. Added a `test-verbose` target (`-v`) for local debugging, kept out of the graded path so the default `make test` output stays scannable.

Verified, not assumed: a deliberately-broken test made `make test` exit non-zero with the failure visible in output (then reverted); a genuinely cold run (all 4 relevant images + the gotools cache volumes removed, ~1GB re-pulled) completed the full `make up && make seed && make test` chain in 2:17, under the 5-minute budget; a scratch copy of the repo run with `go` entirely absent from `PATH` passed all three targets, proving nothing on the graded path shells out to a host Go install.

**Tests:** No new Go test — this is a build/tooling change, not application logic. The verification is the pipeline itself: `make up && make seed && make test` green with `go` absent from `PATH`, and a deliberately-broken test proving the container's exit code still propagates.
