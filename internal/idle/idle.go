// Package idle reports when the user goes idle and when they come back.
//
// Backends, tried in this order by "auto":
//
//	mutter  GNOME (Wayland or X11): org.gnome.Mutter.IdleMonitor over D-Bus
//	wayland KDE Plasma and wlroots compositors: ext-idle-notify-v1
//	x11     any X11 session: the MIT-SCREEN-SAVER extension
package idle

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// Event is a transition in user activity.
type Event int

const (
	Idle   Event = iota // no input for the configured timeout
	Active              // input resumed after Idle
)

func (e Event) String() string {
	if e == Idle {
		return "idle"
	}
	return "active"
}

// Monitor delivers Idle/Active transitions on Events until ctx ends.
type Monitor interface {
	Name() string
	Events() <-chan Event
	Close() error
}

// Open picks a backend. backend is "auto", "mutter", "wayland" or "x11".
func Open(ctx context.Context, backend string, timeout time.Duration) (Monitor, error) {
	try := []string{backend}
	if backend == "" || backend == "auto" {
		try = autoOrder()
	}
	var errs []error
	for _, b := range try {
		var m Monitor
		var err error
		switch b {
		case "mutter":
			m, err = openMutter(ctx, timeout)
		case "wayland":
			m, err = openWayland(ctx, timeout)
		case "x11":
			m, err = openX11(ctx, timeout)
		default:
			err = fmt.Errorf("unknown idle backend %q", b)
		}
		if err == nil {
			return m, nil
		}
		errs = append(errs, fmt.Errorf("%s: %w", b, err))
	}
	return nil, fmt.Errorf("no idle backend works here: %w", errors.Join(errs...))
}

func autoOrder() []string {
	desktop := strings.ToUpper(os.Getenv("XDG_CURRENT_DESKTOP"))
	order := []string{}
	if strings.Contains(desktop, "GNOME") {
		order = append(order, "mutter")
	}
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		order = append(order, "wayland")
	}
	if os.Getenv("DISPLAY") != "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		order = append(order, "x11")
	}
	if !strings.Contains(desktop, "GNOME") {
		order = append(order, "mutter") // harmless last resort
	}
	return order
}

// poller turns an "idle for how long?" query into Idle/Active events.
// Used by backends that report idle time rather than pushing transitions.
type poller struct {
	name   string
	events chan Event
	cancel context.CancelFunc
	closer func() error
}

func (p *poller) Name() string         { return p.name }
func (p *poller) Events() <-chan Event { return p.events }
func (p *poller) Close() error {
	p.cancel()
	if p.closer != nil {
		return p.closer()
	}
	return nil
}

func startPoller(ctx context.Context, name string, timeout time.Duration, query func() (time.Duration, error), closer func() error) *poller {
	ctx, cancel := context.WithCancel(ctx)
	p := &poller{name: name, events: make(chan Event, 4), cancel: cancel, closer: closer}
	go func() {
		defer close(p.events)
		idle := false
		var last time.Duration
		for {
			// Poll slowly while waiting; quickly while a saver is up so
			// waking feels instant.
			interval := time.Second
			if idle {
				interval = 200 * time.Millisecond
			} else if remaining := timeout - last; remaining < interval && remaining > 0 {
				interval = remaining + 20*time.Millisecond
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(interval):
			}
			d, err := query()
			if err != nil {
				continue // transient (compositor restart); keep trying
			}
			switch {
			case !idle && d >= timeout:
				idle = true
				p.events <- Idle
			case idle && d < last:
				idle = false
				p.events <- Active
			}
			last = d
		}
	}()
	return p
}
