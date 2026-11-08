// Package daemon watches for idle and runs savers.
//
// States:
//
//	watching ──idle (not inhibited, not locked)──▶ saving
//	saving   ──any input / saver exits 0 / lock──▶ watching
//	saving   ──switch_minutes elapsed──▶ saving (next saver)
package daemon

import (
	"context"
	"fmt"
	"log"
	"math/rand/v2"
	"strings"
	"time"

	"archavist.com/drift/internal/catalog"
	"archavist.com/drift/internal/conf"
	"archavist.com/drift/internal/idle"
	"archavist.com/drift/internal/session"
)

// Daemon is the long-running watcher started by the systemd user unit.
type Daemon struct {
	Global   conf.Section
	Catalog  catalog.Catalog
	Renderer string

	sess     *session.Session
	launch   *Launcher
	playlist []string
	next     int
}

// Run blocks until ctx is cancelled or the idle backend fails.
func (d *Daemon) Run(ctx context.Context) error {
	g := d.Global
	timeout := g.Minutes("idle_minutes", 5)
	if timeout < 10*time.Second {
		timeout = 10 * time.Second
	}
	switchEvery := g.Minutes("switch_minutes", 15)

	var err error
	d.playlist, err = d.resolvePlaylist()
	if err != nil {
		return err
	}
	d.launch = &Launcher{Renderer: d.Renderer, Global: g}

	d.sess, err = session.Open()
	if err != nil {
		log.Printf("no session bus (%v); inhibitor and lock checks disabled", err)
	} else {
		defer d.sess.Close()
	}

	mon, err := idle.Open(ctx, g.String("idle_backend", "auto"), timeout)
	if err != nil {
		return err
	}
	defer mon.Close()

	desktop := "unknown"
	if d.sess != nil {
		desktop = string(d.sess.Desktop)
	}
	log.Printf("watching: desktop=%s backend=%s after=%s savers=%s",
		desktop, mon.Name(), timeout, strings.Join(d.playlist, ","))

	var (
		isIdle    bool
		running   *Group
		started   time.Time
		cooldown  time.Time // after a crash, wait before retrying
		tick      = time.NewTicker(5 * time.Second)
		groupDone <-chan int
	)
	defer tick.Stop()

	stop := func(why string) {
		if running != nil {
			log.Printf("stopping %s (%s)", running.Name, why)
			running.Stop()
			running, groupDone = nil, nil
		}
	}
	defer stop("shutdown")

	tryStart := func() {
		if running != nil || !isIdle || time.Now().Before(cooldown) {
			return
		}
		if d.sess != nil {
			if d.sess.Inhibited() {
				return // re-checked on the next tick
			}
			if d.sess.Locked() {
				return
			}
		}
		def := d.pick()
		grp, err := d.launch.Start(def)
		if err != nil {
			log.Printf("cannot start %s: %v", def.Name, err)
			cooldown = time.Now().Add(time.Minute)
			return
		}
		log.Printf("started %s", def.Name)
		running, groupDone, started = grp, grp.Done(), time.Now()
	}

	for {
		select {
		case <-ctx.Done():
			return nil

		case ev, ok := <-mon.Events():
			if !ok {
				return fmt.Errorf("idle backend %s stopped", mon.Name())
			}
			switch ev {
			case idle.Idle:
				isIdle = true
				tryStart()
			case idle.Active:
				isIdle = false
				stop("activity")
			}

		case code := <-groupDone:
			// One monitor's saver exited. 0 = the user woke it there.
			if code == 0 {
				isIdle = false // the backend's Active event will follow
				stop("woken")
			} else {
				log.Printf("%s exited with status %d; pausing a minute", running.Name, code)
				stop("crash")
				cooldown = time.Now().Add(time.Minute)
			}

		case <-tick.C:
			if running != nil && d.sess != nil && d.sess.Locked() {
				stop("screen locked")
				continue
			}
			if running != nil && switchEvery > 0 && time.Since(started) >= switchEvery && len(d.playlist) > 1 {
				stop("rotate")
				tryStart()
				continue
			}
			tryStart()
		}
	}
}

// resolvePlaylist turns savers = into catalog names, warning about unknowns.
func (d *Daemon) resolvePlaylist() ([]string, error) {
	want := d.Global.List("savers")
	if len(want) == 0 || (len(want) == 1 && strings.EqualFold(want[0], "all")) {
		want = d.Catalog.Names()
	}
	var out []string
	for _, n := range want {
		n = strings.ToLower(n)
		if _, ok := d.Catalog[n]; ok {
			out = append(out, n)
		} else {
			log.Printf("savers: %q not found (see `drift list`)", n)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no usable savers configured")
	}
	if strings.EqualFold(d.Global.String("order", "random"), "random") {
		rand.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	}
	return out, nil
}

// pick returns the next saver in the (possibly shuffled) playlist.
func (d *Daemon) pick() catalog.Def {
	name := d.playlist[d.next%len(d.playlist)]
	d.next++
	if d.next%len(d.playlist) == 0 && strings.EqualFold(d.Global.String("order", "random"), "random") {
		last := name
		rand.Shuffle(len(d.playlist), func(i, j int) { d.playlist[i], d.playlist[j] = d.playlist[j], d.playlist[i] })
		if len(d.playlist) > 1 && d.playlist[0] == last { // avoid an immediate repeat
			d.playlist[0], d.playlist[1] = d.playlist[1], d.playlist[0]
		}
	}
	return d.Catalog[name]
}
