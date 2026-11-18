package forms

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Whole-form autosave uses one wire shape for the HTMX renderer, OfflineQueueJS
// replay, and AutosaveHandler (CORE-33):
//
//	Content-Type: application/x-www-form-urlencoded  (live HTMX + offline replay)
//	  body:    every control in the form (hx-include=this) including csrf
//	  headers: X-CSRF-Token: <token>
//	           HX-Trigger / HX-Request as HTMX sends
//
//	Content-Type: application/json  (optional offline / API clients)
//	  body:    {"offline":true,"answers":{"<field>":{"value":...}}}
//	  headers: X-CSRF-Token: <token>
//	           X-Togavsu-Forms-Offline: 1
//
// The form element carries one hx-post (debounced change/input). Masked rule: an
// untouched masked control is stripped from the post (sends nothing for that
// field) and never overwrites stored ciphertext; post ClearMaskedValue to erase.
//
// OfflineQueueJS is a small script modules embed beside the HTMX form. It queues
// autosave bodies in localStorage while offline and flushes them when the
// browser is online again. storageKey namespaces the queue per response.
const OfflineQueueJS = `(function(){
  var KEY = window.TOGAVSU_FORMS_QUEUE_KEY || "togavsu-forms-offline-queue";
  // Opaque-origin CSP sandboxes (no allow-same-origin) throw on localStorage; keep a
  // same-tab memory fallback so enqueue/flush still work on /p/ and /m/ pages.
  var mem = [];
  function load(){
    try { return JSON.parse(localStorage.getItem(KEY)||"[]"); }
    catch (e) { return mem.slice(); }
  }
  function save(q){
    mem = q;
    try { localStorage.setItem(KEY, JSON.stringify(q)); }
    catch (e) { /* SecurityError on opaque origin — memory queue remains */ }
  }
  function csrfToken(){
    var el = document.querySelector("input[name=csrf]");
    return el && el.value ? el.value : (window.TOGAVSU_PUBLIC_CSRF || "");
  }
  function enqueue(url, body, headers){
    var q = load();
    q.push({url:url, body:body, headers:headers||{}, at:Date.now()});
    save(q);
  }
  async function flush(){
    if (!navigator.onLine) return;
    var q = load();
    if (!q.length) return;
    var keep = [];
    for (var i=0;i<q.length;i++){
      var item = q[i];
      try {
        var hdrs = Object.assign({}, item.headers||{});
        if (!hdrs["Content-Type"] && !hdrs["content-type"]) {
          hdrs["Content-Type"] = "application/x-www-form-urlencoded";
        }
        var csrf = csrfToken();
        if (csrf && !hdrs["X-CSRF-Token"] && !hdrs["x-csrf-token"]) {
          hdrs["X-CSRF-Token"] = csrf;
        }
        var res = await fetch(item.url, {
          method: "POST",
          headers: hdrs,
          body: item.body,
          credentials: "include"
        });
        if (!res.ok) keep.push(item);
      } catch (e) { keep.push(item); }
    }
    save(keep);
  }
  document.addEventListener("htmx:configRequest", function(e){
    var elt = e.detail && e.detail.elt;
    var xhr = e.detail && e.detail.xhr;
    var params = e.detail && e.detail.parameters;
    var headers = e.detail && e.detail.headers;
    if (!xhr || !params) return;
    // Whole-form post: strip empty masked controls so they never overwrite sealed data.
    var root = elt;
    if (root && root.querySelectorAll) {
      var masked = root.querySelectorAll("[data-autosave-masked='1']");
      for (var mi=0; mi<masked.length; mi++){
        var inp = masked[mi];
        if (!inp || !inp.name) continue;
        if (inp.value == null || String(inp.value) === "") {
          delete params[inp.name];
        }
      }
    }
    try {
      var csrf = csrfToken();
      if (csrf) {
        if (headers && typeof headers === "object") headers["X-CSRF-Token"] = csrf;
        if (!params.csrf) params.csrf = csrf;
      }
      var parts = [];
      Object.keys(params).forEach(function(k){
        var v = params[k];
        if (Array.isArray(v)) {
          v.forEach(function(one){ parts.push(encodeURIComponent(k)+"="+encodeURIComponent(one==null?"":one)); });
        } else {
          parts.push(encodeURIComponent(k)+"="+encodeURIComponent(v==null?"":v));
        }
      });
      xhr._togavsuBody = parts.join("&");
      xhr._togavsuContentType = "application/x-www-form-urlencoded";
      xhr._togavsuCSRF = csrf || "";
      xhr._togavsuURL = (elt && elt.getAttribute && elt.getAttribute("hx-post")) || "";
    } catch (err) {}
  });
  document.addEventListener("htmx:sendError", function(e){
    var xhr = e.detail && e.detail.xhr;
    var elt = e.detail && e.detail.elt;
    if (!elt || !elt.getAttribute) return;
    var url = elt.getAttribute("hx-post") || (xhr && xhr._togavsuURL) || "";
    if (!url) return;
    var rawBody = (xhr && xhr._togavsuBody) || "";
    if (!rawBody) return;
    var hdrs = {"Content-Type":"application/x-www-form-urlencoded","X-Togavsu-Forms-Offline":"1"};
    var csrf = (xhr && xhr._togavsuCSRF) || csrfToken();
    if (csrf) hdrs["X-CSRF-Token"] = csrf;
    enqueue(url, rawBody, hdrs);
  });
  window.addEventListener("online", flush);
  document.addEventListener("DOMContentLoaded", flush);
  window.TogavsuFormsOffline = {enqueue:enqueue, flush:flush, key:KEY, csrfToken:csrfToken};
})();`

