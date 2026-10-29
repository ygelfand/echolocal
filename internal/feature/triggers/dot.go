//go:build board_biscuit

package triggers

import setting "github.com/ygelfand/libcountertop/pkg/settings"

func commandNames() []string { return nil }

func applyCommands() {}

func labelChoices() func() []setting.Option { return nil }
