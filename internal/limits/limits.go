// Package limits defines the resource bounds applied to suites, requests,
// streams and stored reports. Every bound has a default and a hard maximum;
// configuration may lower or raise a value but never past the maximum.
package limits

import (
	"fmt"
	"time"
)

// Limits bounds work done by one command or one server run.
type Limits struct {
	MaxSuiteBytes    int64         `yaml:"max_suite_bytes" json:"max_suite_bytes"`
	MaxCases         int           `yaml:"max_cases" json:"max_cases"`
	MaxConcurrency   int           `yaml:"max_concurrency" json:"max_concurrency"`
	MaxRequestBytes  int64         `yaml:"max_request_bytes" json:"max_request_bytes"`
	MaxResponseBytes int64         `yaml:"max_response_bytes" json:"max_response_bytes"`
	RequestTimeout   time.Duration `yaml:"request_timeout" json:"request_timeout"`
	MaxRequestTime   time.Duration `yaml:"max_request_timeout" json:"max_request_timeout"`
	RunTimeout       time.Duration `yaml:"run_timeout" json:"run_timeout"`
	MaxStreamBytes   int64         `yaml:"max_stream_bytes" json:"max_stream_bytes"`
	MaxStreamEvents  int           `yaml:"max_stream_events" json:"max_stream_events"`
	MaxLineBytes     int           `yaml:"max_line_bytes" json:"max_line_bytes"`
	MaxEventBytes    int           `yaml:"max_event_bytes" json:"max_event_bytes"`
	MaxDifferences   int           `yaml:"max_differences" json:"max_differences"`
	MaxFindings      int           `yaml:"max_findings" json:"max_findings"`
	MaxValuePreview  int           `yaml:"max_value_preview" json:"max_value_preview"`
	MaxReportBytes   int64         `yaml:"max_report_bytes" json:"max_report_bytes"`
	MaxPlanBytes     int64         `yaml:"max_plan_bytes" json:"max_plan_bytes"`
	MaxDocumentBytes int64         `yaml:"max_document_bytes" json:"max_document_bytes"`
}

const (
	kib = 1 << 10
	mib = 1 << 20
)

// Default returns the default limits.
func Default() Limits {
	return Limits{
		MaxSuiteBytes:    1 * mib,
		MaxCases:         200,
		MaxConcurrency:   4,
		MaxRequestBytes:  256 * kib,
		MaxResponseBytes: 2 * mib,
		RequestTimeout:   10 * time.Second,
		MaxRequestTime:   2 * time.Minute,
		RunTimeout:       10 * time.Minute,
		MaxStreamBytes:   8 * mib,
		MaxStreamEvents:  10000,
		MaxLineBytes:     64 * kib,
		MaxEventBytes:    1 * mib,
		MaxDifferences:   200,
		MaxFindings:      2000,
		MaxValuePreview:  160,
		MaxReportBytes:   4 * mib,
		MaxPlanBytes:     16 * mib,
		MaxDocumentBytes: 4 * mib,
	}
}

// Hard returns the largest values configuration may set.
func Hard() Limits {
	return Limits{
		MaxSuiteBytes:    8 * mib,
		MaxCases:         2000,
		MaxConcurrency:   64,
		MaxRequestBytes:  8 * mib,
		MaxResponseBytes: 64 * mib,
		RequestTimeout:   10 * time.Minute,
		MaxRequestTime:   10 * time.Minute,
		RunTimeout:       2 * time.Hour,
		MaxStreamBytes:   256 * mib,
		MaxStreamEvents:  1000000,
		MaxLineBytes:     4 * mib,
		MaxEventBytes:    16 * mib,
		MaxDifferences:   10000,
		MaxFindings:      100000,
		MaxValuePreview:  4096,
		MaxReportBytes:   64 * mib,
		MaxPlanBytes:     128 * mib,
		MaxDocumentBytes: 64 * mib,
	}
}

