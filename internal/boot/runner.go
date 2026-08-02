package boot

import (
	"time"

	"github.com/ygelfand/echolocal/internal/service"
)

// forever is the restart policy for anything that should never be down.
func forever() service.Option { return service.Restart(time.Second, 30*time.Second) }
