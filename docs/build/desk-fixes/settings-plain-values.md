# Plain setting values (desk fix 2)

Patrick's report from testing 1.0 on Desk, on the Case Number Patterns row: "There is no attorney on the
planet that can decode that." The row showed `["^\\d{2}[A-Z]{2}-[A-Z]{2}\\d+$"]` as both its value and its
default. Rule: no setting row shows code, a regular expression, JSON or a Go duration to the admin. Each
value is shown and edited in a plain form; anything that must stay raw sits behind an Advanced switch that is
closed by default. Stored values, keys, the API and the audit log keep their current formats, so nothing that
reads a setting changes.

## Case numbers

- Add the setting `practice.case_number_examples` (label "Case Number Formats", Practice tab, JSON list of
  strings, default `["23AR-CV00825"]`, help "Type one real case number per line in each format your courts
  use. Togavsu learns the format from the example.").
- Its row is the one the admin sees: a textarea with one example per line. Saving it derives one pattern per
  example and stores both settings in one transaction, with one audit entry each.
- Derivation, per example: trim it; each run of digits becomes `\d{n}`, except the last digit run, which
  becomes `\d+`; each run of letters becomes `[A-Z]{n}` (letters are matched upper case); any other character
  is escaped literally; anchor with `^` and `$`. `23AR-CV00825` must derive exactly
  `^\d{2}[A-Z]{2}-[A-Z]{2}\d+$`, the current default. Refuse an empty example, one with no digit, and one
  longer than 40 characters, with a plain error that names the example.
- The `practice.case_number_patterns` row moves behind Advanced on that tab. When the stored patterns are not
  the ones derived from the stored examples (set through the API or Advanced), the Case Number Formats row
  says "Custom patterns are in use; see Advanced." instead of the examples.
- Matching code keeps reading `practice.case_number_patterns`; nothing else changes there.

## Every other kind

- Lists of words (party roles, matter markers, default folders, activity types, expense types): a textarea,
  one item per line, stored as the same JSON list. Blank lines are dropped.
- Durations: a whole number and a unit picker (milliseconds, seconds, minutes, hours, days); shown as
  "12 hours", "15 minutes", "180 milliseconds" in the largest unit that holds the value exactly. Every value
  the old text form accepted, including sub-second ones such as search.delay's 180 ms, can be entered and is
  stored exactly. Defaults are shown the same way.
- Byte sizes (largest upload, upload chunk size, editor run memory): a number and MB or GB; shown as
  "2 GB", "8 MB", "1 GB".
- Doc Tender retry schedule: one delay per line in plain form ("Right away", "15 minutes", "2 hours"); stored as
  the same JSON list.
- Default notifications: a grid of checkboxes, one row per kind in plain words, columns Email and In app; the
  trust flag email box is checked and cannot be cleared.
- Custom colors: one row per named color, each with a "Use the preset" choice and a color picker. A color
  left on "Use the preset" is not written; only colors the admin picked are stored, so saving the form with
  nothing picked stores `{}` as before.
- Model prices and per-task model overrides: a plain read-only table of what is set (or "None set"); the
  JSON editor for each sits behind Advanced.
- File naming pattern: keep the token form, and show under it a sample file name built from it with sample
  values, so the admin sees what a file will be called.
- On/off settings stay On/Off. Money stays in dollars. Plain text stays plain text.
- The Default column uses the same plain form as the value. "(none)" reads "None".
- Help text holds no code. Where a setting's help shows JSON or a pattern (ai.model_prices today), reword it
  in plain words for the default view and move the format example next to the Advanced editor.

## Done means

On the admin page, at the Desk install, no row shows a regular expression, JSON, a Go duration such as
`12h0m0s`, or a raw byte count unless the admin opens Advanced. Saving any plain form stores the same value
the old form would have stored for the same input. Every existing setting test still passes.
