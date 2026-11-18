package forms

import (
	"fmt"
	"html"
	"io"
	"strings"
)

// RenderOptions controls HTMX wiring and error display for RenderScreen.
type RenderOptions struct {
	// AutosaveURL, when set, adds hx-post / hx-trigger on each editable control.
	AutosaveURL string
	// FormAction is the full-submit action; empty omits the wrapping <form>.
	FormAction string
	// FormMethod defaults to POST.
	FormMethod string
	// CSRFToken is written into a hidden input when FormAction is set.
	CSRFToken string
	// Errors maps field id → message shown beside the control.
	Errors map[string]string
	// RevealMasked decrypts nothing itself; when true, masked values are shown
	// as the stored string (module should decrypt into Answers first for staff).
	RevealMasked bool
	// NamePrefix prefixes input name attributes (default empty).
	NamePrefix string
}

// RenderScreen writes HTMX-friendly HTML for one screen at audience.
func RenderScreen(w io.Writer, screen Screen, answers Answers, audience Audience, opts RenderOptions) error {
	method := opts.FormMethod
	if method == "" {
		method = "POST"
	}
	if opts.FormAction != "" {
		if _, err := fmt.Fprintf(w, `<form class="tv-form" method="%s" action="%s" data-audience="%s">`+"\n",
			html.EscapeString(method), html.EscapeString(opts.FormAction), html.EscapeString(string(audience))); err != nil {
			return err
		}
	} else {
		if _, err := fmt.Fprintf(w, `<div class="tv-form" data-audience="%s">`+"\n", html.EscapeString(string(audience))); err != nil {
			return err
		}
	}
	// CSRF hidden input for full submit and for HTMX autosave hx-include / offline queue.
	if opts.CSRFToken != "" {
		if _, err := fmt.Fprintf(w, `<input type="hidden" name="csrf" value="%s">`+"\n", html.EscapeString(opts.CSRFToken)); err != nil {
			return err
		}
	}
	if screen.Title != "" {
		if _, err := fmt.Fprintf(w, `<h2 class="tv-form-screen-title">%s</h2>`+"\n", html.EscapeString(screen.Title)); err != nil {
			return err
		}
	}
	for _, field := range VisibleFields(screen, answers, audience) {
		if err := renderField(w, field, answers.Get(field.ID), opts, ""); err != nil {
			return err
		}
	}
	if opts.FormAction != "" {
		_, err := io.WriteString(w, "</form>\n")
		return err
	}
	_, err := io.WriteString(w, "</div>\n")
	return err
}

