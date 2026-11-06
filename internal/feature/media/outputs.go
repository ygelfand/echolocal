package media

import (
	"github.com/ygelfand/libcountertop/pkg/say"
	setting "github.com/ygelfand/libcountertop/pkg/settings"

	"github.com/ygelfand/echolocal/internal/board"
	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/hardware/speaker"
)

func outputControls() *setting.Controls[config.Speaker] {
	rows := outputRows()
	if len(rows) == 0 {
		return nil
	}
	return &setting.Controls[config.Speaker]{
		Table: setting.NewTable(
			"speaker",
			[]setting.Group{component.SpeakerGroup},
			rows,
			setting.Messages{Text: say.T, Missing: say.Missing},
		),
		Device: component.DevicePlayback,
		Read:   config.SpeakerSection.Read,
		Save: func(s setting.Setting[config.Speaker], v string) error {
			if err := setting.Store(config.SpeakerSection, s, v); err != nil {
				return err
			}
			c := config.SpeakerSection.Read()
			spk := speaker.Get()
			spk.SetOutputs(c.SpeakerOutput, c.HeadphoneOutput)
			spk.SetGain(c.SpeakerGain)
			return nil
		},
	}
}

func outputRows() []setting.Setting[config.Speaker] {
	var rows []setting.Setting[config.Speaker]
	if component.Board().Has(board.HeadphoneJack) {
		modes := config.OutputModes()
		rows = append(rows,
			setting.Pick(
				setting.Setting[config.Speaker]{
					Name:  "speaker_output",
					ID:    "speaker_output",
					Group: component.SpeakerGroup,
					Icon:  "mdi:speaker",
				},
				modes,
				func(c *config.Speaker) *config.OutputMode { return &c.SpeakerOutput },
			),
			setting.Pick(
				setting.Setting[config.Speaker]{
					Name:  "headphone_output",
					ID:    "headphone_output",
					Group: component.SpeakerGroup,
					Icon:  "mdi:headphones",
				},
				modes,
				func(c *config.Speaker) *config.OutputMode { return &c.HeadphoneOutput },
			),
		)
	}
	if lo, hi, ok := speaker.GainRange(); ok {
		rows = append(rows, setting.Count(
			setting.Setting[config.Speaker]{
				Name:   "speaker_gain",
				ID:     "speaker_gain",
				Group:  component.SpeakerGroup,
				Icon:   "mdi:volume-plus",
				Slider: true,
				Min:    lo,
				Max:    hi,
			},
			func(c *config.Speaker) *int { return &c.SpeakerGain },
		))
	}
	return rows
}
