package mongomig

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"
)

// MongoDB's export formats wrap values in type markers — {"$oid": "..."},
// {"$numberLong": "123"} and so on — and mongoexport emits either one JSON
// document per line or a single array. The types below unwrap that so an export
// can be imported without a running MongoDB.

// extString reads a value that may be a plain string or {"$oid": "..."}.
type extString string

func (v *extString) UnmarshalJSON(b []byte) error {
	var plain string
	if err := json.Unmarshal(b, &plain); err == nil {
		*v = extString(plain)
		return nil
	}

	var wrapper map[string]json.RawMessage
	if err := json.Unmarshal(b, &wrapper); err != nil {
		return fmt.Errorf("expected a string, got %s", b)
	}
	for _, key := range []string{"$oid", "$symbol", "$code"} {
		if raw, ok := wrapper[key]; ok {
			var s string
			if err := json.Unmarshal(raw, &s); err != nil {
				return err
			}
			*v = extString(s)
			return nil
		}
	}
	return fmt.Errorf("expected a string, got %s", b)
}

func (v extString) String() string { return string(v) }

// extInt64 reads a number that may be plain, or wrapped as $numberLong,
// $numberInt, $numberDouble or $date.
type extInt64 int64

func (v *extInt64) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		*v = 0
		return nil
	}

	// Parse through json.Number rather than float64: byte counters can exceed
	// what a float64 represents exactly, and silently rounding a usage total is
	// worse than failing.
	var number json.Number
	if err := json.Unmarshal(b, &number); err == nil {
		n, err := number.Int64()
		if err == nil {
			*v = extInt64(n)
			return nil
		}
		f, err := number.Float64()
		if err != nil {
			return fmt.Errorf("parse number %s: %w", b, err)
		}
		*v = extInt64(f)
		return nil
	}

	var wrapper map[string]json.RawMessage
	if err := json.Unmarshal(b, &wrapper); err != nil {
		return fmt.Errorf("expected a number, got %s", b)
	}

	for _, key := range []string{"$numberLong", "$numberInt", "$numberDouble", "$numberDecimal", "$date"} {
		raw, ok := wrapper[key]
		if !ok {
			continue
		}
		// $date nests a $numberLong in the canonical form, and is an ISO string
		// in the relaxed one.
		if key == "$date" {
			var nested extInt64
			if err := nested.UnmarshalJSON(raw); err == nil {
				*v = nested
				return nil
			}
			var iso string
			if err := json.Unmarshal(raw, &iso); err == nil {
				ms, err := parseISO(iso)
				if err != nil {
					return err
				}
				*v = extInt64(ms)
				return nil
			}
			return fmt.Errorf("unrecognised $date: %s", raw)
		}

		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			if n, err := strconv.ParseInt(s, 10, 64); err == nil {
				*v = extInt64(n)
				return nil
			}
			// $numberDouble and $numberDecimal are not whole numbers.
			f, err := strconv.ParseFloat(s, 64)
			if err != nil {
				return fmt.Errorf("parse %s %q: %w", key, s, err)
			}
			*v = extInt64(f)
			return nil
		}
		return v.UnmarshalJSON(raw)
	}
	return fmt.Errorf("expected a number, got %s", b)
}

func (v extInt64) Int64() int64 { return int64(v) }

// extBool reads a boolean that may be missing or null.
type extBool bool

func (v *extBool) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		*v = false
		return nil
	}
	var plain bool
	if err := json.Unmarshal(b, &plain); err != nil {
		return fmt.Errorf("expected a boolean, got %s", b)
	}
	*v = extBool(plain)
	return nil
}

func parseISO(s string) (int64, error) {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.999Z"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UnixMilli(), nil
		}
	}
	return 0, fmt.Errorf("unrecognised date %q", s)
}

func skipSpace(b []byte) []byte { return bytes.TrimSpace(b) }

func newReader(b []byte) io.Reader { return bytes.NewReader(b) }

func isEOF(err error) bool { return errors.Is(err, io.EOF) }

// decodeDocuments reads either a JSON array of documents or one document per
// line, which are the two shapes mongoexport produces.
func decodeDocuments(data []byte, into func(json.RawMessage) error) error {
	trimmed := skipSpace(data)
	if len(trimmed) == 0 {
		return nil
	}

	if trimmed[0] == '[' {
		var docs []json.RawMessage
		if err := json.Unmarshal(trimmed, &docs); err != nil {
			return fmt.Errorf("parse JSON array: %w", err)
		}
		for i, doc := range docs {
			if err := into(doc); err != nil {
				return fmt.Errorf("document %d: %w", i+1, err)
			}
		}
		return nil
	}

	decoder := json.NewDecoder(newReader(trimmed))
	for i := 1; ; i++ {
		var doc json.RawMessage
		err := decoder.Decode(&doc)
		if err != nil {
			if isEOF(err) {
				return nil
			}
			return fmt.Errorf("document %d: %w", i, err)
		}
		if err := into(doc); err != nil {
			return fmt.Errorf("document %d: %w", i, err)
		}
	}
}
