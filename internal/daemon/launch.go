package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"archavist.com/drift/internal/catalog"
	"archavist.com/drift/internal/conf"
)

// Launcher starts one saver across monitors as child processes.
type Launcher struct {
	Renderer string       // path to the drift-render binary
	Global   conf.Section // the [""] section of drift.conf
}

// Group is a running saver: one process per monitor.
type Group struct {
	Name  string
	procs []*proc
	done  chan int // receives each process's exit code as it exits
}

type proc struct {
	cmd    *exec.Cmd
	exited chan struct{} // closed once Wait returns
}

// Done delivers each child's exit code; exit code 0 means "user woke it".
func (g *Group) Done() <-chan int { return g.done }

// Stop terminates every process in the group (SIGTERM, then SIGKILL) and
// waits for them to exit.
func (g *Group) Stop() {
	for _, p := range g.procs {
		_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGTERM)
	}
	deadline := time.After(2 * time.Second)
	for _, p := range g.procs {
		select {
		case <-p.exited:
		case <-deadline:
			_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
			<-p.exited
		}
	}
}

// monitorList resolves the monitors = option against the actual count.
func (l *Launcher) monitorList() ([]int, error) {
	out, err := exec.Command(l.Renderer, "monitors").Output()
	if err != nil {
		return nil, fmt.Errorf("counting monitors: %w", err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || n < 1 {
		n = 1
	}
	spec := strings.ToLower(l.Global.String("monitors", "all"))
	switch spec {
	case "all":
		ids := make([]int, n)
		for i := range ids {
			ids[i] = i
		}
		return ids, nil
	case "primary":
		return []int{0}, nil
	}
	var ids []int
	for _, p := range l.Global.List("monitors") {
		if i, err := strconv.Atoi(p); err == nil && i >= 0 && i < n {
			ids = append(ids, i)
		}
	}
	if len(ids) == 0 {
		ids = []int{0}
	}
	return ids, nil
}

// Start launches def on the configured monitors.
func (l *Launcher) Start(def catalog.Def) (*Group, error) {
	mons, err := l.monitorList()
	if err != nil {
		return nil, err
	}
	g := &Group{Name: def.Name, done: make(chan int, len(mons))}
	for _, m := range mons {
		var cmd *exec.Cmd
		if def.Kind.External {
			cmd, err = l.mpvCommand(def, m)
			if err != nil {
				g.Stop()
				return nil, err
			}
		} else {
			cmd = exec.Command(l.Renderer, "run", "--saver", def.Name, "--monitor", strconv.Itoa(m))
		}
		cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGTERM}
		if err := cmd.Start(); err != nil {
			g.Stop()
			return nil, fmt.Errorf("starting %s on monitor %d: %w", def.Name, m, err)
		}
		p := &proc{cmd: cmd, exited: make(chan struct{})}
		g.procs = append(g.procs, p)
		go func() {
			_ = cmd.Wait()
			close(p.exited)
			g.done <- cmd.ProcessState.ExitCode()
		}()
	}
	return g, nil
}

// mpvInput makes mpv quit on any key or click. Pointer motion is caught by
// the daemon's idle monitor instead, which avoids spurious wakes when the
// window maps under a resting cursor.
const mpvInput = `ANY_UNICODE quit
ESC quit
ENTER quit
SPACE quit
BS quit
TAB quit
UP quit
DOWN quit
LEFT quit
RIGHT quit
MBTN_LEFT quit
MBTN_RIGHT quit
MBTN_MID quit
WHEEL_UP quit
WHEEL_DOWN quit
`

func (l *Launcher) mpvCommand(def catalog.Def, monitor int) (*exec.Cmd, error) {
	file := def.Opts.String("file", "")
	if file == "" {
		return nil, fmt.Errorf("%s: video saver needs file = <video or folder>", def.Source)
	}
	file = def.Path(file)
	st, err := os.Stat(file)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", def.Source, err)
	}

	inputConf := filepath.Join(os.TempDir(), fmt.Sprintf("drift-mpv-input-%d.conf", os.Getuid()))
	if err := os.WriteFile(inputConf, []byte(mpvInput), 0o600); err != nil {
		return nil, err
	}

	args := []string{
		"--fs", "--fs-screen=" + strconv.Itoa(monitor), "--screen=" + strconv.Itoa(monitor),
		"--ontop", "--title=drift — " + def.Name,
		"--no-osc", "--osd-level=0", "--no-input-cursor", "--cursor-autohide=always",
		"--input-default-bindings=no", "--input-conf=" + inputConf,
		"--stop-screensaver=no", // mpv must not count as user activity or an inhibitor
		"--hwdec=auto-safe", "--really-quiet", "--keep-open=no",
	}
	if !def.Opts.Bool("audio", false) {
		args = append(args, "--no-audio")
	}
	if st.IsDir() {
		args = append(args, "--loop-playlist=inf")
		if def.Opts.Bool("shuffle", true) {
			args = append(args, "--shuffle")
		}
	} else {
		args = append(args, "--loop-file=inf")
	}
	args = append(args, strings.Fields(def.Opts.String("mpv_args", ""))...)
	args = append(args, "--", file)

	mpv := l.Global.String("mpv", "mpv")
	if _, err := exec.LookPath(mpv); err != nil {
		return nil, fmt.Errorf("video savers need mpv installed (%s not found)", mpv)
	}
	return exec.Command(mpv, args...), nil
}
