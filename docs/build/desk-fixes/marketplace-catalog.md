# Desk fix: a marketplace you can browse and install from

Patrick opened Admin > Modules and marketplace on his install and found nothing to install:
alerts, "No modules installed", a file upload and the replaced-domains table. A Marketplace
card does exist (`web/templates/admin/admin.html`, `#marketplace`, filled by
`internal/core/modules_admin.go`, installing through the `install-listing` action), but it
sits at the bottom of the tab and shows listings only after a search, so it reads as empty.
Behind it, the core cannot reach any marketplace (the default address
`togavsu-marketplace` is a placeholder and plain http is refused off loopback), the
marketplace upload refuses the kit's own signed package file, and listings ignore the
manifest's price. This fix closes all of these. Two lanes build it in parallel; their owned
paths do not overlap.

## Lane A: the catalog in the core (desk-fix-marketplace-catalog)

1. **Extend the existing Marketplace card; add no second card or route.** Move the
   `#marketplace` card to the top of the Modules tab, above Marketplace alerts, and change it
   in place. It stays server rendered and keeps using `m.x.Market.Listings` and the existing
   `install-listing` action (`svc.InstallFromMarketplace`) and their tests.
   - Show every listing when the search box is empty (browse), and the matches when `q` is
     set. Pulled listings never show.
   - Each listing: name, version, author, price, the operator's warning when flagged, and
     the permission scopes it asks for inside a closed `<details>` labelled
     "Permissions (N)". Listings carry scopes only; do not change the listing contract or
     fetch packages to show reasons here (the reasons show at approval after install).
   - Price: the listing's price text; when that is empty, the manifest's `price`; when both
     are empty, "Free". Add the manifest price to what the core reads from a listing if it
     is not already there.
   - One button per listing. Not installed: **Install** (the existing form). Installed at
     the listed version: a muted "Installed" and no button. Installed at another version:
     **Update to X.Y.Z**, posting to the existing module update action with that version.
   - Errors are one plain sentence in an alert at the top of the card, naming the module by
     its name, never raw error text. `BrowseError` stops showing `err.Error()`.
   - When the marketplace address is the placeholder, not set, refused or unreachable, the
     card shows: "The marketplace isn't reachable at ADDRESS." and "Set Marketplace Address
     in Settings, on the Modules tab." with that as a link to the settings page's Modules
     tab. Install from a file stays available.
   - Phone width: add phone-width UI QC entries (Width 390) in
     `internal/dashboard/compose/qc_pg_test.go` for the Modules tab with three listings; no
     horizontal overflow.
2. **Reaching a marketplace on the firm's own Docker network.** `marketplace.BaseURL`
   allows plain http on a loopback host and also on a single-label host name (no dot, not
   an IP address), which is a Docker service name on the install's own network such as
   `togavsu-marketplace`. Everything else still needs https. Tests both ways, and update the
   comment and the setting's help text ("Marketplace the firm checks: an https address, or
   http://NAME:PORT for a marketplace running beside Togavsu.").
3. Change nothing else on the page. `GET /api/v1/marketplace/listings` keeps its shape; if
   it gains the manifest price field, update OpenAPI and the contract tests with it.
4. Tests: extend the existing Modules tab and install-listing tests: browse with an empty
   search, price fallback, flagged warning, installed and update states, the unreachable
   message, a failed install showing the plain sentence, a successful install landing in
   Installed modules with permissions pending, and `BaseURL`.

## Lane B: the marketplace takes the kit's files, and a publish command (desk-fix-marketplace-publish)

1. **Upload takes the kit's signed package file as is.** `POST /v1/authors/me/packages`
   accepts the whole file that `togavsu-module package` writes (`package_base64`,
   `signature`, `key_id`, `digest`, `listing_id`, `manifest` and any other field the kit
   writes), plus the optional `price_note`. When `digest` is present it must match the
   package bytes; a mismatch is refused (`digest_mismatch`). Any field the kit does not
   write is still refused. Keep the old three-field body working.