func renderField(w io.Writer, field Field, ans Answer, opts RenderOptions, namePrefix string) error {
	name := opts.NamePrefix + namePrefix + field.ID
	id := "f-" + strings.ReplaceAll(name, ".", "-")
	id = strings.ReplaceAll(id, "[", "-")
	id = strings.ReplaceAll(id, "]", "")
	class := "tv-field tv-field-" + string(field.Type)
	if opts.Errors[field.ID] != "" || opts.Errors[name] != "" {
		class += " tv-field-error"
	}
	if _, err := fmt.Fprintf(w, `<div class="%s" data-field="%s" data-type="%s">`+"\n",
		html.EscapeString(class), html.EscapeString(field.ID), html.EscapeString(string(field.Type))); err != nil {
		return err
	}
	if field.Type != TypeHidden && field.Label != "" {
		if _, err := fmt.Fprintf(w, `<label for="%s">%s</label>`+"\n", html.EscapeString(id), html.EscapeString(field.Label)); err != nil {
			return err
		}
	}
	if field.Help != "" && field.Type != TypeHidden {
		if _, err := fmt.Fprintf(w, `<p class="tv-help">%s</p>`+"\n", html.EscapeString(field.Help)); err != nil {
			return err
		}
	}
	maskedAutosave := field.Type == TypeMasked && !opts.RevealMasked
	hx := autosaveAttrs(opts.AutosaveURL, name, opts.CSRFToken, maskedAutosave)
	unknownChecked := ""
	if ans.Unknown {
		unknownChecked = " checked"
	}
	switch field.Type {
	case TypeText, TypeEmail, TypeTel, TypeDate, TypeMasked, TypeFile:
		inputType := string(field.Type)
		if field.Type == TypeMasked {
			inputType = "password"
			if opts.RevealMasked {
				inputType = "text"
			}
		}
		if field.Type == TypeFile {
			inputType = "file"
		}
		val := ans.StringValue()
		if field.Type == TypeMasked && !opts.RevealMasked && val != "" {
			val = ""
			if _, err := fmt.Fprintf(w, `<input type="%s" id="%s" name="%s" placeholder="%s"%s%s autocomplete="off">`+"\n",
				inputType, html.EscapeString(id), html.EscapeString(name),
				html.EscapeString(field.Placeholder), hx, disabledIfUnknown(ans)); err != nil {
				return err
			}
		} else {
			if _, err := fmt.Fprintf(w, `<input type="%s" id="%s" name="%s" value="%s" placeholder="%s"%s%s>`+"\n",
				inputType, html.EscapeString(id), html.EscapeString(name),
				html.EscapeString(val), html.EscapeString(field.Placeholder), hx, disabledIfUnknown(ans)); err != nil {
				return err
			}
		}
	case TypeNumber:
		val := ""
		if n, ok := ans.FloatValue(); ok {
			val = fmt.Sprintf("%v", n)
		}
		if _, err := fmt.Fprintf(w, `<input type="number" id="%s" name="%s" value="%s"%s%s>`+"\n",
			html.EscapeString(id), html.EscapeString(name), html.EscapeString(val), hx, disabledIfUnknown(ans)); err != nil {
			return err
		}
	case TypeTextarea:
		if _, err := fmt.Fprintf(w, `<textarea id="%s" name="%s" placeholder="%s"%s%s>%s</textarea>`+"\n",
			html.EscapeString(id), html.EscapeString(name), html.EscapeString(field.Placeholder),
			hx, disabledIfUnknown(ans), html.EscapeString(ans.StringValue())); err != nil {
			return err
		}
	case TypeSelect:
		if _, err := fmt.Fprintf(w, `<select id="%s" name="%s"%s%s>`+"\n",
			html.EscapeString(id), html.EscapeString(name), hx, disabledIfUnknown(ans)); err != nil {
			return err
		}
		_, _ = io.WriteString(w, `<option value=""></option>`+"\n")
		cur := ans.StringValue()
		for _, opt := range field.Options {
			sel := ""
			if opt.Value == cur {
				sel = " selected"
			}
			label := opt.Label
			if label == "" {
				label = opt.Value
			}
			if _, err := fmt.Fprintf(w, `<option value="%s"%s>%s</option>`+"\n",
				html.EscapeString(opt.Value), sel, html.EscapeString(label)); err != nil {
				return err
			}
		}
		_, _ = io.WriteString(w, "</select>\n")
	case TypeMultiSelect:
		if _, err := fmt.Fprintf(w, `<select id="%s" name="%s" multiple%s%s>`+"\n",
			html.EscapeString(id), html.EscapeString(name), hx, disabledIfUnknown(ans)); err != nil {
			return err
		}
		selected := map[string]bool{}
		for _, s := range ans.StringsValue() {
			selected[s] = true
		}
		for _, opt := range field.Options {
			sel := ""
			if selected[opt.Value] {
				sel = " selected"
			}
			label := opt.Label
			if label == "" {
				label = opt.Value
			}
			if _, err := fmt.Fprintf(w, `<option value="%s"%s>%s</option>`+"\n",
				html.EscapeString(opt.Value), sel, html.EscapeString(label)); err != nil {
				return err
			}
		}
		_, _ = io.WriteString(w, "</select>\n")
	case TypeBoolean:
		checked := ""
		if ans.BoolValue() {
			checked = " checked"
		}
		if _, err := fmt.Fprintf(w, `<input type="checkbox" id="%s" name="%s" value="true"%s%s%s>`+"\n",
			html.EscapeString(id), html.EscapeString(name), checked, hx, disabledIfUnknown(ans)); err != nil {
			return err
		}
	case TypeRadio:
		for i, opt := range field.Options {
			oid := fmt.Sprintf("%s-%d", id, i)
			checked := ""
			if opt.Value == ans.StringValue() {
				checked = " checked"
			}
			label := opt.Label
			if label == "" {
				label = opt.Value
			}
			if _, err := fmt.Fprintf(w, `<label class="tv-radio"><input type="radio" id="%s" name="%s" value="%s"%s%s%s> %s</label>`+"\n",
				html.EscapeString(oid), html.EscapeString(name), html.EscapeString(opt.Value),
				checked, hx, disabledIfUnknown(ans), html.EscapeString(label)); err != nil {
				return err
			}
		}
	case TypeAddress:
		addr := ans.AddressValue()
		for _, part := range []struct{ key, label, val string }{
			{"line1", "Line 1", addr.Line1},
			{"line2", "Line 2", addr.Line2},
			{"city", "City", addr.City},
			{"state", "State", addr.State},
			{"postal", "Postal", addr.Postal},
			{"country", "Country", addr.Country},
		} {
			pid := id + "-" + part.key
			pname := name + "." + part.key
			if _, err := fmt.Fprintf(w, `<label for="%s">%s</label>`+"\n", html.EscapeString(pid), html.EscapeString(part.label)); err != nil {
				return err
			}
			if _, err := fmt.Fprintf(w, `<input type="text" id="%s" name="%s" value="%s"%s%s>`+"\n",
				html.EscapeString(pid), html.EscapeString(pname), html.EscapeString(part.val), hx, disabledIfUnknown(ans)); err != nil {
				return err
			}
		}
	case TypeReadOnly:
		val := field.Value
		if ans.StringValue() != "" {
			val = ans.StringValue()
		}
		if _, err := fmt.Fprintf(w, `<p class="tv-readonly" id="%s">%s</p>`+"\n",
			html.EscapeString(id), html.EscapeString(val)); err != nil {
			return err
		}
	case TypeHidden:
		val := ans.StringValue()
		if val == "" {
			val = field.Value
		}
		if _, err := fmt.Fprintf(w, `<input type="hidden" id="%s" name="%s" value="%s">`+"\n",
			html.EscapeString(id), html.EscapeString(name), html.EscapeString(val)); err != nil {
			return err
		}
	case TypeRepeating:
		rows := ans.RowsValue()
		if _, err := fmt.Fprintf(w, `<div class="tv-repeating" data-field="%s">`+"\n", html.EscapeString(field.ID)); err != nil {
			return err
		}
		for i, row := range rows {
			if err := renderRepeatRow(w, field, name, i, row, opts); err != nil {
				return err
			}
		}
		// Template row for Add (empty answers).
		_, _ = io.WriteString(w, `<template class="tv-repeat-template">`+"\n")
		if err := renderRepeatRow(w, field, name, len(rows), nil, opts); err != nil {
			return err
		}
		_, _ = io.WriteString(w, `</template>`+"\n")
		if _, err := fmt.Fprintf(w, `<button type="button" class="tv-repeat-add" data-field="%s">Add</button>`+"\n",
			html.EscapeString(field.ID)); err != nil {
			return err
		}
		_, _ = io.WriteString(w, `</div>`+"\n")
	default:
		if _, err := fmt.Fprintf(w, `<!-- unsupported field type %s -->`+"\n", html.EscapeString(string(field.Type))); err != nil {
			return err
		}
	}
	if field.AllowUnknown && field.Type != TypeHidden && field.Type != TypeReadOnly {
		// Hidden 0 + checkbox 1: unchecking posts an explicit clear so ParseFormAnswers
		// can turn Unknown off (unchecked checkboxes alone omit the name).
		if _, err := fmt.Fprintf(w, `<input type="hidden" name="%s.__unknown" value="0">`+"\n", html.EscapeString(name)); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, `<label class="tv-unknown"><input type="checkbox" name="%s.__unknown" value="1"%s%s> I don't know</label>`+"\n",
			html.EscapeString(name), unknownChecked, hx); err != nil {
			return err
		}
	}
	errMsg := opts.Errors[field.ID]
	if errMsg == "" {
		errMsg = opts.Errors[name]
	}
	if errMsg != "" {
		if _, err := fmt.Fprintf(w, `<p class="tv-error">%s</p>`+"\n", html.EscapeString(errMsg)); err != nil {
			return err
		}
	}
	_, err := io.WriteString(w, "</div>\n")
	return err
}

