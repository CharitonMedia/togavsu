//! New Pleading from the matter (PRD 8.20: "the page gives LawCraft ... the matter's court,
//! county, case number and parties ... so a pleading's caption fills from the matter").
//!
//! LawCraft at the pin has no command that seeds its New Pleading window: law-ui opens it
//! as `PleadingForm::default()`, and only the firm block (`law.firm.set`) and saved courts
//! (`law.courts.save`) reach it. law-ui's form is public, so the host page sends
//! `togavsu.pleadings.defaults` on open and [`Seeder`] fills the window's court type,
//! county, case number and both sides' parties and roles each time New Pleading opens.
//! What a person then types stays theirs: a window is filled once, when it opens. Edit
//! Pleading (the open pleading's own values) is never touched.
//!
//! Parameters are `law.pleadings.new`'s own fields: `{ courtType?, county?, caseNumber?,
//! firstSide?: { names: [{ name, client? }], role }, secondSide?: (same), weRepresent?:
//! "first" | "second" }`. Anything else is refused.

use serde_json::{Map, Value, json};
use wordcraft_law_ui::LawAddIn;
use wordcraft_law_ui::pleading::{PartyRow, PleadingForm, SideForm};
use wordcraft_ui_egui::WordApp;

/// The host command that sets New Pleading's defaults.
pub const COMMAND: &str = "togavsu.pleadings.defaults";

/// Most names on one side (`law.pleadings.new`'s limit).
const MAX_NAMES: usize = 20;
/// The roles New Pleading lists for each side; any other is typed in as Other.
const FIRST_ROLES: [&str; 2] = ["Petitioner", "Plaintiff"];
const SECOND_ROLES: [&str; 2] = ["Respondent", "Defendant"];

/// One side of the matter's caption.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct Side {
    pub role: String,
    /// (name, our client)
    pub names: Vec<(String, bool)>,
}

/// The matter's caption, as New Pleading starts from it.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct Defaults {
    pub court_type: String,
    pub county: String,
    pub case_number: String,
    pub first: Option<Side>,
    pub second: Option<Side>,
    /// `Some(true)`: we represent the first side.
    pub we_first: Option<bool>,
}

fn text(o: &Map<String, Value>, key: &str) -> Result<String, String> {
    match o.get(key) {
        None | Some(Value::Null) => Ok(String::new()),
        Some(Value::String(s)) => Ok(s.trim().to_string()),
        Some(_) => Err(format!("{key} is text")),
    }
}

fn side(v: Option<&Value>, which: &str) -> Result<Option<Side>, String> {
    let Some(v) = v.filter(|v| !v.is_null()) else { return Ok(None) };
    let o = v.as_object().ok_or_else(|| format!("{which} is an object"))?;
    if let Some(k) = o.keys().find(|k| *k != "names" && *k != "role") {
        return Err(format!("{which}.{k} is not a New Pleading field"));
    }
    let mut names = Vec::new();
    match o.get("names") {
        None | Some(Value::Null) => {}
        Some(Value::Array(list)) => {
            if list.len() > MAX_NAMES {
                return Err(format!("{which} has more than {MAX_NAMES} names"));
            }
            for n in list {
                let (name, client) = match n {
                    Value::String(s) => (s.trim().to_string(), false),
                    Value::Object(p) => {
                        if let Some(k) = p.keys().find(|k| *k != "name" && *k != "client") {
                            return Err(format!("{which}.names: {k} is not a New Pleading field"));
                        }
                        let client = match p.get("client") {
                            None | Some(Value::Null) => false,
                            Some(Value::Bool(b)) => *b,
                            Some(_) => return Err(format!("{which}.names: client is true or false")),
                        };
                        (text(p, "name")?, client)
                    }
                    _ => return Err(format!("{which}.names are names")),
                };
                if name.is_empty() {
                    return Err(format!("{which}.names has an empty name"));
                }
                names.push((name, client));
            }
        }
        Some(_) => return Err(format!("{which}.names is a list")),
    }
    Ok(Some(Side { role: text(o, "role")?, names }))
}

