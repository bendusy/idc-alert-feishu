package rotate

import (
	"fmt"
	"github.com/prometheus/common/model"
	"regexp"
	"strings"
	"time"
)

type MentionRotator struct {
	baseDate  time.Time
	cycleDays int
	openIDs   []string
}

// by now, support week and day
var durationRE = regexp.MustCompile(`^(([0-9]+)w)?(([0-9]+)d)?$`)

// parse to days
func parseDuration(durationStr string) (int, error) {
	durationStr = strings.TrimSpace(durationStr)
	matches := durationRE.FindStringSubmatch(durationStr)
	if matches == nil {
		return 0, fmt.Errorf("not a valid duration string: %q", durationStr)
	}

	duration, err := model.ParseDuration(durationStr)
	if err != nil {
		return 0, err
	}
	return int(time.Duration(duration) / time.Millisecond / (1000 * 60 * 60 * 24)), nil
}

func New(rotationStr string, openIDs []string) (*MentionRotator, error) {
	parts := strings.Split(rotationStr, ":")
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid rotation string: %v", rotationStr)
	}
	baseDateStr := parts[0]
	rotateDuration := parts[1]

	// parse base date: add timezone (system timezone)
	baseDateStr += time.Now().Format("Z07:00")
	baseDate, err := time.Parse("2006-01-02Z07:00", baseDateStr)
	if err != nil {
		return nil, err
	}

	// parse rotate duration
	days, err := parseDuration(rotateDuration)
	if err != nil {
		return nil, err
	}
	if days <= 0 {
		return nil, fmt.Errorf("rotate duration at least 1: %v", days)
	}

	return &MentionRotator{
		baseDate:  baseDate,
		cycleDays: days,
		openIDs:   openIDs,
	}, nil
}

func abs(x int) int {
	if x < 0 {
		return -1 * x
	}
	return x
}

func adjustDays(relativeDays int, cycleDays int) int {
	if cycleDays <= 0 {
		panic("unexpected")
	}
	if relativeDays < 0 {
		// example: -3 -2 -1 1 2 3 4 5
		// cycle = 2
		// -1 => 3
		relativeDays = abs(cycleDays - relativeDays)
	} else {
		// = 0 means at that day
		relativeDays += 1
	}
	return relativeDays
}

func (r MentionRotator) Rotate(t time.Time) []string {
	if len(r.openIDs) <= 1 {
		return r.openIDs
	}
	days := calendarDaysBetween(r.baseDate, t)
	days = adjustDays(days, r.cycleDays)
	index := (bucketIndexEveryN(days, r.cycleDays) - 1) % len(r.openIDs)
	var res []string
	res = append(res, r.openIDs[index])
	return res
}

// calendarDaysBetween 返回 from 到 to 相差的日历天数（基于 from 的时区的午夜对齐），
// 避免直接用时长除以 24h —— 那在夏令时切换日（一天 23 或 25 小时）会偏差 ±1 天。
func calendarDaysBetween(from, to time.Time) int {
	loc := from.Location()
	to = to.In(loc)
	fromDay := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, loc)
	toDay := time.Date(to.Year(), to.Month(), to.Day(), 0, 0, 0, 0, loc)
	// 两个午夜之间的差值，按符号四舍五入到整天，吸收 DST 带来的 ±1h
	diff := toDay.Sub(fromDay)
	if diff >= 0 {
		return int((diff + 12*time.Hour) / (24 * time.Hour))
	}
	return int((diff - 12*time.Hour) / (24 * time.Hour))
}

func bucketIndexEveryN(v, bucketSize int) int {
	if bucketSize <= 0 {
		panic("unexpected")
	}
	if v <= 0 {
		panic("unexpected")
	}
	if v%bucketSize != 0 {
		return (v - v%bucketSize + bucketSize) / bucketSize
	}
	return v / bucketSize
}