// RepeatingJS wires Add/Remove for TypeRepeating groups rendered by RenderScreen.
const RepeatingJS = `(function(){
  function reindex(container){
    var rows = container.querySelectorAll(":scope > fieldset.tv-repeat-row");
    for (var i=0;i<rows.length;i++){
      rows[i].setAttribute("data-index", String(i));
      var inputs = rows[i].querySelectorAll("[name]");
      for (var j=0;j<inputs.length;j++){
        var n = inputs[j].getAttribute("name");
        if (!n) continue;
        inputs[j].setAttribute("name", n.replace(/\[\d+\]/, "["+i+"]"));
        if (inputs[j].id) inputs[j].id = inputs[j].id.replace(/-\d+-/, "-"+i+"-");
      }
    }
  }
  document.addEventListener("click", function(ev){
    var t = ev.target;
    if (!t || !t.classList) return;
    if (t.classList.contains("tv-repeat-add")) {
      var field = t.getAttribute("data-field");
      var wrap = t.closest(".tv-repeating") || document.querySelector('.tv-repeating[data-field="'+field+'"]');
      if (!wrap) return;
      var tpl = wrap.querySelector("template.tv-repeat-template");
      if (!tpl) return;
      var node = tpl.content.cloneNode(true);
      wrap.insertBefore(node, t);
      reindex(wrap);
      return;
    }
    if (t.classList.contains("tv-repeat-remove")) {
      var row = t.closest("fieldset.tv-repeat-row");
      var wrap = t.closest(".tv-repeating");
      if (!row || !wrap) return;
      row.parentNode.removeChild(row);
      reindex(wrap);
    }
  });
})();`

// AutosavePatch is the decoded whole-form (or JSON) autosave body.
type AutosavePatch struct {
	// Answers is the partial answers map to merge (preferred path for HTMX and offline).
	Answers Answers `json:"answers,omitempty"`
	// Field is a single field id for explicit JSON clients (not set from HX-Trigger-Name).
	Field string `json:"field,omitempty"`
	// Value is the single-field value (used with Field only when set).
	Value any `json:"value,omitempty"`
	// Unknown marks an "I don't know" toggle for Field.
	Unknown *bool `json:"unknown,omitempty"`
	// Offline is set by OfflineQueueJS when replaying a queued save.
	Offline bool `json:"offline,omitempty"`
	// Raw is retained for older offline queue payloads; preferred path uses Answers.
	Raw any `json:"raw,omitempty"`

	// form, when set by AutosaveHandlerForm, drives ParseFormAnswers and masked preserve.
	form Form
}

// Apply merges the patch into dst using the one autosave shape:
// Answers merge first (with masked preserve when form is set); Field overwrites only
// when Value or Unknown is explicitly set.
func (p AutosavePatch) Apply(dst *Answers) {
	if len(p.Answers) > 0 {
		if len(maskedFieldIDs(p.form)) > 0 || len(p.form.Tracks) > 0 {
			*dst = MergeFormSubmit(*dst, p.Answers, p.form)
		} else {
			dst.Merge(p.Answers)
		}
	}
	if p.Field == "" {
		return
	}
	// No explicit value/unknown: ignore Field (covers HX-Trigger-Name leftovers and
	// offline replays that only carry answers).
	if p.Unknown == nil && p.Value == nil {
		return
	}
	if p.Unknown != nil && *p.Unknown {
		dst.Set(p.Field, Answer{Unknown: true})
		return
	}
	if p.Unknown != nil && !*p.Unknown && p.Value == nil {
		cur := dst.Get(p.Field)
		cur.Unknown = false
		dst.Set(p.Field, cur)
		return
	}
	if maskedFieldIDs(p.form)[p.Field] {
		s, _ := p.Value.(string)
		if s == "" {
			cur := dst.Get(p.Field)
			if p.Unknown != nil && !*p.Unknown {
				cur.Unknown = false
			}
			dst.Set(p.Field, cur)
			return
		}
		if s == ClearMaskedValue {
			dst.Set(p.Field, Answer{Value: ""})
			return
		}
	}
	dst.Set(p.Field, Answer{Value: p.Value})
}

