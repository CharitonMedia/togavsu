package api

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
)

var eventRegistry = sync.OnceValues(func() (map[string]bool, error) {
	var doc struct {
		Events []string `json:"x-togavsu-core-events"`
	}
	if err := json.Unmarshal(OpenAPI, &doc); err != nil {
		return nil, err
	}
	if len(doc.Events) == 0 {
		return nil, errors.New("core event registry is empty")
	}
	names := map[string]bool{}
	for _, name := range doc.Events {
		names[name] = true
	}
	return names, nil
})

// CoreEventNames returns the shared registry of published and emitted core actions.
func CoreEventNames() ([]string, error) {
	names, err := eventRegistry()
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(names))
	for name := range names {
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}

// CoreEventAction fails closed if the generated registry is unavailable.
func CoreEventAction(action string) bool {
	names, err := eventRegistry()
	return err != nil || names[action]
}

// CoreEventNamespace reserves the root of every core action for binding events.
func CoreEventNamespace(action string) (bool, error) {
	names, err := eventRegistry()
	if err != nil {
		return true, err
	}
	root, _, _ := strings.Cut(action, ".")
	for name := range names {
		r, _, _ := strings.Cut(name, ".")
		if root == r {
			return true, nil
		}
	}
	return false, nil
}
