package activitypostgres

import (
	"encoding/json"
	"fmt"
)

// StringArrayJSON scans a PostgreSQL TEXT[] projected through TO_JSON. The
// database/sql compatibility layer does not decode array text into []string.
type StringArrayJSON []string

func (value *StringArrayJSON) Scan(source any) error {
	var encoded []byte
	switch source := source.(type) {
	case []byte:
		encoded = source
	case string:
		encoded = []byte(source)
	case nil:
		*value = StringArrayJSON{}
		return nil
	default:
		return fmt.Errorf("scan PostgreSQL string array JSON from %T", source)
	}
	var decoded []string
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return fmt.Errorf("decode PostgreSQL string array JSON: %w", err)
	}
	if decoded == nil {
		decoded = []string{}
	}
	*value = decoded
	return nil
}
