# drift

Moving screensavers that protect OLED panels from burn-in, in the spirit of
the classic XP-era savers. Works with **GNOME and KDE Plasma** on **Wayland
or X11**, and installs on Debian/Ubuntu and RHEL/Fedora families.

Built in: `mystify` (bouncing polygons with echo trails), `pipes` (shaded 3D
pipes), `starfield`, `ribbons` (Bézier curves), `rain` (streams of falling
glyphs), `marquee` (scrolling text).
You can also add your own artwork as a moving **sprite**, or loop your own
**video**, with a 5-line config file and no rebuild.

drift is a screensaver only. Wallpapers are deliberately out of scope.

---

## Install

Download the package for your system from the
[latest release](https://github.com/klye-guy/drift/releases/latest):

```sh
# Debian / Ubuntu / Kubuntu
sudo apt install ./drift_*_amd64.deb

# RHEL / Rocky / Fedora
sudo dnf install ./drift-*.x86_64.rpm
```

ARM64 packages and plain tarballs are on the same page. Then each user turns
drift on:

```sh
systemctl --user enable --now drift.service
```

Runtime dependencies are only the GL/X11 client libraries every desktop
already has; `mpv` is needed only for video savers.

### Building from source

```sh
make deps        # apt or dnf: Go toolchain, gcc, GL and X11 headers
make build       # bin/drift and bin/drift-render
make install     # /usr/local/bin + systemd user unit, no packaging
make deb rpm     # packages via nfpm (https://nfpm.goreleaser.com)
make deb-native  # .deb with plain dpkg-deb, no nfpm needed
```

### Releasing

Bump the version in `VERSION` and push to `main`:

```sh
echo 0.1.5 > VERSION
git commit -am "Release 0.1.5" && git push
```

GitHub Actions then builds for x86_64 and arm64 and test-installs the
packages on Debian, Ubuntu, Rocky and Fedora. If those pass, it creates the
`v0.1.5` tag and publishes the release. Pushing a matching `v0.1.5` tag
yourself, or using "Run workflow" on the Release action, does the same.
Versions that already have a release are skipped.

### One desktop setting matters

Set your desktop's own **screen-off / blank timer longer** than drift's
`idle_minutes` (default 5). drift then runs first, and the panel still powers
off later.

- **GNOME:** Settings → Power → Screen Blank
- **KDE:** System Settings → Power Management → Turn off screen

---

## Use

```text
drift list                      available savers
drift preview pipes             try one in a window (Esc or q closes)
drift test crystal              fullscreen on every monitor, right now
drift snapshot pipes out.png 6  save the frame shown at 6 s
drift init                      write ~/.config/drift/drift.conf
drift paths                     where drift looks for things
journalctl --user -u drift -f   daemon log
```

## Configure

`~/.config/drift/drift.conf` is layered over `/etc/drift/drift.conf`. After
editing, run `systemctl --user restart drift`.

```ini
idle_minutes   = 5          # start after this much idle time
savers         = all        # or: mystify, pipes, crystal
order          = random     # random | sequence
switch_minutes = 15         # rotate savers while idle (0 = never)
monitors       = all        # all | primary | 0,2
brightness     = 0.85       # dim everything drift draws
max_fps        = 0          # 0 = refresh rate; 30 saves power
idle_backend   = auto       # auto | mutter | wayland | x11

[mystify]                   # per-saver defaults
polygons = 3
```

Each saver's options are documented at the top of its source file in
`internal/savers/`.

---

## Adding savers

There are three ways, from easiest to most flexible. Saver definitions are
`.conf` files in `~/.config/drift/savers/` (per user), `/etc/drift/savers/`,
or `/usr/share/drift/savers/`. The file name becomes the saver name. Ready-made
examples ship in `/usr/share/doc/drift/examples/savers/`.

### 1. Your own image or animation: no code

```ini
# ~/.config/drift/savers/mychar.conf   →  saver "mychar"
kind   = sprite
image  = mychar.png      # next to this file, or an absolute or ~/ path
motion = wander          # bounce | drift | float | wander
height = 0.45            # fraction of screen height
flip   = true            # mirror to face the direction of travel
```

`image` can be:

- a PNG or JPEG (use a transparent PNG so only the character moves);
- an animated GIF;
- a folder of numbered PNG frames, for example an animation exported from
  Clip Studio. Set `fps = 12`.

Other options: `speed`, `count`, `spin`, `sway`, `trail`, `gif_speed`. See
`internal/savers/sprite.go` for details.

Check it with `drift preview mychar`, then add it to `savers =` (or leave
`savers = all`).

### 2. A looping video: no code

```ini
# ~/.config/drift/savers/aquarium.conf
kind    = video
file    = ~/Videos/aquarium.mp4   # or a folder to shuffle through
audio   = false
```

### 3. A variant of a built-in: no code

```ini
# ~/.config/drift/savers/mystify-busy.conf
kind     = mystify
polygons = 4
echoes   = 10
```

### 4. A new effect in Go: one file

Create `internal/savers/<name>.go`:

```go
package savers

import (
    "github.com/hajimehoshi/ebiten/v2"
    "archavist.com/drift/internal/saver"
)

func init() {
    saver.Register(saver.Kind{
        Name:        "aquarium",
        Description: "Fish swimming back and forth",
        New:         func() saver.Saver { return &aquarium{} },
    })
}

type aquarium struct{ /* state */ }

func (a *aquarium) Setup(env saver.Env) error { /* env.W, env.H, env.Opts, env.Rand */ return nil }
func (a *aquarium) Update(dt float64)         { /* advance by dt seconds */ }
func (a *aquarium) Draw(dst *ebiten.Image)    { /* draw the frame (screen starts black) */ }
```

Rebuild, and it appears in `drift list`, the playlist, and as an
`[aquarium]` config section. Nothing else changes.

Helpers in `util.go`:

- `hsv()` for colors;
- `bouncer` for points that bounce off the edges;
- `fader` for a fading-trail canvas that fades all the way to true black.

`starfield.go` is the smallest example to copy.

**Burn-in rule of thumb for new savers:** nothing should stay in one place.
Drift the whole composition, change position or color between passes, and
keep the background black.

---

## How it works

```text
drift daemon ──(idle backend)──► idle?  ──► inhibited? locked? ──► launch
     │                                                               │
     │   drift-render run --saver X --monitor N   (one per monitor) ◄┘
     │   or  mpv --fs …                            (video savers)
     └── any input / lock / switch_minutes ──► stop all ──► watch again
```

- **`drift`** is the daemon and CLI. It is pure Go with no cgo and no
  graphics stack, so it stays small, starts anywhere, and works over SSH.
- **`drift-render`** draws savers with
  [Ebitengine](https://ebitengine.org) through XWayland or X11. It is a
  separate binary because the graphics library needs a display the moment it
  loads.

**Idle detection.** The `auto` backend picks one of these:

| Session | Backend | Mechanism |
|---|---|---|
| GNOME (Wayland or X11) | `mutter` | `org.gnome.Mutter.IdleMonitor` over D-Bus |
| KDE Plasma Wayland (also Sway, Hyprland) | `wayland` | `ext-idle-notify-v1`, a small pure-Go Wayland client |
| X11 (e.g. Plasma X11 on Debian 12) | `x11` | MIT-SCREEN-SAVER extension |

**Respecting other apps.** drift won't start while something inhibits the
screensaver, such as a video player, a game, or a presentation:

- GNOME: `org.gnome.SessionManager.IsInhibited`
- KDE: PowerDevil's `PolicyAgent.HasInhibition(ChangeScreenSettings)`, the
  same check KDE's own locker makes

It also doesn't run, and stops, while the screen is locked, because the lock
screen covers normal windows anyway.

**Waking.** Any key, click, scroll, or mouse movement of more than about 12 px
wakes it. Small jitters and the first 0.75 s after start are ignored.

---

## Known limits

- **Keyboard focus:** drift-render takes keyboard focus when it starts, so a
  wake-up key normally goes to the saver. If a desktop refuses that, drift
  still wakes on the key, but the key may also reach the window underneath.
- **The lock screen itself is static.** drift can't draw over it. Pair drift
  with a short screen-off timer after locking.
- **Multi-monitor placement:** window managers (KWin especially) may open
  every new window on the active screen. drift-render sidesteps this the way
  xscreensaver does: it takes its window away from the window manager and
  places it directly over its monitor using RandR. If that isn't possible,
  the journal says `can't pin window …; using fullscreen`.
  `drift-render monitors -v` lists the monitors drift can see.
- **Monitor numbering for video savers** follows mpv's `--fs-screen`, which
  can differ from drift-render's order on some multi-monitor Wayland setups.
  Use `monitors = primary` for video if it picks the wrong one.
- **Panels and docks are the biggest burn-in risk on a desktop OLED**, and a
  screensaver only helps while you're away. Auto-hide them, or use the panel's
  pixel-shift feature.

## Layout

```text
cmd/drift/            daemon + CLI (no graphics deps)
cmd/drift-render/     renderer entry point
internal/savers/      one file per built-in saver  ← add new ones here
internal/saver/       Saver interface + registry
internal/catalog/     saver definitions and .conf discovery (no graphics deps)
internal/screen/      fullscreen/preview window host, wake-on-input
internal/idle/        mutter / wayland / x11 idle backends
internal/session/     inhibitor and lock-screen checks (GNOME, KDE)
internal/daemon/      state machine, process launch, mpv
packaging/            systemd user unit, install scripts, dpkg-deb builder
examples/savers/      sample .conf files (sprite, video, variant)
```
