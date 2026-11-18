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

# 3. Upload the signed package: three fields of the signed package file (the endpoint
#    refuses its other fields). Add "price_note" to set the listing's price text; the
#    manifest's price is not read.
jq '{package_base64, signature, key_id}' documents.signed.json \
  | curl -s -X POST $M/v1/authors/me/packages -H "Authorization: Bearer $T" -d @-

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
| Answers and "I don't know" | `Answers`, `Answer` (`Unknown`), `Address`, `Merge`, `MergeFormSubmit` (keeps masked on empty post; `ClearMaskedValue` clears), `ParseFormAnswers` (explicit `name.__unknown=0` clears Unknown), `UnknownToggleJS` |
| Conditions (`eq`, `neq`, `in`, `exists`, `notExists`, `gt`/`lt`/`gte`/`lte`, count ops on repeating groups) | `Condition`, `Op*`, `Eval` |
| Validation | `Validate`, `ValidateScreen`, `Issues` |
| HTMX renderer | `RenderScreen(w, screen, answers, audience, RenderOptions)` |
| Autosave + offline queue | One field-level shape (field value + CSRF; masked untouched sends nothing): `AutosaveHandler` / `AutosaveHandlerForm` (CORS for `Origin: null`, empty masked keep stored), `AutosavePatch.Apply`, `DecodeAutosave`, `OfflineQueueJS` (memory fallback when `localStorage` throws; skips empty masked) |
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
`coretest.Rendition` / `coretest.Download` return types, no `internal/files` import) without
converters. The database comes from `TOGAVSU_TEST_DATABASE_URL`, a Postgres superuser URL;
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
