package parser

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type matchCandidate struct {
	start   int
	end     int
	time    time.Time
	endTime time.Time
	isRange bool
}

const eveSuffixPattern = `(?:eve(?:\s+online)?(?:\s+(?:server\s+)?(?:time|timke|tiem|tme|times|timezone))?)`

var (
	// Relative times: e.g. "In 7 days", "after 7 days", "in 6 days 23 hours", "in 2 hours"
	reRelative = regexp.MustCompile(`(?i)\b(in|after)\s+(\d+\s*(?:days?|weeks?|months?|hours?|hrs?|minutes?|mins?)(?:\s*(?:and\s+)?\d+\s*(?:days?|weeks?|months?|hours?|hrs?|minutes?|mins?))*)\b`)
	reDurationUnit = regexp.MustCompile(`(?i)(\d+)\s*(days?|weeks?|months?|hours?|hrs?|minutes?|mins?)`)

	// Contextual: "until 20:00 the next day", "by 20:00 the next day"
	reUntilNextDay = regexp.MustCompile(`(?i)\b(?:(until|by)\s+)?([01]?\d|2[0-4])(?::|h)([0-5]\d)(?:\s+` + eveSuffixPattern + `)?\s+the\s+next\s+day\b`)

	// Contextual: "20:00 eve online time today", "today at 20:00"
	reTodayAfter  = regexp.MustCompile(`(?i)\b([01]?\d|2[0-4])(?::|h)([0-5]\d)(?:\s+` + eveSuffixPattern + `)?\s+today\b`)
	reTodayBefore = regexp.MustCompile(`(?i)\btoday(?:\s+(?:at|@))?\s+([01]?\d|2[0-4])(?::|h)([0-5]\d)(?:\s+` + eveSuffixPattern + `)?\b`)

	// Contextual: "tonight at 20:00", "tnite at around 21:00 Eve Time"
	reTonightTime = regexp.MustCompile(`(?i)\b(?:tonight|tnite)(?:[,]?\s*(?:(?:at|around|@)\s*|\s*))([0-2]?\d)(?::|h)([0-5]\d)(?:\s+` + eveSuffixPattern + `)?\b`)

	// Contextual: "Tomorrow (Sunday)", "Tomorrow"
	reTomorrowDay = regexp.MustCompile(`(?i)\btomorrow(?:\s*\((monday|tuesday|wednesday|thursday|friday|saturday|sunday)\))?`)

	// Contextual: "19:30 on friday"
	reTimeOnWeekday = regexp.MustCompile(`(?i)\b([0-2]?\d)(?::|h)([0-5]\d)(?:\s+` + eveSuffixPattern + `)?\s+on\s+(monday|tuesday|wednesday|thursday|friday|saturday|sunday)\b`)

	// Contextual: "THURSDAY before 19:00", "next friday at 19:00", "monday after 19:00 eve time", "monday before 10h00", "Saturday @16:39 EVE"
	reWeekdayTime = regexp.MustCompile(`(?i)\b(?:(next)\s+)?(monday|tuesday|wednesday|thursday|friday|saturday|sunday)(?:[,]?\s*(?:(?:before|after|around|at)\s+|@\s*|\s*))([0-2]?\d)(?::|h)([0-5]\d)(?:\s+` + eveSuffixPattern + `)?\b`)

	// Date + Time (Day First): e.g. "Saturday, 5 September 2026 18:21", "10th of September @ 20:00 Eve Time", "1 September 20:00"
	reDateTime = regexp.MustCompile(`(?i)\b(?:(monday|tuesday|wednesday|thursday|friday|saturday|sunday)[,]?\s+)?([0-2]?\d|3[01])(?:st|nd|rd|th)?(?:\s+of)?\s+(january|february|march|april|may|june|july|august|september|october|november|december|jan|feb|mar|apr|jun|jul|aug|sep|sept|oct|nov|dec)(?:\s+(20\d\d))?(?:[,]?\s*(?:at|@)?\s*)([0-2]?\d)(?::|h)([0-5]\d)(?:\s+` + eveSuffixPattern + `)?\b`)

	// Date + Time (Month First): e.g. "October 23, 2026, at 20:00 EVE Time"
	reDateTimeMonthFirst = regexp.MustCompile(`(?i)\b(?:(monday|tuesday|wednesday|thursday|friday|saturday|sunday)[,]?\s+)?(january|february|march|april|may|june|july|august|september|october|november|december|jan|feb|mar|apr|jun|jul|aug|sep|sept|oct|nov|dec)\s+([0-2]?\d|3[01])(?:st|nd|rd|th)?(?:[,]?\s+(20\d\d))?(?:[,]?\s*(?:at|@)?\s*)([0-2]?\d)(?::|h)([0-5]\d)(?:\s+` + eveSuffixPattern + `)?\b`)

	// Date without time: e.g. "27th of this month", "27th of next month"
	reDateThisMonth = regexp.MustCompile(`(?i)\b([0-2]?\d|3[01])(?:st|nd|rd|th)?\s+of\s+(this|next)\s+month\b`)

	// Date range or ordinal weekday date: e.g. "Monday 12th", "Sunday 18th"
	reDateWeekdayOrdinal = regexp.MustCompile(`(?i)\b(monday|tuesday|wednesday|thursday|friday|saturday|sunday)[,]?\s+([0-2]?\d|3[01])(?:st|nd|rd|th)\b`)

	// Time range: e.g. "around 12:00-01:00 Eve Time", "12:00 - 01:00 Eve Time", "20:00 to 22:00"
	reTimeRange = regexp.MustCompile(`(?i)\b(?:around|at|@)?\s*([0-2]?\d)(?::|h)([0-5]\d)\s*(?:-|to)\s*([0-2]?\d)(?::|h)([0-5]\d)(?:\s+` + eveSuffixPattern + `)?\b`)

	// Standalone times:
	// Colon or H format: e.g. "21:00 Eve Time", "22:00", "20h00 eve", "0:00 Eve Timke"
	reStandaloneColonOrH = regexp.MustCompile(`(?i)\b([0-2]?\d)(?::|h)([0-5]\d)(?:\s+` + eveSuffixPattern + `)?\b`)

	// 4-digit military format: e.g. "2000 eve time" (must be followed by Eve suffix to avoid matching bare numbers/years)
	reStandalone4Digits = regexp.MustCompile(`(?i)\b([01]\d|2[0-4])([0-5]\d)\s+` + eveSuffixPattern + `\b`)
)

