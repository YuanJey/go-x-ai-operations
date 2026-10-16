package rules

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

type scheduleWindow struct {
	startMinute int
	endMinute   int
}

func DailySlots(document Document, actionName string, day time.Time) ([]time.Time, error) {
	action, ok := document.Actions[actionName]
	if !ok {
		return nil, fmt.Errorf("action %s is not configured", actionName)
	}
	if action.Quota == nil || action.Quota.PerDay == nil {
		return nil, fmt.Errorf("action %s has no daily quota", actionName)
	}
	candidates, err := dailyCandidates(document.Account.Timezone, action, day)
	if err != nil {
		return nil, err
	}
	count := *action.Quota.PerDay
	if count == 0 || len(candidates) == 0 {
		return nil, nil
	}
	if count > len(candidates) {
		return nil, fmt.Errorf("action %s quota %d exceeds daily capacity %d", actionName, count, len(candidates))
	}
	if count == 1 {
		return []time.Time{candidates[len(candidates)/2]}, nil
	}

	slots := make([]time.Time, 0, count)
	for index := 0; index < count; index++ {
		candidateIndex := int(math.Round(float64(index) * float64(len(candidates)-1) / float64(count-1)))
		slots = append(slots, candidates[candidateIndex])
	}
	return slots, nil
}

func DailyCapacity(document Document, actionName string, day time.Time) (int, error) {
	action, ok := document.Actions[actionName]
	if !ok {
		return 0, fmt.Errorf("action %s is not configured", actionName)
	}
	candidates, err := dailyCandidates(document.Account.Timezone, action, day)
	if err != nil {
		return 0, err
	}
	return len(candidates), nil
}

func IsWithinSchedule(document Document, actionName string, at time.Time) (bool, error) {
	action, ok := document.Actions[actionName]
	if !ok {
		return false, fmt.Errorf("action %s is not configured", actionName)
	}
	timezone := action.Schedule.Timezone
	if timezone == "" {
		timezone = document.Account.Timezone
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return false, fmt.Errorf("invalid schedule timezone %s", timezone)
	}
	local := at.In(location)
	if !weekdayAllowed(action.Schedule.Days, local.Weekday()) {
		return false, nil
	}
	if len(action.Schedule.Windows) == 0 {
		return true, nil
	}
	windows, err := parseWindows(action.Schedule.Windows)
	if err != nil {
		return false, err
	}
	minute := local.Hour()*60 + local.Minute()
	for _, window := range windows {
		if minute >= window.startMinute && minute <= window.endMinute {
			return true, nil
		}
	}
	return false, nil
}

func MaxDailyCapacity(document Document, actionName string, from time.Time) (int, error) {
	maximum := 0
	for offset := 0; offset < 7; offset++ {
		capacity, err := DailyCapacity(document, actionName, from.AddDate(0, 0, offset))
		if err != nil {
			return 0, err
		}
		if capacity > maximum {
			maximum = capacity
		}
	}
	return maximum, nil
}

func dailyCandidates(accountTimezone string, action Action, day time.Time) ([]time.Time, error) {
	timezone := action.Schedule.Timezone
	if timezone == "" {
		timezone = accountTimezone
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, fmt.Errorf("invalid schedule timezone %s", timezone)
	}
	localDay := day.In(location)
	if !weekdayAllowed(action.Schedule.Days, localDay.Weekday()) {
		return nil, nil
	}
	windows, err := parseWindows(action.Schedule.Windows)
	if err != nil {
		return nil, err
	}

	interval := action.Cooldown.MinInterval.Value()
	if interval <= 0 {
		interval = time.Minute
	}
	var candidates []time.Time
	var last time.Time
	for _, window := range windows {
		start := localClock(localDay, window.startMinute)
		end := localClock(localDay, window.endMinute)
		next := start
		if !last.IsZero() && next.Before(last.Add(interval)) {
			next = last.Add(interval)
		}
		for !next.After(end) {
			candidates = append(candidates, next.UTC())
			last = next
			next = next.Add(interval)
		}
	}
	return candidates, nil
}

func parseWindows(values []string) ([]scheduleWindow, error) {
	windows := make([]scheduleWindow, 0, len(values))
	for _, value := range values {
		parts := strings.Split(value, "-")
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid schedule window %q", value)
		}
		start, err := parseClock(parts[0], false)
		if err != nil {
			return nil, fmt.Errorf("invalid schedule window %q: %w", value, err)
		}
		end, err := parseClock(parts[1], true)
		if err != nil {
			return nil, fmt.Errorf("invalid schedule window %q: %w", value, err)
		}
		if end <= start {
			return nil, fmt.Errorf("schedule window %q must end after it starts", value)
		}
		windows = append(windows, scheduleWindow{startMinute: start, endMinute: end})
	}
	sort.Slice(windows, func(i, j int) bool { return windows[i].startMinute < windows[j].startMinute })
	for index := 1; index < len(windows); index++ {
		if windows[index].startMinute <= windows[index-1].endMinute {
			return nil, fmt.Errorf("schedule windows overlap")
		}
	}
	return windows, nil
}

func parseClock(value string, allowEndOfDay bool) (int, error) {
	parts := strings.Split(value, ":")
	if len(parts) != 2 {
		return 0, fmt.Errorf("clock must use HH:MM")
	}
	hour, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, fmt.Errorf("invalid hour")
	}
	minute, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, fmt.Errorf("invalid minute")
	}
	if allowEndOfDay && hour == 24 && minute == 0 {
		return 24 * 60, nil
	}
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, fmt.Errorf("clock is out of range")
	}
	return hour*60 + minute, nil
}

func localClock(day time.Time, minuteOfDay int) time.Time {
	if minuteOfDay == 24*60 {
		return time.Date(day.Year(), day.Month(), day.Day()+1, 0, 0, 0, 0, day.Location())
	}
	return time.Date(
		day.Year(), day.Month(), day.Day(),
		minuteOfDay/60, minuteOfDay%60, 0, 0, day.Location(),
	)
}

func weekdayAllowed(days []string, weekday time.Weekday) bool {
	if len(days) == 0 {
		return true
	}
	target := strings.ToLower(weekday.String()[:3])
	for _, day := range days {
		if strings.ToLower(day) == target {
			return true
		}
	}
	return false
}
