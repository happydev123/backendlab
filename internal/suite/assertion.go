package suite

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

var assertionKeys = map[string]bool{"pointer": true, "equals": true, "exists": true, "type": true, "matches": true}

// UnmarshalYAML decodes an assertion. It exists because yaml.v3 never
// passes a null node to a field's unmarshaler, so "equals: null" would
// otherwise be indistinguishable from an absent equals key. Unknown keys
// are rejected here since Node.Decode does not inherit KnownFields.
func (a *Assertion) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("line %d: assertion must be a mapping", n.Line)
	}
	explicitNull := false
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		if !assertionKeys[k.Value] {
			return fmt.Errorf("line %d: field %s not found in type suite.Assertion", k.Line, k.Value)
		}
		if k.Value == "equals" && v.Kind == yaml.ScalarNode && v.ShortTag() == "!!null" {
			explicitNull = true
		}
	}
	type plain Assertion
	var p plain
	if err := n.Decode(&p); err != nil {
		return err
	}
	*a = Assertion(p)
	if explicitNull {
		a.Equals = &JSONValue{V: nil}
	}
	return nil
}
