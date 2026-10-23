package wakeobserve

import (
	"path/filepath"
	"testing"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/echolocal/internal/wakecapture"
)

func TestObservationActionsValidateRequestsAndRemainOffByDefault(t *testing.T) {
	o := &Observer{wakecapture.New(filepath.Join(t.TempDir(), "captures"))}
	actions := map[string]*esphome.Action{}
	for _, a := range o.Actions() {
		actions[a.Name] = a
		if !a.Answers {
			t.Error("missing response", a.Name)
		}
	}
	if len(actions) != 3 {
		t.Fatal("unexpected action surface")
	}
	call := func(name string, args map[string]any) (any, error) {
		t.Helper()
		c, err := esphome.NewCall(args)
		if err != nil {
			t.Fatal(err)
		}
		return actions[name].Run(c)
	}
	v, err := call("wake_observe_control", map[string]any{"operation": "status"})
	if err != nil || v.(wakecapture.Status).Enabled {
		t.Fatal("default status", v, err)
	}
	for _, args := range []map[string]any{
		{"operation": "start", "hours": 0, "limit": 1},
		{"operation": "start", "hours": 25, "limit": 1},
		{"operation": "start", "hours": 1, "limit": 101},
		{"operation": "delete"},
	} {
		if _, err := call("wake_observe_control", args); err == nil {
			t.Fatal("accepted invalid control", args)
		}
	}
	if _, err := call(
		"wake_observe_control",
		map[string]any{"operation": "start", "hours": 1, "limit": 1},
	); err != nil {
		t.Fatal(err)
	}
	if !o.Status().Enabled {
		t.Fatal("start did not enable")
	}
	if _, err := call(
		"wake_observe_control",
		map[string]any{"operation": "start", "hours": 1, "limit": 1},
	); err == nil {
		t.Fatal("active session replaced")
	}
	if _, err := call(
		"wake_observe_data",
		map[string]any{"id": "../secret", "kind": "json", "page": 0},
	); err == nil {
		t.Fatal("path traversal accepted")
	}
	if _, err := call("wake_observe_events", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := call(
		"wake_observe_control",
		map[string]any{"operation": "stop"},
	); err != nil ||
		o.Status().Enabled {
		t.Fatal("stop", err)
	}
}
