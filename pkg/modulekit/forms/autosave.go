package forms

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// OfflineQueueJS is a small script modules embed beside the HTMX form. It queues
// autosave PATCH bodies in localStorage while offline and flushes them when the
// browser is online again. storageKey namespaces the queue per response.
const OfflineQueueJS = `(function(){
  var KEY = window.TOGAVSU_FORMS_QUEUE_KEY || "togavsu-forms-offline-queue";
  function load(){ try { return JSON.parse(localStorage.getItem(KEY)||"[]"); } catch(e){ return []; } }
  function save(q){ localStorage.setItem(KEY, JSON.stringify(q)); }
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
        var res = await fetch(item.url, {
          method: "POST",
          headers: Object.assign({"Content-Type":"application/json"}, item.headers||{}),
          body: item.body,
          credentials: "same-origin"
        });
        if (!res.ok) keep.push(item);
      } catch (e) { keep.push(item); }
    }
    save(keep);
  }
  document.addEventListener("htmx:sendError", function(e){
    var xhr = e.detail && e.detail.xhr;
    var elt = e.detail && e.detail.elt;
    if (!elt || !elt.getAttribute) return;
    var url = elt.getAttribute("hx-post");
    if (!url) return;
    var body = JSON.stringify({offline:true, field: elt.getAttribute("data-autosave-name")||"", raw: (xhr && xhr._togavsuBody) || null});
    enqueue(url, body, {"X-Togavsu-Forms-Offline":"1"});
  });
  window.addEventListener("online", flush);
  document.addEventListener("DOMContentLoaded", flush);
  window.TogavsuFormsOffline = {enqueue:enqueue, flush:flush, key:KEY};
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
}

// Apply merges the patch into dst.
func (p AutosavePatch) Apply(dst *Answers) {
	if len(p.Answers) > 0 {
		dst.Merge(p.Answers)
	}
	if p.Field == "" {
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

// AutosaveHandler returns an http.Handler that decodes AutosavePatch JSON,
// calls save, and answers 204 (or 200 with the merged answers when ?echo=1).
func AutosaveHandler(save AutosaveFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost && r.Method != http.MethodPut && r.Method != http.MethodPatch {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		patch, err := DecodeAutosave(r.Body, 0)
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

// ParseFormAnswers builds Answers from a standard form POST (application/x-www-form-urlencoded
// or multipart). Keys ending in ".__unknown" mark "I don't know". Address parts use
// "id.line1" style names as RenderScreen emits.
func ParseFormAnswers(values map[string][]string) Answers {
	out := Answers{}
	unknown := map[string]bool{}
	for key, vals := range values {
		if strings.HasSuffix(key, ".__unknown") {
			id := strings.TrimSuffix(key, ".__unknown")
			unknown[id] = len(vals) > 0 && vals[0] != "" && vals[0] != "0" && vals[0] != "false"
			continue
		}
		if len(vals) == 0 {
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
	for id, u := range unknown {
		if u {
			out.Set(id, Answer{Unknown: true})
		}
	}
	return out
}
