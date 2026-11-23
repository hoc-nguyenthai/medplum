package messaging

import "encoding/json"

func jsonMarshal(v any) (string, error) {
	b, err := json.Marshal(v)
	return string(b), err
}

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }
