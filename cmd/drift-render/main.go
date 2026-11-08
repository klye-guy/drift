// drift-render draws screensavers. The drift daemon launches one per monitor;
// you normally run it through `drift preview`, `drift snapshot` or
// `drift test` rather than directly.
//
// It is a separate binary because the graphics stack initializes (and
// requires a display) at program start; keeping it out of `drift` means the
// daemon stays small and the CLI works over SSH.
package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"archavist.com/drift/internal/catalog"
	"archavist.com/drift/internal/paths"
	"archavist.com/drift/internal/saver"
	_ "archavist.com/drift/internal/savers" // registers every built-in saver
	"archavist.com/drift/internal/screen"
)

const usage = `drift-render — draws drift screensavers

  drift-render run --saver <name> [--monitor <n>]   fullscreen, wake on input
  drift-render preview <name>                       windowed (Esc/q closes)
  drift-render snapshot <name> <out.png> [seconds]  save one frame
  drift-render monitors [-v]                        print monitor count (-v: details)
  drift-render kinds                                list compiled-in kinds
`

func main() {
	log.SetFlags(0)
	log.SetPrefix("drift-render: ")
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	switch cmd, args := os.Args[1], os.Args[2:]; cmd {
	case "run":
		err = cmdRun(args)
	case "preview":
		err = cmdPreview(args)
	case "snapshot":
		err = cmdSnapshot(args)
	case "monitors":
		if len(args) > 0 && (args[0] == "-v" || args[0] == "--verbose") {
			fmt.Print(screen.MonitorInfo())
		} else {
			fmt.Println(screen.Monitors())
		}
	case "kinds":
		fmt.Print(catalog.FormatKinds(saver.Kinds()))
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

type setup struct {
	opts screen.Options
	def  catalog.Def
}

func load(name string) (setup, error) {
	f, err := paths.LoadConf()
	if err != nil {
		return setup{}, err
	}
	cat, warns := catalog.Load(f, saver.Kinds(), paths.SaverDirs()...)
	def, ok := cat[strings.ToLower(name)]
	if !ok {
		for _, w := range warns {
			log.Print(w)
		}
		return setup{}, fmt.Errorf("no saver named %q (try `drift list`)", name)
	}
	if def.Kind.External {
		return setup{}, fmt.Errorf("%s is a %s saver; it runs in mpv, not drift-render (use `drift test %s`)",
			def.Name, def.Kind.Name, def.Name)
	}
	g := f.Section("")
	return setup{def: def, opts: screen.Options{
		Brightness: g.Float("brightness", 1),
		MaxFPS:     g.Int("max_fps", 0),
	}}, nil
}

func cmdRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	name := fs.String("saver", "", "saver name")
	mon := fs.Int("monitor", 0, "monitor index (0 = primary)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	s, err := load(*name)
	if err != nil {
		return err
	}
	s.opts.Monitor = *mon
	return screen.Run(s.def, s.opts)
}

func cmdPreview(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: drift-render preview <name>")
	}
	s, err := load(args[0])
	if err != nil {
		return err
	}
	s.opts.Preview = true
	return screen.Run(s.def, s.opts)
}

func cmdSnapshot(args []string) error {
	if len(args) < 2 || len(args) > 3 {
		return errors.New("usage: drift-render snapshot <name> <out.png> [seconds]")
	}
	s, err := load(args[0])
	if err != nil {
		return err
	}
	s.opts.SnapshotPath = args[1]
	s.opts.SnapshotAfter = 6
	if len(args) == 3 {
		if _, err := fmt.Sscanf(args[2], "%g", &s.opts.SnapshotAfter); err != nil {
			return fmt.Errorf("seconds: %w", err)
		}
	}
	return screen.Run(s.def, s.opts)
}