// Merge returns l with every zero field taken from Default.
func (l Limits) Merge() Limits {
	d := Default()
	pick64 := func(v, def int64) int64 {
		if v == 0 {
			return def
		}
		return v
	}
	pick := func(v, def int) int {
		if v == 0 {
			return def
		}
		return v
	}
	pickD := func(v, def time.Duration) time.Duration {
		if v == 0 {
			return def
		}
		return v
	}
	return Limits{
		MaxSuiteBytes:    pick64(l.MaxSuiteBytes, d.MaxSuiteBytes),
		MaxCases:         pick(l.MaxCases, d.MaxCases),
		MaxConcurrency:   pick(l.MaxConcurrency, d.MaxConcurrency),
		MaxRequestBytes:  pick64(l.MaxRequestBytes, d.MaxRequestBytes),
		MaxResponseBytes: pick64(l.MaxResponseBytes, d.MaxResponseBytes),
		RequestTimeout:   pickD(l.RequestTimeout, d.RequestTimeout),
		MaxRequestTime:   pickD(l.MaxRequestTime, d.MaxRequestTime),
		RunTimeout:       pickD(l.RunTimeout, d.RunTimeout),
		MaxStreamBytes:   pick64(l.MaxStreamBytes, d.MaxStreamBytes),
		MaxStreamEvents:  pick(l.MaxStreamEvents, d.MaxStreamEvents),
		MaxLineBytes:     pick(l.MaxLineBytes, d.MaxLineBytes),
		MaxEventBytes:    pick(l.MaxEventBytes, d.MaxEventBytes),
		MaxDifferences:   pick(l.MaxDifferences, d.MaxDifferences),
		MaxFindings:      pick(l.MaxFindings, d.MaxFindings),
		MaxValuePreview:  pick(l.MaxValuePreview, d.MaxValuePreview),
		MaxReportBytes:   pick64(l.MaxReportBytes, d.MaxReportBytes),
		MaxPlanBytes:     pick64(l.MaxPlanBytes, d.MaxPlanBytes),
		MaxDocumentBytes: pick64(l.MaxDocumentBytes, d.MaxDocumentBytes),
	}
}

// Validate checks that every field is positive and within Hard.
func (l Limits) Validate() error {
	h := Hard()
	type check struct {
		name     string
		val, max int64
	}
	checks := []check{
		{"max_suite_bytes", l.MaxSuiteBytes, h.MaxSuiteBytes},
		{"max_cases", int64(l.MaxCases), int64(h.MaxCases)},
		{"max_concurrency", int64(l.MaxConcurrency), int64(h.MaxConcurrency)},
		{"max_request_bytes", l.MaxRequestBytes, h.MaxRequestBytes},
		{"max_response_bytes", l.MaxResponseBytes, h.MaxResponseBytes},
		{"request_timeout", int64(l.RequestTimeout), int64(h.RequestTimeout)},
		{"max_request_timeout", int64(l.MaxRequestTime), int64(h.MaxRequestTime)},
		{"run_timeout", int64(l.RunTimeout), int64(h.RunTimeout)},
		{"max_stream_bytes", l.MaxStreamBytes, h.MaxStreamBytes},
		{"max_stream_events", int64(l.MaxStreamEvents), int64(h.MaxStreamEvents)},
		{"max_line_bytes", int64(l.MaxLineBytes), int64(h.MaxLineBytes)},
		{"max_event_bytes", int64(l.MaxEventBytes), int64(h.MaxEventBytes)},
		{"max_differences", int64(l.MaxDifferences), int64(h.MaxDifferences)},
		{"max_findings", int64(l.MaxFindings), int64(h.MaxFindings)},
		{"max_value_preview", int64(l.MaxValuePreview), int64(h.MaxValuePreview)},
		{"max_report_bytes", l.MaxReportBytes, h.MaxReportBytes},
		{"max_plan_bytes", l.MaxPlanBytes, h.MaxPlanBytes},
		{"max_document_bytes", l.MaxDocumentBytes, h.MaxDocumentBytes},
	}
	for _, c := range checks {
		if c.val <= 0 {
			return fmt.Errorf("limits.%s must be positive", c.name)
		}
		if c.val > c.max {
			return fmt.Errorf("limits.%s exceeds the hard maximum (%d)", c.name, c.max)
		}
	}
	if l.RequestTimeout > l.MaxRequestTime {
		return fmt.Errorf("limits.request_timeout exceeds limits.max_request_timeout")
	}
	if l.MaxLineBytes > l.MaxEventBytes {
		return fmt.Errorf("limits.max_line_bytes exceeds limits.max_event_bytes")
	}
	return nil
}