/// Read `togavsu.pleadings.defaults`' parameters.
pub fn parse(v: &Value) -> Result<Defaults, String> {
    let o = v.as_object().ok_or("the parameters are an object")?;
    const KEYS: [&str; 6] = ["courtType", "county", "caseNumber", "firstSide", "secondSide", "weRepresent"];
    if let Some(k) = o.keys().find(|k| !KEYS.contains(&k.as_str())) {
        return Err(format!("{k} is not a New Pleading field"));
    }
    let we_first = match text(o, "weRepresent")?.as_str() {
        "" => None,
        "first" => Some(true),
        "second" => Some(false),
        _ => return Err("weRepresent is first or second".into()),
    };
    Ok(Defaults {
        court_type: text(o, "courtType")?.to_uppercase(),
        county: text(o, "county")?.to_uppercase(),
        case_number: text(o, "caseNumber")?,
        first: side(o.get("firstSide"), "firstSide")?,
        second: side(o.get("secondSide"), "secondSide")?,
        we_first,
    })
}

fn fill_side(form: &mut SideForm, side: Option<&Side>, ours: Option<bool>, roles: &[&str]) {
    if let Some(s) = side {
        if !s.role.is_empty() {
            form.other = !roles.contains(&s.role.as_str());
            form.role = s.role.clone();
        }
        if !s.names.is_empty() {
            form.parties = s.names.iter().map(|(name, client)| PartyRow { name: name.clone(), client: *client, ..PartyRow::default() }).collect();
            return;
        }
    }
    // No names from the matter on this side: its one empty row is marked as ours or not.
    if let Some(ours) = ours {
        for p in &mut form.parties {
            p.client = ours;
        }
    }
}

impl Defaults {
    /// Fill a New Pleading window from the matter; fields the matter leaves empty keep
    /// New Pleading's own start.
    pub fn apply(&self, form: &mut PleadingForm) {
        if !self.court_type.is_empty() {
            form.court_type = self.court_type.clone();
        }
        if !self.county.is_empty() {
            form.county = self.county.clone();
        }
        if !self.case_number.is_empty() {
            form.case_number = self.case_number.clone();
        }
        fill_side(&mut form.first, self.first.as_ref(), self.we_first, &FIRST_ROLES);
        fill_side(&mut form.second, self.second.as_ref(), self.we_first.map(|f| !f), &SECOND_ROLES);
    }
}

// ponytail: New Pleading is filled by writing law-ui's public PleadingForm from the wrapper,
// because LawCraft at the pin has no command for it. Ceiling: the fields law-ui exposes at
// the pin (court type, county, case number, sides' names, client marks and roles); a pin
// move that renames them stops the build rather than misfilling. Upgrade: a LawCraft
// law.pleadings.defaults command taking these same parameters, sent by the page as is.
// Trigger: that command landing upstream, or the next LawCraft pin move.
/// Fills each New Pleading window from the matter when it opens.
#[derive(Default)]
pub struct Seeder {
    defaults: Option<Defaults>,
    /// A New Pleading or Edit Pleading window was open at the last tick.
    open: bool,
}

impl Seeder {
    /// `togavsu.pleadings.defaults`: keep the matter's caption for New Pleading.
    pub fn set(&mut self, params: &Value) -> Result<Value, String> {
        self.defaults = Some(parse(params)?);
        Ok(json!({ "pleadingDefaults": true }))
    }

    /// Once a frame: fill a New Pleading window that opened since the last tick. Returns
    /// the window's `law.pleadings.new` parameters as filled, for the host page.
    pub fn tick(&mut self, app: &mut WordApp) -> Option<Value> {
        let form = app.addin_mut::<LawAddIn>(wordcraft_law::MODULE).and_then(|a| a.state.pleading.as_mut());
        let was_open = std::mem::replace(&mut self.open, form.is_some());
        let form = form?;
        if was_open || form.edit {
            return None;
        }
        self.defaults.as_ref()?.apply(form);
        Some(form.params())
    }
}

#[cfg(test)]
#[allow(clippy::unwrap_used, clippy::expect_used, clippy::panic)]
mod tests {
    use super::*;
    use wordcraft_doc::{Document, StoryRef};
    use wordcraft_engine::Session;
    use wordcraft_ui_egui::Services;

    /// The New Pleading form keeps firm details beside the profile store; one test at a time.
    static LOCK: std::sync::Mutex<()> = std::sync::Mutex::new(());

