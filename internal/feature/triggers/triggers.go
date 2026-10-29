package triggers

import (
	"sync"

	esphome "github.com/ygelfand/go-esphome-device"

	sharedcomponent "github.com/ygelfand/libcountertop/pkg/runtime/component"
	setting "github.com/ygelfand/libcountertop/pkg/settings"
	"github.com/ygelfand/libcountertop/pkg/settings/schema"
	"github.com/ygelfand/libcountertop/pkg/voice"

	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/feature/actions"
)

func init() { component.Register(sharedcomponent.Device, Get, sharedcomponent.Order(42)) }

type Triggers struct {
	knobs *setting.Controls[schema.Voice]
}

var get = sync.OnceValue(func() *Triggers {
	t := &Triggers{knobs: &setting.Controls[schema.Voice]{
		Table: voice.NewTable(
			actions.Get().Names(),
			commandNames(),
			labelChoices(),
		),
		Device: component.DeviceVoice,
		Read:   config.VoiceSection.Read,
		Save:   save,
	}}
	component.Settings.Add(t.knobs)
	return t
})

func Get() *Triggers { return get() }

func (t *Triggers) Name() string { return "triggers" }

func (t *Triggers) Entities() []esphome.Entity { return t.knobs.Entities() }

func (t *Triggers) Restore(cfg config.Config) { t.knobs.PublishFrom(cfg.Voice) }

func (t *Triggers) Controls() *setting.Controls[schema.Voice] { return t.knobs }

func save(s setting.Setting[schema.Voice], v string) error {
	err := setting.Store(config.VoiceSection, s, v)
	applyCommands()
	actions.Get().Declare()
	return err
}
