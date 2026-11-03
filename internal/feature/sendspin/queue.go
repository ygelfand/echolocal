//go:build !board_biscuit

package sendspin

import (
	"github.com/ygelfand/libcountertop/pkg/homeassistant/musicassistant"
	sharedplayer "github.com/ygelfand/libcountertop/pkg/media/sendspin"

	"github.com/ygelfand/echolocal/internal/feature/product"
)

func queue() sharedplayer.Queue {
	return musicassistant.Build(product.Get(), ClientID)
}
