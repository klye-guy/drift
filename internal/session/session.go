// Package session asks the desktop whether a saver should start: is some
// app inhibiting idle (video playing, a game, a presentation), and is the
// screen already locked?
//
// Both GNOME and KDE are covered; other desktops fall back to the generic
// freedesktop interfaces where they exist. Any failed call is treated as
// "no" so a missing service never stops the saver from protecting the panel.
package session

import (
	"os"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
)

// Desktop is the detected environment.
type Desktop string

const (
	GNOME Desktop = "gnome"
	KDE   Desktop = "kde"
	Other Desktop = "other"
)

// Detect reads XDG_CURRENT_DESKTOP.
func Detect() Desktop {
	d := strings.ToUpper(os.Getenv("XDG_CURRENT_DESKTOP"))
	switch {
	case strings.Contains(d, "GNOME"), strings.Contains(d, "UNITY"):
		return GNOME
	case strings.Contains(d, "KDE"):
		return KDE
	}
	return Other
}

// Session holds a D-Bus connection for status queries.
type Session struct {
	Desktop Desktop
	conn    *dbus.Conn
}

// Open connects to the session bus.
func Open() (*Session, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, err
	}
	return &Session{Desktop: Detect(), conn: conn}, nil
}

// Close releases the bus connection.
func (s *Session) Close() error { return s.conn.Close() }

const callTimeout = 2 * time.Second

func (s *Session) callBool(dest, path, method string, args ...any) (bool, bool) {
	obj := s.conn.Object(dest, dbus.ObjectPath(path))
	ch := make(chan *dbus.Call, 1)
	obj.Go(method, 0, ch, args...)
	select {
	case call := <-ch:
		var v bool
		if call.Err != nil || call.Store(&v) != nil {
			return false, false
		}
		return v, true
	case <-time.After(callTimeout):
		return false, false
	}
}

// Inhibited reports whether an application has asked the desktop not to
// treat the session as idle (screen dimming/screensaver inhibition).
func (s *Session) Inhibited() bool {
	switch s.Desktop {
	case GNOME:
		// GsmInhibitorFlag 8 = "inhibit the session being marked as idle".
		if v, ok := s.callBool("org.gnome.SessionManager", "/org/gnome/SessionManager",
			"org.gnome.SessionManager.IsInhibited", uint32(8)); ok {
			return v
		}
	case KDE:
		// PowerDevil PolicyAgent RequiredPolicy 4 = ChangeScreenSettings;
		// this is the same check KDE's own screen locker uses.
		if v, ok := s.callBool("org.kde.Solid.PowerManagement.PolicyAgent",
			"/org/kde/Solid/PowerManagement/PolicyAgent",
			"org.kde.Solid.PowerManagement.PolicyAgent.HasInhibition", uint32(4)); ok {
			return v
		}
	}
	if v, ok := s.callBool("org.freedesktop.PowerManagement", "/org/freedesktop/PowerManagement/Inhibit",
		"org.freedesktop.PowerManagement.Inhibit.HasInhibit"); ok {
		return v
	}
	return false
}

// Locked reports whether the lock screen is up. Normal windows can't draw
// over it, so there's no point running a saver then.
func (s *Session) Locked() bool {
	if s.Desktop == GNOME {
		if v, ok := s.callBool("org.gnome.ScreenSaver", "/org/gnome/ScreenSaver",
			"org.gnome.ScreenSaver.GetActive"); ok {
			return v
		}
	}
	if v, ok := s.callBool("org.freedesktop.ScreenSaver", "/ScreenSaver",
		"org.freedesktop.ScreenSaver.GetActive"); ok {
		return v
	}
	return false
}
