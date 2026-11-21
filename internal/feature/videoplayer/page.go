//go:build !board_biscuit

package videoplayer

import (
	sharedpage "github.com/ygelfand/libcountertop/pkg/display/videopage"

	"github.com/ygelfand/echolocal/internal/feature/media"
	"github.com/ygelfand/echolocal/internal/feature/shell"
)

func NewPage(c sharedpage.Controls) *sharedpage.Page {
	return sharedpage.New(
		c,
		sharedpage.Dependencies{
			Shell: shell.Get(),
			Left:  func() { media.Card().Left() },
		},
	)
}
