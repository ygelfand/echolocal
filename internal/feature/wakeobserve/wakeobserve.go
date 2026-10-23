// Package wakeobserve adds opt-in diagnostics through existing authenticated actions.
package wakeobserve

import (
	"errors"
	"path/filepath"
	"sync"

	esphome "github.com/ygelfand/go-esphome-device"
	sharedcomponent "github.com/ygelfand/libcountertop/pkg/runtime/component"

	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/hardware/speaker"
	"github.com/ygelfand/echolocal/internal/layout"
	"github.com/ygelfand/echolocal/internal/wakecapture"
)

type Observer struct{ *wakecapture.Recorder }

func (*Observer) Name() string { return "wake-observation" }

var Get = sync.OnceValue(func() *Observer {
	r := wakecapture.New(filepath.Join(layout.StateDir, "wake-observation-v1"))
	r.Snapshot = func() wakecapture.Settings {
		c := config.Get().Microphone
		return wakecapture.Settings{
			Mic:         string(c.Mixing),
			Gain:        c.Gain,
			Leveling:    c.Leveling,
			AEC:         c.Cancel,
			Sensitivity: c.Sensitivity,
			Playing:     speaker.Sound().Busy(),
			Muted:       c.Muted,
		}
	}
	return &Observer{r}
})

func init() { component.Register(sharedcomponent.Device, Get, sharedcomponent.Order(35)) }
func (o *Observer) Actions() []*esphome.Action {
	return []*esphome.Action{
		{Name: "wake_observe_control", Answers: true, Args: []esphome.Arg{
			{
				Name: "operation",
				Type: esphome.ArgString,
			},
			{Name: "hours", Type: esphome.ArgInt},
			{Name: "limit", Type: esphome.ArgInt},
		}, Run: func(c esphome.Call) (any, error) {
			switch c.String("operation") {
			case "start":
				if err := o.Start(c.Int("hours"), c.Int("limit")); err != nil {
					return nil, err
				}
			case "stop":
				o.Stop()
			case "status":
			default:
				return nil, errors.New("observation_operation")
			}
			return o.Status(), nil
		}},
		{Name: "wake_observe_events", Answers: true, Run: func(esphome.Call) (any, error) {
			ids, err := o.List()
			return map[string]any{"version": 1, "ids": ids, "status": o.Status()}, err
		}},
		{Name: "wake_observe_data", Answers: true, Args: []esphome.Arg{
			{
				Name: "id",
				Type: esphome.ArgString,
			},
			{Name: "kind", Type: esphome.ArgString},
			{Name: "page", Type: esphome.ArgInt},
		}, Run: func(c esphome.Call) (any, error) {
			data, pages, err := o.Data(c.String("id"), c.String("kind"), c.Int("page"))
			return map[string]any{
				"version": 1,
				"id":      c.String("id"),
				"kind":    c.String("kind"),
				"page":    c.Int("page"),
				"pages":   pages,
				"data":    data,
			}, err
		}},
	}
}
