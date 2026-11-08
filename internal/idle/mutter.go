package idle

import (
	"context"
	"time"

	"github.com/godbus/dbus/v5"
)

// openMutter uses GNOME's idle monitor, which sees all input in both the
// Wayland and X11 GNOME sessions.
func openMutter(ctx context.Context, timeout time.Duration) (Monitor, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, err
	}
	obj := conn.Object("org.gnome.Mutter.IdleMonitor", "/org/gnome/Mutter/IdleMonitor/Core")
	query := func() (time.Duration, error) {
		var ms uint64
		if err := obj.Call("org.gnome.Mutter.IdleMonitor.GetIdletime", 0).Store(&ms); err != nil {
			return 0, err
		}
		return time.Duration(ms) * time.Millisecond, nil
	}
	if _, err := query(); err != nil {
		conn.Close()
		return nil, err
	}
	return startPoller(ctx, "mutter", timeout, query, conn.Close), nil
}
