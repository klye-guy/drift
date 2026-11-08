package screen

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/randr"
	"github.com/jezek/xgb/xproto"
)

// pinToOutput takes our window away from the window manager and places it
// exactly over the named RandR output (e.g. "HDMI-A-1").
//
// Why: KWin (and some other WMs) decide for themselves which screen a new
// window or a fullscreen request goes to, and override where the client
// asks to be. A window with override-redirect set is not managed at all, so
// the X server — XWayland under KDE/GNOME Wayland — puts it exactly where
// we say. xscreensaver has used the same technique for decades.
//
// The window is found by _NET_WM_PID, so this must run after the toolkit
// has created and mapped it.
func pinToOutput(output string) error {
	c, err := xgb.NewConn()
	if err != nil {
		return err
	}
	defer c.Close()
	if err := randr.Init(c); err != nil {
		return fmt.Errorf("randr: %w", err)
	}
	root := xproto.Setup(c).DefaultScreen(c).Root

	x, y, w, h, err := outputGeometry(c, root, output)
	if err != nil {
		return err
	}

	win, err := findOwnWindow(c, root, 3*time.Second)
	if err != nil {
		return err
	}

	// 1. Withdraw the window so the WM lets go of it.
	xproto.UnmapWindow(c, win)
	if err := waitParent(c, win, root, time.Second); err != nil {
		// Some WMs keep the frame around; reparent ourselves.
		xproto.ReparentWindow(c, win, root, 0, 0)
	}

	// 2. Mark it unmanaged, then place, raise and map it.
	xproto.ChangeWindowAttributes(c, win, xproto.CwOverrideRedirect, []uint32{1})
	xproto.ConfigureWindow(c, win,
		xproto.ConfigWindowX|xproto.ConfigWindowY|xproto.ConfigWindowWidth|
			xproto.ConfigWindowHeight|xproto.ConfigWindowStackMode,
		[]uint32{uint32(x), uint32(y), uint32(w), uint32(h), xproto.StackModeAbove})
	if err := xproto.MapWindowChecked(c, win).Check(); err != nil {
		return err
	}

	// 3. Take keyboard focus so a wake-up keypress reaches us rather than
	//    the app underneath. Best effort: the WM may refuse.
	time.Sleep(50 * time.Millisecond)
	_ = xproto.SetInputFocusChecked(c, xproto.InputFocusParent, win, xproto.TimeCurrentTime).Check()

	// Confirm.
	g, err := xproto.GetGeometry(c, xproto.Drawable(win)).Reply()
	if err != nil {
		return err
	}
	if int(g.X) != x || int(g.Y) != y {
		return fmt.Errorf("window ended up at %d,%d instead of %d,%d", g.X, g.Y, x, y)
	}
	return nil
}

// outputGeometry returns the position and size of a connected output.
func outputGeometry(c *xgb.Conn, root xproto.Window, name string) (x, y, w, h int, err error) {
	res, err := randr.GetScreenResourcesCurrent(c, root).Reply()
	if err != nil {
		return 0, 0, 0, 0, err
	}
	for _, o := range res.Outputs {
		info, err := randr.GetOutputInfo(c, o, res.ConfigTimestamp).Reply()
		if err != nil || string(info.Name) != name || info.Crtc == 0 {
			continue
		}
		crtc, err := randr.GetCrtcInfo(c, info.Crtc, res.ConfigTimestamp).Reply()
		if err != nil {
			return 0, 0, 0, 0, err
		}
		return int(crtc.X), int(crtc.Y), int(crtc.Width), int(crtc.Height), nil
	}
	return 0, 0, 0, 0, fmt.Errorf("output %q not found in RandR", name)
}

// findOwnWindow searches the window tree for a viewable top-level window
// whose _NET_WM_PID is this process.
func findOwnWindow(c *xgb.Conn, root xproto.Window, timeout time.Duration) (xproto.Window, error) {
	pidAtom, err := xproto.InternAtom(c, true, uint16(len("_NET_WM_PID")), "_NET_WM_PID").Reply()
	if err != nil || pidAtom.Atom == 0 {
		return 0, errors.New("_NET_WM_PID atom missing")
	}
	pid := uint32(os.Getpid())

	var search func(w xproto.Window, depth int) xproto.Window
	search = func(w xproto.Window, depth int) xproto.Window {
		tree, err := xproto.QueryTree(c, w).Reply()
		if err != nil {
			return 0
		}
		for _, child := range tree.Children {
			p, err := xproto.GetProperty(c, false, child, pidAtom.Atom, xproto.AtomCardinal, 0, 1).Reply()
			if err == nil && p.ValueLen == 1 && xgb.Get32(p.Value) == pid {
				if a, err := xproto.GetWindowAttributes(c, child).Reply(); err == nil &&
					a.MapState == xproto.MapStateViewable {
					return child
				}
			}
			if depth > 0 {
				if found := search(child, depth-1); found != 0 {
					return found
				}
			}
		}
		return 0
	}

	deadline := time.Now().Add(timeout)
	for {
		// Depth 3 covers WM frames (root → frame → [decoration] → client).
		if w := search(root, 3); w != 0 {
			return w, nil
		}
		if time.Now().After(deadline) {
			return 0, errors.New("could not find our window")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// waitParent waits until win's parent is want.
func waitParent(c *xgb.Conn, win, want xproto.Window, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if t, err := xproto.QueryTree(c, win).Reply(); err == nil && t.Parent == want {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("window manager did not release the window")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
