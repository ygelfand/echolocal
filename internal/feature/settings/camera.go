//go:build !board_biscuit

package settings

import (
	"sync"

	"github.com/ygelfand/libcountertop/pkg/camera/live"
	sharedpreview "github.com/ygelfand/libcountertop/pkg/display/camerapreview"
	sharedsettings "github.com/ygelfand/libcountertop/pkg/display/settings"
	sharedshell "github.com/ygelfand/libcountertop/pkg/display/shell"
	"github.com/ygelfand/libcountertop/pkg/say"

	"github.com/ygelfand/echolocal/internal/feature/livecam"
	"github.com/ygelfand/echolocal/internal/feature/rtspd"
	"github.com/ygelfand/echolocal/internal/feature/shell"
	"github.com/ygelfand/echolocal/internal/hardware/display"
)

var Preview = sync.OnceValue(func() *sharedpreview.Preview {
	return sharedpreview.New(sharedpreview.Dependencies{
		Shell:   shell.Get(),
		Camera:  livecam.Get().Sessions(),
		Display: display.Get(),
	})
})

var cameraPages = sync.OnceValue(func() *sharedsettings.CameraPages[live.Knobs] {
	cam := livecam.Get()
	preview := Preview()
	return sharedsettings.NewCameraPages(sharedsettings.CameraOptions[live.Knobs]{
		Table:   cam.Table,
		Read:    cam.Saved,
		Save:    cam.Set,
		Reset:   cam.Reset,
		Push:    shell.Get().Push,
		Redraw:  shell.Get().Redraw,
		Preview: preview.Want,
		Wrap:    preview.Page,
	})
})

func cameraPage() *sharedshell.Page { return cameraPages().Page() }

func streamPage() *sharedshell.Page {
	return sharedsettings.ServicePage(say.T("features.rtsp"), rtspd.Get().Settings())
}
