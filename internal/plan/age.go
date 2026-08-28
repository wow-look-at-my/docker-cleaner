package plan

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ParseAge reads a duration that may carry day or week units, which Go's own
// parser rejects. Both age flags share this, so 30d and 168h mean the same
// thing wherever they appear.
func ParseAge(s string) (time.Duration, error) {
	raw := strings.TrimSpace(s)
	if raw == "" {
		return 0, fmt.Errorf("empty duration")
	}
	if strings.HasPrefix(raw, "-") {
		return 0, fmt.Errorf("%q is negative; an age must not be", s)
	}

	var total time.Duration
	rest := raw
	for {
		i := 0
		for i < len(rest) && (rest[i] >= '0' && rest[i] <= '9') {
			i++
		}
		if i == 0 || i == len(rest) {
			break
		}
		unit := rest[i]
		if unit != 'd' && unit != 'w' {
			break
		}
		n, err := strconv.Atoi(rest[:i])
		if err != nil {
			return 0, fmt.Errorf("%q is not a duration", s)
		}
		if unit == 'd' {
			total += time.Duration(n) * 24 * time.Hour
		} else {
			total += time.Duration(n) * 7 * 24 * time.Hour
		}
		rest = rest[i+1:]
	}

	if rest != "" {
		d, err := time.ParseDuration(rest)
		if err != nil {
			return 0, fmt.Errorf("%q is not a duration: use a form like 30d, 4w, 720h or 1h30m", s)
		}
		if d < 0 {
			return 0, fmt.Errorf("%q is negative; an age must not be", s)
		}
		total += d
	}
	return total, nil
}

// GoDuration renders an age the way docker's own filters demand. buildx takes
// a Go duration, so until=7d is rejected and until=168h is not.
func GoDuration(d time.Duration) string {
	return strconv.FormatInt(int64(d/time.Hour), 10) + "h"
}

// HumanAge renders an elapsed time for the report.
func HumanAge(d time.Duration) string {
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
