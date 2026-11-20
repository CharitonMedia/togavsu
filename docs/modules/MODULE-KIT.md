# Module kit

Marketplace modules build in their own repositories (`github.com/CharitonMedia/togavsu-<id>`)
and cannot import the core's `internal/` packages. The kit gives them the core's own code
through three public surfaces:

- `pkg/modulekit`: manifest and migration checks, package build, signing and verification,
  plus CORE-31/32 helpers (`IssueStepToken` / `VerifyStepToken`, signed public CSRF
  `IssueSignedPublicCSRF` / `CheckSignedPublicCSRF`, `PublicCSRFPassHeader` /
  `X-Togavsu-Public-CSRF` on proxied public pages, `SignWebhook` / `VerifyWebhook` —
  contract calls use the same HMAC as events). Provided contract operation names
  `events` and `providers` are reserved (they collide with the core's
  `POST …/contracts/{contract}/events` and `GET …/providers` routes) and fail
  `ParseManifest` / `Check`.
- `cmd/togavsu-module`: the same checks, packaging and verification as a command.
- `pkg/modulekit/forms`: CORE-33 form definitions, validation, HTMX rendering and autosave
  (JSON or form-urlencoded with CSRF on autosave/offline), repeating add/remove
  (`RepeatingJS`, index cap `MaxRepeatingIndex`), offline queue, and masked-field
  encryption (`EnsureKey` only on `ErrSecretNotFound`); answers stay in the module's own
  schema.
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
| Read a package directory's own files (`togavsu-module.yaml`, `pending.yaml`, `migrations/`; see [What a package holds](#what-a-package-holds)) | `ReadDir(dir)`, `Files(fsys)` |
| Manifest checks | `ParseManifest(src)` |
| Install checks: unpacking, manifest, scopes, migration files, migration rules | `Check(files)`, `CheckPackage(raw)`, `CheckMigrations(m, ms)` |
| Every refusal at once, structured | `CheckAll(files)` (a `Refusals` list of `Refusal`) |
| What this core accepts | `Accepts()`, `Scopes()`, `KnownScope(scope)` |
| A package without the parts that wait on core changes | `Installable(files)` and `BuildInstallable(priv, files)` (an `InstallablePackage`: `Files`, `Held` in manifest order, `Pending`, `Unused`, and `UnusedRefusals()`), `ParsePending(src)`, `Unused(entries, held)`; `Pending.Core` is a `CoreItems` list with `Has(item)` |
| Write the pending list | `WritePending(entries)` (a whole list), `AddPending(src, entry)` (one entry appended in place, comments kept); both read the result back with `ParsePending` |
| Update checksum rule (no migration that ran may change or go missing) | `CheckUpdate(shipped, next)` against the previous release's shipped package, `CheckUpgrade(prev, next)`, `CheckChecksums(applied, ms)`, `Applied(ms)` |
| The package inside a signed package file | `ReadSignedFile(file)` |
| Installable package (gzip tar) | `Archive(files)` |
| Marketplace listing tar and its manifest | `BuildPackage(manifest, extra)`, `ReadManifest(pkg)` |
| Signed package file | `Build(priv, files)` (checks first), `SignFile(priv, pkg)` (signs bytes unchecked) |
| Keys | `ParseSeed(text)`, `KeyID(pub)`, `PublicKeyPEM(pub)`, `ParsePublicKeyPEM(text)`, `Trust(pub)`; `ProveKey(priv, proofMessage)` signs the marketplace's key proof (see [Submitting to the marketplace](#submitting-to-the-marketplace)) |
| Verification | `Digest(pkg)`, `Verify(pkg, digest, sig, keyID, trusted)`, `VerifyFile(file, trusted)` |
| Settings source check, the core's own AC-030 check (`settings.CheckModule`): no environment read outside a directory named `internal/bootstrap`, no numeric or duration configuration literal outside settings. The core's fixed-site allowances name only core functions, so a module names its fixed numbers as constants, as Matter Clock does | `CheckSource(root)` |
| The code the install API would answer with | `Code(err)`: `migration_refused`, `tampered_module`, `unsigned_module`, `validation` |