func parseMonth(m string) (time.Month, bool) {
	switch strings.ToLower(m) {
	case "january", "jan":
		return time.January, true
	case "february", "feb":
		return time.February, true
	case "march", "mar":
		return time.March, true
	case "april", "apr":
		return time.April, true
	case "may":
		return time.May, true
	case "june", "jun":
		return time.June, true
	case "july", "jul":
		return time.July, true
	case "august", "aug":
		return time.August, true
	case "september", "sep", "sept":
		return time.September, true
	case "october", "oct":
		return time.October, true
	case "november", "nov":
		return time.November, true
	case "december", "dec":
		return time.December, true
	}
	return 0, false
}

func parseWeekday(w string) (time.Weekday, bool) {
	switch strings.ToLower(w) {
	case "sunday", "sun":
		return time.Sunday, true
	case "monday", "mon":
		return time.Monday, true
	case "tuesday", "tue", "tues":
		return time.Tuesday, true
	case "wednesday", "wed":
		return time.Wednesday, true
	case "thursday", "thu", "thur", "thurs":
		return time.Thursday, true
	case "friday", "fri":
		return time.Friday, true
	case "saturday", "sat":
		return time.Saturday, true
	}
	return 0, false
}

func parseTimeOfDay(hourStr, minStr string) (int, int) {
	h, _ := strconv.Atoi(hourStr)
	m, _ := strconv.Atoi(minStr)
	return h, m
}

