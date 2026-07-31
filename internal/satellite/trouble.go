package satellite

import (
	"time"

	"github.com/ygelfand/echolocal/internal/led"
	"github.com/ygelfand/echolocal/internal/settings"
)

// troubleFlash is how long the ring shows a failure, and troubleColor what it shows. Red is not used
// anywhere else, so it never has to be told apart from an effect or a volume arc.
const troubleFlash = 1500 * time.Millisecond

var troubleColor = led.Color{R: 0xC0, G: 0x00, B: 0x00}

// troubleRing says something failed, on its own claim above the turn so ending whatever failed cannot
// take the indication away with it. Which animation is the user's choice, and None is one of the answers:
// some rooms would rather the ring stayed out of it.
func troubleRing(leds *led.Driver) {
	name := settings.Get().Ring.TroubleOr(settings.DefaultTrouble)
	if name == "" {
		return
	}
	leds.Claim(led.PriorityTrouble).ShowFor(led.Content{Effect: name, Base: troubleColor}, troubleFlash)
}