`Check` also refuses a scope the core's scope list doesn't hold (`Scopes()`, the list the
core checks a module's approved scopes against). The install API itself still reads scopes
only when the admin approves, so the kit tells you at package time what the core would
refuse at approval.

`Build` runs `Check` and refuses to sign a package `Check` refuses, or one that still holds
its `pending.yaml` (`pending_unbuilt`). `SignFile` signs whatever bytes it is given; use it
only to make a refused package on purpose in a test.

## Upgrading

### From core `3d9453e` or earlier

`Build` signed whatever files it was given there, unchecked. It now checks first and
refuses a package the core would refuse, or one holding `pending.yaml`. A build that signed
a refused package (to test the core's refusal, say) now fails: sign `Archive(files)` with
`SignFile` for that, and build a package with pending parts with `BuildInstallable`.

### From a pre-release pending kit to core `0e111ca`

Modules that pinned a commit of the pending-list branch before it merged (PR 57; its
branch commit `9b5f884`, say) see three signature changes:

| Before | Now |
| --- | --- |
| `Installable(files) (map[string][]byte, []Held, error)` | `Installable(files) (InstallablePackage, error)`: the files are `ip.Files`, the held parts `ip.Held`, plus `ip.Pending` and `ip.Unused` |
| `BuildInstallable(priv, files) ([]byte, []Held, error)` | `BuildInstallable(priv, files) ([]byte, InstallablePackage, error)`: the held parts are `ip.Held` |
| `Pending.Core string` (also `Held.Pending.Core`) | `Pending.Core CoreItems`, a `[]string`: one item or several. `Core.String()` joins them with " and "; `Core.Has("CORE-22")` asks for one; compare a list with `strings.Join(p.Core, ",")`, never `p.Core == "CORE-22"` |

```go
// before
files, held, err := modulekit.Installable(raw)
signed, held, err := modulekit.BuildInstallable(priv, raw)
if h.Pending.Core == "CORE-08" { ... }
// now
ip, err := modulekit.Installable(raw)        // ip.Files, ip.Held, ip.Unused
signed, ip, err := modulekit.BuildInstallable(priv, raw)
if h.Pending.Core.Has("CORE-08") { ... }
```

`Held` comes in manifest order (the order the parts are written, then held migrations by
file name), not the order the checks found them, so a test that pinned the old order needs
its expected list rewritten.

## What a package holds

A package holds exactly the files the core reads: `togavsu-module.yaml` at its root and
numbered migrations, `migrations/NNNN_name.sql`. The core refuses any other file at install
(`unknown_file`), and `Check`, `CheckAll` and `Build` refuse it the same way, so nothing
ships that no check read. `pending.yaml` lives beside the manifest in the package directory
(`package/pending.yaml`); it never ships, and a package holding it is refused like any other
extra file, with a hint to build with `Installable`.

`ReadDir` and `Files` read only those files from a package directory, plus `pending.yaml`:
they ignore everything else there (a `files.go` that embeds the directory, a README), and
names starting with `.` or `_`, as `go:embed` does. So `togavsu-module package -dir package`
ships the same files whatever else sits in the directory. A map you build yourself goes
through `Check` like any other, so an extra file in it is refused, not dropped.

`go:embed` of the package directory (`//go:embed togavsu-module.yaml pending.yaml
migrations`, or of the whole directory) embeds `pending.yaml`, and with a directory pattern
the `.go` file and anything else in it too. So read the embedded files with `Files(fsys)`,
never by listing the embed FS yourself, and build what ships with `Installable` or
`BuildInstallable`: anything that lists, archives or ships the embedded files on its own
would carry the pending list and the parts it holds back.

`VerifyFile` is the whole local-install check: the file's form and digest, signature and
signer, the signed manifest, then the install checks. Only `Archive` output installs, and
the marketplace takes the same signed bytes (see
[Submitting to the marketplace](#submitting-to-the-marketplace)); `BuildPackage` writes a
plain tar for marketplace listing tests only.

## cmd/togavsu-module

```sh
go run github.com/CharitonMedia/togavsu/cmd/togavsu-module check package
go run github.com/CharitonMedia/togavsu/cmd/togavsu-module check -json package
go run github.com/CharitonMedia/togavsu/cmd/togavsu-module check -installable package
go run github.com/CharitonMedia/togavsu/cmd/togavsu-module check -installable -strict -json package
go run github.com/CharitonMedia/togavsu/cmd/togavsu-module check -previous ../v1/package package
go run github.com/CharitonMedia/togavsu/cmd/togavsu-module check -installable -previous-file <id>-1.0.0.signed.json -previous-pub signing.pub package
go run github.com/CharitonMedia/togavsu/cmd/togavsu-module package -dir package -key signing.key -out <id>.signed.json
go run github.com/CharitonMedia/togavsu/cmd/togavsu-module package -installable -dir package -key signing.key -out <id>.signed.json
go run github.com/CharitonMedia/togavsu/cmd/togavsu-module package -installable -unsigned -dir package -out <new dir>
go run github.com/CharitonMedia/togavsu/cmd/togavsu-module verify -file <id>.signed.json -pub signing.pub
go run github.com/CharitonMedia/togavsu/cmd/togavsu-module key -key signing.key > signing.pub
go run github.com/CharitonMedia/togavsu/cmd/togavsu-module key -key signing.key -prove "<proof_message>"
go run github.com/CharitonMedia/togavsu/cmd/togavsu-module pending -dir package -field sidebar[*].section -core CORE-08 -reason "File the entry under Money."
go run github.com/CharitonMedia/togavsu/cmd/togavsu-module accepts
```

The key file holds a base64 Ed25519 seed, any 32 random bytes: make a throwaway one with
`openssl rand -base64 32 > signing.key` (keep the release key out of the repository).
`package` prints the PEM public key the admin adds as a trusted signing key, and
`key -key signing.key` prints only that PEM, so `> signing.pub` gives the file `verify -pub`
and `-previous-pub` take. `key -prove` signs the marketplace's key proof and nothing else.
`pending` adds one entry to `package/pending.yaml` (`AddPending`), creating it if missing;
`-core` takes one item or several separated by commas. `package` refuses to sign a package `check` refuses. Both
print every refusal, one line each, as `<file> <path or statement>: <code>: <message>`;
`check -json` prints `{ok, id, version, migrations, digest, refusals}` on stdout instead.
With `-installable`, `check` and `package` work on `Installable`'s files and print one
stderr line per held-back part with the core item it waits on, and one line per pending
entry that held nothing back; `check -json` adds `held` and `unused` (the entries that held
nothing back) to its result. Held-back parts print in manifest order. `-strict` (with `-installable`) makes an entry that holds
nothing back a `pending_unused` refusal: exit 1, and the refusal in `-json`, so CI notices
when a core item has landed and the entry can go. Under `-json` every failure, an
`Installable` refusal or an unreadable directory included, is the same `{ok, refusals}`
result. `package -unsigned -out <dir>` writes the checked files (with `-installable`,
`Installable`'s) to a new or empty directory instead of signing, so you can read exactly
what would ship; it takes no `-key`.

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
| `unknown_file` | a package file the core doesn't read (anything but the manifest and `migrations/`) | every one |
| `unknown_field` | a manifest key, at any depth, this core doesn't read | every one |
| `unknown_scope` | a scope not in `Scopes()` | every one |
| `core_reference` | a migration reference to a core relation a module can't link to | every one |
| `core_relation_missing` | a migration reference to a core relation modules may link to that this core's schema doesn't have | every one |
| `migration_statement` | any other refused migration statement | every one |
| `migration_crosscheck` | `data.extends` or `data.views` that the migrations don't create | every one |
| `manifest_invalid` | a manifest value the core refuses (the manifest checks) | every one, with two limits: the widget checks report their first, and a value of the wrong type (a list where a string goes, say) stops the manifest checks, since nothing past it decodes |
| `manifest_yaml`, `package_unreadable`, `migration_file` | the manifest, the archive or a migration file name can't be read | the first; nothing past it can be checked |
| `pending_invalid`, `pending_edit`, `pending_order`, `pending_unbuilt`, `pending_unused` | the pending list (next section); `pending_unused` only with `-strict` or `UnusedRefusals()` | each one |
| `upgrade_refused`, `upgrade_baseline` | the update check, and a previous version given by a source directory that held parts back | the first |
| `unreadable` | a file or directory the command was given can't be read (command only) | the first |

A package with no refusals passes the core's install check; `Check` confirms that by
running `CheckPackage` on the same bytes. A refusal `Installable` returns because no
pending entry covers it ends its message and error text with `(not covered by
pending.yaml)`, and says why when an entry names the place but can't hold that kind of
refusal.

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
- `core_items`: the `CORE-NN` items this core binary has built (CORE-36). Each entry is a
  heading in `CORE-CHANGES.md` or `CORE-CHANGES-2.md`.

`-strict` still tells you when a pending entry holds nothing back, which is when its
part's core item has landed.

## Building and signing before your core items land

A module often needs manifest fields, scopes or core tables that a numbered core change
(the CORE items in `CORE-CHANGES.md`) has not delivered yet. Keep those parts in the
package where they belong, list them in `pending.yaml` beside `togavsu-module.yaml` in the
package directory (`package/pending.yaml`), and build with `BuildInstallable` (or
`togavsu-module package -installable`). `Build`, and the core itself, refuse a package that
still holds `pending.yaml`. The pinned core then gets a
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
  - field: tabs[placement=client]
    core: CORE-10
    reason: The client portal's tabs.
  - migration: migrations/0004_trust_bank_map.sql
    core: CORE-22
    reason: Map each trust account to its own bank account.
  - migration: migrations/0005_refund_link.sql
    core: [CORE-22, CORE-24]
    reason: Link a refund to the core's payment refund.
```

Each entry names exactly one part, the core item it waits on (`core`: one item, or a list
such as `[CORE-22, CORE-24]` when it waits on several) and one line on what the part does
(`reason`). Each kind of entry holds back only the refusals it can cover; any other refusal
is an error, even inside a held part:

- `field`: a manifest path. Keys join with dots, list indexes go in brackets, `[*]` (or
  `[]`) is any index, and `[key=value]` is every list item whose `key` holds `value`
  (`tabs[id=pay]`, `tabs[placement=client]`; quote a value with spaces:
  `tabs[title="Pay now"]`). Paths name the manifest as written, before anything is held
  back, so `tabs[2]` and `tabs[3]` are the third and fourth tabs you wrote, however many
  other parts go; prefer `[key=value]`, which survives reordering. The entry covers a
  refusal at that path or inside it: `sidebar[*].icon` covers `sidebar[2].icon`, and
  `tabs[id=pay]` covers that tab's `placement` and holds back the whole tab. It covers
  unknown fields (`unknown_field`), refused values (`manifest_invalid`) and data
  cross-checks (`migration_crosscheck`), never a scope: name a scope in a `scope` entry.
- `scope`: a permission scope; it covers that scope's `unknown_scope`, and the whole
  permission entry is held back.
- `migration`: a whole migration file; it covers only the file's references to core
  relations (`core_reference`, `core_relation_missing`). Any other refusal in the file is an
  error now.

`Installable(files)` checks the package, removes each part a pending entry covers, and
checks again until nothing is refused, so one call holds back every covered part even where
one refusal shows only once another part is gone. It returns an `InstallablePackage`: the
files without `pending.yaml` and without the held-back parts (`Files`), the parts it held
back (`Held`, each with the entry, the file, the concrete path in the manifest as written
and the refusals), the parsed list (`Pending`) and the entries that held nothing back
(`Unused`). A part the core accepts is never removed, so you edit nothing when a core item
lands; delete the unused entries then (`-strict` makes them fail the check). `Installable` returns an error, a `Refusals` list, for every
refusal no entry covers, and when removing a part breaks what is left (for instance a held
migration creates a table a `data.extends` entry needs; hold that entry back too).

Fields and scopes are removed from the manifest text in place, so its comments and layout
stay as written. The edit is read back, and it must parse as exactly the manifest without
those parts, or `Installable` refuses with `pending_edit`. A held part's own comment lines
go with it: the comment lines directly above it at its indent, with no blank line between
(a comment block that starts the file stays). Removing the first key of a `- key: value`
list item moves the dash to the item's next key and keeps the item's comments; an item's
only key can't be removed, so hold back the item. Nor can a value the core requires (a
tab's `placement` or `path`, `version`, `container.port`): removing it leaves the same
refusal, so `Installable` stops with `pending_edit` naming the part; hold back the item
that holds it (`tabs[id=pay]`) instead.

Migrations are held back by whole file, never by statement. A migration a firm has run can
never change (`CheckUpgrade`), so a statement removed now and restored later would change
an applied migration and make the module impossible to update. A whole file that waits at
the end of the list is added later as a new migration, which an update allows. So put each
statement that waits on a core change in its own migration file, numbered after every
migration that installs today (for a foreign key to a table you already create, an
`ALTER TABLE ... ADD CONSTRAINT ... FOREIGN KEY ... REFERENCES core.<table> (id) ON DELETE
RESTRICT` in that file). `Installable` refuses with `pending_order` a held-back migration
numbered before one that installs.

A held-back migration comes back only when the core both lets modules link to each core
relation it references (`core_relations` in `Accepts()`) and has the relation in its schema
(`core_relation_missing` until then); the kit knows the core's schema from the core's own
migration list, with no database. It is also checked now as if those relations existed, so
another problem in the file (an `ON DELETE CASCADE` on the same reference, say) is refused
now with `(not covered by pending.yaml: held back for ...)`, not on the day the core item
lands.

While a foreign key to a core table is held back, nothing in the database stops your module
from storing an id that names no core row. Keep the data valid yourself until the
constraint lands: check each id through the core API when you store it (and clear or fix
rows whose record was deleted), or the later `ALTER TABLE ... ADD CONSTRAINT` fails on the
rows that break it and the update fails when it runs that migration.

Your tests should build the installable package, verify it and run the acceptance suite on
it, so they keep passing while core items land:

```go
signed, ip, err := modulekit.BuildInstallable(priv, files)
// ip.Held lists what waits and ip.Unused the entries that can go; err is nil only if
// every refusal is covered
c.Install(signed)
```

## Submitting to the marketplace

The marketplace (`marketplace/`, API in `marketplace/README.md`) lists signed packages. It
runs no install check and its operator reviews nothing: it checks only that a verified key
of yours signed the exact bytes and that the manifest has `id`, `name`, `version` and
`permissions`. `package` refuses to sign what the core would refuse, so submit only what
`package` built. The listing is named by the manifest's `id`; the first author to list an
id owns it, and a listed version never changes (a second upload of it is `409 conflict`),
so ship a fix as a new version.

The Togavsu marketplace has no public address yet (the core's `modules.marketplace_address`
default is the placeholder `togavsu-marketplace`). For a dry run, run one locally:
`go run github.com/CharitonMedia/togavsu/marketplace/cmd/marketplace -config market.json`
with `{"listen": "127.0.0.1:8090", "data_dir": "<dir>", "operator_token_file": "<file of
32 or more random bytes>", "secure_cookies": false}`. A core reaches it over http only on a
loopback host.

`togavsu-module` below is `go run github.com/CharitonMedia/togavsu/cmd/togavsu-module`.

```sh
M=http://127.0.0.1:8090                       # the marketplace
openssl rand -base64 32 > signing.key         # throwaway key; your release key in a real submission
togavsu-module package -installable -dir package -key signing.key -out documents.signed.json
togavsu-module key -key signing.key > signing.pub

# 1. An author account (password of 12 or more characters), then a session token.
curl -s -X POST $M/v1/authors -d '{"name":"Your Name","email":"you@example.com","password":"<password>"}'
T=$(curl -s -X POST $M/v1/sessions -d '{"email":"you@example.com","password":"<password>"}' | jq -r .token)

# 2. Register the public key and prove you hold it, once per key.
curl -s -X POST $M/v1/authors/me/keys -H "Authorization: Bearer $T" \
  -d "$(jq -n --rawfile pem signing.pub '{public_key_pem: $pem}')" > key.json
SIG=$(togavsu-module key -key signing.key -prove "$(jq -r .proof_message key.json)")
curl -s -X POST $M/v1/authors/me/keys/$(jq -r .key_id key.json)/verify -H "Authorization: Bearer $T" \
  -d "$(jq -n --arg s "$SIG" '{signature: $s}')"

# 3. Upload the signed package file as the body unchanged. The listing price comes from
#    the manifest's price (string or structured {amount_cents, currency, interval, note}).
curl -s -X POST $M/v1/authors/me/packages -H "Authorization: Bearer $T" \
  -H "Content-Type: application/json" --data-binary @documents.signed.json

# 4. The listing, and the package exactly as a firm's core will fetch it.
curl -s $M/v1/listings/documents
curl -s $M/v1/listings/documents/versions/1.0.0/package > served.json
togavsu-module verify -file served.json -pub signing.pub
```

A firm's admin then installs it from the marketplace (`POST /api/v1/modules` with
`{"source": "marketplace", "listing_id": "documents"}`), trusting the keys the marketplace
publishes with no local key, and approves its permissions as for a file install. A
`coretest` suite can do the same against the local marketplace: set
`modules.marketplace_address` with `PATCH /api/v1/settings/modules.marketplace_address`
`{"value": "http://127.0.0.1:8090"}` as `c.Admin`, then post the install.

## pkg/modulekit/forms

CORE-33. Import `github.com/CharitonMedia/togavsu/pkg/modulekit/forms`. The package is
types and helpers only: no core tables, no module schema. Intake and mo-estate-planning
store answers in their own schemas and call the kit to define, validate, render and
autosave forms.

| Need | Call |
| --- | --- |
| Definition model (tracks, screens, fields) | `Form`, `Track`, `Screen`, `Field`, `FieldType` (`AllFieldTypes`: the 16 types) |
| Audiences | `AudienceStaff`, `AudienceClient`, `AudiencePublic`; `Field.VisibleTo`, `VisibleFields` |
| Answers and "I don't know" | `Answers`, `Answer` (`Unknown`), `Address`, `Merge`, `MergeFormSubmit` (keeps masked on empty post; `ClearMaskedValue` clears; repeating rows match by `RowIDKey`/`__row`, never by index, including nested repeating groups), `ParseFormAnswers(form, values)` (schema-driven types for all 16 fields; projects `__present` into each repeating row so nested clears work; explicit `name.__unknown=0` clears Unknown), `UnknownToggleJS`, `RepeatingJS` (mints/keeps `__row` on add/reindex; reindexes only the group's `data-name` `[N]`, so nested groups leave outer/inner indexes alone) |
| Conditions (`eq`, `neq`, `in`, `exists`, `notExists`, `gt`/`lt`/`gte`/`lte`, count ops on repeating groups) | `Condition`, `Op*`, `Eval` |
| Validation | `Validate`, `ValidateScreen`, `Issues` |
| HTMX renderer | `RenderScreen(w, screen, answers, audience, RenderOptions)` — one `hx-post` on the `<form>` (`hx-include="this"`); booleans emit hidden `false` |
| Autosave + offline queue | Whole-form shape (every control + CSRF; masked untouched stripped): `AutosaveHandler` / `AutosaveHandlerForm` (CORS for `Origin: null`, typed `ParseFormAnswers`, empty masked keep stored), `AutosavePatch.Apply`, `DecodeAutosave`, `OfflineQueueJS` (memory fallback when `localStorage` throws; strips empty masked; replays urlencoded) |
| Masked fields | `SecretStore` (`PutSecret`/`GetSecret`), optional `CreateIfAbsentStore`, `HTTPSecretStore` (core PUT with `If-None-Match: *`), `EncryptMasked`, `DecryptMasked`, `SealAnswers` (skips already-sealed under the field key), `EnsureKey` / `LoadKey` (create-if-absent or put+re-read); ciphertext in answers (`CipherPrefix`), AES-256 key name in the module secret store |

```go
import "github.com/CharitonMedia/togavsu/pkg/modulekit/forms"

issues := forms.Validate(def, answers, forms.AudienceClient)
_ = forms.RenderScreen(w, screen, answers, forms.AudiencePublic, forms.RenderOptions{
    AutosaveURL: "/m/intake/api/autosave",
})
mux.Handle("/m/intake/api/autosave", forms.AutosaveHandlerForm(def, func(r *http.Request, p forms.AutosavePatch) (forms.Answers, error) {
    cur := load(r) // module-owned storage
    p.Apply(&cur)
    return cur, save(r, cur)
}))
```

## Acceptance suites with coretest

`coretest.New(t)` opens the core the release image runs (`core.Open`, every domain, the
admin page, the module runtime, the trust root) on a fresh database reached through a
non-superuser core role, as Compose sets it up. Stand-ins: the supervisor (deploy starts
the registered handler in process), SMTP mail (`c.Mail`, CORE-29), and Doc Tender markdown
renditions (`c.SeedMarkdownRendition` / `c.OpenMarkdownRendition`, CORE-36; public
`coretest.Rendition` / `coretest.Download` return types, no `internal/files` import) and an in-process document-converter stand-in (`c.Converter`, described below). The database comes from `TOGAVSU_TEST_DATABASE_URL`, a Postgres superuser URL;
`New` skips without it and fails in CI. The database, the core role, the module logins
and the servers are removed at cleanup.

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

## Core update 2d: background reads and callback work

No release or public version bump accompanies this change. Request permissions in the
signed manifest; the admin must approve them at install. The bearer remains bounded by
those approvals on REST and MCP. Background calls have no viewer, are audited under the
module id with source `service`, and sit outside every matter wall.

| Background read | Approved scope |
| --- | --- |
| Calendar event get/list | `calendar:read` |
| Note get/list | `notes:read` |
| File list (ordinary file gets retain their existing scope) | `files:read` |
| Deadline get/list | `deadlines:read` |
| Matter-party get/list, including `?contact_id=` to find a contact's matters | `matters:read` |
| Contact-address get/list | `contacts:read` |
| Firm profile, including `time_zone` | `firm:read` |
| Party-role and custom-field-definition get/list | `settings:read` |
| User directory | `users:directory` |

These reads use the existing `/api/v1` endpoints and their MCP tools. Each has a live
`modules.service_call_limit.<operationId>` setting, initially 120 calls per minute per
module. Directory rows include `work_email` for staff and modules. Client rows remain
only `id` and `name`. `?matter_id=` first checks the caller's matter access, then returns
only active users whose current roles and wall membership permit that matter.

For a walled record, use a context token for a viewer inside the wall. A service
principal sits outside every wall: gets answer `not_found`, and lists omit the records.
Delivered events grant no extra record access. No record response discloses wall membership.
The directory's `matter_id` filter applies the caller's access before selecting users;
a service principal can use it only for non-walled matters. Every module call, delegated
or background, is audited under the module id with its operation and requested record.
Delegated actions keep the user as `actor_id`; `module_id` records the module separately,
and timeline/feed rows show the person via the module. Background calls name the module
as the actor.

Under `mail:send`, a service principal can send to `to_contact_ids` with `matter_id`.
The core resolves only current primary addresses of parties on that accessible matter;
nonparties and contacts without an address are skipped. A walled matter stays unavailable
to a service send. Addresses never appear in responses. The audit/outbox records the
matter, and successful sends add `mail.sent` to its timeline.

Read own settings at `GET /api/v1/modules/{id}/settings`. With `settings:read` and an
approved `contracts:call` or `contracts:provide`, `GET /api/v1/settings` and
`GET /api/v1/settings/modules.contract_call_max_bytes` expose the applicable contract
runtime limit, without exposing other firm settings or secrets. To save setup values,
request `module_settings:write` and use `PUT /api/v1/modules/{id}/own-settings` with an
admin viewer context. It validates the manifest schema, audits changes, and refuses
another module's id or a staff/background caller. The administrative `/settings` write
still requires `modules:write`, which modules may never request.

Contract calls use a freshly signed forwarded context whose audience is the selected
provider and whose consumer/contract link is rechecked on delegation. A provider verifies
it through its own `context-tokens/verify` endpoint and presents it with its own bearer
on core calls. The consumer's original token is not a provider credential. Unlinking
invalidates forwarded delegation immediately. Provider events use the targeted envelope
in [the module API](../api/modules.md#core-brokered-contract-events), retain its id and data, and deliver only to its
named enabled, linked consumer; repeated ids do not enqueue duplicates. An oversized
provider response returns HTTP 413 with code `too_large`.

### Timestamped core webhooks and provider callbacks

The core signs every event, contract call and routed system mail in the v1.0.0 format:
`X-Togavsu-Signature: t=<unix>,v1=<hex>`, where the lowercase hex digest is
HMAC-SHA256 of the decimal timestamp, a dot, and the exact raw body bytes. It also sends
`X-Togavsu-Timestamp` with the same Unix seconds. Receivers compare the HMAC in constant
time and reject timestamps more than five minutes from their clock in either direction.
The authenticated timestamp prevents refreshing an expired signature. Deduplicate event
ids durably before applying effects: a valid request can repeat within the window, and
retries change the timestamp while keeping the event id. Empty secrets refuse signing,
verification and delivery.

`modulekit.SignTimestampedWebhook(secret, now, body)` returns the timestamped signature.
`headers := modulekit.SignWebhookHeaders(secret, now, body)` returns both headers
(`http.Header`), or nil for an empty secret. Copy them onto the request.
`modulekit.VerifyWebhook(secret, signature, body, now, 0)` accepts both `t=,v1=` and the
batch 2 `sha256=<hex>` raw-body HMAC. The timestamp in `t=,v1=` is checked and authenticated.
`modulekit.VerifyWebhookHeaders(secret, request.Header, body, now, 0)` also checks
`X-Togavsu-Timestamp` when present; it must match the signed timestamp for `t=,v1=`.
Zero tolerance means five minutes; a positive tolerance can only shorten it.

Every existing exported name and signature remains available. `SignWebhook` keeps its
batch 2 `sha256=<hex>` output. That compatibility form has no authenticated time; the
header-aware verifier checks its separate timestamp when present, but changing that
unsigned timestamp can bypass its freshness check. Use the timestamped form for new
senders. A bare `sha256=` signature remains accepted for existing string-API callers.
The legacy `sha256=` form carries no timestamp, so modules must deduplicate event ids durably.

Provider webhooks use their provider's own scheme. `ProviderHMAC(secret, body)` and
`VerifyProviderHMAC(secret, body, decodedSignature)` compute/check HMAC-SHA256.
`ProviderCRC(secret, challenge)` returns Zoom's hex HMAC for its `encryptedToken` field.
`ProviderCRCBase64(secret, challenge)` returns the separate `sha256=<base64>` convention;
it is not Zoom's format. All refuse an empty secret and leave provider-specific issuer,
audience and token checks to the receiver.

A Google Pub/Sub push may opt in per hook to receive its provider Authorization header:

```yaml
permissions:
  - scope: webhooks:authorization
    reason: Validate the provider's signed push identity.
inbound_webhooks:
  - id: pubsub
    path: /push
    forward_authorization: true
```

Only that declared hook with the approved scope forwards the header. It is never core
authentication. The module verifies the provider identity itself. Cookies and core
context, event, task and public-step credentials are stripped. Other hooks continue
stripping Authorization. Disabled modules receive nothing.

### Runtime pending criteria and test conversion

A manifest may install while a runtime behavior still waits on core. Name that behavior
explicitly instead of pretending an accepted field or scope was held back:

```yaml
pending:
  - runtime: processor.payment_principal
    core: CORE-23
    reason: The core does not yet accept this background payment workflow.
```

`Pending.Runtime`, `WritePending`, `AddPending` and `togavsu-module pending -runtime`
support this kind. It does not remove code, permissions or migrations. `Installable`
reports it in `Held` with `runtime_pending`; contributors can report that acceptance
criterion pending and run everything else. `RuntimeSupported(name)` and
`Accepts().runtime_criteria` describe granular implemented behaviors (unlike the broader
`core_items`). Once supported, the entry becomes unused: strict checks refuse it with
`pending_unused`. Delete it and run the runtime criterion. Unknown dotted names stay
pending; a misspelling does not prove an unimplemented feature. The kit's manifest and
migration inventory now reads the same Goose Up section the core applies, ignoring Down.

`coretest.New` supplies `c.Converter`, an in-process document-conversion stand-in. It
never reads or writes `/usr/local/bin`. `SetResult(extension, syntheticBytes)` supplies
an output, `Fail(error)` injects a failure (`nil` restores normal behavior), and `Calls()`
returns copied inputs/from/to. The default synthetic PDF supports conversion/link/access
acceptance, not rendering fidelity. Real file access, output checks, immutable versions,
audit and typed `conversion_of` links still run. Pinned LawCraft rendering remains a
separate integration criterion. Mail and Markdown rendition stand-ins remain available.

File purge retains immutable record/version metadata but refuses to erase bytes while
module RESTRICT links retain the file, naming each retaining module. Remove those links
before retrying. Module events now appear in both the matter timeline and Activities
Feed. For delegated calls the person stays `actor_id` and the module is recorded in
`module_id`; for background calls the module is the actor.

### Contact phone-row actions

Declare a contact phone-row item under the approved `contacts:read` permission:

```yaml
contact_phone_items:
  - id: call
    title: Call
    path: /call
```

The contact screen places it beside each readable phone. The core frames the module
page with the normal sandbox and viewer context, passing only `phone_id` and `contact_id`. The module reads the phone through the API. Opening the frame rechecks contact access. The agent equivalent is
`GET /api/v1/modules/contact-phone-items?phone_id=` (`listContactPhoneItems` on MCP),
with cursor pagination. Disabled or unapproved items disappear immediately. This adds
no contact claim to context tokens and no documents-list action slot.

## Core update 2e: authenticated delivery sender

Every core-to-module event, contract call and routed system mail now carries the
header field `X-Togavsu-Sender`: the pushing provider's module id for contract
events, the calling consumer's module id for contract calls, and `core` for
core-emitted events (including grants) and system mail. This is separate from
payload fields: `module` in a core event still names the recipient. Payload
`sender` or `provider_id` values are untrusted claims.

Core-only source and identity labels are reserved module IDs: `core`, `upload`,
`client_upload`, `editor`, `billing`, `drop_folder`,
`ui`, `api_token`, `module`, `service`, `system`, `api`, `manual`, `timer`, `import`.
Manifest validation, local/marketplace installation, catalog publication and contract linking refuse
them with a reserved-id error. Approved IDs, including `email`, `zoom` and
`importer`, remain valid for installs, links and authenticated senders even when
the same label appears in file provenance. Explicit `owner_module_id` determines
ownership.
The published `ModuleManifest.id` reservation list is checked against
`ReservedModuleID` by `TestModuleManifestReservedIDsContract`.

The core reads queued senders from `core.module_deliveries.created_by`, recorded
when the push was accepted. Historical empty values mean `core`. Retries retain
that identity and body even after provider links change. An unlink prevents new
pushes/calls; it does not reattribute a queued delivery to another provider.

The existing headers and exact v1 signature remain unchanged:

```text
X-Togavsu-Timestamp: <unix>
X-Togavsu-Signature: t=<unix>,v1=<hex>
X-Togavsu-Sender: <sender>
X-Togavsu-Sender-Signature: t=<unix>,v2=<hex>
```

Both digests are lowercase hex HMAC-SHA256 keyed by the recipient module's
nonempty webhook secret. Input bytes are UTF-8/ASCII prefixes followed by the
exact raw body, without a trailing newline:

- v1: `<unix>.<raw body>` (unchanged).
- v2: `v2.<unix>.<sender>.<raw body>` (the literal `v2.` separates signature versions).

`<unix>` is canonical decimal Unix seconds, identical in all three timestamp
positions. `<sender>` is `core` or a module id matching
`^[a-z0-9][a-z0-9-]{0,63}$`; it cannot contain a dot. The v2 signature lives in a
separate header because the 2d verifier requires exactly `t=…,v1=…` in the
original header. Modules pinned to 2d continue verifying that same delivery.

New receivers use
`sender, err := modulekit.VerifyWebhookSender(secret, request.Header, body, now, 0)`.
It requires valid timestamped v1 and v2 signatures, compares MACs in constant time,
and returns an identity only on success. Missing/duplicate sender or signature
headers, legacy signatures without binding, changed identity/body/timestamps,
empty secrets and expired signatures fail. Zero tolerance means five minutes in
either direction; a positive tolerance can only shorten that window.
`VerifyWebhookHeaders` keeps its existing behavior and does not authenticate
sender identity. Do not use an unverified sender to select a provider, matter or
record. Sender identity grants no access to core records. Deduplicate event ids
durably, including across retries, which receive fresh timestamps/signatures.

For synthetic sender-aware requests, use
`modulekit.SignWebhookSenderHeaders(secret, now, sender, body)`; it returns all
four headers, or nil for an empty secret or invalid/reserved module id (including
`core`). Use `modulekit.SignCoreWebhookHeaders(secret, now, body)` for a synthetic
core emission. In production, only a queued row with empty `created_by` uses the
core signer; a nonempty module sender of `core` is refused, including on retry.
In `coretest.Serve` handlers,
`sender, body, err := coretest.ReadDelivery(m, request)` reads and verifies using
the real deployment secret. `c.DeliverEvents()` uses the production queue and
sender signer for contract events, core events and retries.


## Core update 2f: intake and document generation

`Accepts().core_items` continues to include CORE-23/29/30/31/36. The granular
`runtime_criteria` now includes `module.permission_reasons`, `module.form_proxy`,
`file.trashed_delivery`, `calendar.busy_across_walls`, `conflict.named_people`,
`module.county_read`, `file.service_status`, `file.publication_author`,
`contract.document_generated`, `coretest.doc_tender` and `contract.processor_payment`.
Remove matching runtime-pending entries and run those acceptance cases.
`processor.payment_principal` still describes the separate unrestricted background
payment workflow and is not claimed by this pre-matter contract.

The admin Modules tab shows each permission's signed reason, escaped, beside its
pending/approved/declined decision. Module API permission rows include `reason`.
During update approval the candidate manifest supplies its declared reasons.
Urlencoded forms through `/m/` retain their exact bytes after session CSRF parsing,
including repeated keys and empty fields. The parser's size ceiling and CSRF
checks still apply; a refused cookie write never reaches the module.

For occupancy, read `GET /api/v1/calendar-events?view=busy&user_id=…&from_time=…&to_time=…`
with `calendar:read` (or MCP `listCalendarEvent` with the same query). Both timestamps
are RFC3339; use an ordered interval within the available-dates maximum range and
an active user. Standard pagination applies. Rows have **only** `starts_at`,
`ends_at` and `all_day`, even across walls. Do not use matter/kind filters in this
view. Ordinary calendar reads still omit hidden events and answer `not_found` on
walled detail gets. No event ids, titles, matter data or attendee identifiers are
exposed by the busy view.

`runConflictCheck` accepts optional `{names: ["Synthetic Person", "Another Person"]}`
in addition to the matter's parties and returns those names with the check.
Live Practice settings `practice.conflict_additional_names_max` (default 20) and
`practice.conflict_name_max_chars` (default 200 Unicode characters per name)
limit this input. Exceeding either returns `validation` on `names` before scoring
or storing a check, through REST, MCP and direct service calls.
Use `createConflictRecord` with `conflicts:write` to register each additional
person; a service call records the module as author and source `service`. Checks
need `conflicts:read`. Existing wall and attorney-only redaction applies to both.
`getCounty` now accepts a `settings:read` service call. Each new service operation
uses its own live `modules.service_call_limit.<operationId>` setting (default 120).

`FileBulkItem` (also single upload publication) accepts optional `status` and
`on_behalf_of_user_id`. The latter must name an active staff, attorney or admin
with write access to the actual matter/contact and `attorney_only` placement,
including contact-only and library publications. The caller's own placement check
runs first; hidden targets return the same `not_found` for any named author or
none. Once the caller may write, missing/inactive/outside-wall users, staff on an
attorney-only publication and other invalid authors all return the same
`validation` error on `on_behalf_of_user_id`. It records the file/version
human author without changing the calling module's source, audit identity or
independent access. Omitting it preserves ordinary authorship. `files:write`
services may update status on files their module published; the existing signed
fee-agreement executed callback remains available. A service still cannot reach
a walled or attorney-only file. `file.trashed` is now a real signed core event.
Every non-targeted `file.*` event identifying a file, including `file.updated`,
`file.downloaded`, `file.trashed` and `file.purged`, requires approved `events:read`,
a matching subscription and approved `files:read`. These events are queued only
when the module's service principal could read the file's current
placement at enqueue. This includes `file.purged` and earlier pending events after
purge: the check uses the retained row and ignores only its purge marker. Hidden,
walled and attorney-only files remain excluded. Normal file and byte reads still
return `not_found` after purge; a missing file service/row fails closed and other
visibility errors stop enqueue.
Targeted grants and books-reader filtering keep their existing behavior.

**Behavior change for existing subscribers:** a subscriber without approved
`files:read` stops receiving `file.updated` and `file.downloaded`. Those events
also stop for walled, attorney-only or non-matter (contact-only or library) files
that the subscriber's service principal cannot read. Viewer access on an
interactive call does not grant background event access.

File ownership is the first-version upload's explicit `owner_module_id`, retained
and committed with publication, independent of `source`. Adding a later version
confers no ownership. Ownership-refused service status writes answer `scope_denied`
for missing IDs and other modules' files alike. Receipt audit transitions are tied
to `module-processor/{consumer_module_id}/{intent_id}` without adding card data,
hosted tokens or amounts to audit.

### Batch 1 file event subscriptions

This inventory comes from the five batch 1 specs under `docs/modules/`. Required
permissions still need admin approval; this is a specification audit, with
real-package compatibility acceptance pending.

| Module spec | Subscriptions to `file.*` events | Spec requests `files:read`? |
| --- | --- | --- |
| [documents](documents/SPEC.md#3-manifest) | `file.updated`, `file.trashed` | Yes, required |
| [email](email/SPEC.md#3-manifest) | None; subscribes to `matter.*`, `contact.*` | Yes, required |
| [client-portal](client-portal/SPEC.md) | None; no subscriptions | Yes, required |
| [lawpay-docuseal](lawpay-docuseal/SPEC.md) | `file.background_authorized`, a targeted authorization grant; the ordinary-file guard does not apply | Yes, required |
| [quickbooks](quickbooks/SPEC.md#event-subscriptions) | None; subscribes to invoice/payment/expense events, `trust.entry_posted`, `contact.updated` | No |

Documents has the required read scope, but updates/trash events for files its
service principal cannot read are withheld. Its library templates and components
must not rely on viewer access to receive background events.

### Running Doc Tender jobs

`coretest.New` now supplies `c.DocTender`. Upload/publish a synthetic file through
the real API, set its category with the metadata API as the module would, then
call `c.RunDocTender()` (or `c.DocTender.Run()`) to drain currently due production
jobs. These helpers return the count processed. The real queue, extraction,
labeling, person locks, immutable Markdown renditions and failure persistence run.
Models, OCR/document reading and conversion are in-process stand-ins and never
call an outside provider or install a host program. Media extraction refuses
unsupported synthetic media rather than invoking host ffmpeg.

`SetCategory(key)` sets the model's proposed existing category; person-locked hints
stay untouched. `SetText(text)` supplies synthetic OCR/document-reader output;
native text/PDF/spreadsheet extraction still reads uploaded bytes. `Fail(err)`
injects an outside-service failure (`nil` restores success). `Calls()` returns a
copy of model requests (`Task`, `Check`, `FileID`, `MatterID`).
`RunOnce(ctx) (bool, error)` runs one due job and exposes its result; retries retain
the real queue schedule. Closed matters make no model call. This verifies module
integration and category retention; real model quality and rendering fidelity are
separate acceptance criteria.

### Document and payment contracts

The [targeted contract-event envelope](../contracts/README.md) remains accepted:
`id`, `contract`, `consumer_id`, `occurred_at`, `action`, `payload`.
For `documents.generate.v1`, the documented generated action is
`document.generated`. The core preserves that envelope and event id, deduplicates
per recipient, and signs its linked consumer delivery with the actual provider
sender. Verify with `ReadDelivery` or `VerifyWebhookSender`. Phone and workflow
envelopes retain their existing action prefixes. Payload data never grants core
record access. Do not substitute top-level `type`/`data` or a core-event envelope.

`modulekit.ProcessorPaymentContract`, `ProcessorPaymentWrite` and
`ProcessorPaymentResult` expose `processor.payment.v1`. Convert integer-cent settings
with `modulekit.Cents(amountInt64)` when constructing typed requests; no core internal
import is needed. Its binding
[definition and processor requirements](../contracts/README.md#processorpaymentv1)
cover authorize/recover, pending/declined refusal before paid-intake matter writes,
immutable intent data and recovery without a second authorization. The processor
module itself is external. Both modules need the declared contract, approved link
and payment scope. `intent_id` defines durable financial idempotency;
`Idempotency-Key` keeps its normal length bound but does not cache this contract
response. Every retry rechecks current scopes and links and returns the current
recovered intent result. A pre-matter approval is not an unrestricted payment-write grant; its consumer can use the scoped `record` continuation described below.

For synthetic processor acceptance, use `p := coretest.NewProcessor()` and
`c.Serve(providerID, p.Handler)` before installing its signed manifest. Declare
`payment_processor` operating and both contract operations; link the consumer as
admin. `p.DeclineNext("card_declined")` scripts a decline;
`p.LoseNextApprovalResponse()` records approval then answers 503, exercising real
core recovery. `p.Calls()` omits tokens and records verified consumer/operation/intent;
`p.Charges()` counts new approvals. No outside processor is called.


## Core update 2g: binding events, browser forms, occupancy and recorded receipts

The batch 2 binding contract event shape is
`{"contract":"<name>","event":"<type>","event_id":"<uuid>","occurred_at":"<RFC3339>","data":{…}}`.
Post it as the provider to `/api/v1/modules/contracts/{contract}/events`.
`modulekit.BindingContractEvent` exposes this shape. It reaches every enabled
consumer declaring the contract with approved `contracts:call` and an admin-approved
link to this provider. Core preserves the original fields and values; `event` may
not use any core event namespace. `event_id` must be a UUID and `data` an object.
Verify the 2e sender-bound delivery, then durably dedupe `event_id` permanently.
The old targeted `id`/`consumer_id`/`action`/`payload` format remains accepted and
unchanged, with its existing single-recipient and action-prefix rules. Both use
the same durable signed retry queue. See [contracts](../contracts/README.md).
For acceptance, `c.PushContractEvent(providerID, event)` uses the real core;
`coretest.ReadDelivery` reads and verifies the consumer's original envelope.

Authenticated frame GETs receive a module-bound HMAC token derived from the session in
`modulekit.FrameSessionCSRFHeader` (`X-Togavsu-Session-CSRF`). Pass it to
`forms.RenderOptions.CSRFToken`, or render an HTML-escaped hidden input named
`csrf` in a urlencoded form. The browser cookie jar suffices for its POST.
Missing, another module's or another session's token is refused. Core API writes
refuse frame tokens; modules never receive the core-wide CSRF token. Caller headers cannot select the
token; unauthenticated/bearer-only requests receive none. Never place it in a URL
or shared cache. Frame responses are `Cache-Control: no-store`. Module context
verification and public-page CSRF remain separate requirements.

`calendar-events?view=occupancy&from_time=…&to_time=…` is a service-only read under
`calendar:read` plus explicitly approved `calendar:occupancy`. Declare the latter
in manifest permissions with a nonempty reason; ordinary required/optional scope
approval rules apply. `calendar:read` alone returns 403 on REST and MCP. It includes every assigned/unassigned and walled event,
with **only** `starts_at` and `ends_at` per row. Use the normal cursor pagination
and existing interval range. User/matter/kind filters are refused. Existing
user-specific `view=busy` and ordinary wall-filtered reads retain their behavior.

After processor approval, create the matter/contact and call
`/api/v1/modules/contracts/processor.payment.v1/record` with
`modulekit.ProcessorPaymentRecord{IntentID: intent, MatterID: modulekit.ID(matter), ContactID: modulekit.ID(contact)}`.
Omit MatterID for a newly created contact-only payment. The consumer needs its
current `contracts:call` and `payments:write`, both modules' approvals and the link.
No viewer/context or money fields are accepted. Core binds the target once and
uses only the stored approval's operating amount, provider and reference, recording
one completed card payment. HTTP 200 repeats return that same payment, including
lost-response recovery, without any processor call or second charge. Other-module,
unknown, pending/declined/trust receipts and changed targets are refused. Matter
and contact walls are checked on replay too. See the full binding and refusal
rules in the [record contract](../contracts/README.md#record-the-approved-receipt).
`c.RecordProcessorPayment(consumerID, request)` exercises this through coretest.

`Accepts().core_items` still includes CORE-23/29/30/31/36. New granular
`runtime_criteria` are `contract.binding_events`, `module.frame_session_csrf`,
`calendar.firm_occupancy` and `processor.receipt_recording`. Remove corresponding
runtime pending entries and run the module criteria. Unrestricted background
payment writes (`processor.payment_principal`) remain unavailable.
