package modules

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/CharitonMedia/togavsu/internal/modules/yamlsubset"
)

// ErrYAML is a manifest that is not in the supported YAML subset.
var ErrYAML = yamlsubset.ErrYAML

// decodeYAML parses the supported YAML subset and decodes it into out through JSON, so
// manifest structs carry ordinary json tags and unknown keys can be refused.
func decodeYAML(src []byte, out any) error {
	raw, err := yamlsubset.ToJSON(src)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("%w: %v", ErrYAML, err)
	}
	return nil
}
