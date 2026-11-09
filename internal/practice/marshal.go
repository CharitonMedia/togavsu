package practice

import "encoding/json"

func marshalStrings(rows []string) (string, error) {
	if rows == nil {
		rows = []string{}
	}
	b, err := json.Marshal(rows)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
