//! Court profiles saved in the editor go back to Togavsu (PRD 8.20: "a profile saved in the
//! editor is written back"). [`Watch`] compares LawCraft's profile store with the last good
//! look and reports the profiles created or changed since.
//!
//! A look that fails (LawCraft not answering `law.profiles.list` or `law.profiles.get` at
//! that moment) is no look at all: it neither reports nor becomes the baseline. Taking an
//! empty failed look as the baseline would make every bundled profile look new on the next
//! good one, and the page would write LawCraft's bundled profiles back as the firm's.

use std::collections::BTreeMap;

use serde_json::{Value, json};
use wordcraft_ui_egui::WordApp;

/// Each court profile as last seen, by id, as JSON without its verified flag.
type Seen = BTreeMap<String, String>;

#[derive(Default)]
pub struct Watch {
    known: Option<Seen>,
}

impl Watch {
    /// After the host page's own commands: what the store holds now is Togavsu's already,
    /// so only later changes are sent back. If the store can't be read, the next good look
    /// becomes the baseline instead.
    pub fn reset(&mut self, app: &mut WordApp) {
        self.known = snapshot(app);
    }

    /// The profiles created or changed since the last good look.
    pub fn changes(&mut self, app: &mut WordApp) -> Vec<Value> {
        let Some(seen) = snapshot(app) else { return Vec::new() };
        let mut out = Vec::new();
        if let Some(old) = &self.known {
            for (id, text) in &seen {
                if old.get(id) != Some(text)
                    && let Ok(profile) = serde_json::from_str::<Value>(text)
                {
                    out.push(profile);
                }
            }
        }
        self.known = Some(seen);
        out
    }
}

/// Every court profile in the editor's store, by id, as JSON without its verified flag (only a
/// person in Togavsu verifies a profile, so approving one in the editor is not sent back).
/// `None` when the store can't be read in full.
fn snapshot(app: &mut WordApp) -> Option<Seen> {
    let Ok(Value::Array(list)) = app.execute("law.profiles.list", json!({})) else { return None };
    let mut out = BTreeMap::new();
    for summary in list {
        let id = summary.get("id").and_then(Value::as_str)?;
        let mut profile = app.execute("law.profiles.get", json!({ "id": id })).ok()?;
        if let Some(o) = profile.as_object_mut() {
            o.remove("verified");
        }
        out.insert(id.to_string(), profile.to_string());
    }
    Some(out)
}

#[cfg(test)]
#[allow(clippy::unwrap_used, clippy::expect_used, clippy::panic)]
mod tests {
    use super::*;
    use wordcraft_doc::Document;
    use wordcraft_engine::Session;
    use wordcraft_law_ui::LawAddIn;
    use wordcraft_ui_egui::Services;

    /// LawCraft's profile store is process-wide; one test at a time.
    static LOCK: std::sync::Mutex<()> = std::sync::Mutex::new(());

    /// The browser's setup: profiles in memory only, the add-in installed, LawCraft on or off.
    fn app(law_on: bool) -> (WordApp, std::sync::MutexGuard<'static, ()>) {
        let guard = LOCK.lock().unwrap_or_else(|e| e.into_inner());
        wordcraft_law::set_profiles_dir(None);
        let mut s = Session::new(Document::new());
        s.prefs.modules.insert(wordcraft_law::MODULE.into(), law_on);
        let mut a = WordApp::new(s, Services::default());
        a.add_addin(Box::new(LawAddIn::default()));
        (a, guard)
    }

    fn turn_law_on(a: &mut WordApp) {
        let mut modules = serde_json::Map::new();
        modules.insert(wordcraft_law::MODULE.into(), true.into());
        a.execute("file.options", json!({ "modules": modules })).unwrap();
    }

    /// A new firm profile, made from a bundled one with its own id and name.
    fn new_profile(a: &mut WordApp, id: &str) -> Value {
        let list = a.execute("law.profiles.list", json!({})).unwrap();
        let first = list[0]["id"].as_str().unwrap().to_string();
        let mut p = a.execute("law.profiles.get", json!({ "id": first })).unwrap();
        p["id"] = json!(id);
        p["name"] = json!(format!("Synthetic {id}"));
        p
    }

    #[test]
    fn bundled_profiles_are_never_reported_after_a_failed_look() {
        let (mut a, _g) = app(false);
        assert!(a.execute("law.profiles.list", json!({})).is_err(), "with LawCraft off the store can't be read");
        let mut w = Watch::default();
        w.reset(&mut a);
        assert!(w.changes(&mut a).is_empty(), "a failed look reports nothing");
        assert!(w.known.is_none(), "a failed look is not the baseline");
        turn_law_on(&mut a);
        let bundled = a.execute("law.profiles.list", json!({})).unwrap();
        assert!(bundled.as_array().is_some_and(|l| !l.is_empty()), "LawCraft ships bundled profiles");
        assert!(w.changes(&mut a).is_empty(), "the first good look is the baseline, not a change");
        assert!(w.changes(&mut a).is_empty());
    }

    #[test]
    fn host_loaded_profiles_stay_and_editor_saves_are_reported_once() {
        let (mut a, _g) = app(true);
        let mut w = Watch::default();
        // The host page loads the firm's profile, then the baseline is taken.
        let firm = new_profile(&mut a, "togavsu-firm-profile");
        a.execute("law.profiles.create", firm).unwrap();
        w.reset(&mut a);
        assert!(w.changes(&mut a).is_empty(), "what the host loaded is not sent back");
        // A person saves a new profile in the editor.
        let saved = new_profile(&mut a, "togavsu-editor-profile");
        a.execute("law.profiles.create", saved).unwrap();
        let out = w.changes(&mut a);
        assert_eq!(out.len(), 1, "{out:?}");
        assert_eq!(out[0]["id"], "togavsu-editor-profile");
        assert!(out[0].get("verified").is_none(), "the verified flag is not sent back");
        assert!(w.changes(&mut a).is_empty(), "reported once");
        // Approving it in the editor only changes the verified flag: nothing to send.
        a.execute("law.profiles.approve", json!({ "id": "togavsu-editor-profile" })).unwrap();
        assert!(w.changes(&mut a).is_empty(), "approval in the editor is not sent back");
    }
}
