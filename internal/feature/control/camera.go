//go:build !board_biscuit

package control

import (
	"github.com/spf13/cobra"
	harness "github.com/ygelfand/libcountertop/pkg/runtime/control"
	"github.com/ygelfand/libcountertop/pkg/runtime/control/cameracmd"

	"github.com/ygelfand/echolocal/internal/feature/vision"
)

func cameraCommand() *cobra.Command {
	camera := harness.Group("camera", "The camera", "")
	camera.AddCommand(harness.Says(&cobra.Command{
		Use:   "still [path]",
		Short: "Take the picture Home Assistant would get, to " + cameracmd.DefaultStill + " or path",
		Args:  cobra.MaximumNArgs(1),
	}, func(args []string) (string, error) { return cameracmd.Still(vision.Get().Still, args) }))
	return camera
}
