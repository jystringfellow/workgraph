package workgraph

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

func ParseEventQueryTime(raw string, now time.Time, allowRelative bool) (time.Time, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return time.Time{}, nil
	}
	if parsed, err := time.Parse(time.RFC3339Nano, trimmed); err == nil {
		return parsed, nil
	}
	if parsed, err := time.ParseInLocation("2006-01-02", trimmed, now.Location()); err == nil {
		return parsed, nil
	}
	if allowRelative {
		if strings.HasSuffix(trimmed, "d") && len(trimmed) > 1 {
			const maxRelativeDays = math.MaxInt64 / int64(24*time.Hour)
			if days, err := strconv.ParseInt(trimmed[:len(trimmed)-1], 10, 64); err == nil && days > 0 && days <= maxRelativeDays {
				return now.Add(-time.Duration(days) * 24 * time.Hour), nil
			}
		}
		if d, err := time.ParseDuration(trimmed); err == nil && d > 0 {
			return now.Add(-d), nil
		}
		return time.Time{}, fmt.Errorf("%q (expected RFC3339, YYYY-MM-DD, or positive duration like 24h or 7d)", raw)
	}
	return time.Time{}, fmt.Errorf("%q (expected RFC3339 or YYYY-MM-DD)", raw)
}
