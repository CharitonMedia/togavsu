package help

import (
	_ "embed"
	"encoding/json"
	"fmt"

	"github.com/CharitonMedia/togavsu/internal/settings"
)

// limits are the help records' input-safety bounds and the guide search weights
// (fixed.json). They are not firm settings.
type limits struct {
	TitleMax         int `json:"title_max"`
	ItemBodyMax      int `json:"item_body_max"`
	PageMarkdownMax  int `json:"page_markdown_max"`
	LLMsParagraphMax int `json:"llms_paragraph_max"`
	WeightTitle      int `json:"weight_title"`
	WeightKeyword    int `json:"weight_keyword"`
	WeightBody       int `json:"weight_body"`
	WeightPhrase     int `json:"weight_phrase"`
}

//go:embed fixed.json
var fixedJSON []byte

var fixed = func() limits {
	var l limits
	if err := json.Unmarshal(fixedJSON, &l); err != nil {
		panic(fmt.Sprintf("help: fixed.json: %v", err))
	}
	return l
}()

// registryDefault is a setting's registry default.
func registryDefault(key string) string {
	for _, d := range settings.Definitions() {
		if d.Key == key {
			return d.Default
		}
	}
	return ""
}
