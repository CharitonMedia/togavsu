package dashboard

import (
	"fmt"

	"github.com/CharitonMedia/togavsu/internal/settings"
)

// templateDict builds a map for nested admin templates (settings-table).
func templateDict(values ...any) (map[string]any, error) {
	if len(values)%2 != 0 {
		return nil, fmt.Errorf("dict needs even args")
	}
	out := make(map[string]any, len(values)/2)
	for i := 0; i < len(values); i += 2 {
		key, ok := values[i].(string)
		if !ok {
			return nil, fmt.Errorf("dict keys must be strings")
		}
		out[key] = values[i+1]
	}
	return out, nil
}

func buildSettingRow(e settings.Entry, byKey map[string]settings.Entry, presetColors map[string]string) settingRow {
	row := settingRow{
		Entry:         e,
		Changed:       e.Overridden && e.Value != e.Default,
		Plain:         settings.PlainOf(e.Definition),
		DefaultPlain:  settings.DisplayPlain(e.Definition, e.Default),
		ValuePlain:    settings.DisplayPlain(e.Definition, e.Value),
		ReadOnlyPlain: settings.HasAdvancedJSON(e.Definition),
	}
	switch row.Plain {
	case settings.PlainDuration:
		row.Amount, row.Unit, _ = settings.DurationParts(e.Value)
	case settings.PlainBytes:
		row.Amount, row.Unit, _ = settings.BytesParts(e.Value)
	case settings.PlainLines, settings.PlainCaseExamples, settings.PlainRetry:
		row.Lines, _ = settings.FormatLinesPlain(e.Value)
		if row.Plain == settings.PlainRetry {
			row.Lines, _ = settings.FormatRetryPlain(e.Value)
		}
	case settings.PlainNotifications:
		row.Notify = settings.NotifyRows(e.Value)
	case settings.PlainColors:
		row.Colors = settings.ColorRows(e.Value, presetColors)
	case settings.PlainNaming:
		row.NamingSample = settings.NamingSample(e.Value)
	}
	if e.Key == "practice.case_number_examples" {
		pat := byKey["practice.case_number_patterns"]
		row.CustomPatterns = !settings.PatternsMatchExamples(e.Value, pat.Value)
	}
	return row
}

func advancedHelp(key string) string {
	switch key {
	case "ai.model_prices":
		return `JSON object of model name to {"input": cents, "output": cents} per million tokens.`
	case "ai.task_overrides":
		return `JSON object of task name to provider and model fields.`
	default:
		return ""
	}
}
