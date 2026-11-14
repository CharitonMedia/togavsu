# Module kit

Marketplace modules build in their own repositories (`github.com/CharitonMedia/togavsu-<id>`)
and cannot import the core's `internal/` packages. The kit gives them the core's own code
through three public surfaces:

- `pkg/modulekit`: manifest and migration checks, package build, signing and verification.
- `cmd/togavsu-module`: the same checks, packaging and verification as a command.
- `pkg/modulekit/coretest`: an acceptance harness that runs the real composed core in the
  test process.

Nothing in the kit is a copy. Each function calls the code the core runs at install, so a
package that passes here passes on a running core, and a module's tests run against the
core version its `go.mod` pins.

`CONTRIBUTOR-GAPS.md` lists every gap an outside author hits on this path and whether it is
closed.

## Depending on the core

```sh
go get github.com/CharitonMedia/togavsu@<core commit or tag>
```

Pin the core release the module targets and move the pin when the module adopts a newer
core API. Use only the dependencies the core's `go.mod` already pins (initial modules PRD,
Code). Import `pkg/modulekit` wherever the module packages or checks itself (its library
and its `package` subcommand, as Matter Clock does), and `pkg/modulekit/coretest` from its
`_test.go` files only.

## pkg/modulekit

| Need | Call |
| --- | --- |
| Read package files (skips `.` and `_` names, as `go:embed` does) | `ReadDir(dir)`, `Files(fsys)` |
| Manifest checks | `ParseManifest(src)` |
| Install checks: unpacking, manifest, scopes, migration files, migration rules | `Check(files)`, `CheckPackage(raw)`, `CheckMigrations(m, ms)` |
| Every refusal at once, structured | `CheckAll(files)` (a `Refusals` list of `Refusal`) |
| What this core accepts | `Accepts()`, `Scopes()`, `KnownScope(scope)` |
| A package without the parts that wait on core changes | `Installable(files)`, `BuildInstallable(priv, files)`, `ParsePending(src)`, `Unused(entries, held)` |
| Update checksum rule (no migration that ran may change or go missing) | `CheckUpdate(shipped, next)` against the previous release's shipped package, `CheckUpgrade(prev, next)`, `CheckChecksums(applied, ms)`, `Applied(ms)` |
| The package inside a signed package file | `ReadSignedFile(file)` |
| Installable package (gzip tar) | `Archive(files)` |
| Marketplace listing tar and its manifest | `BuildPackage(manifest, extra)`, `ReadManifest(pkg)` |
| Signed package file | `Build(priv, files)` (checks first), `SignFile(priv, pkg)` (signs bytes unchecked) |
| Keys | `ParseSeed(text)`, `KeyID(pub)`, `PublicKeyPEM(pub)`, `ParsePublicKeyPEM(text)`, `Trust(pub)` |
| Verification | `Digest(pkg)`, `Verify(pkg, digest, sig, keyID, trusted)`, `VerifyFile(file, trusted)` |
| Settings source check, the core's own AC-030 check (`settings.CheckModule`): no environment read outside a directory named `internal/bootstrap`, no numeric or duration configuration literal outside settings. The core's fixed-site allowances name only core functions, so a module names its fixed numbers as constants, as Matter Clock does | `CheckSource(root)` |
| The code the install API would answer with | `Code(err)`: `migration_refused`, `tampered_module`, `unsigned_module`, `validation` |

