// Package finding defines the structured result shared by every BackendLab
// module: comparisons, assertions, stream checks, extraction validation and
// query-plan analysis all report through the same type.
package finding

import "fmt"

// Module names the component that produced a finding.
type Module string

const (
	ModuleReplay   Module = "replay"
	ModuleCompare  Module = "compare"
	ModuleStream   Module = "stream"
	ModuleValidate Module = "validate"
	ModulePlans    Module = "plans"
)

// Kind separates the four outcomes the product keeps apart: transport
// problems, failed assertions, differences between environments, and
// analysis that could not reach a conclusion. Observations record facts
// (such as timings) that are not judged pass or fail.
type Kind string

const (
	KindTransportError   Kind = "transport_error"
	KindAssertionFailure Kind = "assertion_failure"
	KindDifference       Kind = "difference"
	KindInconclusive     Kind = "inconclusive"
	KindUnsupported      Kind = "unsupported"
	KindObservation      Kind = "observation"
)

// Severity orders findings by consequence. Only SeverityError findings make
// a report fail.
type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
	SeverityInfo    Severity = "info"
)

// Finding is one structured result. All string fields must already be
// sanitized (redacted and bounded) by the producing module.
type Finding struct {
	Module   Module   `json:"module"`
	Code     string   `json:"code"`
	Kind     Kind     `json:"kind"`
	Severity Severity `json:"severity"`
	CaseID   string   `json:"case_id,omitempty"`
	// Location is a JSON Pointer, header name, event index, plan node path
	// or field path, depending on the module.
	Location string `json:"location,omitempty"`
	Message  string `json:"message"`
	Expected string `json:"expected,omitempty"`
	Observed string `json:"observed,omitempty"`
	// Details holds extra sanitized key/value context. Map keys are
	// encoded in sorted order by encoding/json, keeping output stable.
	Details map[string]string `json:"details,omitempty"`
}

// Failing reports whether the finding makes a run fail.
func (f Finding) Failing() bool { return f.Severity == SeverityError }

// Validate checks that the required fields are set and known.
func (f Finding) Validate() error {
	if f.Module == "" || f.Code == "" || f.Message == "" {
		return fmt.Errorf("finding requires module, code and message (got %q/%q)", f.Module, f.Code)
	}
	switch f.Kind {
	case KindTransportError, KindAssertionFailure, KindDifference, KindInconclusive, KindUnsupported, KindObservation:
	default:
		return fmt.Errorf("finding %s: unknown kind %q", f.Code, f.Kind)
	}
	switch f.Severity {
	case SeverityError, SeverityWarning, SeverityInfo:
	default:
		return fmt.Errorf("finding %s: unknown severity %q", f.Code, f.Severity)
	}
	return nil
}

// WithDetail returns a copy of f with key set to value in Details.
func (f Finding) WithDetail(key, value string) Finding {
	d := make(map[string]string, len(f.Details)+1)
	for k, v := range f.Details {
		d[k] = v
	}
	d[key] = value
	f.Details = d
	return f
}