func resolveWeekdayOrdinal(sentAt time.Time, wStr string, dayVal int) (time.Time, bool) {
	w, ok := parseWeekday(wStr)
	if !ok {
		return time.Time{}, false
	}
	for _, mOffset := range []int{0, 1, -1} {
		refMonth := sentAt.AddDate(0, mOffset, 0)
		d := time.Date(refMonth.Year(), refMonth.Month(), dayVal, 0, 0, 0, 0, time.UTC)
		if d.Weekday() == w {
			return d, true
		}
	}
	return time.Date(sentAt.Year(), sentAt.Month(), dayVal, 0, 0, 0, 0, time.UTC), true
}

func isValidCandidate(text string, c matchCandidate) bool {
	if c.time.IsZero() || c.start < 0 || c.end > len(text) || c.start >= c.end {
		return false
	}

	// Do not match inside an HTML tag or Discord tag <...>
	if lastOpen := strings.LastIndexByte(text[:c.start], '<'); lastOpen != -1 {
		if lastClose := strings.LastIndexByte(text[:c.start], '>'); lastOpen > lastClose {
			return false
		}
	}

	// Do not match inside a markdown link URL ](...)
	if lastOpenParen := strings.LastIndex(text[:c.start], "]("); lastOpenParen != -1 {
		if lastCloseParen := strings.LastIndexByte(text[:c.start], ')'); lastOpenParen > lastCloseParen {
			return false
		}
	}

	// Do not match if preceded by letter, digit, colon, or slash
	if c.start > 0 {
		prev := text[c.start-1]
		if (prev >= 'a' && prev <= 'z') || (prev >= 'A' && prev <= 'Z') || (prev >= '0' && prev <= '9') || prev == ':' || prev == '/' {
			return false
		}
		// Do not match inside a Discord tag <t:
		if c.start >= 3 && text[c.start-3:c.start] == "<t:" {
			return false
		}
	}

	// Do not match if followed by letter, digit, or colon
	if c.end < len(text) {
		next := text[c.end]
		if (next >= 'a' && next <= 'z') || (next >= 'A' && next <= 'Z') || (next >= '0' && next <= '9') || next == ':' {
			return false
		}
	}

	// Do not re-tag if already followed by a Discord timestamp like (<t:...:F>) or <t:...>
	remaining := text[c.end:]
	if strings.HasPrefix(remaining, " (<t:") || strings.HasPrefix(remaining, "(<t:") ||
		strings.HasPrefix(remaining, " <t:") || strings.HasPrefix(remaining, "<t:") {
		return false
	}

	return true
}

func filterOverlapping(text string, candidates []matchCandidate) []matchCandidate {
	var valid []matchCandidate
	for _, c := range candidates {
		if isValidCandidate(text, c) {
			valid = append(valid, c)
		}
	}

	// Sort candidates:
	// 1. Longer match spans win (e.g. full date+time wins over embedded time)
	// 2. Earlier start position wins
	sort.Slice(valid, func(i, j int) bool {
		lenI := valid[i].end - valid[i].start
		lenJ := valid[j].end - valid[j].start
		if lenI != lenJ {
			return lenI > lenJ
		}
		return valid[i].start < valid[j].start
	})

	used := make([]bool, len(text))
	var selected []matchCandidate

	for _, c := range valid {
		overlap := false
		for k := c.start; k < c.end; k++ {
			if used[k] {
				overlap = true
				break
			}
		}
		if !overlap {
			for k := c.start; k < c.end; k++ {
				used[k] = true
			}
			selected = append(selected, c)
		}
	}

	// Sort selected by start offset ascending for left-to-right reconstruction
	sort.Slice(selected, func(i, j int) bool {
		return selected[i].start < selected[j].start
	})

	return selected
}

