package validate

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/happydev123/backendlab/internal/jsonutil"
	"github.com/happydev123/backendlab/internal/limits"
	"github.com/happydev123/backendlab/internal/report"
)

// LoadDocuments reads a documents file of at most lim.MaxDocumentBytes
// (zero fields of lim take their defaults) and parses it with
// ParseDocuments. Document IDs are left empty; Validator.Identify (called
// by Run) assigns them from the rules' document_id. The returned
// report.Input names the file and its SHA-256 digest.
func LoadDocuments(path string, lim limits.Limits) ([]Document, report.Input, error) {
	lim = lim.Merge()
	f, err := os.Open(path)
	if err != nil {
		return nil, report.Input{}, fmt.Errorf("open documents: %w", err)
	}
	defer f.Close()
	data, err := jsonutil.ReadAllLimit(f, lim.MaxDocumentBytes)
	if err != nil {
		return nil, report.Input{}, fmt.Errorf("documents %s: %w", path, err)
	}
	docs, err := ParseDocuments(data)
	if err != nil {
		return nil, report.Input{}, fmt.Errorf("documents %s: %w", path, err)
	}
	sum := sha256.Sum256(data)
	return docs, report.Input{Kind: "documents", Name: filepath.ToSlash(path), SHA256: hex.EncodeToString(sum[:])}, nil
}

// ParseDocuments accepts three layouts:
//
//   - a single JSON object: one document;
//   - a JSON array whose elements are all objects: one document each;
//   - JSON Lines: one object per line (blank lines are skipped).
//
// Numbers stay json.Number. Trailing data after a JSON value, non-object
// documents, empty input and more than MaxDocuments documents are errors.
func ParseDocuments(data []byte) ([]Document, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, errors.New("no documents (input is empty)")
	}
	switch trimmed[0] {
	case '[':
		v, err := jsonutil.Decode(trimmed)
		if err != nil {
			return nil, err
		}
		arr := v.([]any)
		if len(arr) == 0 {
			return nil, errors.New("no documents (the array is empty)")
		}
		if len(arr) > MaxDocuments {
			return nil, fmt.Errorf("more than %d documents", MaxDocuments)
		}
		docs := make([]Document, len(arr))
		for i, el := range arr {
			if _, ok := el.(map[string]any); !ok {
				return nil, fmt.Errorf("document %d is %s, want object", i, aType(el))
			}
			docs[i] = Document{Index: i, Value: el}
		}
		return docs, nil
	case '{':
		v, err := jsonutil.Decode(trimmed)
		if err == nil {
			return []Document{{Index: 0, Value: v}}, nil
		}
		if !firstLineIsObject(trimmed) {
			return nil, err
		}
		return parseJSONLines(trimmed)
	default:
		return nil, errors.New("documents must be a JSON object, a JSON array of objects, or JSON Lines")
	}
}

// firstLineIsObject reports whether the first line holds a complete JSON
// object, which distinguishes JSON Lines from a malformed multi-line
// object (whose own parse error is the useful one).
func firstLineIsObject(data []byte) bool {
	line, _, _ := bytes.Cut(data, []byte("\n"))
	v, err := jsonutil.Decode(bytes.TrimSpace(line))
	if err != nil {
		return false
	}
	_, ok := v.(map[string]any)
	return ok
}

func parseJSONLines(data []byte) ([]Document, error) {
	var docs []Document
	for n, line := range bytes.Split(data, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		if len(docs) >= MaxDocuments {
			return nil, fmt.Errorf("more than %d documents", MaxDocuments)
		}
		v, err := jsonutil.Decode(line)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", n+1, err)
		}
		if _, ok := v.(map[string]any); !ok {
			return nil, fmt.Errorf("line %d: document is %s, want object", n+1, aType(v))
		}
		docs = append(docs, Document{Index: len(docs), Value: v})
	}
	return docs, nil
}
