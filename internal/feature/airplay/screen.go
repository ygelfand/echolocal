//go:build !board_biscuit

package airplay

import (
	"image"

	"github.com/ygelfand/libcountertop/pkg/display/video"
	sharedairplay "github.com/ygelfand/libcountertop/pkg/media/airplay"
	"github.com/ygelfand/libcountertop/pkg/media/playback"

	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/hardware/display"
)

func screen(o *sharedairplay.Options) {
	b := component.Board()
	o.Size = func() (int, int) {
		w, h := display.Get().Native()
		return display.Get().Orientation().Size(w, h)
	}
	o.Largest = image.Pt(b.DecodeWidth, b.DecodeHeight)
	o.Video = func() playback.Target { return video.Target(display.Get(), b.MaxFPS) }
}
