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

## Depending on the core

```sh
go get github.com/CharitonMedia/togavsu@<core commit or tag>
```

Pin the core release the module targets and move the pin when the module adopts a newer
core API. Use only the dependencies the core's `go.mod` already pins (initial modules PRD,
Code). Import `pkg/modulekit` from the module's `package` subcommand and
`pkg/modulekit/coretest` from its `_test.go` files only.

## pkg/modulekit

| Need | Call |
| --- | --- |
| Read package files (skips `.` and `_` names, as `go:embed` does) | `ReadDir(dir)`, `Files(fsys)` |
| Manifest checks | `ParseManifest(src)` |
| Install checks: unpacking, manifest, migration files, migration rules | `Check(files)`, `CheckPackage(raw)`, `CheckMigrations(m, ms)` |
| Update checksum rule (no migration that ran may change or go missing) | `CheckUpgrade(prev, next)`, `CheckChecksums(applied, ms)`, `Applied(ms)` |
| Installable package (gzip tar) | `Archive(files)` |
| Marketplace listing tar and its manifest | `BuildPackage(manifest, extra)`, `ReadManifest(pkg)` |
| Signed package file | `SignFile(priv, pkg)`, `Build(priv, files)` |
| Keys | `ParseSeed(text)`, `KeyID(pub)`, `PublicKeyPEM(pub)`, `ParsePublicKeyPEM(text)`, `Trust(pub)` |
| Verification | `Digest(pkg)`, `Verify(pkg, digest, sig, keyID, trusted)`, `VerifyFile(file, trusted)` |
| The code the install API would answer with | `Code(err)`: `migration_refused`, `tampered_module`, `unsigned_module`, `validation` |

`VerifyFile` is the whole local-install check: the file's form and digest, signature and
signer, the signed manifest, then the install checks. Only `Archive` output installs;
`BuildPackage` writes the plain tar the marketplace listing reads.

## cmd/togavsu-module

```sh
go run github.com/CharitonMedia/togavsu/cmd/togavsu-module check package
go run github.com/CharitonMedia/togavsu/cmd/togavsu-module check -previous ../v1/package package
go run github.com/CharitonMedia/togavsu/cmd/togavsu-module package -dir package -key signing.key -out <id>.signed.json
go run github.com/CharitonMedia/togavsu/cmd/togavsu-module verify -file <id>.signed.json -pub signing.pub
```

The key file holds a base64 Ed25519 seed; `package` prints the PEM public key the admin
adds as a trusted signing key. `package` refuses to sign a package `check` refuses. Exit
codes: 0 passed, 1 refused, 2 usage. The command never makes or stores a key; keep the
release key out of the repository, and generate throwaway keys at test time.

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
	signed, _ := modulekit.Build(priv, files)
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
