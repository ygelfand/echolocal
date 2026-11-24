//go:build !board_biscuit

package wakeword

import (
	"github.com/ygelfand/libcountertop/pkg/display/visual"
	setting "github.com/ygelfand/libcountertop/pkg/settings"
)

func looks() []setting.Option { return visual.Choices() }
