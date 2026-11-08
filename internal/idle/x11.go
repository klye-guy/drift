package idle

import (
	"context"
	"time"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/screensaver"
	"github.com/jezek/xgb/xproto"
)

// openX11 uses the MIT-SCREEN-SAVER extension. Only meaningful in a real X11
// session: under XWayland it sees X clients' input only.
func openX11(ctx context.Context, timeout time.Duration) (Monitor, error) {
	conn, err := xgb.NewConn()
	if err != nil {
		return nil, err
	}
	if err := screensaver.Init(conn); err != nil {
		conn.Close()
		return nil, err
	}
	root := xproto.Setup(conn).DefaultScreen(conn).Root
	query := func() (time.Duration, error) {
		info, err := screensaver.QueryInfo(conn, xproto.Drawable(root)).Reply()
		if err != nil {
			return 0, err
		}
		return time.Duration(info.MsSinceUserInput) * time.Millisecond, nil
	}
	if _, err := query(); err != nil {
		conn.Close()
		return nil, err
	}
	return startPoller(ctx, "x11", timeout, query, func() error { conn.Close(); return nil }), nil
}
