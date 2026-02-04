// Package validate implements ExtractCheck: validation of structured JSON
// that some other system already extracted from documents such as invoices
// or receipts. It does not read PDFs or images and performs no OCR.
//
// A document is checked against an optional JSON Schema and a declarative
// rules file (YAML, version 1). Rules describe presence, allowed values,
// decimal formats, arithmetic relationships (sums and products with
// explicit tolerance and rounding), dates and batch-level uniqueness. There
// are no expressions and no code execution: every rule type is a fixed,
// documented check.
//
// All money arithmetic uses github.com/shopspring/decimal. Values are
// parsed from json.Number or from decimal strings and never pass through
// float64, so 0.1 + 0.2 equals 0.3 exactly.
package validate

// Bounds applied while parsing rules and checking documents. Exceeding a
// bound is an error (or an inconclusive finding during checks), never a
// silent truncation.
const (
	// MaxRulesBytes bounds a rules file.
	MaxRulesBytes = 1 << 20
	// MaxRules bounds the number of rules in one file.
	MaxRules = 500
	// MaxFieldsPerRule bounds fields, terms, factors and allowed values
	// lists of a single rule.
	MaxFieldsPerRule = 256
	// MaxAllowedValues bounds the values list of an allowed_values rule.
	MaxAllowedValues = 1000
	// MaxPatternLength bounds the source of a pattern rule's regexp.
	MaxPatternLength = 1024
	// MaxMatches bounds how many values one wildcard field pattern may
	// match in one document.
	MaxMatches = 10000
	// MaxDocuments bounds the documents in one batch file.
	MaxDocuments = 100000
	// MaxIDLength bounds a document identifier taken from the document.
	MaxIDLength = 128
	// MaxRuleIDLength bounds a rule identifier.
	MaxRuleIDLength = 64
)

// CurrentVersion is the only rules file version this package reads.
const CurrentVersion = 1

// rulesFile is the YAML layout of a rules file. It is decoded with
// yamlutil.DecodeStrict, so unknown keys anywhere are rejected.
type rulesFile struct {
	Version    int        `yaml:"version"`
	Schema     string     `yaml:"schema"`
	DocumentID string     `yaml:"document_id"`
	Rules      []ruleSpec `yaml:"rules"`
}

// ruleSpec is the union of every rule type's keys. Strict YAML decoding
// rejects keys that no rule type knows; parse.go additionally rejects keys
// that the rule's own type does not use.
type ruleSpec struct {
	ID          string `yaml:"id"`
	Type        string `yaml:"type"`
	Description string `yaml:"description"`
	Severity    string `yaml:"severity"`

	Field  string   `yaml:"field"`
	Fields []string `yaml:"fields"`

	// required
	AllowNull bool `yaml:"allow_null"`

	// allowed_values, pattern
	Values  []string `yaml:"values"`
	Pattern string   `yaml:"pattern"`

	// decimal
	Accept   []string `yaml:"accept"`
	MaxScale *int     `yaml:"max_scale"`
	Min      *string  `yaml:"min"`
	Max      *string  `yaml:"max"`

	// sum, product, line items
	Terms     []termSpec `yaml:"terms"`
	Factors   []string   `yaml:"factors"`
	Equals    string     `yaml:"equals"`
	Tolerance string     `yaml:"tolerance"`
	Round     *roundSpec `yaml:"round"`
	Items     string     `yaml:"items"`
	Amount    string     `yaml:"amount"`
	Quantity  string     `yaml:"quantity"`
	UnitPrice string     `yaml:"unit_price"`

	// date, date_order
	Layout     string `yaml:"layout"`
	Before     string `yaml:"before"`
	After      string `yaml:"after"`
	AllowEqual bool   `yaml:"allow_equal"`
}

// termSpec is one term of a sum rule.
type termSpec struct {
	Field string `yaml:"field"`
	// Sign is +1 (default when omitted) or -1.
	Sign *int `yaml:"sign"`
	// Optional terms that are absent or null contribute nothing instead
	// of making the rule inconclusive.
	Optional bool `yaml:"optional"`
}

// roundSpec configures rounding of a computed value before comparison.
type roundSpec struct {
	Scale *int   `yaml:"scale"`
	Mode  string `yaml:"mode"`
}

// Rule type names as written in rules files. Finding codes are
// "validate.<type>".
const (
	typeRequired        = "required"
	typeAllowedValues   = "allowed_values"
	typePattern         = "pattern"
	typeDecimal         = "decimal"
	typeSum             = "sum"
	typeLineItemsSum    = "line_items_sum"
	typeLineItemProduct = "line_item_product"
	typeProduct         = "product"
	typeDate            = "date"
	typeDateOrder       = "date_order"
	typeUnique          = "unique"
)
