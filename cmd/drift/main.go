// drift — classic moving screensavers for OLED burn-in protection on
// GNOME and KDE (Wayland or X11).
//
// This binary is the daemon and CLI. It has no graphics dependencies; all
// drawing happens in drift-render, which it launches per monitor.
package main

import (
	"context"
	_ "embed"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"

	"archavist.com/drift/internal/catalog"
	"archavist.com/drift/internal/conf"
	"archavist.com/drift/internal/daemon"
	"archavist.com/drift/internal/paths"
)

var version = "dev" // set with -ldflags "-X main.version=..."

//go:embed drift.conf.example
var exampleConf string

const usage = `drift — moving screensavers to protect OLED panels

Usage:
  drift daemon                    watch for idle and run savers (systemd runs this)
  drift list                      list available savers
  drift kinds                     list saver kinds, for writing .conf files
  drift preview <name>            show a saver in a window (Esc or q closes)
  drift test [name]               run a saver fullscreen on all monitors now
  drift snapshot <name> <out.png> [seconds]
                                  render a saver and save one frame as PNG
  drift init                      write a starter config to ~/.config/drift
  drift paths                     show where drift looks for config and savers
  drift version
`

func main() {
	log.SetFlags(0)
	log.SetPrefix("drift: ")
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	switch cmd, args := os.Args[1], os.Args[2:]; cmd {
	case "daemon":
		err = cmdDaemon()
	case "list", "ls":
		err = cmdList()
	case "kinds":
		err = cmdKinds()
	case "preview", "snapshot":
		err = execRenderer(cmd, args)
	case "test":
		err = cmdTest(args)
	case "init":
		err = cmdInit()
	case "paths":
		cmdPaths()
	case "version", "--version", "-v":
		fmt.Println("drift", version)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "drift: unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
	if err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

// kinds asks drift-render which saver kinds it was built with. That needs a
// display; without one, only the video kind is known.
func kinds() ([]catalog.Kind, error) {
	if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		return []catalog.Kind{catalog.Video}, fmt.Errorf("no DISPLAY or WAYLAND_DISPLAY in this environment")
	}
	k, err := catalog.QueryRenderer(paths.Renderer())
	if err != nil {
		return []catalog.Kind{catalog.Video}, err
	}
	return k, nil
}

// load reads drift.conf and builds the saver catalog.
func load(warn bool) (conf.File, catalog.Catalog, error) {
	f, err := paths.LoadConf()
	if err != nil {
		return nil, nil, err
	}
	ks, kerr := kinds()
	cat, warns := catalog.Load(f, ks, paths.SaverDirs()...)
	if kerr != nil {
		return f, cat, fmt.Errorf("drift-render needs a graphical session: %w", kerr)
	}
	if warn {
		for _, w := range warns {
			log.Print(w)
		}
	}
	return f, cat, nil
}

func lookup(cat catalog.Catalog, name string) (catalog.Def, error) {
	def, ok := cat[strings.ToLower(name)]
	if !ok {
		return def, fmt.Errorf("no saver named %q (try `drift list`)", name)
	}
	return def, nil
}

func cmdDaemon() error {
	f, cat, err := load(true)
	if err != nil {
		return err // systemd restarts us once the session is fully up
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	d := &daemon.Daemon{Global: f.Section(""), Catalog: cat, Renderer: paths.Renderer()}
	return d.Run(ctx)
}

func cmdList() error {
	_, cat, err := load(true)
	if err != nil {
		log.Print(err)
		log.Print("showing only savers that don't need drift-render")
	}
	home, _ := os.UserHomeDir()
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tKIND\tFROM\tDESCRIPTION")
	for _, n := range cat.Names() {
		d := cat[n]
		src := d.Source
		if home != "" {
			src = strings.Replace(src, home, "~", 1)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", d.Name, d.Kind.Name, src, d.Description())
	}
	return tw.Flush()
}

func cmdKinds() error {
	ks, err := kinds()
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "KIND\tNEEDS .conf\tDESCRIPTION")
	for _, k := range ks {
		need := ""
		if k.NeedsConfig {
			need = "yes"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", k.Name, need, k.Description)
	}
	tw.Flush()
	return err
}

// execRenderer hands preview/snapshot straight to drift-render.
func execRenderer(cmd string, args []string) error {
	r := paths.Renderer()
	if !filepath.IsAbs(r) {
		return fmt.Errorf("drift-render not found next to drift or in $PATH")
	}
	return syscall.Exec(r, append([]string{r, cmd}, args...), os.Environ())
}

func cmdTest(args []string) error {
	f, cat, err := load(true)
	if err != nil {
		return err
	}
	g := f.Section("")
	name := ""
	if len(args) > 0 {
		name = args[0]
	} else if list := g.List("savers"); len(list) > 0 && !strings.EqualFold(list[0], "all") {
		name = list[0]
	} else {
		name = cat.Names()[0]
	}
	def, err := lookup(cat, name)
	if err != nil {
		return err
	}
	l := &daemon.Launcher{Renderer: paths.Renderer(), Global: g}
	grp, err := l.Start(def)
	if err != nil {
		return err
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-grp.Done(): // waking any monitor ends the test everywhere
	case <-sig:
	}
	grp.Stop()
	return nil
}

func cmdInit() error {
	dir := paths.UserDir()
	if err := os.MkdirAll(filepath.Join(dir, "savers"), 0o755); err != nil {
		return err
	}
	p := filepath.Join(dir, "drift.conf")
	if _, err := os.Stat(p); err == nil {
		fmt.Println("exists, left alone:", p)
	} else if err := os.WriteFile(p, []byte(exampleConf), 0o644); err != nil {
		return err
	} else {
		fmt.Println("wrote", p)
	}
	fmt.Println("add your own savers as .conf files in", filepath.Join(dir, "savers"))
	fmt.Println("examples to copy from: /usr/share/doc/drift/examples/savers")
	return nil
}

func cmdPaths() {
	fmt.Println("config (later overrides earlier):")
	for _, p := range paths.ConfFiles() {
		fmt.Println("  " + p + exists(p))
	}
	fmt.Println("saver definitions (*.conf; later overrides earlier by name):")
	for _, p := range paths.SaverDirs() {
		fmt.Println("  " + p + exists(p))
	}
	fmt.Println("renderer:\n  " + paths.Renderer())
}

func exists(p string) string {
	if _, err := os.Stat(p); err != nil {
		return "  (missing)"
	}
	return ""
}
