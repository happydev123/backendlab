package validate

import (
	"fmt"
	"time"
)

// maxDateText bounds date strings handed to time.Parse.
const maxDateText = 64

// namedLayouts are layout names accepted in addition to Go layouts.
var namedLayouts = map[string]string{
	"date":    "2006-01-02",
	"rfc3339": time.RFC3339,
}

// layoutProbe is the fixed instant used to check custom layouts. Its
// fields are all distinct so a layout that drops one is detected.
var layoutProbe = time.Date(2024, time.March, 9, 14, 5, 7, 0, time.UTC)

// dateLayout is a validated Go time layout.
type dateLayout struct {
	layout string
	name   string
}

// compileLayout accepts "date" (2006-01-02, the default when empty),
// "rfc3339", or a Go reference-time layout such as "02.01.2006". A custom
// layout must format and parse the probe instant back to the same calendar
// date: layouts without Go reference elements ("YYYY-MM-DD") or without a
// year, month or day are rejected, because ordering dates parsed with them
// would be meaningless.
func compileLayout(s string) (dateLayout, error) {
	if s == "" {
		s = "date"
	}
	if l, ok := namedLayouts[s]; ok {
		return dateLayout{layout: l, name: s}, nil
	}
	if len(s) > maxDateText {
		return dateLayout{}, fmt.Errorf("layout is longer than %d bytes", maxDateText)
	}
	out := layoutProbe.Format(s)
	if out == s {
		return dateLayout{}, fmt.Errorf("layout %q contains no Go reference-time elements (write dates as in 2006-01-02, or use date or rfc3339)", s)
	}
	t, err := time.Parse(s, out)
	if err != nil {
		return dateLayout{}, fmt.Errorf("layout %q cannot parse its own output: %v", s, err)
	}
	if t.Year() != layoutProbe.Year() || t.Month() != layoutProbe.Month() || t.Day() != layoutProbe.Day() {
		return dateLayout{}, fmt.Errorf("layout %q must include year, month and day", s)
	}
	return dateLayout{layout: s, name: s}, nil
}

// parse parses a date string with the layout. Date-only layouts yield
// midnight UTC; layouts with zone offsets compare as instants.
func (l dateLayout) parse(s string) (time.Time, error) {
	if len(s) > maxDateText {
		return time.Time{}, fmt.Errorf("longer than %d bytes", maxDateText)
	}
	t, err := time.Parse(l.layout, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("does not match layout %s", l.describe())
	}
	return t, nil
}

// describe renders the layout for messages: `date (2006-01-02)`.
func (l dateLayout) describe() string {
	if l.name != l.layout {
		return fmt.Sprintf("%s (%s)", l.name, l.layout)
	}
	return l.layout
}
