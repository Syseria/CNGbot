package parser

import (
	"testing"
	"time"
)

func TestInjectDiscordTimestamps(t *testing.T) {
	// 7 September 2026, 21:00 UTC
	sentAt := time.Date(2026, time.September, 7, 21, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		// Absolute times
		{"Absolute time with Eve Time", "21:00 Eve Time", "21:00 Eve Time (<t:1788814800:F>)"},
		{"Absolute time only", "22:00", "22:00 (<t:1788818400:F>)"},
		{"Absolute time next day", "20:00 eve time", "20:00 eve time (<t:1788897600:F>)"},
		{"Absolute time next day h format", "20h00 eve", "20h00 eve (<t:1788897600:F>)"},
		{"Absolute time next day no colon", "2000 eve time", "2000 eve time (<t:1788897600:F>)"},
		{"Typo Eve Timke next day", "0:00 Eve Timke", "0:00 Eve Timke (<t:1788825600:F>)"},

		// Absolute dates
		{"Full date text", "Saturday, 5 September 2026 18:21", "Saturday, 5 September 2026 18:21 (<t:1788632460:F>)"},
		{"Ordinal date with @", "10th of September @ 20:00 Eve Time", "10th of September @ 20:00 Eve Time (<t:1789070400:F>)"},
		{"Date with @", "10 September @ 20:00 Eve Time", "10 September @ 20:00 Eve Time (<t:1789070400:F>)"},
		{"Date with Eve only", "10th of September @ 20:00 Eve", "10th of September @ 20:00 Eve (<t:1789070400:F>)"},
		{"Date without @", "10th of September 20:00 Eve Time", "10th of September 20:00 Eve Time (<t:1789070400:F>)"},
		{"Ordinal 1st with @", "1st of September @ 20:00 Eve Time", "1st of September @ 20:00 Eve Time (<t:1788292800:F>)"},
		{"Ordinal 2nd with @", "2nd of September @ 20:00 Eve Time", "2nd of September @ 20:00 Eve Time (<t:1788379200:F>)"},
		{"Ordinal 3rd with @", "3rd of September @ 20:00 Eve Time", "3rd of September @ 20:00 Eve Time (<t:1788465600:F>)"},
		{"Date without @ no ordinal", "1 September 20:00", "1 September 20:00 (<t:1788292800:F>)"},
		{"Day of this month", "27th of this month", "27th of this month (<t:1790467200:F>)"},

		// Relative times
		{"In days capitalized", "In 7 days", "In 7 days (<t:1789419600:F>)"},
		{"After days", "after 7 days", "after 7 days (<t:1789419600:F>)"},
		{"In days and hours", "in 6 days 23 hours", "in 6 days 23 hours (<t:1789416000:F>)"},
		{"In hours", "in 2 hours", "in 2 hours (<t:1788822000:F>)"},

		// Contextual days
		{"Time on day", "19:30 on friday", "19:30 on friday (<t:1789155000:F>)"},
		{"Until time next day", "until 20:00 the next day", "until 20:00 the next day (<t:1788897600:F>)"},
		{"Day before time", "THURSDAY before 19:00", "THURSDAY before 19:00 (<t:1789066800:F>)"},
		{"Day around time", "THURSDAY around 19:00", "THURSDAY around 19:00 (<t:1789066800:F>)"},
		{"Next weekday time", "next friday at 19:00", "next friday at 19:00 (<t:1789153200:F>)"},
		{"Weekday after time past", "monday after 19:00 eve time", "monday after 19:00 eve time (<t:1789412400:F>)"},
		{"Weekday before time past", "monday before 10h00", "monday before 10h00 (<t:1789380000:F>)"},
		{"Time today", "20:00 eve online time today", "20:00 eve online time today (<t:1788897600:F>)"},

		// Sentences and Idempotence
		{"Sentence with multiple times", "Fleet is at 20:00 eve time and second fleet is at 22:00.", "Fleet is at 20:00 eve time (<t:1788897600:F>) and second fleet is at 22:00 (<t:1788818400:F>)."},
		{"Already formatted timestamp", "21:00 Eve Time (<t:1788814800:F>)", "21:00 Eve Time (<t:1788814800:F>)"},
		{"Time inside HTML href tag not matched", `<a href="https://example.com/op?time=20:00">Link</a>`, `<a href="https://example.com/op?time=20:00">Link</a>`},
		{"Time inside anchor text matched", `<a href="https://example.com">Fleet at 20:00 eve time</a>`, `<a href="https://example.com">Fleet at 20:00 eve time (<t:1788897600:F>)</a>`},
		{"Time inside markdown link URL not matched", `[Link](https://example.com/op?time=20:00)`, `[Link](https://example.com/op?time=20:00)`},
		{"Time inside markdown link text matched", `[Fleet at 20:00 eve time](https://example.com)`, `[Fleet at 20:00 eve time (<t:1788897600:F>)](https://example.com)`},

		// Negative matches (should not change)
		{"Bare duration 7 days", "7 days", "7 days"},
		{"Bare duration 1 day", "1 day", "1 day"},
		{"Bare duration 1 week", "1 week", "1 week"},
		{"Bare duration 2 weeks", "2 weeks", "2 weeks"},
		{"Bare duration 1 month", "1 month", "1 month"},
		{"Bare duration 2 months", "2 months", "2 months"},
		{"Bare duration 24 hours", "24 hours", "24 hours"},
		{"Bare duration 1 hour", "1 hour", "1 hour"},
		{"Bare duration 30 minutes", "30 minutes", "30 minutes"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := InjectDiscordTimestamps(tt.input, sentAt)
			if got != tt.expected {
				t.Errorf("InjectDiscordTimestamps() = %v, want %v", got, tt.expected)
			}
		})
	}
}
