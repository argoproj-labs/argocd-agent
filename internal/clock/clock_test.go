package clock

import (
	"testing"
	"time"
)

func Test_StandarClock(t *testing.T) {
	clock := StandardClock()
	var now, cnow time.Time
	// For the unlikely event that now and cnow are being called in another
	// second.
	for {
		now = time.Now()
		cnow = clock.Now()
		if now.Unix() == cnow.Unix() {
			break
		}
	}
}
