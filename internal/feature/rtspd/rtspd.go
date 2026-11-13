package rtspd

import (
	"sync"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/libcountertop/pkg/camera/live"
	"github.com/ygelfand/libcountertop/pkg/camera/onvif"
	camerartspd "github.com/ygelfand/libcountertop/pkg/camera/rtspd"
	"github.com/ygelfand/libcountertop/pkg/media/rtsp"
	sharedcomponent "github.com/ygelfand/libcountertop/pkg/runtime/component"
	setting "github.com/ygelfand/libcountertop/pkg/settings"
	"github.com/ygelfand/libcountertop/pkg/settings/schema"

	"github.com/ygelfand/echolocal/internal/android/prop"
	"github.com/ygelfand/echolocal/internal/board"
	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/feature/livecam"
	"github.com/ygelfand/echolocal/internal/feature/privacy"
	"github.com/ygelfand/echolocal/internal/feature/vision"
	"github.com/ygelfand/echolocal/internal/hardware/mic"
	"github.com/ygelfand/echolocal/internal/layout"
)

const (
	Port      = 8554
	ONVIFPort = 8000
)

func init() {
	component.Register(
		sharedcomponent.Device,
		Get,
		sharedcomponent.Order(70),
		component.Needs(board.Camera),
	)
}

type Server struct {
	knobs *setting.Controls[schema.RTSP]
	srv   *camerartspd.Server
}

var (
	once   sync.Once
	shared *Server
)

func Get() *Server {
	once.Do(func() {
		shared = &Server{srv: camerartspd.New(camerartspd.Options{
			Port:     Port,
			Identity: rtsp.Identity{Name: "EchoLocal", CNAMEPrefix: "echolocal-"},
			Source:   livecam.Get().Sessions(),
			Helper:   "echolocal-camera",
			Served:   livecam.Get().Served,
			FPS:      live.FPS,
			Bitrate:  livecam.Get().Bitrate,
			ONVIF:    describe,
			Auth:     camerartspd.Credentials(func() schema.RTSP { return config.Get().RTSP }),
			Mic:      microphone{},
			MicMuted: privacy.Get().MicMuted,
		})}
		shared.build()
		livecam.Get().StreamsChanged.Listen(func([]int) { shared.reload() })
	})
	return shared
}

type microphone struct{}

func (microphone) Frames() (<-chan []int16, func()) { return mic.Get().Listen("rtsp") }

func describe() onvif.Config {
	dev := config.Get().Device
	serial, _ := prop.Get("ro.serialno")
	if serial == "" {
		serial = dev.Name
	}
	return onvif.Config{
		Port: ONVIFPort,
		Device: onvif.Device{
			Manufacturer: layout.Manufacturer,
			Model:        dev.Board.Model,
			Firmware:     layout.Version,
			Serial:       serial,
			Hardware:     dev.Board.Model,
			Name:         dev.Name,
		},
		Hardware: "EchoLocal",
		Snapshot: vision.Get().Still,
	}
}

func (s *Server) Name() string { return "rtsp" }

func (s *Server) Entities() []esphome.Entity { return s.knobs.Entities() }

func (s *Server) Restore(cfg config.Config) {
	s.knobs.PublishFrom(cfg.RTSP)
	if cfg.RTSP.Enabled {
		s.srv.Start()
	}
}

func (s *Server) build() {
	s.knobs = camerartspd.Controls(
		component.DeviceCamera,
		func() schema.RTSP { return config.Get().RTSP },
		s.apply,
	)
	component.Settings.Add(s.knobs)
}

func (s *Server) Settings() *setting.Controls[schema.RTSP] { return s.knobs }

func (s *Server) apply(at schema.RTSP) error {
	err := config.Set().RTSP().All(at)
	if at.Enabled {
		s.srv.Start()
	} else {
		s.srv.Stop()
	}
	return err
}

func (s *Server) reload() {
	if !config.Get().RTSP.Enabled {
		return
	}
	s.srv.Stop()
	s.srv.Start()
}