func renderRepeatRow(w io.Writer, field Field, name string, i int, row map[string]Answer, opts RenderOptions) error {
	if _, err := fmt.Fprintf(w, `<fieldset class="tv-repeat-row" data-index="%d">`+"\n", i); err != nil {
		return err
	}
	prefix := fmt.Sprintf("%s[%d].", name, i)
	ans := Answers(row)
	for _, nested := range field.Fields {
		if err := renderField(w, nested, ans.Get(nested.ID), opts, prefix); err != nil {
			return err
		}
	}
	_, _ = io.WriteString(w, `<button type="button" class="tv-repeat-remove">Remove</button>`+"\n")
	_, _ = io.WriteString(w, "</fieldset>\n")
	return nil
}

func autosaveAttrs(url, name, csrf string, masked bool) string {
	if url == "" {
		return ""
	}
	// One autosave shape: field value + csrf (hx-include), X-CSRF-Token header.
	// Masked (unrevealed): change-only trigger so tabbing an empty control does not
	// post; data-autosave-masked lets OfflineQueueJS skip empty posts.
	trigger := `change delay:400ms, blur`
	maskedAttr := ""
	if masked {
		trigger = `change delay:400ms`
		maskedAttr = ` data-autosave-masked="1"`
	}
	attrs := fmt.Sprintf(` hx-post="%s" hx-trigger="%s" hx-include="closest .tv-field, [name=csrf]" hx-swap="none" data-autosave-name="%s"%s`,
		html.EscapeString(url), trigger, html.EscapeString(name), maskedAttr)
	if csrf != "" {
		attrs += fmt.Sprintf(` hx-headers="%s"`, html.EscapeString(`{"X-CSRF-Token":"`+jsonStringEscape(csrf)+`"}`))
	}
	return attrs
}

