//go:build !board_biscuit

package videoplayer

import (
	sharedvolume "github.com/ygelfand/libcountertop/pkg/audio/volume"
	sharedpage "github.com/ygelfand/libcountertop/pkg/display/videopage"

	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/feature/media"
	"github.com/ygelfand/echolocal/internal/feature/shell"
	"github.com/ygelfand/echolocal/internal/feature/volume"
)

func NewPage(c sharedpage.Controls) *sharedpage.Page {
	return sharedpage.New(
		c,
		sharedpage.Dependencies{
			Shell:  shell.Get(),
			Volume: sharedvolume.For(volume.Get(), config.StreamMedia),
			Left:   func() { media.Card().Left() },
		},
	)
}
