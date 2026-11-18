# Core update fails once modules are installed (desk fix 5)

Found on Patrick's Desk install, Nov 17: after the five modules were installed from the marketplace, the next
`core migrate` exited 1 with `core: seal ledgers: db: seal ledgers: ERROR: dependent privileges exist
(SQLSTATE 2BP01)`. No migration ran and the core kept serving the old build, so no core update can be applied
on an install with modules. The same script runs in three places, so all three break the same way:
`db.SealLedgers` at the end of `core migrate`, the re-seal that `wrapLedgerMigration` adds to any migration that
names an append-only table, and the supervisor's restore (`internal/supervisor/postgres.go`).

## Cause

Module install grants each module role `REFERENCES (id)` on the core tables it extends
(`internal/modules/schema.go`, `GRANT REFERENCES (id) ON core.<t> TO <module role>`). On an append-only table
the application role can pass that on only because the seal gives it `REFERENCES (id) ... WITH GRANT OPTION`.
On Desk the application role has granted `REFERENCES (id)` on `core.trust_ledger` to all five module roles.
`ledgerseal.Script` then runs `REVOKE ALL ON TABLE core.<t> FROM PUBLIC, <app>` for every table. A table-level
REVOKE also revokes the matching column privileges, so it tries to take back the application role's
`REFERENCES (id)` grant option, and Postgres refuses because the module roles' grants depend on it.

## Fix

- In `ledgerseal.Script`, for each append-only table, replace `REVOKE ALL ON TABLE q FROM PUBLIC, a;` with:
  - `REVOKE ALL ON TABLE q FROM PUBLIC;`
  - `REVOKE UPDATE, DELETE, TRUNCATE, TRIGGER, MAINTAIN ON TABLE q FROM a;` (compose and every CI job run
    postgres:17, which has MAINTAIN)
  - `REVOKE GRANT OPTION FOR SELECT, INSERT ON TABLE q FROM a;`
  - then the existing `GRANT SELECT, INSERT ...` and `GRANT REFERENCES (id) ... WITH GRANT OPTION`, unchanged.
- Never use CASCADE: it would strip the module roles' grants and break installed modules.
- The writer roles' lines (`REVOKE ALL ON TABLE q FROM <writer>`) stay as they are; writers hold no grant
  option and pass nothing on.
- What the application role may do on each ledger is unchanged: SELECT and INSERT, REFERENCES (id) with grant
  option, nothing else. `VerifySealed` is unchanged and must still pass.

## Tests

- `internal/db/ledgers_test.go` (Postgres): seal; as the application role create a role and
  `GRANT REFERENCES (id) ON core.trust_ledger` to it; run `SealLedgers` again and it succeeds; the created
  role still holds `REFERENCES (id)` on `core.trust_ledger`; `VerifySealed` passes; the application role still
  has no UPDATE, DELETE or TRUNCATE on any append-only table, and has no grant option on SELECT or INSERT.
- Same setup, then `ApplySealed` with a migration whose SQL names `trust_ledger`: it applies and the table is
  sealed again afterwards.
- A core or modules Postgres test that installs a module which extends a ledger table and then runs the core
  migrate path twice: both runs succeed.
- The supervisor's restore keeps using `ledgerseal.Script`; its existing tests must still pass.

## Not in this fix

No change to which tables are append-only, to module install grants, or to `VerifySealed`.
