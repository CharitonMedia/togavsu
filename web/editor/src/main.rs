//! LawCraft in the browser for Togavsu (PRD 8.20).
//!
//! This is LawCraft's `wordcraft-web` app at the pinned commit with two Togavsu
//! additions: the LawCraft add-in (`wordcraft-law` and `wordcraft-law-ui`) is installed and
//! turned on, and, in host mode (`?host=parent`), the edit page can run a few LawCraft
//! commands (`togavsu:run`), New Pleading opens filled from the matter (`caption.rs`), and the page hears about court profiles saved in the editor
//! (`togavsu:profile`). The app's own bridge (`wordcraft:ready`, `open`, `save`, `dirty`,
//! `failed`) is LawCraft's `host.rs`, used unchanged. `web.rs` is made at build time from
//! LawCraft's and `web.patch` (build.sh).
#![deny(clippy::unwrap_used, clippy::expect_used, clippy::panic, clippy::unimplemented, clippy::todo, clippy::unreachable)]

#[cfg(any(target_arch = "wasm32", test))]
#[path = "../../wordcraft-web/src/prefs.rs"]
mod prefs;
#[cfg(any(target_arch = "wasm32", test))]
#[path = "../../wordcraft-web/src/save_fallback.rs"]
mod save_fallback;

#[cfg(target_arch = "wasm32")]
#[path = "../../wordcraft-web/src/host.rs"]
mod host;
#[cfg(any(target_arch = "wasm32", test))]
mod caption;
#[cfg(any(target_arch = "wasm32", test))]
mod profiles;
#[cfg(target_arch = "wasm32")]
mod togavsu;
#[cfg(target_arch = "wasm32")]
mod web;

#[cfg(target_arch = "wasm32")]
fn main() {
    web::start();
}

#[cfg(not(target_arch = "wasm32"))]
fn main() {
    eprintln!("togavsu-editor only runs in the browser: build it with web/editor/build.sh");
}
