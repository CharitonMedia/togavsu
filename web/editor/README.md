# LawCraft for Togavsu (PRD 8.20)

The core's document editor is LawCraft at commit
`770a8fab12392485c0c2789fb254da7d9296df4d` (`internal/domain.LawCraftCommit`), from
`CharitonMedia/LawCraft`. This folder is Togavsu's small wrapper crate for the browser
build; `internal/editor` is everything the core does with it.

## What is here

- `Cargo.toml`, `Trunk.toml`, `index.html`, `src/main.rs`: the `togavsu-editor` crate.
  It is LawCraft's `wordcraft-web` app with the `wordcraft-law` and `wordcraft-law-ui`
  add-in installed and turned on. LawCraft's own host bridge (`host.rs`, `prefs.rs`,
  `save_fallback.rs`, `web/font_store.rs`) is used unchanged through `#[path]`.
- `web.patch`: the only change to LawCraft's `web.rs` (install and turn on the add-in,
  connect and drive the Togavsu bridge). `build.sh` applies it to the file at the pin;
  if it no longer applies, the build stops.
- `src/togavsu.rs`: `togavsu:run` (the edit page gives LawCraft the firm's details, the
  firm's court profiles and the matter's court through `law.firm.set`,
  `law.profiles.create`/`approve` and `law.courts.save`; nothing else is accepted) and
  `togavsu:profile` (a court profile saved in the editor goes back to Togavsu, unverified).
- `build.sh LAWCRAFT_CHECKOUT OUT_DIR`: checks the checkout is exactly the pinned commit
  and unmodified, builds `wordcraft-cli` against LawCraft's own `Cargo.lock` (`--locked`),
  places the wrapper in the checkout as `apps/togavsu-editor`, builds it with `trunk build
  --release`, and restores the lock. Output: `OUT_DIR/wordcraft-cli` and `OUT_DIR/editor/`.
  LawCraft's repository is not changed.
- `e2e/edit-page.mjs`: drives the edit page with the real build in headless Chromium
  (SwiftShader WebGL2); run by `internal/editor` `TestBrowserEditPageE2E`.

## Build and test locally (synthetic data only)

```sh
git clone https://github.com/CharitonMedia/LawCraft /tmp/lawcraft
git -C /tmp/lawcraft checkout 770a8fab12392485c0c2789fb254da7d9296df4d
rustup target add wasm32-unknown-unknown    # and trunk 0.21 on PATH
web/editor/build.sh /tmp/lawcraft /tmp/lawcraft-out

docker run -d --name tgpg -e POSTGRES_PASSWORD=synthetic -e POSTGRES_USER=togavsu -p 55432:5432 postgres:17
export TOGAVSU_TEST_DATABASE_URL='postgres://togavsu:synthetic@localhost:55432/postgres?sslmode=disable'
export TOGAVSU_WORDCRAFT_CLI=/tmp/lawcraft-out/wordcraft-cli TOGAVSU_EDITOR_DIST=/tmp/lawcraft-out/editor
go test -count=1 ./internal/editor/

# the browser round trip (needs Node, puppeteer-core and Chromium)
TOGAVSU_BROWSER_E2E=1 CHROME=/usr/bin/chromium PUPPETEER_CORE=/path/to/node_modules/puppeteer-core \
  go test -count=1 -run Browser -v ./internal/editor/
```

## Serving

The core serves `OUT_DIR/editor` at `/editor/` (`editor.Handler`, `editor.DistDir`) with a
Content Security Policy that allows WebAssembly compilation (`'wasm-unsafe-eval'`) and
trunk's inline loader by hash, and runs `OUT_DIR/wordcraft-cli` for editor runs,
conversions, previews and New Pleading. The edit page is `/files/{id}/edit`.

Moving the pin is an ordinary Togavsu change: update `LawCraftCommit`, rebuild, refresh
`web.patch` if `web.rs` moved, and rerun the tests above.
