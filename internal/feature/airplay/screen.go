//go:build !board_biscuit

package airplay

import (
	"image"

	sharedairplay "github.com/ygelfand/libcountertop/pkg/media/airplay"

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
}
