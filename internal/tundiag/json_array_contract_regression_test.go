package tundiag

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestWriteJSONKeepsRequiredCollectionsAsArrays(t *testing.T) {
	var output bytes.Buffer
	if err := WriteJSON(&output, Report{}); err != nil {
		t.Fatal(err)
	}

	var document map[string]json.RawMessage
	if err := json.Unmarshal(output.Bytes(), &document); err != nil {
		t.Fatalf("decode diagnostic JSON: %v", err)
	}
	for _, field := range []string{"probes", "warnings", "errors"} {
		if got := string(document[field]); got != "[]" {
			t.Fatalf("required field %q serialized as %s; want []", field, got)
		}
	}
}
