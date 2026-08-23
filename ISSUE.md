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
