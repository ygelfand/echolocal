package bluetooth

import "time"

const (
	radioRetryInitial = time.Second
	radioRetryMax     = 30 * time.Second
)

// settleRadio applies each requested state and retries failures with bounded backoff. A new request
// interrupts the wait and restarts from the shortest delay.
func settleRadio(wanted <-chan struct{}, tune func() bool, after func(time.Duration) <-chan time.Time) {
	for range wanted {
		wait := radioRetryInitial
		for !tune() {
			select {
			case _, ok := <-wanted:
				if !ok {
					return
				}
				wait = radioRetryInitial
			case <-after(wait):
				if next := wait * 2; next < radioRetryMax {
					wait = next
				} else {
					wait = radioRetryMax
				}
			}
		}
	}
}
