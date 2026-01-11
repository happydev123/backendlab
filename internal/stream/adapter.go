package stream

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/happydev123/backendlab/internal/finding"
)

// AdapterChatDelta interprets JSON payloads that carry incremental text.
// It recognizes three common shapes:
//
//	{"choices":[{"delta":{"content":"..."},"finish_reason":null}]}
//	{"delta":"..."}
//	{"type":"...delta","delta":{"text":"..."}}
//
// Objects whose "type" does not end in "delta" are counted as lifecycle
// events and otherwise ignored. Anything else is unrecognized, which is
// reported as inconclusive and never as a failure.
const AdapterChatDelta = "chat-delta"

// maxFinishReasons bounds the distinct finish reasons kept.
const maxFinishReasons = 10

func knownAdapter(name string) bool { return name == AdapterChatDelta }

// AdapterResult summarizes what an adapter assembled from the payloads.
type AdapterResult struct {
	Name string
	// Text is the assembled text, at most the configured byte bound. It
	// is stream data: redact it before printing.
	Text string
	// Preview is Text with registered secrets scrubbed, truncated to
	// Limits.MaxValuePreview, ready for display.
	Preview string
	// TextBytes counts all assembled bytes, including any beyond the bound.
	TextBytes int64
	// Truncated is set when Text holds only the first part.
	Truncated bool
	// Deltas counts events that carried a recognized delta.
	Deltas int
	// Lifecycle counts recognized non-delta events (typed objects).
	Lifecycle int
	// Unrecognized counts events whose payload had no known shape.
	Unrecognized int
	// FirstUnrecognized is the index of the first unrecognized event, or -1.
	FirstUnrecognized int
	// FinishReasons lists distinct finish or stop reasons in order of
	// first appearance, redacted and truncated like Preview.
	FinishReasons []string
}

// chatDelta accumulates text from delta payloads.
type chatDelta struct {
	res     AdapterResult
	text    strings.Builder
	maxText int
}

func newChatDelta(maxText int) *chatDelta {
	return &chatDelta{res: AdapterResult{Name: AdapterChatDelta, FirstUnrecognized: -1}, maxText: maxText}
}

// observe interprets one decoded payload. ok is false when payload was
// not valid JSON; such events count as unrecognized.
func (c *chatDelta) observe(index int, payload any, ok bool) {
	obj, isObj := payload.(map[string]any)
	if !ok || !isObj {
		c.unrecognized(index)
		return
	}
	if choices, has := obj["choices"].([]any); has {
		c.choices(index, choices)
		return
	}
	typ, hasType := obj["type"].(string)
	switch d := obj["delta"].(type) {
	case string:
		c.delta(d)
		return
	case map[string]any:
		if hasType && strings.HasSuffix(typ, "delta") {
			text, hasText := d["text"].(string)
			reason, hasReason := d["stop_reason"].(string)
			if hasText || hasReason {
				if hasText {
					c.delta(text)
				} else {
					c.res.Deltas++
				}
				if hasReason {
					c.finish(reason)
				}
				return
			}
		}
	}
	if hasType && !strings.HasSuffix(typ, "delta") {
		c.res.Lifecycle++
		return
	}
	c.unrecognized(index)
}

// choices handles the first element of a "choices" array. Additional
// choices (n > 1) are not interleaved into the assembled text.
func (c *chatDelta) choices(index int, choices []any) {
	if len(choices) == 0 {
		c.unrecognized(index)
		return
	}
	choice, ok := choices[0].(map[string]any)
	if !ok {
		c.unrecognized(index)
		return
	}
	delta, hasDelta := choice["delta"].(map[string]any)
	reason, hasReason := choice["finish_reason"].(string)
	if !hasDelta && !hasReason {
		c.unrecognized(index)
		return
	}
	switch text, isText := delta["content"].(string); {
	case isText:
		c.delta(text)
	case hasDelta:
		c.res.Deltas++ // role-only or tool deltas carry no text
	default:
		c.res.Lifecycle++ // finish_reason without a delta object
	}
	if hasReason && reason != "" {
		c.finish(reason)
	}
}

func (c *chatDelta) delta(text string) {
	c.res.Deltas++
	c.res.TextBytes += int64(len(text))
	if room := c.maxText - c.text.Len(); room < len(text) {
		c.res.Truncated = true
		text = truncateUTF8(text, room)
	}
	c.text.WriteString(text)
}

func (c *chatDelta) finish(reason string) {
	for _, r := range c.res.FinishReasons {
		if r == reason {
			return
		}
	}
	if len(c.res.FinishReasons) < maxFinishReasons {
		c.res.FinishReasons = append(c.res.FinishReasons, reason)
	}
}

func (c *chatDelta) unrecognized(index int) {
	if c.res.Unrecognized == 0 {
		c.res.FirstUnrecognized = index
	}
	c.res.Unrecognized++
}

// result returns the summary.
func (c *chatDelta) result() *AdapterResult {
	r := c.res
	r.Text = c.text.String()
	r.FinishReasons = append([]string(nil), c.res.FinishReasons...)
	return &r
}

// truncateUTF8 cuts s to at most n bytes without splitting a rune.
func truncateUTF8(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	for n > 0 && (s[n]&0xC0) == 0x80 {
		n--
	}
	return s[:n]
}

// report adds the adapter's inconclusive findings to s. Unknown shapes
// never fail a run: the adapter is an interpretation aid, not a contract.
func (c *chatDelta) report(s *sink, events int) {
	r := c.res
	if r.Unrecognized > 0 {
		s.add(finding.Finding{
			Code: CodeAdapterUnrecognized, Kind: finding.KindInconclusive, Severity: finding.SeverityInfo,
			Location: eventLocation(r.FirstUnrecognized),
			Message:  fmt.Sprintf("%d of %d events have no payload shape the %s adapter recognizes", r.Unrecognized, events, AdapterChatDelta),
			Details:  map[string]string{"unrecognized": strconv.Itoa(r.Unrecognized)},
		})
	}
	if events > 0 && r.Deltas == 0 {
		s.issue(CodeAdapterNoDeltas, finding.KindInconclusive, finding.SeverityInfo, "",
			fmt.Sprintf("the %s adapter found no delta events; no text was assembled", AdapterChatDelta))
	}
	if r.Truncated {
		s.add(finding.Finding{
			Code: CodeAdapterTruncated, Kind: finding.KindObservation, Severity: finding.SeverityInfo,
			Message: fmt.Sprintf("assembled text has %d bytes; only the first %d are kept", r.TextBytes, c.maxText),
		})
	}
}
