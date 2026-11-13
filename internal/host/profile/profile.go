package profile

import (
	"fmt"
	"slices"
	"strings"

	"github.com/ygelfand/echolocal/internal/android/services"
	"github.com/ygelfand/echolocal/internal/board"
	"github.com/ygelfand/echolocal/internal/host/bootimg"
	"github.com/ygelfand/echolocal/internal/host/sysimg"
)

var profiles = map[string]Profile{
	board.Biscuit.Device:  biscuit,
	board.Checkers.Device: checkers,
	board.Cronos.Device:   cronos,
	board.Crown.Device:    crown,
	board.Athena.Device:   athena,
	board.Rook.Device:     rook,
	board.Radar.Device:    radar,
}

type Profile struct {
	Board board.Board

	Boot bootimg.Image

	System sysimg.Layout

	Disable, Enable []string

	Hide []services.Package

	SupportedSDK         string
	UnsupportedSDKReason string
}

// Fire OS 6 is Android 7.1.
const (
	fireOS6    = "25"
	fireOS6Why = "want 25 (Fire OS 6); Fire OS 5 needs echolocal 0.0.6 or earlier"
)

func (p Profile) SDK() string {
	if p.SupportedSDK == "" {
		return fireOS6
	}
	return p.SupportedSDK
}

func (p Profile) SDKRefusal() string {
	if p.UnsupportedSDKReason == "" {
		return fireOS6Why
	}
	return p.UnsupportedSDKReason
}

func For(device string) (Profile, error) {
	b, known := board.For(device)
	if !known {
		return Profile{}, fmt.Errorf("profile: %q is not a device this build installs to", device)
	}

	p, ok := profiles[b.Device]
	if !ok {
		return Profile{Board: b}, fmt.Errorf("EchoLocal does not support %s yet", b)
	}
	if b.ServiceName == "" {
		return Profile{Board: b}, fmt.Errorf("EchoLocal does not support %s yet", b)
	}
	return p, nil
}

func Boards() []board.Board {
	out := make([]board.Board, 0, len(profiles))
	for _, p := range profiles {
		out = append(out, p.Board)
	}
	slices.SortFunc(
		out,
		func(a, b board.Board) int { return strings.Compare(a.Codename, b.Codename) },
	)
	return out
}
