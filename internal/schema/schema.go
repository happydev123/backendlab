// Package schema compiles and applies JSON Schema documents. It wraps
// github.com/santhosh-tekuri/jsonschema/v6 with BackendLab's conventions:
// schemas are single local files (internal "$ref"s to "#/$defs/..." work,
// remote and cross-file references do not), input size is bounded, and
// errors come back as a flat, deterministic list of instance locations.
package schema

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/happydev123/backendlab/internal/jsonutil"
)

// MaxSchemaBytes bounds a schema file.
const MaxSchemaBytes = 1 << 20

// MaxErrors bounds the violations returned for one instance.
const MaxErrors = 50

// Schema is a compiled JSON Schema.
type Schema struct {
	name     string
	compiled *jsonschema.Schema
}

// Violation is one schema failure at an instance location.
type Violation struct {
	// Location is a JSON Pointer into the validated instance.
	Location string
	// Keyword is the schema keyword path that failed ("/properties/total/type").
	Keyword string
	Message string
}

// CompileFile reads and compiles the schema at path.
func CompileFile(path string) (*Schema, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open schema: %w", err)
	}
	defer f.Close()
	data, err := jsonutil.ReadAllLimit(f, MaxSchemaBytes)
	if err != nil {
		return nil, fmt.Errorf("schema %s: %w", path, err)
	}
	return Compile(filepath.Base(path), data)
}

// Compile compiles schema data. name identifies it in messages.
func Compile(name string, data []byte) (*Schema, error) {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("schema %s: parse: %w", name, err)
	}
	c := jsonschema.NewCompiler()
	// The library's default loader reads file:// URLs. Replace it so only
	// the in-memory document and the bundled meta-schemas resolve; remote
	// and file references fail to compile.
	c.UseLoader(denyLoader{})
	loc := "mem://schema/" + sanitizeName(name)
	if err := c.AddResource(loc, doc); err != nil {
		return nil, fmt.Errorf("schema %s: %w", name, err)
	}
	compiled, err := c.Compile(loc)
	if err != nil {
		return nil, fmt.Errorf("schema %s: compile: %w", name, err)
	}
	return &Schema{name: name, compiled: compiled}, nil
}

type denyLoader struct{}

func (denyLoader) Load(url string) (any, error) {
	return nil, fmt.Errorf("external schema reference %q is not supported; inline it under $defs", url)
}

func sanitizeName(name string) string {
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "schema.json"
	}
	return b.String()
}

// Name returns the schema's display name.
func (s *Schema) Name() string { return s.name }

// Validate checks v (decoded with json.Number numbers) and returns the
// violations sorted by location, at most MaxErrors of them. A nil slice
// means the instance is valid.
func (s *Schema) Validate(v any) []Violation {
	err := s.compiled.Validate(v)
	if err == nil {
		return nil
	}
	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		return []Violation{{Location: "", Keyword: "", Message: err.Error()}}
	}
	out := []Violation{}
	collectLeaves(ve.BasicOutput(), &out)
	if len(out) == 0 {
		out = append(out, Violation{Message: "instance does not match schema"})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Location != out[j].Location {
			return out[i].Location < out[j].Location
		}
		if out[i].Keyword != out[j].Keyword {
			return out[i].Keyword < out[j].Keyword
		}
		return out[i].Message < out[j].Message
	})
	out = dedupe(out)
	if len(out) > MaxErrors {
		out = out[:MaxErrors]
	}
	return out
}

func collectLeaves(u *jsonschema.OutputUnit, out *[]Violation) {
	if u == nil {
		return
	}
	if u.Error != nil && len(u.Errors) == 0 {
		msg := u.Error.String()
		// The root unit of the basic output carries a generic summary.
		if u.KeywordLocation != "" || !strings.HasPrefix(msg, "jsonschema validation failed") {
			*out = append(*out, Violation{Location: u.InstanceLocation, Keyword: u.KeywordLocation, Message: msg})
		}
	}
	for i := range u.Errors {
		collectLeaves(&u.Errors[i], out)
	}
}

func dedupe(vs []Violation) []Violation {
	out := vs[:0]
	for i, v := range vs {
		if i > 0 && v == vs[i-1] {
			continue
		}
		out = append(out, v)
	}
	return out
}
