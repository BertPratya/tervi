package daemon

import (
	"fmt"
	"time"
)

// reconnectWait returns the jittered wait before the numbered try.
func reconnectWait(try int, random float64) time.Duration {
	if try < 1 {
		try = 1
	}
	base := time.Second
	for index := 1; index < try && base < 30*time.Second; index++ {
		base *= 2
	}
	if base > 30*time.Second {
		base = 30 * time.Second
	}
	if random < 0 {
		random = 0
	}
	if random >= 1 {
		random = 0.999999999999
	}
	return time.Duration(float64(base) * (0.8 + 0.4*random))
}

// SecondsText formats a seconds count with singular wording for one second.
func SecondsText(seconds int) string {
	if seconds == 1 {
		return "1 second"
	}
	return fmt.Sprintf("%d seconds", seconds)
}