`Check` also refuses a scope the core's scope list doesn't hold (`Scopes()`, the list the
core checks a module's approved scopes against). The install API itself still reads scopes
only when the admin approves, so the kit tells you at package time what the core would
refuse at approval.

`Build` runs `Check` and refuses to sign a package `Check` refuses, or one that still holds
its `pending.yaml`. `SignFile` signs whatever bytes it is given; use it only to make a
refused package on purpose in a test.

`VerifyFile` is the whole local-install check: the file's form and digest, signature and
signer, the signed manifest, then the install checks. Only `Archive` output installs;
`BuildPackage` writes the plain tar the marketplace listing reads.

## cmd/togavsu-module

```sh
go run github.com/CharitonMedia/togavsu/cmd/togavsu-module check package
go run github.com/CharitonMedia/togavsu/cmd/togavsu-module check -json package
go run github.com/CharitonMedia/togavsu/cmd/togavsu-module check -installable package
go run github.com/CharitonMedia/togavsu/cmd/togavsu-module check -previous ../v1/package package
go run github.com/CharitonMedia/togavsu/cmd/togavsu-module check -installable -previous-file <id>-1.0.0.signed.json -previous-pub signing.pub package
go run github.com/CharitonMedia/togavsu/cmd/togavsu-module package -dir package -key signing.key -out <id>.signed.json
go run github.com/CharitonMedia/togavsu/cmd/togavsu-module package -installable -dir package -key signing.key -out <id>.signed.json
go run github.com/CharitonMedia/togavsu/cmd/togavsu-module verify -file <id>.signed.json -pub signing.pub
go run github.com/CharitonMedia/togavsu/cmd/togavsu-module accepts
```

The key file holds a base64 Ed25519 seed; `package` prints the PEM public key the admin
adds as a trusted signing key. `package` refuses to sign a package `check` refuses. Both
print every refusal, one line each, as `<file> <path or statement>: <code>: <message>`;
`check -json` prints `{ok, id, version, migrations, digest, refusals}` on stdout instead.
With `-installable`, `check` and `package` work on `Installable`'s files and print one
stderr line per held-back part with the core item it waits on, and one line per pending
entry that held nothing back. Under `-json` every failure, an `Installable` refusal or an
unreadable directory included, is the same `{ok, refusals}` result.

The update check compares against what the previous release shipped. Pass its signed
package file with `-previous-file` and the key it was signed with as `-previous-pub`: the
command verifies the signature and reads that package's module id and migrations
(`CheckUpdate`), without judging it again by today's core. Running `Installable` on the
previous release's source would apply today's core to it, so a migration it held back
(and never shipped) would count as shipped. `-previous <dir>` therefore takes a source
directory only when it has no `pending.yaml`, and refuses with `upgrade_baseline`
otherwise. An update failure is `upgrade_refused`. `accepts` prints `Accepts()` as JSON. Exit codes: 0 passed,
1 refused, 2 usage. The command never makes or stores a key; keep the release key out of
the repository, and generate throwaway keys at test time.

## Structured refusals

`Check` returns a `Refusals` error: every refusal it found, not only the first. Use
`errors.As` to get the list (`var refs modulekit.Refusals`) or the first one
(`var r modulekit.Refusal`); `Code(err)` is still the install API's code for the first, and
each refusal's `Error()` is the core's own error text for it. `CheckAll(files)` returns the
list directly, empty when `Check` passes.

Each `Refusal` has:

| Field | Meaning |
| --- | --- |
| `Kind` | `package`, `manifest`, `scope`, `migration`, or `pending` (the pending list) |
| `Code` | the stable code below; match on it, never on message text |
| `File` | `togavsu-module.yaml`, `migrations/NNNN_name.sql` or `pending.yaml` |
| `Path` | the manifest field path, such as `sidebar[0].section` or `permissions[3]` |
| `Statement` | the 1-based statement number in a migration |
| `Value` | the unknown key, the unknown scope, or the refused core relation (`core.trust_accounts`) |
| `Message`, `APICode` | the core's message, and the code the install API answers with |

| Code | What it reports | All reported? |
| --- | --- | --- |
| `unknown_field` | a manifest key, at any depth, this core doesn't read | every one |
| `unknown_scope` | a scope not in `Scopes()` | every one |
| `core_reference` | a migration reference to a core relation a module can't link to | every one |
| `migration_statement` | any other refused migration statement | every one |
| `migration_crosscheck` | `data.extends` or `data.views` that the migrations don't create | every one |
| `manifest_invalid` | a manifest value the core refuses (the manifest checks) | the first, since one bad value can make the next check meaningless |
| `manifest_yaml`, `package_unreadable`, `migration_file` | the manifest, the archive or a migration file name can't be read | the first; nothing past it can be checked |
| `pending_invalid`, `pending_edit`, `pending_order`, `pending_unbuilt` | the pending list (next section) | each one |
| `upgrade_refused`, `upgrade_baseline` | the update check, and a previous version given by a source directory that held parts back | the first |
| `unreadable` | a file or directory the command was given can't be read (command only) | the first |

A package with no refusals passes the core's install check; `Check` confirms that by
running `CheckPackage` on the same bytes.

## What a core accepts

`Accepts()` (and `togavsu-module accepts`) answers from the tables the install checks use:

- `manifest_schema`: a digest of the accepted manifest fields. It changes exactly when the
  core adds or drops a field, so compare it to know whether a core reads your manifest the
  way the one you built against does.
- `manifest_fields`: every field path the manifest decoder reads (`[*]` is any index).
- `scopes`, `scope_patterns`, `forbidden_scopes`: the scope list, the shape accepted for
  another module's published view (`views:<module>.<view>:read`), and the scopes a module
  may never request.
- `core_records`, `core_relations`: the record names `data` declarations take and the core
  tables a migration may reference.

It does not say which CORE items a core has built; read the core's release notes for that.

## Building and signing before your core items land

A module often needs manifest fields, scopes or core tables that a numbered core change
(the CORE items in `CORE-CHANGES.md`) has not delivered yet. Keep those parts in the
package where they belong, list them in `pending.yaml` at the package root, and build with
`BuildInstallable` (or `togavsu-module package -installable`). The pinned core then gets a
package it accepts, and each part comes back on its own once the core accepts it.

```yaml
# pending.yaml: parts of this package that wait on a core change.
pending:
  - field: notifications
    core: CORE-03
    reason: Tell admins when an item fails to sync.
  - field: sidebar[*].section
    core: CORE-08
    reason: File the entry under Money.
  - scope: books:read
    core: CORE-02
    reason: Read the books without a viewer for the nightly sync.
  - migration: migrations/0004_trust_bank_map.sql
    core: CORE-22
    reason: Map each trust account to its own bank account.
```

Each entry names exactly one part, the core item it waits on (`core`) and one line on what
the part does (`reason`):

- `field`: a manifest path. Keys join with dots, list indexes go in brackets, and `[*]`
  (or `[]`) is any index. The entry covers a refusal at that path or inside it:
  `sidebar[*].icon` covers `sidebar[2].icon`, and `tabs[2]` covers `tabs[2].placement` and
  holds back the whole tab. It covers unknown fields and refused values alike.
- `scope`: a permission scope; the whole permission entry is held back.
- `migration`: a whole migration file.

`Installable(files)` checks the package, removes each part a pending entry covers, and
checks again until nothing is refused. It returns the files without `pending.yaml` and
without the held-back parts, and the list it held back (each a `Held` with the entry, the
file and the concrete path). A part the core accepts is never removed, so you edit nothing
when a core item lands; `Unused(entries, held)` lists the entries that held nothing back,
which you can then delete. `Installable` returns an error, a `Refusals` list, for every
refusal no entry covers, and when removing a part breaks what is left (for instance a held
migration creates a table a `data.extends` entry needs; hold that entry back too).

Fields and scopes are removed from the manifest text in place, so its comments and layout
stay as written. The edit is read back, and it must parse as exactly the manifest without
those parts, or `Installable` refuses with `pending_edit`. Removing the first key of a
`- key: value` list item moves the dash to the item's next key; an item's only key can't
be removed, so hold back the item.

Migrations are held back by whole file, never by statement. A migration a firm has run can
never change (`CheckUpgrade`), so a statement removed now and restored later would change
an applied migration and make the module impossible to update. A whole file that waits at
the end of the list is added later as a new migration, which an update allows. So put each
statement that waits on a core change in its own migration file, numbered after every
migration that installs today (for a foreign key to a table you already create, an
`ALTER TABLE ... ADD CONSTRAINT ... FOREIGN KEY ... REFERENCES core.<table> (id) ON DELETE
RESTRICT` in that file). `Installable` refuses with `pending_order` a held-back migration
numbered before one that installs.

Your tests should build the installable package, verify it and run the acceptance suite on
it, so they keep passing while core items land:

```go
signed, held, err := modulekit.BuildInstallable(priv, files)
// held lists what waits; err is nil only if every refusal is covered
c.Install(signed)
```

## Acceptance suites with coretest

`coretest.New(t)` opens the core the release image runs (`core.Open`, every domain, the
admin page, the module runtime, the trust root) on a fresh database reached through a
non-superuser core role, as Compose sets it up. The one stand-in is the supervisor:
deploying a module starts the handler the test registered for its id, in process, with
the secrets the core handed over. The database comes from `TOGAVSU_TEST_DATABASE_URL`, a
Postgres superuser URL; `New` skips without it and fails in CI. The database, the core
role, the module logins and the servers are removed at cleanup.

```go
func TestAcceptance(t *testing.T) {
	c := coretest.New(t)
	c.Serve("my-module", func(m coretest.Module) http.Handler {
		return mymodule.New(mymodule.Config{Core: m.CoreURL, Token: m.Token, WebhookSecret: m.WebhookSecret, DB: m.DB}).Handler()
	})
	pub, priv, _ := ed25519.GenerateKey(nil)
	files, _ := modulekit.ReadDir("package")
	signed, _, _ := modulekit.BuildInstallable(priv, files) // Build, if nothing waits on the core
	c.TrustKey("My module key", pub)
	c.Install(signed)
	c.ApproveAll("my-module")
	page := c.Tab("staff", "my-module", "home", "")
	// ...assert on page.Code and page.Body, then c.Uninstall("my-module", true)
}
```

People are real signed-in users (session cookie and CSRF), one of every role, with a
matter everyone can see (`c.Open`, whose client party the client user is) and a matter
walled to the attorney (`c.Walled`). `pkg/modulekit/coretest/coretest_pg_test.go` runs
the sample module, Matter Clock, through the harness.

### Names for specs written against internal/modules/harness_pg_test.go

| Harness name | coretest |
| --- | --- |
| `newEnv(t)` | `coretest.New(t)` |
| `e.rt.handlers["<id>"] = h` | `c.Handle("<id>", h)`, or `c.Serve("<id>", build)` to build from the deployment's token, webhook secret and database login |
| `e.rt.last("<id>")` | `c.Last("<id>")` (a `Deployment`: `Version`, `Secrets`) |
| `e.rt.headers["<id>"]` | `c.Runtime.Headers("<id>")`; also `Paths`, `Requests`, `Deployments` |
| `e.trustKey(name, pub)` | `c.TrustKey(name, pub)` |
| install: `e.do("POST", "/api/v1/modules", pkg, e.as("admin")...)` | `c.Install(signed)`, or `c.TryInstall(signed)` for the response; `c.TryUpdate(id, signed)` |
| `e.approveAll(id)` | `c.ApproveAll(id)` (the admin's Modules tab) |
| `e.contextFor(who, id, matter)` | `c.ContextFor(who, id, matter)` (matter `""` for none) |
| `e.moduleDB(role, password)` | `c.ModuleDB(id)` from the latest deployment, or `c.OpenModuleDB(role, password)` |
| `e.users`, `e.admin`, `e.staff`, `e.attorney` | `c.Users`, `c.Admin`, `c.Staff`, `c.Attorney`, plus `c.Client`, `c.Accountant` |
| `X-Test-User: <who>`, `e.as(who)` | `c.As(who)`: the same person through the real session and CSRF, no test header |
| `e.do(method, path, body, e.as(who)...)` | `c.As(who).Do(method, path, body)`; `Must`, `Raw`, `Upload` |
| module bearer plus context token | `c.ModuleClient(id).Do(method, path, body, coretest.ContextHeader, token)` |
| agent bearer token | `c.AgentToken(who, scopes...)` |
| `e.srv.URL` | `c.URL` |
| `e.open`, `e.walled` | `c.Open`, `c.Walled` (ids); `c.Matter(...)` and `c.Wall(...)` for more |
| `e.activity`, `e.logTime(p, matter, n)` | `c.Activity`, `c.LogTime(who, matter, n)` |
| event pump and delivery | `c.DeliverEvents()` |
| `e.count(q)`, `e.schemaExists(id)` | `c.Count(q)`, `c.SchemaExists(id)` |
| `e.backups` | `c.Backups` (`Made`, `Fail`, `During`) |
| `e.principal` | none needed: the core resolves the caller from the session or bearer and the context header |

`who` is a role name: `admin`, `attorney`, `staff`, `client` or `accountant`
(`coretest.Admin` and the like).
