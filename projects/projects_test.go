package projects

import (
	"strings"
	"testing"
	"time"
)

func TestCategoryForName(t *testing.T) {
	cases := map[string]string{
		"Compressed Veldspar II-Grade":  "ore",
		"Compressed Bezdnacine":         "ore",
		"Fullerite-C50":                 "gas",
		"Compressed Fullerite-C540":     "gas",
		"Neural Network Analyzer":       "blue_loot",
		"Ancient Coordinates Database":  "blue_loot",
		"Intact Armor Plates":           "salvage",
		"Ward Console":                  "salvage",
		"Datacore - Nuclear Physics":    "datacores",
		"Tritanium":                     "",
		"Compressed Veldspar IV-Grade ": "",
	}
	for name, want := range cases {
		if got := CategoryForName(name); got != want {
			t.Errorf("CategoryForName(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestFormatPercent(t *testing.T) {
	cases := map[float64]string{
		1: "100%", 0.5: "50%", 0.01: "1%", 0.125: "12.5%",
	}
	for pct, want := range cases {
		if got := formatPercent(pct); got != want {
			t.Errorf("formatPercent(%v) = %q, want %q", pct, got, want)
		}
	}
}

func TestBuildMessage(t *testing.T) {
	// Deliverable with an item link and finished state.
	msg := buildMessage("guild", "project", "Sarocor Mining Initiative", "completed", []deliverable{{GroupID: 460, Name: "Veldspar"}})
	for _, want := range []string{
		"**project finished (completed)** — Sarocor Mining Initiative",
		"Deliver: Veldspar (item group)",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("buildMessage() = %q, want substring %q", msg, want)
		}
	}

	// Unknown-name fallback keeps the type ID link intact.
	msg = buildMessage("guild", "freelance job", "Deliver stuff", "expired", []deliverable{{TypeID: 34}})
	if !strings.Contains(msg, "https://evetycoon.com/market/34") {
		t.Errorf("buildMessage() = %q, want evetycoon link", msg)
	}
	if !strings.Contains(msg, "**freelance job finished (expired)** — Deliver stuff") {
		t.Errorf("buildMessage() = %q, want finished header", msg)
	}

	// No deliverables: just the header.
	msg = buildMessage("guild", "project", "Kill things", "expired", nil)
	if strings.Contains(msg, "Deliver:") {
		t.Errorf("buildMessage() = %q, unexpected deliver line", msg)
	}
}

func TestFinishedStates(t *testing.T) {
	// Every ESI state that means "finished" must be covered, including Closed
	// (manually closed) and Expired, which keep their old last_modified.
	for _, state := range []string{"Completed", "Expired", "Closed"} {
		if _, ok := finishedStates[state]; !ok {
			t.Errorf("state %q should be treated as finished", state)
		}
	}
	for _, state := range []string{"Active", "Deleted", "Unspecified", ""} {
		if _, ok := finishedStates[state]; ok {
			t.Errorf("state %q should not be treated as finished", state)
		}
	}
}

func TestESITimeParsing(t *testing.T) {
	// Timestamps with and without fractional seconds must compare correctly.
	a := parseESITime("2025-06-01T00:00:00.100Z")
	b := parseESITime("2025-06-01T00:00:00Z")
	if !a.After(b) {
		t.Errorf("fractional-second timestamp should be after plain-second timestamp")
	}
	if !parseESITime("2025-06-01T00:00:00Z").Equal(b) {
		t.Errorf("plain timestamp parse mismatch")
	}
	if !parseESITime("garbage").IsZero() {
		t.Errorf("invalid timestamp should return zero time")
	}
	if formatESITime(time.Time{}) != "" {
		t.Errorf("zero time should format to empty string")
	}
	rt := formatESITime(a)
	if back := parseESITime(rt); !back.Equal(a) {
		t.Errorf("format/parse roundtrip failed: %q -> %v", rt, back)
	}
}

func TestListingUnmarshal(t *testing.T) {
	// Project listing shape
	projectsJSON := []byte(`{"cursor":{"after":"a","before":"b"},"projects":[{"id":"x","name":"P","state":"Completed","last_modified":"2025-05-27T12:20:21.469Z"}]}`)
	var l listing
	if err := l.UnmarshalJSON(projectsJSON); err != nil {
		t.Fatalf("Unmarshal projects: %v", err)
	}
	if len(l.Entries) != 1 || l.Entries[0].Name != "P" || l.Cursor == nil || l.Cursor.Before != "b" {
		t.Errorf("project listing decoded wrong: %+v %+v", l.Entries, l.Cursor)
	}

	// Freelance job listing shape
	jobsJSON := []byte(`{"freelance_jobs":[{"id":"y","name":"J","state":"Expired","last_modified":"2025-05-27T12:20:21.469Z"}]}`)
	l = listing{}
	if err := l.UnmarshalJSON(jobsJSON); err != nil {
		t.Fatalf("Unmarshal jobs: %v", err)
	}
	if len(l.Entries) != 1 || l.Entries[0].Name != "J" {
		t.Errorf("job listing decoded wrong: %+v", l.Entries)
	}
}