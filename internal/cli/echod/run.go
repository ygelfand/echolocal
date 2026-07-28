package echod

import (
	"log/slog"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/ygelfand/echolocal/internal/alog"
	"github.com/ygelfand/echolocal/internal/boot"
	"github.com/ygelfand/echolocal/internal/layout"
)

func newRunCmd() *cobra.Command {
	var name string

	c := &cobra.Command{
		Use:   "run",
		Short: "Run the device agent",
		Long: "Plays the boot animation, then stays resident. echod is installed in place of\n" +
			"Amazon's ledcontroller service, so init starts it from on post-fs-data and\n" +
			"restarts it if it exits, and nothing else drives the ring.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			h := alog.NewHandler(layout.LogTag, cmd.ErrOrStderr())
			defer h.Close()
			slog.SetDefault(slog.New(h))

			// init discards our stderr, so a panic would otherwise vanish and look like a silent
			// restart. Log it, then let it kill the process as it would have.
			defer func() {
				if r := recover(); r != nil {
					slog.Error("panic", "panic", r, "stack", string(debug.Stack()))
					h.Close()
					panic(r)
				}
			}()

			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			// Launched by init or an adb shell, either of which can go away.
			signal.Ignore(syscall.SIGHUP)

			return boot.Run(ctx, boot.Config{Name: name, Version: Version})
		},
	}

	c.Flags().StringVar(&name, "name", "", "device name Home Assistant sees; derived from the serial if unset")
	return c
}
