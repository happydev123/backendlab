// Package yamlutil decodes configuration and suite YAML strictly: unknown
// keys, duplicate keys and multiple documents are errors.
package yamlutil

import (
	"bytes"
	"errors"
	"io"
	"strings"

	"gopkg.in/yaml.v3"
)

// DecodeStrict decodes a single YAML document into dst.
func DecodeStrict(data []byte, dst any) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return errors.New("empty document")
		}
		return clean(err)
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("multiple YAML documents are not supported")
	}
	return nil
}

// clean turns yaml.v3's multi-line error text into a single readable line.
func clean(err error) error {
	msg := strings.TrimPrefix(err.Error(), "yaml: ")
	msg = strings.ReplaceAll(msg, "unmarshal errors:\n  ", "")
	msg = strings.ReplaceAll(msg, "\n  ", "; ")
	return errors.New(msg)
}
