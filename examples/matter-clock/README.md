# Matter Clock (sample module)

Matter Clock is the sample module of PRD section 11. It is test scaffolding for the
module interface and does not ship in the release.

- **Widget** "Hours this month" (`/widget`): the viewer's matters with hours worked this
  month. Its click-through opens the module's `hours` tab.
- **Tabs**: `hours` on the home screen and `matter` ("Hours and target") on the matter
  screen.
- **Help guide**: one page under the "Matter Clock" heading, covering the widget and tab.
- **Data**: migration `0001_matter_targets.sql` creates `matter_targets` in the module's
  own schema (`mod_matter_clock`), keyed by and linked to `core.matters(id)` with
  `ON DELETE RESTRICT`. The manifest declares it as the `targets` extension of matters
  (read with `GET /api/v1/matters/{id}?include=matter-clock.targets`) and publishes the
  `monthly_targets` view (read by another module under
  `views:matter-clock.monthly_targets:read`).
- **Events**: subscribes to `time_entry.*` and `matter.created`; `/events` checks the
  webhook signature and records each event once in `received_events`.

The module reaches core records only through the core API, with its scoped token plus
the frame's context token, so the core answers exactly what that viewer may see. It reads
and writes only its own schema through its own database login.

## Build, sign and install

```sh
# From the repository root. The key file holds a base64 ed25519 seed (synthetic test key).
go run ./examples/matter-clock/cmd/matter-clock package -key signing.key -out matter-clock.tar.gz
docker build -f examples/matter-clock/Dockerfile -t togavsu-matter-clock .
```

The admin adds the printed PEM public key as a trusted signing key (Modules tab, or
`POST /api/v1/trusted-signing-keys` with `name` and `public_key_pem`), then installs
`matter-clock.tar.gz` from a local file on the Modules tab (or `POST /api/v1/modules`
with the file as `package`) and approves its permissions. The manifest's image digest is
a placeholder; set it to the digest of the image you built before signing.

The automated acceptance tests in `internal/modules/*_pg_test.go` build and sign this
package with a generated key, install it on a running core against Postgres, and drive
its widget, tabs, include reads, events, updates and uninstall.
