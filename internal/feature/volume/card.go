package volume

import (
	"github.com/ygelfand/libcountertop/pkg/display/shell"

	"github.com/ygelfand/echolocal/internal/config"
)

func (v *Volume) Card() shell.View              { return v.card }
func (v *Volume) Picked() (config.Stream, bool) { return v.card.Picked() }
func (v *Volume) Pick(s config.Stream)          { v.card.Pick(s) }
func (v *Volume) Show(s config.Stream)          { v.card.Show(s) }
func (v *Volume) Expand()                       { v.card.Expand() }
func (v *Volume) Linger()                       { v.card.Linger() }
func (v *Volume) Dismiss()                      { v.card.Dismiss() }