2. **Price defaults to the manifest.** When the upload has no `price_note`, the listing's
   price is the manifest's `price` (trimmed, bounded like a note). A `price_note` still
   overrides it. The listing page and `GET /v1/listings` show it.
3. **`togavsu-module publish`.** A new kit subcommand that does the whole submission:
   `togavsu-module publish -market URL -key signing.key -name NAME -email EMAIL
   -password-file FILE FILE.signed.json...`. It creates the author account if it does not
   exist (an existing one signs in), registers the key and proves it if it is not verified
   yet, and uploads each file. A version already listed with the same digest counts as done
   (prints "already listed"); a version listed with different bytes is an error. It prints
   one line per file: module id, version, listed or already listed. Add `Publish` to
   `pkg/modulekit` so the command is a thin wrapper, with tests against an in-process
   marketplace.
4. **A marketplace beside Togavsu in Docker.** Add a `togavsu-marketplace` service to
   `compose.yaml` under the compose profile `marketplace`, so a normal install does not run
   it and `docker compose --profile marketplace up -d` does. Image built from
   `marketplace/Dockerfile`; state in a named volume `marketdata` mounted at the image's own
   `/var/lib/togavsu-marketplace` (created there owned by 65532, the user the image runs
   as); listens on 8090 on the internal network only, no published port.
   - The operator token: the existing bootstrap secrets step writes it once (at least 32
     random bytes, never overwritten) to `/marketsecrets/operator_token` on a new volume
     `marketsecrets`, which only the bootstrap service and `togavsu-marketplace` mount. The
     file is `root:65532` mode 0640 and the directory 0750, so the marketplace (uid 65532)
     can read it and nothing else can, the same way bootstrap already handles appsecrets. Never into `appsecrets` or any volume
     the core, supervisor or caddy mount. Keep `scripts/ci/check-compose.py` passing, and
     add a check there that no service but those two mounts `marketsecrets`.
   - The config: a committed `deploy/marketplace/config.json` (`listen` 0.0.0.0:8090,
     `data_dir` /var/lib/togavsu-marketplace, `operator_token_file` /marketsecrets/operator_token,
     `secure_cookies` false) bind-mounted read-only at
     `/etc/togavsu-marketplace/config.json`, which `marketplace/cmd/marketplace` reads.
   - Document in `deploy/` and `marketplace/README.md`: start it, publish with
     `togavsu-module publish -market http://togavsu-marketplace:8090` (run on the same
     network), and set Marketplace Address to `http://togavsu-marketplace:8090`.
5. Update `marketplace/README.md`, `docs/modules/MODULE-KIT.md` (Submitting to the
   marketplace: the `jq` step goes away; `publish` is the one-command path) and mark the
   upload-body and price rows of `docs/modules/CONTRIBUTOR-GAPS.md` fixed.
6. Tests: the upload with the kit's full file, with a wrong digest, with an unknown field;
   price fallback and override; `publish` end to end (new author, existing author, key
   already verified, already listed, conflicting bytes). In `scripts/ci/test-bootstrap.sh`,
   map `/marketsecrets` to a temp directory like the other secret paths, and check the token
   is at least 32 bytes, is not overwritten on a second run, and has owner group 65532 and
   modes 0640/0750. In `scripts/ci/check-compose.py`, check the marketplace service is only
   under the `marketplace` profile, mounts `marketsecrets` read-only and `marketdata` at
   `/var/lib/togavsu-marketplace`, and publishes no port, and that no other service mounts
   `marketsecrets`. In `scripts/ci/test-compose-first-run.sh`, also start the stack with
   `--profile marketplace` and require `GET /v1/listings` on the marketplace to answer 200
   from inside the network (a startup check, so a permissions mistake fails CI).

## Done

Both lanes merged; a kit-built signed file goes into the marketplace with one command, shows
in the core's catalog with its price, installs from the Install button, and the same file
installs from Install from a file when its key is trusted.
