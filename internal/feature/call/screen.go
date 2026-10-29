//go:build !board_biscuit

package call

import (
	"github.com/ygelfand/libcountertop/pkg/display/callvideo"
	"github.com/ygelfand/libcountertop/pkg/display/callview"
	sharedcall "github.com/ygelfand/libcountertop/pkg/media/call"

	"github.com/ygelfand/echolocal/internal/feature/livecam"
	"github.com/ygelfand/echolocal/internal/feature/message"
	"github.com/ygelfand/echolocal/internal/feature/privacy"
	"github.com/ygelfand/echolocal/internal/feature/shell"
	"github.com/ygelfand/echolocal/internal/hardware/display"
)

func configureSettings(c *sharedcall.Calls) {}

func front(d *sharedcall.Dependencies) {
	d.Privacy = privacy.Get()
	d.Stage = callview.NewStage(shell.Get())
	d.Video = callvideo.New(callvideo.Dependencies{
		Display: display.Get(),
		Camera:  callvideo.NewCamera(livecam.Get().Sessions()),
	})
	d.Messages = message.Get()
}
