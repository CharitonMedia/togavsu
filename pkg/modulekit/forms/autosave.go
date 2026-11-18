package forms

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// OfflineQueueJS is a small script modules embed beside the HTMX form. It queues
// autosave PATCH bodies in localStorage while offline and flushes them when the
// browser is online again. storageKey namespaces the queue per response.
const OfflineQueueJS = `(function(){
  var KEY = window.TOGAVSU_FORMS_QUEUE_KEY || "togavsu-forms-offline-queue";
  function load(){ try { return JSON.parse(localStorage.getItem(KEY)||"[]"); } catch(e){ return []; } }
  function save(q){ localStorage.setItem(KEY, JSON.stringify(q)); }
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
          hdrs["Content-Type"] = "application/json";
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
    var xhr = e.detail && e.detail.xhr;
    var params = e.detail && e.detail.parameters;
    var headers = e.detail && e.detail.headers;
    if (!xhr || !params) return;
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
    } catch (err) {}
  });
  document.addEventListener("htmx:sendError", function(e){
    var xhr = e.detail && e.detail.xhr;
    var elt = e.detail && e.detail.elt;
    if (!elt || !elt.getAttribute) return;
    var url = elt.getAttribute("hx-post");
    if (!url) return;
    var field = elt.getAttribute("data-autosave-name") || "";
    var rawBody = (xhr && xhr._togavsuBody) || "";
    var answers = {};
    if (rawBody) {
      rawBody.split("&").forEach(function(pair){
        if (!pair) return;
        var i = pair.indexOf("=");
        var k = decodeURIComponent((i<0?pair:pair.slice(0,i)).replace(/\+/g," "));
        var v = decodeURIComponent((i<0?"":pair.slice(i+1)).replace(/\+/g," "));
        if (k === "csrf") return;
        if (k.slice(-10)===".__unknown") {
          answers[k.slice(0,-10)] = {unknown:true};
        } else {
          answers[k] = {value:v};
        }
      });
    }
    if (field && !answers[field]) {
      var input = null;
      if (elt.name === field) input = elt;
      else if (elt.querySelector) input = elt.querySelector("[name='"+field+"']");
      if (input && input.value != null) answers[field] = {value: input.value};
    }
    var body = JSON.stringify({offline:true, answers: answers, field: field});
    var hdrs = {"Content-Type":"application/json","X-Togavsu-Forms-Offline":"1"};
    var csrf = (xhr && xhr._togavsuCSRF) || csrfToken();
    if (csrf) hdrs["X-CSRF-Token"] = csrf;
    enqueue(url, body, hdrs);
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

// AutosavePatch is the JSON body modules accept on an autosave endpoint.
type AutosavePatch struct {
	// Answers is a partial answers map to merge.
	Answers Answers `json:"answers,omitempty"`
	// Field is a single field id when the client sends one control.
	Field string `json:"field,omitempty"`
	// Value is the single-field value (used with Field).
	Value any `json:"value,omitempty"`
	// Unknown marks an "I don't know" toggle for Field.
	Unknown *bool `json:"unknown,omitempty"`
	// Offline is set by OfflineQueueJS when replaying a queued save.
	Offline bool `json:"offline,omitempty"`
	// Raw is retained for older offline queue payloads; preferred path uses Answers.
	Raw any `json:"raw,omitempty"`
}

// Apply merges the patch into dst.
func (p AutosavePatch) Apply(dst *Answers) {
	if len(p.Answers) > 0 {
		dst.Merge(p.Answers)
	}
	if p.Field == "" {
		return
	}
	// Offline replay without a value and without Answers must not wipe the field.
	if p.Offline && p.Unknown == nil && p.Value == nil && len(p.Answers) == 0 {
		return
	}
	ans := Answer{}
	if p.Unknown != nil && *p.Unknown {
		ans.Unknown = true
	} else {
		ans.Value = p.Value
	}
	dst.Set(p.Field, ans)
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

// AutosaveHandler returns an http.Handler that decodes AutosavePatch JSON or
// application/x-www-form-urlencoded (as the HTMX renderer emits), calls save,
// and answers 204 (or 200 with the merged answers when ?echo=1).
func AutosaveHandler(save AutosaveFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost && r.Method != http.MethodPut && r.Method != http.MethodPatch {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		patch, err := decodeAutosaveRequest(r)
		if err != nil {
			http.Error(w, "invalid autosave body", http.StatusBadRequest)
			return
		}
		answers, err := save(r, patch)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
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

func decodeAutosaveRequest(r *http.Request) (AutosavePatch, error) {
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
		return DecodeAutosave(strings.NewReader(trimmed), 0)
	}
	vals, err := url.ParseQuery(trimmed)
	if err != nil {
		return AutosavePatch{}, err
	}
	return patchFromForm(r, vals), nil
}

func patchFromForm(r *http.Request, values url.Values) AutosavePatch {
	answers := ParseFormAnswers(values)
	field := strings.TrimSpace(r.Header.Get("HX-Trigger-Name"))
	return AutosavePatch{Answers: answers, Field: field, Offline: r.Header.Get("X-Togavsu-Forms-Offline") == "1"}
}

// ParseFormAnswers builds Answers from a standard form POST (application/x-www-form-urlencoded
// or multipart). Keys ending in ".__unknown" mark "I don't know". Address parts use
// "id.line1" style names as RenderScreen emits. Repeating rows use "id[N].sub".
func ParseFormAnswers(values map[string][]string) Answers {
	out := Answers{}
	unknown := map[string]bool{}
	type rowPart struct {
		idx  int
		rest string
		vals []string
	}
	repeating := map[string][]rowPart{}
	for key, vals := range values {
		if strings.HasSuffix(key, ".__unknown") {
			id := strings.TrimSuffix(key, ".__unknown")
			unknown[id] = len(vals) > 0 && vals[0] != "" && vals[0] != "0" && vals[0] != "false"
			continue
		}
		if len(vals) == 0 {
			continue
		}
		if id, idx, rest, ok := splitRepeatingKey(key); ok {
			repeating[id] = append(repeating[id], rowPart{idx: idx, rest: rest, vals: vals})
			continue
		}
		if i := strings.IndexByte(key, '.'); i > 0 {
			id, part := key[:i], key[i+1:]
			addr := out.Get(id).AddressValue()
			switch part {
			case "line1":
				addr.Line1 = vals[0]
			case "line2":
				addr.Line2 = vals[0]
			case "city":
				addr.City = vals[0]
			case "state":
				addr.State = vals[0]
			case "postal":
				addr.Postal = vals[0]
			case "country":
				addr.Country = vals[0]
			default:
				out.Set(key, Answer{Value: vals[0]})
				continue
			}
			out.Set(id, Answer{Value: addr})
			continue
		}
		if len(vals) > 1 {
			out.Set(key, Answer{Value: append([]string{}, vals...)})
		} else if vals[0] == "true" || vals[0] == "false" {
			out.Set(key, Answer{Value: vals[0] == "true"})
		} else {
			out.Set(key, Answer{Value: vals[0]})
		}
	}
	for id, parts := range repeating {
		max := -1
		for _, p := range parts {
			if p.idx > MaxRepeatingIndex {
				continue // refuse unbounded allocation from forged indices
			}
			if p.idx > max {
				max = p.idx
			}
		}
		if max < 0 {
			continue
		}
		rows := make([]map[string]Answer, max+1)
		for i := range rows {
			rows[i] = map[string]Answer{}
		}
		for _, p := range parts {
			if p.idx > MaxRepeatingIndex {
				continue
			}
			rowVals := map[string][]string{p.rest: p.vals}
			parsed := ParseFormAnswers(rowVals)
			for k, a := range parsed {
				rows[p.idx][k] = a
			}
		}
		out.Set(id, Answer{Value: rows})
	}
	for id, u := range unknown {
		if u {
			out.Set(id, Answer{Unknown: true})
		}
	}
	return out
}

// MaxRepeatingIndex is the highest accepted repeating-row index in a form POST
// (0..MaxRepeatingIndex inclusive). Larger indices are ignored so an anonymous
// public-page POST cannot force a multi-gigabyte allocation.
const MaxRepeatingIndex = domain.FormsMaxRepeatingIndex

// splitRepeatingKey parses "kids[0].name" into id, index, rest.
func splitRepeatingKey(key string) (id string, idx int, rest string, ok bool) {
	lb := strings.IndexByte(key, '[')
	if lb <= 0 {
		return "", 0, "", false
	}
	rb := strings.IndexByte(key[lb:], ']')
	if rb <= 1 {
		return "", 0, "", false
	}
	rb = lb + rb
	if rb+1 >= len(key) || key[rb+1] != '.' {
		return "", 0, "", false
	}
	n, err := strconv.Atoi(key[lb+1 : rb])
	if err != nil || n < 0 {
		return "", 0, "", false
	}
	return key[:lb], n, key[rb+2:], true
}
