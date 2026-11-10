//! Togavsu's additions to the host bridge, beside LawCraft's own (`host.rs`). Same rule as
//! that bridge: only the parent window on the page's own origin is heard or answered.
//!
//! - `togavsu:run` (from the parent): `{ id, commands: [{ id, parameters }] }`. The edit page
//!   gives LawCraft the firm's details (`law.firm.set`), the firm's court profiles
//!   (`law.profiles.create`, and `law.profiles.approve` for the ones a person approved in
//!   Togavsu) and the matter's court (`law.courts.save`) when the editor opens. No other
//!   command is accepted here. The answer is `togavsu:result` `{ id, results, error }`.
//! - `togavsu:profile` (to the parent): `{ profile }` whenever a court profile is created or
//!   changed in the editor, so Togavsu stores it (unverified until a person approves it).

use std::cell::RefCell;
use std::collections::BTreeMap;
use std::rc::Rc;

use serde_json::{Value, json};
use wasm_bindgen::JsCast as _;
use wasm_bindgen::closure::Closure;
use wordcraft_ui_egui::WordApp;

/// The commands the host page may run in the editor.
const HOST_COMMANDS: &[&str] = &["law.firm.set", "law.profiles.create", "law.profiles.approve", "law.courts.save"];

// ponytail: court profiles saved in the editor are found by comparing LawCraft's store once
// a second. Ceiling: up to a second's delay, and a get per profile per check. Upgrade: a
// LawCraft hook for profile saves offered upstream. Trigger: more than 50 firm profiles,
// or the check showing in frame times.
/// How often, in milliseconds, the editor looks for court profiles saved in it.
const PROFILE_CHECK_MS: f64 = 1000.0;

/// Turn LawCraft on, as File › Options › Add-ins does.
pub fn enable_law(app: &mut WordApp) {
    let mut modules = serde_json::Map::new();
    modules.insert(wordcraft_law::MODULE.into(), true.into());
    if let Err(e) = app.execute("file.options", json!({ "modules": modules })) {
        log::error!("LawCraft could not be turned on: {e}");
    }
}

struct Job {
    id: f64,
    commands: Vec<(String, Value)>,
}

pub struct Extension {
    parent: web_sys::Window,
    origin: String,
    queue: Rc<RefCell<Vec<Job>>>,
    /// Each court profile as last seen, by id, without its verified flag.
    known: Option<BTreeMap<String, String>>,
    last_check: f64,
}

impl Extension {
    /// `None` outside a same-origin frame, as for LawCraft's bridge.
    pub fn connect(ctx: egui::Context) -> Option<Self> {
        let window = web_sys::window()?;
        let parent = window.parent().ok().flatten()?;
        if js_sys::Object::is(&parent, &window) {
            return None;
        }
        let origin = window.location().origin().ok()?;
        if origin == "null" || parent.location().origin().ok()? != origin {
            return None;
        }
        let queue: Rc<RefCell<Vec<Job>>> = Rc::default();
        let (q, from, at) = (queue.clone(), parent.clone(), origin.clone());
        let on_message = Closure::<dyn FnMut(web_sys::MessageEvent)>::new(move |event: web_sys::MessageEvent| {
            if event.origin() != at || !event.source().is_some_and(|s| js_sys::Object::is(&s, &from)) {
                return;
            }
            let Some(job) = job(&event.data()) else { return };
            q.borrow_mut().push(job);
            ctx.request_repaint();
        });
        window.add_event_listener_with_callback("message", on_message.as_ref().unchecked_ref()).ok()?;
        // The listener lives as long as the page.
        on_message.forget();
        Some(Self { parent, origin, queue, known: None, last_check: 0.0 })
    }

    /// Run queued host commands, then report court profiles that changed in the editor.
    pub fn tick(&mut self, app: &mut WordApp) {
        let jobs = std::mem::take(&mut *self.queue.borrow_mut());
        if !jobs.is_empty() {
            for job in jobs {
                let mut results = Vec::new();
                let mut error = None;
                for (id, params) in job.commands {
                    if !HOST_COMMANDS.contains(&id.as_str()) {
                        error = Some(format!("{id} is not a command the host page may run"));
                        break;
                    }
                    match app.execute(&id, params) {
                        Ok(v) => results.push(v),
                        Err(e) => {
                            error = Some(format!("{id}: {e}"));
                            break;
                        }
                    }
                }
                self.post(&json!({ "type": "togavsu:result", "id": job.id, "results": results, "error": error }));
            }
            // What the host loaded is Togavsu's already; only later changes are sent back.
            self.known = Some(snapshot(app));
            return;
        }
        let now = js_sys::Date::now();
        if now - self.last_check < PROFILE_CHECK_MS {
            return;
        }
        self.last_check = now;
        let seen = snapshot(app);
        if let Some(old) = &self.known {
            for (id, text) in &seen {
                if old.get(id) != Some(text)
                    && let Ok(profile) = serde_json::from_str::<Value>(text)
                {
                    self.post(&json!({ "type": "togavsu:profile", "profile": profile }));
                }
            }
        }
        self.known = Some(seen);
    }

    fn post(&self, message: &Value) {
        let Ok(value) = js_sys::JSON::parse(&message.to_string()) else { return };
        if let Err(e) = self.parent.post_message(&value, &self.origin) {
            log::warn!("couldn't reach the host page: {e:?}");
        }
    }
}

/// A `togavsu:run` message as a job.
fn job(data: &wasm_bindgen::JsValue) -> Option<Job> {
    let kind = js_sys::Reflect::get(data, &"type".into()).ok()?.as_string()?;
    if kind != "togavsu:run" {
        return None;
    }
    let id = js_sys::Reflect::get(data, &"id".into()).ok().and_then(|v| v.as_f64()).unwrap_or(0.0);
    let commands = js_sys::Reflect::get(data, &"commands".into()).ok()?;
    let text = js_sys::JSON::stringify(&commands).ok()?.as_string()?;
    let Value::Array(list) = serde_json::from_str::<Value>(&text).ok()? else { return None };
    let commands = list
        .into_iter()
        .filter_map(|c| {
            let id = c.get("id")?.as_str()?.to_string();
            let params = c.get("parameters").cloned().unwrap_or_else(|| json!({}));
            Some((id, params))
        })
        .collect();
    Some(Job { id, commands })
}

/// Every court profile in the editor's store, by id, as JSON without its verified flag (only a
/// person in Togavsu verifies a profile, so approving one in the editor is not sent back).
fn snapshot(app: &mut WordApp) -> BTreeMap<String, String> {
    let mut out = BTreeMap::new();
    let Ok(Value::Array(list)) = app.execute("law.profiles.list", json!({})) else { return out };
    for summary in list {
        let Some(id) = summary.get("id").and_then(Value::as_str) else { continue };
        if let Ok(mut profile) = app.execute("law.profiles.get", json!({ "id": id })) {
            if let Some(o) = profile.as_object_mut() {
                o.remove("verified");
            }
            out.insert(id.to_string(), profile.to_string());
        }
    }
    out
}