    fn law_app() -> (WordApp, std::sync::MutexGuard<'static, ()>) {
        let guard = LOCK.lock().unwrap_or_else(|e| e.into_inner());
        let n = std::time::SystemTime::now().duration_since(std::time::UNIX_EPOCH).map(|d| d.as_nanos()).unwrap_or(0);
        let dir = std::env::temp_dir().join(format!("togavsu-caption-{}-{n}", std::process::id()));
        std::fs::create_dir_all(&dir).unwrap();
        wordcraft_law::set_profiles_dir(Some(dir));
        let mut s = Session::new(Document::new());
        s.prefs.modules.insert(wordcraft_law::MODULE.into(), true);
        let mut a = WordApp::new(s, Services::default());
        a.add_addin(Box::new(LawAddIn::default()));
        (a, guard)
    }

    /// New Pleading opened as the ribbon and Tell me open it: law.pleadings.new with no
    /// parameters.
    fn open_new_pleading(a: &mut WordApp) {
        a.run("law.pleadings.new", json!({})).unwrap();
        assert!(form(a).is_some(), "New Pleading is open");
    }

    fn form(a: &mut WordApp) -> Option<&mut PleadingForm> {
        a.addin_mut::<LawAddIn>(wordcraft_law::MODULE).unwrap().state.pleading.as_mut()
    }

    /// What Togavsu's Context sends for the synthetic matter Roe v. Roe (we are the
    /// respondent here, so our client is on the second side).
    fn matter() -> Value {
        json!({
            "courtType": "CIRCUIT", "county": "ADAIR", "caseNumber": "26AR-DR00123",
            "firstSide": {"names": [{"name": "John Roe", "client": false}], "role": "Petitioner"},
            "secondSide": {"names": [{"name": "Jane Roe", "client": true}, {"name": "Jim Roe", "client": true}], "role": "Respondent"},
            "weRepresent": "second",
        })
    }

    #[test]
    fn new_pleading_opens_with_the_matters_caption() {
        let (mut a, _g) = law_app();
        // The id Togavsu's internal/editor PleadingDefaults sends.
        assert_eq!(COMMAND, "togavsu.pleadings.defaults");
        let mut seeder = Seeder::default();
        assert_eq!(seeder.set(&matter()).unwrap(), json!({"pleadingDefaults": true}));
        assert!(seeder.tick(&mut a).is_none(), "nothing is open yet");
        open_new_pleading(&mut a);
        let filled = seeder.tick(&mut a).expect("the window is filled when it opens");
        let f = form(&mut a).unwrap();
        assert_eq!((f.court_type.as_str(), f.county.as_str(), f.case_number.as_str()), ("CIRCUIT", "ADAIR", "26AR-DR00123"));
        assert_eq!(filled["caseNumber"], "26AR-DR00123");
        assert_eq!(filled["county"], "ADAIR");
        assert_eq!(filled["firstSide"], json!({"names": [{"name": "John Roe", "client": false}], "role": "Petitioner"}));
        assert_eq!(filled["secondSide"], json!({"names": [{"name": "Jane Roe", "client": true}, {"name": "Jim Roe", "client": true}], "role": "Respondent"}));
        // With the firm's details, the form the matter filled is complete: Create can run, and
        // LawCraft writes the matter's caption.
        assert!(f.missing().is_some_and(|m| m.contains("Firm name")), "only the firm is missing: {:?}", f.missing());
        f.firm = wordcraft_law_ui::pleading::firm_from(&json!({
            "firmName": "Synthetic Law LLC", "address1": "1 Main St", "city": "Kirksville", "state": "MO", "zip": "63501",
            "phone": "(660) 555-0100", "email": "office@example.test", "attorneyName": "Ada Example", "barNumber": "12345",
        }));
        assert_eq!(f.missing(), None);
        let params = f.params();
        a.run("law.pleadings.new", params).unwrap();
        let body = a.session.doc.plain_text(StoryRef::Body);
        for want in ["26AR-DR00123", "ADAIR", "JOHN ROE", "JANE ROE", "JIM ROE"] {
            assert!(body.to_uppercase().contains(want), "the caption has {want}: {body}");
        }
    }