// jsonStringEscape escapes a value for embedding inside a JSON string in an HTML attribute.
func jsonStringEscape(s string) string {
	replacer := strings.NewReplacer(
		`\`, `\\`,
		`"`, `\"`,
		"\n", `\n`,
		"\r", `\r`,
		"\t", `\t`,
	)
	return replacer.Replace(s)
}

func disabledIfUnknown(ans Answer) string {
	if ans.Unknown {
		return " disabled"
	}
	return ""
}

// UnknownToggleJS re-enables field controls when the "I don't know" checkbox is
// unchecked (disabled inputs are left out of form posts and hx-include). Modules
// embed this beside the rendered form, like OfflineQueueJS / RepeatingJS.
const UnknownToggleJS = `(function(){
  function setDisabled(field, on){
    if (!field) return;
    var nodes = field.querySelectorAll("input, select, textarea");
    for (var i=0;i<nodes.length;i++){
      var el = nodes[i];
      if (!el.name) continue;
      if (el.name.slice(-10)===".__unknown") continue;
      el.disabled = !!on;
    }
  }
  document.addEventListener("change", function(e){
    var t = e.target;
    if (!t || !t.name || t.name.slice(-10)!==".__unknown" || t.type!=="checkbox") return;
    var field = t.closest(".tv-field");
    setDisabled(field, t.checked);
  });
})();`