// DecodeAutosave reads a JSON AutosavePatch from r. Body is limited to maxBytes
// (1 MiB when maxBytes <= 0).
func DecodeAutosave(r io.Reader, maxBytes int64) (AutosavePatch, error) {
	if maxBytes <= 0 {
		maxBytes = 1 << 20
	}
	dec := json.NewDecoder(io.LimitReader(r, maxBytes))
	dec.UseNumber()
	var p AutosavePatch
	if err := dec.Decode(&p); err != nil {
		return AutosavePatch{}, err
	}
	return p, nil
}

// AutosaveFunc loads current answers, applies a patch, and persists them.
// The module owns storage; this only shapes the HTTP dance.
type AutosaveFunc func(r *http.Request, patch AutosavePatch) (Answers, error)

// AutosaveHandler returns an http.Handler that decodes the whole-form autosave shape
// (JSON or application/x-www-form-urlencoded), calls save, and answers 204 (or 200 with
// the merged answers when ?echo=1). Origin: null gets CORS so HTMX from sandboxed /p/
// and /m/ pages can complete. Prefer AutosaveHandlerForm when the form definition is known
// (required for typed ParseFormAnswers and masked empty-preserve).
func AutosaveHandler(save AutosaveFunc) http.Handler {
	return AutosaveHandlerForm(Form{}, save)
}

// AutosaveHandlerForm is AutosaveHandler with a form definition so POSTs are parsed
// by field type and empty masked posts keep stored ciphertext (MergeFormSubmit).
func AutosaveHandlerForm(form Form, save AutosaveFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		applyFormsOpaqueCORS(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodPost && r.Method != http.MethodPut && r.Method != http.MethodPatch {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		patch, err := decodeAutosaveRequest(r, form)
		if err != nil {
			http.Error(w, "invalid autosave body", http.StatusBadRequest)
			return
		}
		patch.form = form
		answers, err := save(r, patch)
		if err != nil {
			http.Error(w, "save failed", http.StatusBadRequest)
			return
		}
		if strings.Contains(r.URL.RawQuery, "echo=1") {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(answers)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func applyFormsOpaqueCORS(w http.ResponseWriter, r *http.Request) {
	if !strings.EqualFold(strings.TrimSpace(r.Header.Get("Origin")), "null") {
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", "null")
	w.Header().Set("Access-Control-Allow-Credentials", "true")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-CSRF-Token, HX-Request, HX-Current-URL, HX-Trigger, HX-Trigger-Name, HX-Target, HX-Prompt, X-Togavsu-Forms-Offline")
	w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, POST, PUT, PATCH, OPTIONS")
	w.Header().Add("Vary", "Origin")
}

func decodeAutosaveRequest(r *http.Request, form Form) (AutosavePatch, error) {
	ct := r.Header.Get("Content-Type")
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return AutosavePatch{}, err
	}
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return AutosavePatch{}, io.ErrUnexpectedEOF
	}
	if strings.Contains(ct, "application/json") || (!strings.Contains(ct, "application/x-www-form-urlencoded") && !strings.Contains(ct, "multipart/form-data") && trimmed[0] == '{') {
		p, err := DecodeAutosave(strings.NewReader(trimmed), 0)
		if err != nil {
			return AutosavePatch{}, err
		}
		if r.Header.Get("X-Togavsu-Forms-Offline") == "1" {
			p.Offline = true
		}
		return p, nil
	}
	vals, err := url.ParseQuery(trimmed)
	if err != nil {
		return AutosavePatch{}, err
	}
	return patchFromForm(r, form, vals), nil
}

// patchFromForm builds the one autosave shape from an HTMX whole-form POST.
func patchFromForm(r *http.Request, form Form, values url.Values) AutosavePatch {
	return AutosavePatch{
		Answers: ParseFormAnswers(form, values),
		Offline: r.Header.Get("X-Togavsu-Forms-Offline") == "1",
	}
}
