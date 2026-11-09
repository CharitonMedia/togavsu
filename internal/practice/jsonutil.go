package practice

import "encoding/json"

func jsonUnmarshal(raw []byte, dest any) error {
	return json.Unmarshal(raw, dest)
}
