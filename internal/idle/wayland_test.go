package idle

import (
	"context"
	"encoding/binary"
	"net"
	"path/filepath"
	"testing"
	"time"
)

// fakeCompositor speaks just enough Wayland to exercise openWayland: it
// advertises globals, answers sync, checks the requests the client sends,
// then emits idled/resumed on the notification object.
func fakeCompositor(t *testing.T, ln *net.UnixListener, gotTimeout chan<- uint32) {
	c, err := ln.AcceptUnix()
	if err != nil {
		t.Error(err)
		return
	}
	defer c.Close()
	w := &wlConn{c: c}

	sendEv := func(obj uint32, op uint16, m *msg) {
		if err := w.send(obj, op, m); err != nil {
			t.Error(err)
		}
	}

	// get_registry(2), sync(3)
	ev, _ := w.next()
	if ev.obj != idDisplay || ev.opcode != 1 {
		t.Errorf("want get_registry, got obj %d op %d", ev.obj, ev.opcode)
	}
	ev, _ = w.next()
	if ev.obj != idDisplay || ev.opcode != 0 {
		t.Errorf("want sync, got obj %d op %d", ev.obj, ev.opcode)
	}
	cb, _ := argU32(ev.args, 0)

	sendEv(idRegistry, 0, new(msg).u32(1).str("wl_compositor").u32(6))
	sendEv(idRegistry, 0, new(msg).u32(7).str("wl_seat").u32(9))
	sendEv(idRegistry, 0, new(msg).u32(12).str("ext_idle_notifier_v1").u32(2))
	sendEv(cb, 0, new(msg).u32(0)) // wl_callback.done

	// bind seat, bind notifier, get_idle_notification
	for i, want := range []struct {
		name  uint32
		iface string
		id    uint32
	}{{7, "wl_seat", idSeat}, {12, "ext_idle_notifier_v1", idNotifier}} {
		ev, _ = w.next()
		name, off := argU32(ev.args, 0)
		iface, off := argStr(ev.args, off)
		_, off = argU32(ev.args, off)
		id, _ := argU32(ev.args, off)
		if ev.obj != idRegistry || name != want.name || iface != want.iface || id != want.id {
			t.Errorf("bind %d: got name=%d iface=%q id=%d", i, name, iface, id)
		}
	}
	ev, _ = w.next()
	if ev.obj != idNotifier || ev.opcode != 1 {
		t.Errorf("want get_idle_notification, got obj %d op %d", ev.obj, ev.opcode)
	}
	id, off := argU32(ev.args, 0)
	ms, off := argU32(ev.args, off)
	seat, _ := argU32(ev.args, off)
	if id != idNotification || seat != idSeat {
		t.Errorf("notification id=%d seat=%d", id, seat)
	}
	gotTimeout <- ms

	sendEv(idNotification, 0, nil) // idled
	sendEv(idNotification, 1, nil) // resumed
	time.Sleep(200 * time.Millisecond)
}

func TestWaylandIdleNotify(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "wayland-test")
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	t.Setenv("XDG_RUNTIME_DIR", dir)
	t.Setenv("WAYLAND_DISPLAY", "wayland-test")

	gotTimeout := make(chan uint32, 1)
	go fakeCompositor(t, ln, gotTimeout)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	m, err := openWayland(ctx, 90*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	if ms := <-gotTimeout; ms != 90000 {
		t.Errorf("timeout sent = %d ms, want 90000", ms)
	}
	for _, want := range []Event{Idle, Active} {
		select {
		case got := <-m.Events():
			if got != want {
				t.Errorf("event = %v, want %v", got, want)
			}
		case <-ctx.Done():
			t.Fatalf("timed out waiting for %v", want)
		}
	}
}

func TestStringPadding(t *testing.T) {
	m := new(msg).str("wl_seat") // 7 chars + NUL = 8, already aligned
	if m.b.Len() != 4+8 {
		t.Errorf("len = %d", m.b.Len())
	}
	m = new(msg).str("ext_idle_notifier_v1") // 20 + NUL = 21 → padded to 24
	if m.b.Len() != 4+24 {
		t.Errorf("len = %d", m.b.Len())
	}
	if n := binary.NativeEndian.Uint32(m.b.Bytes()); n != 21 {
		t.Errorf("length prefix = %d, want 21 (includes NUL)", n)
	}
}
