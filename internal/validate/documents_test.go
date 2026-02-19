package validate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/happydev123/backendlab/internal/jsonutil"
	"github.com/happydev123/backendlab/internal/limits"
)

func TestLoadDocumentsLayouts(t *testing.T) {
	tests := []struct {
		file string
		n    int
	}{
		{"single.json", 1},
		{"array.json", 2},
		{"lines.jsonl", 3},
	}
	for _, tt := range tests {
		path := filepath.Join("testdata", "documents", tt.file)
		docs, in, err := LoadDocuments(path, limits.Limits{})
		if err != nil {
			t.Fatalf("%s: %v", tt.file, err)
		}
		if len(docs) != tt.n {
			t.Fatalf("%s: %d documents", tt.file, len(docs))
		}
		for i, d := range docs {
			if d.Index != i || d.ID != "" {
				t.Fatalf("%s: document %d = %+v", tt.file, i, d)
			}
		}
		data, _ := os.ReadFile(path)
		sum := sha256.Sum256(data)
		if in.Kind != "documents" || in.Name != "testdata/documents/"+tt.file || in.SHA256 != hex.EncodeToString(sum[:]) {
			t.Fatalf("%s: input = %+v", tt.file, in)
		}
		if tt.n > 1 {
			if _, ok := docs[1].Value.(map[string]any)["total"].(json.Number); !ok {
				t.Fatalf("%s: numbers must stay json.Number", tt.file)
			}
		}
	}
}

func TestParseDocumentsErrors(t *testing.T) {
	tests := []struct {
		name, data, err string
	}{
		{"empty", "  \n", "no documents"},
		{"empty array", "[]", "array is empty"},
		{"array of scalars", `[{"a":1}, 2]`, "document 1 is a number, want object"},
		{"scalar", `"x"`, "JSON object, a JSON array of objects, or JSON Lines"},
		{"trailing garbage after array", `[{"a":1}] x`, "unexpected data after top-level value"},
		{"trailing garbage after object", `{"a":1} x`, "unexpected data after top-level value"},
		{"malformed multi-line object", "{\n\"a\": 1,\n", "parse JSON"},
		{"jsonl with garbage", "{\"a\":1}\n{\"b\":2} x\n", "line 2"},
		{"jsonl with array line", "{\"a\":1}\n[1]\n", "line 2: document is an array, want object"},
		{"jsonl bad line", "{\"a\":1}\n{oops}\n", "line 2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseDocuments([]byte(tt.data))
			if err == nil || !strings.Contains(err.Error(), tt.err) {
				t.Fatalf("err = %v, want %q", err, tt.err)
			}
		})
	}
	docs, err := ParseDocuments([]byte("\xef\xbb\xbf{\"a\":1}"))
	if err != nil || len(docs) != 1 {
		t.Fatalf("BOM: %v", err)
	}
	docs, err = ParseDocuments([]byte("{\"a\":1}\r\n\r\n{\"b\":2}\r\n"))
	if err != nil || len(docs) != 2 || docs[1].Index != 1 {
		t.Fatalf("CRLF JSON Lines: %v %+v", err, docs)
	}
	if _, _, err := LoadDocuments(filepath.Join("testdata", "documents", "garbage.jsonl"), limits.Limits{}); err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("garbage file: %v", err)
	}
	if _, _, err := LoadDocuments(filepath.Join("testdata", "documents", "absent.json"), limits.Limits{}); err == nil {
		t.Fatal("missing file accepted")
	}
}

func TestLoadDocumentsSizeBound(t *testing.T) {
	path := filepath.Join("testdata", "documents", "array.json")
	_, _, err := LoadDocuments(path, limits.Limits{MaxDocumentBytes: 16})
	if !errors.Is(err, jsonutil.ErrTooLarge) {
		t.Fatalf("err = %v", err)
	}
}
