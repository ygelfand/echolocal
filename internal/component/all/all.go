// Package all is what this device is made of.
//
// Components register themselves from init, so a component nobody imports is a component that
// silently does not exist — no entity, no lifecycle, no error. This is the one list that pulls them
// in, and importing it is what makes the registry complete.
//
// Order is not decided here. Each component declares its phase and its place within it, so this list
// can stay alphabetical and mean nothing but membership.
package all

import (
	_ "github.com/ygelfand/echolocal/internal/android/setup"
	_ "github.com/ygelfand/echolocal/internal/feature/activity"
	_ "github.com/ygelfand/echolocal/internal/feature/api"
	_ "github.com/ygelfand/echolocal/internal/feature/bluetooth"
	_ "github.com/ygelfand/echolocal/internal/feature/buttons"
	_ "github.com/ygelfand/echolocal/internal/feature/detect"
	_ "github.com/ygelfand/echolocal/internal/feature/diag"
	_ "github.com/ygelfand/echolocal/internal/feature/feedback"
	_ "github.com/ygelfand/echolocal/internal/feature/firmware"
	_ "github.com/ygelfand/echolocal/internal/feature/light"
	_ "github.com/ygelfand/echolocal/internal/feature/maintenance"
	_ "github.com/ygelfand/echolocal/internal/feature/media"
	_ "github.com/ygelfand/echolocal/internal/feature/microphone"
	_ "github.com/ygelfand/echolocal/internal/feature/mute"
	_ "github.com/ygelfand/echolocal/internal/feature/recording"
	_ "github.com/ygelfand/echolocal/internal/feature/room"
	_ "github.com/ygelfand/echolocal/internal/feature/voice"
	_ "github.com/ygelfand/echolocal/internal/feature/wakeword"
)
