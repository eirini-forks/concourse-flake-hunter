package commands

import (
	"fmt"
	"time"
)

func timeSince(timestamp int64) string {
	t := time.Unix(timestamp, 0)
	timeSince := time.Since(t)

	hoursSince := timeSince / time.Hour
	if hoursSince < 1 {
		return fmt.Sprintf("%dm", timeSince/time.Minute)
	}
	if hoursSince < 24 {
		return fmt.Sprintf("%dh", hoursSince)
	}
	return fmt.Sprintf("%dd", hoursSince/24)
}
