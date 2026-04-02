package dpma

import (
	"fmt"
	"time"
)

type weekInfo struct {
	Year        int
	Week        int
	YYYYWW      string
	PublishedAt time.Time
}

// generateWeeks returns the last numWeeks completed weeks, newest first.
// The current week is excluded since DPMA may not have published it yet.
func generateWeeks(numWeeks int) []weekInfo {
	year, week := currentISOWeek()
	// Start from previous week (exclude current)
	year, week = prevWeek(year, week)

	weeks := make([]weekInfo, 0, numWeeks)
	for range numWeeks {
		weeks = append(weeks, weekInfo{
			Year:        year,
			Week:        week,
			YYYYWW:      fmt.Sprintf("%04d%02d", year, week),
			PublishedAt: isoWeekThursday(year, week),
		})
		year, week = prevWeek(year, week)
	}
	return weeks
}

// currentISOWeek returns the current ISO year and week number.
var currentISOWeek = func() (int, int) {
	return time.Now().ISOWeek()
}

// prevWeek steps back one ISO week, handling year boundaries.
func prevWeek(year, week int) (int, int) {
	if week > 1 {
		return year, week - 1
	}
	// Week 1 -> last week of previous year
	year--
	return year, isoWeeksInYear(year)
}

// isoWeeksInYear returns the number of ISO weeks in a given year (52 or 53).
func isoWeeksInYear(year int) int {
	// Dec 28 is always in the last ISO week of the year
	_, w := time.Date(year, 12, 28, 0, 0, 0, 0, time.UTC).ISOWeek()
	return w
}

// isoWeekThursday returns the Thursday of the given ISO week.
// Thursday is the DPMA publication day and the defining day of an ISO week.
func isoWeekThursday(year, week int) time.Time {
	// Jan 4 is always in ISO week 1
	jan4 := time.Date(year, 1, 4, 0, 0, 0, 0, time.UTC)
	_, jan4Week := jan4.ISOWeek()
	// Find Monday of ISO week 1
	monday := jan4.AddDate(0, 0, -int(jan4.Weekday()-time.Monday))
	if jan4.Weekday() == time.Sunday {
		monday = jan4.AddDate(0, 0, -6)
	}
	// Advance to the target week's Thursday
	return monday.AddDate(0, 0, (week-jan4Week)*7+3)
}

// parseYYYYWW parses a "YYYYWW" string into year and week integers.
func parseYYYYWW(s string) (int, int, error) {
	if len(s) != 6 {
		return 0, 0, fmt.Errorf("invalid week format: expected YYYYWW, got %q", s)
	}
	var year, week int
	if _, err := fmt.Sscanf(s, "%04d%02d", &year, &week); err != nil {
		return 0, 0, fmt.Errorf("failed to parse week %q: %w", s, err)
	}
	if year < 1 || week < 1 || week > 53 {
		return 0, 0, fmt.Errorf("invalid week %q: year must be positive, week must be 1-53", s)
	}
	return year, week, nil
}