    #[test]
    fn a_window_is_filled_once_so_typing_is_kept() {
        let (mut a, _g) = law_app();
        let mut seeder = Seeder::default();
        seeder.set(&matter()).unwrap();
        open_new_pleading(&mut a);
        assert!(seeder.tick(&mut a).is_some());
        form(&mut a).unwrap().case_number = "26AR-DR99999".into();
        assert!(seeder.tick(&mut a).is_none(), "an open window is not filled again");
        assert_eq!(form(&mut a).unwrap().case_number, "26AR-DR99999");
        // Closed and opened again: a new window starts from the matter.
        a.addin_mut::<LawAddIn>(wordcraft_law::MODULE).unwrap().state.pleading = None;
        assert!(seeder.tick(&mut a).is_none());
        open_new_pleading(&mut a);
        assert!(seeder.tick(&mut a).is_some());
        assert_eq!(form(&mut a).unwrap().case_number, "26AR-DR00123");
    }

    #[test]
    fn edit_pleading_and_unset_defaults_are_left_alone() {
        let (mut a, _g) = law_app();
        let mut seeder = Seeder::default();
        open_new_pleading(&mut a);
        assert!(seeder.tick(&mut a).is_none(), "no defaults: New Pleading starts as LawCraft's");
        assert_eq!(form(&mut a).unwrap().county, "");
        a.addin_mut::<LawAddIn>(wordcraft_law::MODULE).unwrap().state.pleading = None;
        assert!(seeder.tick(&mut a).is_none());
        let mut edit = PleadingForm::default();
        edit.edit = true;
        a.addin_mut::<LawAddIn>(wordcraft_law::MODULE).unwrap().state.pleading = Some(edit);
        seeder.set(&matter()).unwrap();
        assert!(seeder.tick(&mut a).is_none(), "Edit Pleading is the open pleading's");
        assert_eq!(form(&mut a).unwrap().county, "");
    }

    #[test]
    fn a_matter_with_missing_parts_keeps_new_pleadings_start() {
        let (mut a, _g) = law_app();
        let mut seeder = Seeder::default();
        // No case number, no opposing party; we represent the first side.
        seeder
            .set(&json!({"courtType": "ASSOCIATE CIRCUIT", "county": "macon", "firstSide": {"names": [{"name": "Jane Roe", "client": true}], "role": "Plaintiff"}, "secondSide": {"names": [], "role": "Defendant"}, "weRepresent": "first"}))
            .unwrap();
        open_new_pleading(&mut a);
        seeder.tick(&mut a).unwrap();
        let f = form(&mut a).unwrap();
        assert_eq!((f.court_type.as_str(), f.county.as_str(), f.case_number.as_str()), ("ASSOCIATE CIRCUIT", "MACON", ""));
        assert_eq!((f.first.role.as_str(), f.first.other, f.second.role.as_str()), ("Plaintiff", false, "Defendant"));
        assert_eq!(f.second.parties.len(), 1);
        assert!(f.second.parties[0].name.is_empty() && !f.second.parties[0].client, "the other side's empty row is not our client");
        assert!(f.missing().is_some_and(|m| m.contains("Second side names")), "{:?}", f.missing());
    }

    #[test]
    fn defaults_outside_new_pleadings_fields_are_refused() {
        let mut seeder = Seeder::default();
        for (bad, why) in [
            (json!([]), "an object"),
            (json!({"path": "/etc/passwd"}), "path is not"),
            (json!({"county": 5}), "county is text"),
            (json!({"weRepresent": "both"}), "first or second"),
            (json!({"firstSide": {"names": "Jane"}}), "a list"),
            (json!({"firstSide": {"names": [{"name": "Jane", "address": "1 Elm"}]}}), "address is not"),
            (json!({"firstSide": {"names": [{"name": " "}]}}), "empty name"),
            (json!({"secondSide": {"names": [{"name": "A", "client": "yes"}]}}), "true or false"),
            (json!({"firstSide": {"names": vec!["X"; 21]}}), "more than 20"),
        ] {
            let err = seeder.set(&bad).expect_err(&bad.to_string());
            assert!(err.contains(why), "{bad}: {err}");
        }
        assert!(seeder.defaults.is_none(), "refused defaults are not kept");
    }
}
