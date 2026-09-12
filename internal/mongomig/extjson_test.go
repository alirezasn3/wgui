package mongomig

import (
	"encoding/json"
	"testing"
)

func TestExtInt64UnwrapsEveryFormMongoexportEmits(t *testing.T) {
	cases := map[string]int64{
		`42`:                                 42,
		`{"$numberLong":"9007199254740993"}`: 9007199254740993,
		`{"$numberInt":"7"}`:                 7,
		`{"$numberDouble":"3.9"}`:            3,
		`{"$numberLong":42}`:                 42,
		`{"$date":{"$numberLong":"1700000000000"}}`: 1700000000000,
		`{"$date":"2023-11-14T22:13:20Z"}`:          1700000000000,
		`null`:                                      0,
	}
	for input, want := range cases {
		var got extInt64
		if err := json.Unmarshal([]byte(input), &got); err != nil {
			t.Errorf("%s: %v", input, err)
			continue
		}
		if got.Int64() != want {
			t.Errorf("%s = %d, want %d", input, got.Int64(), want)
		}
	}
}

// A $numberLong carrying a value beyond float64's exact range must survive, or
// large usage totals would be silently rounded.
func TestExtInt64KeepsLargeValuesExact(t *testing.T) {
	var got extInt64
	if err := json.Unmarshal([]byte(`{"$numberLong":"9007199254740993"}`), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Int64() != 9007199254740993 {
		t.Errorf("got %d, want 9007199254740993 exactly", got.Int64())
	}
}

func TestExtStringUnwrapsObjectIDs(t *testing.T) {
	for input, want := range map[string]string{
		`"plain"`:                             "plain",
		`{"$oid":"6512aaaabbbbccccdddd0001"}`: "6512aaaabbbbccccdddd0001",
	} {
		var got extString
		if err := json.Unmarshal([]byte(input), &got); err != nil {
			t.Errorf("%s: %v", input, err)
			continue
		}
		if got.String() != want {
			t.Errorf("%s = %q, want %q", input, got.String(), want)
		}
	}
}

func TestDecodeDocumentsHandlesBothExportShapes(t *testing.T) {
	count := func(data string) int {
		n := 0
		if err := decodeDocuments([]byte(data), func(json.RawMessage) error { n++; return nil }); err != nil {
			t.Fatalf("decode %q: %v", data, err)
		}
		return n
	}

	if got := count("{\"a\":1}\n{\"a\":2}\n{\"a\":3}\n"); got != 3 {
		t.Errorf("line-delimited gave %d documents, want 3", got)
	}
	if got := count(`[{"a":1},{"a":2}]`); got != 2 {
		t.Errorf("array gave %d documents, want 2", got)
	}
	if got := count("   \n"); got != 0 {
		t.Errorf("empty input gave %d documents, want 0", got)
	}
	// Pretty-printed documents span several lines, so the decoder must follow
	// JSON structure rather than split on newlines.
	if got := count("{\n  \"a\": 1\n}\n{\n  \"a\": 2\n}\n"); got != 2 {
		t.Errorf("pretty-printed input gave %d documents, want 2", got)
	}
}

func TestDecodeDocumentsReportsWhereItFailed(t *testing.T) {
	err := decodeDocuments([]byte("{\"a\":1}\n{oops}\n"), func(json.RawMessage) error { return nil })
	if err == nil {
		t.Fatal("expected an error on malformed input")
	}
	if !contains(err.Error(), "document 2") {
		t.Errorf("error %q does not say which document failed", err)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
