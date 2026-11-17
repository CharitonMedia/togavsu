# Desk fix: a marketplace you can browse and install from

Patrick opened Admin > Modules and marketplace on his install and found nothing to install:
alerts, "No modules installed", a file upload and the replaced-domains table. There is no
catalog to browse, the core cannot reach any marketplace (the default address
`togavsu-marketplace` is a placeholder and plain http is refused off loopback), the
marketplace upload refuses the kit's own signed package file, and listings ignore the
manifest's price. This fix closes all four. Two lanes build it in parallel; their owned
paths do not overlap.

## Lane A: the catalog in the core (desk-fix-marketplace-catalog)

1. **Catalog section.** Admin > Modules and marketplace opens with a card headed
   "Marketplace", above Marketplace alerts. It is server rendered (works without
   JavaScript) and reads the configured marketplace through the existing client
   (`internal/marketplace`, the same calls behind `GET /api/v1/marketplace/listings`).
   - A search box (`q`, GET, keeps the tab) and the listings, one row or card each:
     name, version, author, price, the permissions it asks for (scope plus its reason,
     inside a closed `<details>` labelled "Permissions (N)"), and the operator's warning
     when the listing is flagged. Pulled listings never show.
   - Price: the listing's price text; when that is empty, the manifest's `price`; when
     both are empty, "Free".
   - Each listing has one button. Not installed: **Install**. Installed at the listed
     version: a muted "Installed" with no button. Installed at another version:
     **Update to X.Y.Z**, which runs the existing marketplace update path.
   - **Install** posts (CSRF, admin only) to a new route under `ModulesActionPrefix`
     that calls the existing marketplace install path (`source: marketplace`,
     `listing_id`). On success the page returns to the Modules tab with the module in
     Installed modules, where its permission approval form already shows. Every failure
     is one plain sentence in an alert at the top of the card, naming the module by its
     name, never a stack of error codes.
   - When the marketplace address is the placeholder, not set, refused, or unreachable,
     the card shows one plain sentence: "The marketplace isn't reachable at ADDRESS." plus
     "Set Marketplace Address on the Settings page's Modules tab." with that as a link to
     `/admin?tab=settings&...` (whatever the existing settings link to the Modules settings
     tab is). Install from a file stays available below it.
   - Phone width: add phone-width UI QC entries (Width 390) in
     `internal/dashboard/compose/qc_pg_test.go` for the Modules tab with a catalog of three
     listings; no horizontal overflow.
2. **Reaching a marketplace on the firm's own Docker network.** `marketplace.BaseURL`
   allows plain http on a loopback host and also on a single-label host name (no dot, not
   an IP address), which is a Docker service name on the install's own network such as
   `togavsu-marketplace`. Everything else still needs https. Tests both ways, and update the
   comment and the setting's help text ("Marketplace the firm checks: an https address, or
   http://NAME:PORT for a marketplace running beside Togavsu.").
3. Change nothing else on the page. API and OpenAPI only if a new admin JSON route is
   added; the existing `GET /api/v1/marketplace/listings` stays as it is.
4. Tests: catalog rendering against an `httptest` marketplace (listings, price fallback,
   flagged warning, installed and update states, unreachable message), the install route
   (admin only, CSRF, success lands in Installed modules with permissions pending, a failure
   shows the plain sentence), and `BaseURL`.

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
   `marketplace/Dockerfile`; state in a named volume; the operator token generated once by
   the existing bootstrap secrets step into its own secret (at least 32 random bytes) and
   mounted as `operator_token_file`; listens on 8090 on the internal network only, no
   published port. Document in `deploy/` and `marketplace/README.md`: start it, publish
   with `togavsu-module publish -market http://togavsu-marketplace:8090` (run on the same
   network), and set Marketplace Address to `http://togavsu-marketplace:8090`.
5. Update `marketplace/README.md`, `docs/modules/MODULE-KIT.md` (Submitting to the
   marketplace: the `jq` step goes away; `publish` is the one-command path) and mark the
   upload-body and price rows of `docs/modules/CONTRIBUTOR-GAPS.md` fixed.
6. Tests: the upload with the kit's full file, with a wrong digest, with an unknown field;
   price fallback and override; `publish` end to end (new author, existing author, key
   already verified, already listed, conflicting bytes); compose config validates with and
   without the profile (`docker compose config` in the existing setup checks if they run
   compose checks).

## Done

Both lanes merged; a kit-built signed file goes into the marketplace with one command, shows
in the core's catalog with its price, installs from the Install button, and the same file
installs from Install from a file when its key is trusted.
