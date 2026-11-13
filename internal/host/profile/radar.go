package profile

import (
	"github.com/ygelfand/echolocal/internal/android/services"
	"github.com/ygelfand/echolocal/internal/board"
	"github.com/ygelfand/echolocal/internal/host/bootimg"
	"github.com/ygelfand/echolocal/internal/host/sysimg"
)

var radar = Profile{
	Board:  board.Radar,
	Boot:   bootimg.Radar,
	System: sysimg.Radar,

	Disable: services.Disabled,
	Enable:  services.Enabled,
}
