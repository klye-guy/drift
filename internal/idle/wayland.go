package idle

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"
)

// A minimal Wayland client speaking just enough of the wire protocol to use
// ext-idle-notify-v1 (KWin, Sway, Hyprland, and others). It never creates a
// surface, so it is invisible and needs no toolkit or libwayland.
//
// Wire format: every message is
//
//	u32 object id | u32 (size<<16 | opcode) | arguments...
//
// with integers in host byte order and strings as u32 length (incl. NUL)
// followed by bytes padded to 4.

const (
	idDisplay      = 1
	idRegistry     = 2
	idSync         = 3
	idSeat         = 4
	idNotifier     = 5
	idNotification = 6
)

type wlConn struct {
	c   *net.UnixConn
	buf []byte
}

func dialWayland() (*wlConn, error) {
	name := os.Getenv("WAYLAND_DISPLAY")
	if name == "" {
		return nil, errors.New("WAYLAND_DISPLAY not set")
	}
	path := name
	if !filepath.IsAbs(path) {
		dir := os.Getenv("XDG_RUNTIME_DIR")
		if dir == "" {
			return nil, errors.New("XDG_RUNTIME_DIR not set")
		}
		path = filepath.Join(dir, name)
	}
	c, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, err
	}
	return &wlConn{c: c}, nil
}

// msg builds and sends a request.
type msg struct{ b bytes.Buffer }

func (m *msg) u32(v uint32) *msg { _ = binary.Write(&m.b, binary.NativeEndian, v); return m }
func (m *msg) str(s string) *msg {
	m.u32(uint32(len(s) + 1))
	m.b.WriteString(s)
	m.b.WriteByte(0)
	for m.b.Len()%4 != 0 {
		m.b.WriteByte(0)
	}
	return m
}

func (w *wlConn) send(obj uint32, opcode uint16, args *msg) error {
	var body []byte
	if args != nil {
		body = args.b.Bytes()
	}
	hdr := make([]byte, 8)
	binary.NativeEndian.PutUint32(hdr[0:], obj)
	binary.NativeEndian.PutUint32(hdr[4:], uint32(8+len(body))<<16|uint32(opcode))
	_, err := w.c.Write(append(hdr, body...))
	return err
}

type event struct {
	obj    uint32
	opcode uint16
	args   []byte
}

// next blocks until one complete event is available.
func (w *wlConn) next() (event, error) {
	for {
		if len(w.buf) >= 8 {
			obj := binary.NativeEndian.Uint32(w.buf[0:])
			so := binary.NativeEndian.Uint32(w.buf[4:])
			size := int(so >> 16)
			if size < 8 {
				return event{}, fmt.Errorf("wayland: bad message size %d", size)
			}
			if len(w.buf) >= size {
				ev := event{obj: obj, opcode: uint16(so & 0xffff), args: append([]byte(nil), w.buf[8:size]...)}
				w.buf = w.buf[size:]
				return ev, nil
			}
		}
		tmp := make([]byte, 4096)
		n, err := w.c.Read(tmp)
		if err != nil {
			return event{}, err
		}
		w.buf = append(w.buf, tmp[:n]...)
	}
}

// argument readers
func argU32(b []byte, off int) (uint32, int) {
	if off+4 > len(b) {
		return 0, len(b)
	}
	return binary.NativeEndian.Uint32(b[off:]), off + 4
}

func argStr(b []byte, off int) (string, int) {
	n, off := argU32(b, off)
	if n == 0 || off+int(n) > len(b) {
		return "", len(b)
	}
	s := string(b[off : off+int(n)-1])
	return s, off + (int(n)+3)&^3
}

func (w *wlConn) checkError(ev event) error {
	if ev.obj == idDisplay && ev.opcode == 0 { // wl_display.error
		obj, off := argU32(ev.args, 0)
		code, off := argU32(ev.args, off)
		text, _ := argStr(ev.args, off)
		return fmt.Errorf("wayland protocol error on object %d (code %d): %s", obj, code, text)
	}
	return nil
}

type waylandMonitor struct {
	w      *wlConn
	events chan Event
}

func (m *waylandMonitor) Name() string         { return "wayland" }
func (m *waylandMonitor) Events() <-chan Event { return m.events }
func (m *waylandMonitor) Close() error         { return m.w.c.Close() }

func openWayland(ctx context.Context, timeout time.Duration) (Monitor, error) {
	w, err := dialWayland()
	if err != nil {
		return nil, err
	}
	fail := func(err error) (Monitor, error) { w.c.Close(); return nil, err }

	// wl_display.get_registry(new_id) then wl_display.sync(new_id) so we know
	// when the initial burst of globals is complete.
	if err := w.send(idDisplay, 1, new(msg).u32(idRegistry)); err != nil {
		return fail(err)
	}
	if err := w.send(idDisplay, 0, new(msg).u32(idSync)); err != nil {
		return fail(err)
	}

	type global struct {
		name, version uint32
	}
	globals := map[string]global{}
	_ = w.c.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		ev, err := w.next()
		if err != nil {
			return fail(err)
		}
		if err := w.checkError(ev); err != nil {
			return fail(err)
		}
		if ev.obj == idRegistry && ev.opcode == 0 { // wl_registry.global
			name, off := argU32(ev.args, 0)
			iface, off := argStr(ev.args, off)
			ver, _ := argU32(ev.args, off)
			if _, seen := globals[iface]; !seen {
				globals[iface] = global{name, ver}
			}
		}
		if ev.obj == idSync && ev.opcode == 0 { // wl_callback.done
			break
		}
	}
	_ = w.c.SetReadDeadline(time.Time{})

	seat, ok1 := globals["wl_seat"]
	notifier, ok2 := globals["ext_idle_notifier_v1"]
	if !ok1 || !ok2 {
		return fail(errors.New("compositor does not offer ext_idle_notifier_v1"))
	}

	// wl_registry.bind(name, interface, version, new_id)
	bind := func(g global, iface string, ver, id uint32) error {
		return w.send(idRegistry, 0, new(msg).u32(g.name).str(iface).u32(ver).u32(id))
	}
	if err := bind(seat, "wl_seat", 1, idSeat); err != nil {
		return fail(err)
	}
	if err := bind(notifier, "ext_idle_notifier_v1", 1, idNotifier); err != nil {
		return fail(err)
	}
	// ext_idle_notifier_v1.get_idle_notification(new_id, timeout_ms, seat).
	// This variant honors idle inhibitors (video players, presentations).
	ms := uint32(min(timeout.Milliseconds(), int64(^uint32(0))))
	if err := w.send(idNotifier, 1, new(msg).u32(idNotification).u32(ms).u32(idSeat)); err != nil {
		return fail(err)
	}

	m := &waylandMonitor{w: w, events: make(chan Event, 4)}
	go func() {
		<-ctx.Done()
		w.c.Close()
	}()
	go func() {
		defer close(m.events)
		for {
			ev, err := w.next()
			if err != nil {
				if !errors.Is(err, net.ErrClosed) && !errors.Is(err, io.EOF) {
					fmt.Fprintln(os.Stderr, "drift: wayland:", err)
				}
				return
			}
			if err := w.checkError(ev); err != nil {
				fmt.Fprintln(os.Stderr, "drift:", err)
				return
			}
			if ev.obj == idNotification {
				switch ev.opcode {
				case 0: // idled
					m.events <- Idle
				case 1: // resumed
					m.events <- Active
				}
			}
			// wl_seat capabilities/name and wl_display.delete_id are ignored.
		}
	}()
	return m, nil
}