// InjectDiscordTimestamps finds EVE time expressions in the text and appends Discord timestamps.
// The sentAt time is used as the reference point (UTC) for relative dates and implied missing dates.
func InjectDiscordTimestamps(text string, sentAt time.Time) string {
	sentAt = sentAt.UTC()
	var candidates []matchCandidate

	// 1. Relative times: "in 7 days", "after 7 days", "in 6 days 23 hours", "in 2 hours"
	matches := reRelative.FindAllStringSubmatchIndex(text, -1)
	for _, loc := range matches {
		matchStr := text[loc[0]:loc[1]]
		durationMatches := reDurationUnit.FindAllStringSubmatch(matchStr, -1)
		t := sentAt
		for _, dm := range durationMatches {
			val, _ := strconv.Atoi(dm[1])
			unit := strings.ToLower(dm[2])
			switch {
			case strings.HasPrefix(unit, "day"):
				t = t.AddDate(0, 0, val)
			case strings.HasPrefix(unit, "week"):
				t = t.AddDate(0, 0, val*7)
			case strings.HasPrefix(unit, "month"):
				t = t.AddDate(0, val, 0)
			case strings.HasPrefix(unit, "hour") || strings.HasPrefix(unit, "hr"):
				t = t.Add(time.Duration(val) * time.Hour)
			case strings.HasPrefix(unit, "min"):
				t = t.Add(time.Duration(val) * time.Minute)
			}
		}
		candidates = append(candidates, matchCandidate{start: loc[0], end: loc[1], time: t})
	}

	// 2. Contextual: until/by time the next day
	matches = reUntilNextDay.FindAllStringSubmatchIndex(text, -1)
	for _, loc := range matches {
		h, m := parseTimeOfDay(text[loc[4]:loc[5]], text[loc[6]:loc[7]])
		targetDate := sentAt.AddDate(0, 0, 1)
		if h == 24 {
			targetDate = targetDate.AddDate(0, 0, 1)
			h = 0
		}
		t := time.Date(targetDate.Year(), targetDate.Month(), targetDate.Day(), h, m, 0, 0, time.UTC)
		candidates = append(candidates, matchCandidate{start: loc[0], end: loc[1], time: t})
	}

	// 3. Contextual: time ... today / tonight
	matches = reTodayAfter.FindAllStringSubmatchIndex(text, -1)
	for _, loc := range matches {
		h, m := parseTimeOfDay(text[loc[2]:loc[3]], text[loc[4]:loc[5]])
		targetDate := sentAt
		targetMin := h*60 + m
		sentAtMin := sentAt.Hour()*60 + sentAt.Minute()
		if h == 24 {
			targetDate = sentAt.AddDate(0, 0, 1)
			h = 0
		} else if targetMin < sentAtMin {
			// If already passed on sent_at date, fallback to next day
			targetDate = sentAt.AddDate(0, 0, 1)
		}
		t := time.Date(targetDate.Year(), targetDate.Month(), targetDate.Day(), h, m, 0, 0, time.UTC)
		candidates = append(candidates, matchCandidate{start: loc[0], end: loc[1], time: t})
	}

	matches = reTodayBefore.FindAllStringSubmatchIndex(text, -1)
	for _, loc := range matches {
		h, m := parseTimeOfDay(text[loc[2]:loc[3]], text[loc[4]:loc[5]])
		targetDate := sentAt
		targetMin := h*60 + m
		sentAtMin := sentAt.Hour()*60 + sentAt.Minute()
		if h == 24 {
			targetDate = sentAt.AddDate(0, 0, 1)
			h = 0
		} else if targetMin < sentAtMin {
			targetDate = sentAt.AddDate(0, 0, 1)
		}
		t := time.Date(targetDate.Year(), targetDate.Month(), targetDate.Day(), h, m, 0, 0, time.UTC)
		candidates = append(candidates, matchCandidate{start: loc[0], end: loc[1], time: t})
	}

	matches = reTonightTime.FindAllStringSubmatchIndex(text, -1)
	for _, loc := range matches {
		h, m := parseTimeOfDay(text[loc[2]:loc[3]], text[loc[4]:loc[5]])
		targetDate := sentAt
		targetMin := h*60 + m
		sentAtMin := sentAt.Hour()*60 + sentAt.Minute()
		if h == 24 {
			targetDate = sentAt.AddDate(0, 0, 1)
			h = 0
		} else if targetMin < sentAtMin {
			targetDate = sentAt.AddDate(0, 0, 1)
		}
		t := time.Date(targetDate.Year(), targetDate.Month(), targetDate.Day(), h, m, 0, 0, time.UTC)
		candidates = append(candidates, matchCandidate{start: loc[0], end: loc[1], time: t})
	}

	// 4. Contextual: Tomorrow / Tomorrow (Weekday)
	type tomInfo struct {
		start      int
		end        int
		targetDate time.Time
	}
	var tomList []tomInfo
	matches = reTomorrowDay.FindAllStringSubmatchIndex(text, -1)
	for _, loc := range matches {
		targetDate := sentAt.AddDate(0, 0, 1)
		if loc[2] != -1 {
			wStr := text[loc[2]:loc[3]]
			if w, ok := parseWeekday(wStr); ok {
				daysAhead := (int(w) - int(sentAt.Weekday()) + 7) % 7
				if daysAhead == 0 {
					daysAhead = 7
				}
				targetDate = sentAt.AddDate(0, 0, daysAhead)
			}
		}
		t := time.Date(targetDate.Year(), targetDate.Month(), targetDate.Day(), 0, 0, 0, 0, time.UTC)
		candidates = append(candidates, matchCandidate{start: loc[0], end: loc[1], time: t})
		tomList = append(tomList, tomInfo{start: loc[0], end: loc[1], targetDate: targetDate})
	}

	// 5. Contextual: time on weekday (e.g. "19:30 on friday")
	matches = reTimeOnWeekday.FindAllStringSubmatchIndex(text, -1)
	for _, loc := range matches {
		h, m := parseTimeOfDay(text[loc[2]:loc[3]], text[loc[4]:loc[5]])
		weekdayStr := text[loc[6]:loc[7]]
		if targetWeekday, ok := parseWeekday(weekdayStr); ok {
			daysAhead := (int(targetWeekday) - int(sentAt.Weekday()) + 7) % 7
			targetMin := h*60 + m
			sentAtMin := sentAt.Hour()*60 + sentAt.Minute()
			if daysAhead == 0 && targetMin < sentAtMin {
				daysAhead = 7
			}
			targetDate := sentAt.AddDate(0, 0, daysAhead)
			if h == 24 {
				targetDate = targetDate.AddDate(0, 0, 1)
				h = 0
			}
			t := time.Date(targetDate.Year(), targetDate.Month(), targetDate.Day(), h, m, 0, 0, time.UTC)
			candidates = append(candidates, matchCandidate{start: loc[0], end: loc[1], time: t})
		}
	}

	// 6. Contextual: [next] weekday (before|after|around|at|@) time
	matches = reWeekdayTime.FindAllStringSubmatchIndex(text, -1)
	for _, loc := range matches {
		hasNext := loc[2] != -1 && strings.ToLower(text[loc[2]:loc[3]]) == "next"
		weekdayStr := text[loc[4]:loc[5]]
		h, m := parseTimeOfDay(text[loc[6]:loc[7]], text[loc[8]:loc[9]])
		if targetWeekday, ok := parseWeekday(weekdayStr); ok {
			daysAhead := (int(targetWeekday) - int(sentAt.Weekday()) + 7) % 7
			targetMin := h*60 + m
			sentAtMin := sentAt.Hour()*60 + sentAt.Minute()
			if hasNext && daysAhead == 0 {
				daysAhead = 7
			} else if daysAhead == 0 && targetMin < sentAtMin {
				daysAhead = 7
			}
			targetDate := sentAt.AddDate(0, 0, daysAhead)
			if h == 24 {
				targetDate = targetDate.AddDate(0, 0, 1)
				h = 0
			}
			t := time.Date(targetDate.Year(), targetDate.Month(), targetDate.Day(), h, m, 0, 0, time.UTC)
			candidates = append(candidates, matchCandidate{start: loc[0], end: loc[1], time: t})
		}
	}

	// 7. Date + Time (Day First)
	matches = reDateTime.FindAllStringSubmatchIndex(text, -1)
	for _, loc := range matches {
		day, _ := strconv.Atoi(text[loc[4]:loc[5]])
		monthStr := text[loc[6]:loc[7]]
		month, ok := parseMonth(monthStr)
		if !ok {
			continue
		}
		year := sentAt.Year()
		if loc[8] != -1 {
			year, _ = strconv.Atoi(text[loc[8]:loc[9]])
		}
		h, m := parseTimeOfDay(text[loc[10]:loc[11]], text[loc[12]:loc[13]])
		targetDate := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
		if h == 24 {
			targetDate = targetDate.AddDate(0, 0, 1)
			h = 0
		}
		t := time.Date(targetDate.Year(), targetDate.Month(), targetDate.Day(), h, m, 0, 0, time.UTC)
		candidates = append(candidates, matchCandidate{start: loc[0], end: loc[1], time: t})
	}

	// 8. Date + Time (Month First)
	matches = reDateTimeMonthFirst.FindAllStringSubmatchIndex(text, -1)
	for _, loc := range matches {
		monthStr := text[loc[4]:loc[5]]
		month, ok := parseMonth(monthStr)
		if !ok {
			continue
		}
		day, _ := strconv.Atoi(text[loc[6]:loc[7]])
		year := sentAt.Year()
		if loc[8] != -1 {
			year, _ = strconv.Atoi(text[loc[8]:loc[9]])
		}
		h, m := parseTimeOfDay(text[loc[10]:loc[11]], text[loc[12]:loc[13]])
		targetDate := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
		if h == 24 {
			targetDate = targetDate.AddDate(0, 0, 1)
			h = 0
		}
		t := time.Date(targetDate.Year(), targetDate.Month(), targetDate.Day(), h, m, 0, 0, time.UTC)
		candidates = append(candidates, matchCandidate{start: loc[0], end: loc[1], time: t})
	}

	// 9. Date without time: "27th of this month", "27th of next month"
	matches = reDateThisMonth.FindAllStringSubmatchIndex(text, -1)
	for _, loc := range matches {
		day, _ := strconv.Atoi(text[loc[2]:loc[3]])
		whichMonth := strings.ToLower(text[loc[4]:loc[5]])
		ref := sentAt
		if whichMonth == "next" {
			ref = ref.AddDate(0, 1, 0)
		}
		t := time.Date(ref.Year(), ref.Month(), day, 0, 0, 0, 0, time.UTC)
		candidates = append(candidates, matchCandidate{start: loc[0], end: loc[1], time: t})
	}

	// 10. Date range or ordinal weekday date: e.g. "Monday 12th", "Sunday 18th"
	matches = reDateWeekdayOrdinal.FindAllStringSubmatchIndex(text, -1)
	for _, loc := range matches {
		wStr := text[loc[2]:loc[3]]
		dayVal, _ := strconv.Atoi(text[loc[4]:loc[5]])
		if d, ok := resolveWeekdayOrdinal(sentAt, wStr, dayVal); ok {
			candidates = append(candidates, matchCandidate{start: loc[0], end: loc[1], time: d})
		}
	}

	// 11. Time ranges: e.g. "around 12:00-01:00 Eve Time"
	matches = reTimeRange.FindAllStringSubmatchIndex(text, -1)
	for _, loc := range matches {
		h1, m1 := parseTimeOfDay(text[loc[2]:loc[3]], text[loc[4]:loc[5]])
		h2, m2 := parseTimeOfDay(text[loc[6]:loc[7]], text[loc[8]:loc[9]])
		startMin := h1*60 + m1
		endMin := h2*60 + m2

		targetDate1 := sentAt
		sentAtMin := sentAt.Hour()*60 + sentAt.Minute()
		if h1 == 24 {
			targetDate1 = sentAt.AddDate(0, 0, 1)
			h1 = 0
		} else if startMin < sentAtMin {
			targetDate1 = sentAt.AddDate(0, 0, 1)
		}
		t1 := time.Date(targetDate1.Year(), targetDate1.Month(), targetDate1.Day(), h1, m1, 0, 0, time.UTC)

		targetDate2 := targetDate1
		if endMin < startMin {
			targetDate2 = targetDate1.AddDate(0, 0, 1)
		}
		if h2 == 24 {
			targetDate2 = targetDate2.AddDate(0, 0, 1)
			h2 = 0
		}
		t2 := time.Date(targetDate2.Year(), targetDate2.Month(), targetDate2.Day(), h2, m2, 0, 0, time.UTC)

		candidates = append(candidates, matchCandidate{
			start:   loc[0],
			end:     loc[1],
			time:    t1,
			endTime: t2,
			isRange: true,
		})
	}

	// Helper to find preceding tomorrow context date for standalone times
	getPrecedingTomorrowDate := func(startOffset int) (time.Time, bool) {
		var lastTom tomInfo
		found := false
		for _, tom := range tomList {
			if tom.end <= startOffset {
				lastTom = tom
				found = true
			}
		}
		if found {
			return lastTom.targetDate, true
		}
		return sentAt, false
	}

	// 12. Standalone times (colon or h format)
	matches = reStandaloneColonOrH.FindAllStringSubmatchIndex(text, -1)
	for _, loc := range matches {
		h, m := parseTimeOfDay(text[loc[2]:loc[3]], text[loc[4]:loc[5]])
		baseDate, hadTomorrow := getPrecedingTomorrowDate(loc[0])
		targetDate := baseDate
		targetMin := h*60 + m
		sentAtMin := sentAt.Hour()*60 + sentAt.Minute()
		if h == 24 {
			targetDate = baseDate.AddDate(0, 0, 1)
			h = 0
		} else if !hadTomorrow && targetMin < sentAtMin {
			targetDate = baseDate.AddDate(0, 0, 1)
		}
		t := time.Date(targetDate.Year(), targetDate.Month(), targetDate.Day(), h, m, 0, 0, time.UTC)
		candidates = append(candidates, matchCandidate{start: loc[0], end: loc[1], time: t})
	}

	// 13. Standalone times (4-digit format with eve suffix)
	matches = reStandalone4Digits.FindAllStringSubmatchIndex(text, -1)
	for _, loc := range matches {
		h, m := parseTimeOfDay(text[loc[2]:loc[3]], text[loc[4]:loc[5]])
		baseDate, hadTomorrow := getPrecedingTomorrowDate(loc[0])
		targetDate := baseDate
		targetMin := h*60 + m
		sentAtMin := sentAt.Hour()*60 + sentAt.Minute()
		if h == 24 {
			targetDate = baseDate.AddDate(0, 0, 1)
			h = 0
		} else if !hadTomorrow && targetMin < sentAtMin {
			targetDate = baseDate.AddDate(0, 0, 1)
		}
		t := time.Date(targetDate.Year(), targetDate.Month(), targetDate.Day(), h, m, 0, 0, time.UTC)
		candidates = append(candidates, matchCandidate{start: loc[0], end: loc[1], time: t})
	}

	// Resolve overlapping candidates
	selected := filterOverlapping(text, candidates)
	if len(selected) == 0 {
		return text
	}

	var sb strings.Builder
	lastIdx := 0
	for _, c := range selected {
		sb.WriteString(text[lastIdx:c.start])
		sb.WriteString(text[c.start:c.end])
		if c.isRange {
			fmt.Fprintf(&sb, " (<t:%d:F> - <t:%d:F>)", c.time.Unix(), c.endTime.Unix())
		} else {
			fmt.Fprintf(&sb, " (<t:%d:F>)", c.time.Unix())
		}
		lastIdx = c.end
	}
	sb.WriteString(text[lastIdx:])
	return sb.String()
}
