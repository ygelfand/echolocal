package volume

import (
	"sync"

	"github.com/ygelfand/libcountertop/pkg/display/idleview"
	sharedshell "github.com/ygelfand/libcountertop/pkg/display/shell"
	sharedview "github.com/ygelfand/libcountertop/pkg/display/volume"
	"github.com/ygelfand/libcountertop/pkg/settings/schema"

	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/feature/feedback"
	"github.com/ygelfand/echolocal/internal/feature/media"
	"github.com/ygelfand/echolocal/internal/feature/shell"
	"github.com/ygelfand/echolocal/internal/feature/tessera"
)

var (
	pageOnce sync.Once
	sound    *sharedshell.Page
)

func page() *sharedshell.Page {
	pageOnce.Do(func() {
		var outputs sharedview.Rows
		if c := media.Get().Outputs(); c != nil {
			outputs = sharedview.Segments(c)
		}
		sound = sharedview.NewPage(sharedview.PageOptions{
			Levels:    Get(),
			Streams:   config.Streams(),
			MainSteps: media.VolumeSteps,
			Chimes:    chimes{},
			Feedback:  feedback.Get().Feedback,
			Media:     media.Get().Settings(),
			Outputs:   outputs,
			Push:      shell.Get().Push,
		})
	})
	return sound
}

func Page() sharedshell.View { return page() }

type shows interface{ Shows(config.Stream) bool }

func showing(s config.Stream) bool {
	top := shell.Get().Top()
	if top == sharedshell.View(page()) {
		return true
	}
	if _, idle := top.(*idleview.View); idle && s == config.StreamMedia &&
		tessera.Firmware().SaverUp() {
		return true
	}
	v, ok := top.(shows)
	return ok && v.Shows(s)
}

type chimes struct{}

func (chimes) Chime() schema.Chime     { return config.Get().Feedback.Chime }
func (chimes) SetChime(c schema.Chime) { feedback.Get().SetChime(c) }
func (chimes) Preview(s schema.Stream) { feedback.Preview(s) }
