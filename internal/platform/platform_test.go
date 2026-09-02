package platform

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestParseLevel(t *testing.T) {
	for in, want := range map[string]Level{"strong": LevelStrong, "USER": LevelUser, " user ": LevelUser} {
		got, err := ParseLevel(in)
		if err != nil || got != want {
			t.Errorf("ParseLevel(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	if _, err := ParseLevel("medium"); err == nil {
		t.Error("expected error for unknown level")
	}
}

func TestStateLockedAt(t *testing.T) {
	cases := []struct {
		st           State
		user, strong bool
		lvl          Level
	}{
		{State{Writable: true}, false, false, 0},
		{State{Writable: false}, true, false, LevelUser},
		{State{Writable: true, UserImmutable: true}, true, false, LevelUser},
		{State{Writable: true, Immutable: true}, true, true, LevelStrong},
	}
	for _, c := range cases {
		if c.st.LockedAt(LevelUser) != c.user || c.st.LockedAt(LevelStrong) != c.strong || c.st.Level() != c.lvl {
			t.Errorf("%+v: user=%v strong=%v level=%v", c.st, c.st.LockedAt(LevelUser), c.st.LockedAt(LevelStrong), c.st.Level())
		}
	}
}

func TestMapErr(t *testing.T) {
	if !errors.Is(mapErr(syscall.EPERM), ErrPermission) {
		t.Error("EPERM should map to ErrPermission")
	}
	if !errors.Is(mapErr(&os.PathError{Op: "chmod", Path: "x", Err: syscall.EACCES}), ErrPermission) {
		t.Error("wrapped EACCES should map to ErrPermission")
	}
	if !errors.Is(mapErr(syscall.EPERM), syscall.EPERM) {
		t.Error("mapped error should still match the original errno")
	}
	if mapErr(nil) != nil {
		t.Error("nil should stay nil")
	}
}

// TestUserLevelRoundTrip exercises the real Locker at LevelUser, which needs
// no privileges on any platform.
func TestUserLevelRoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(p, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	l := New()
	t.Cleanup(func() { _ = l.Unlock(p) })

	st, err := l.Status(p)
	if err != nil || !st.Writable || st.Level() != 0 {
		t.Fatalf("initial status: %+v err=%v", st, err)
	}
	if err := l.Lock(p, LevelUser); err != nil {
		t.Fatalf("lock: %v", err)
	}
	st, _ = l.Status(p)
	if !st.LockedAt(LevelUser) || st.Level() != LevelUser {
		t.Fatalf("after lock: %+v", st)
	}
	if !IsRoot() && PathWritableTest(p) {
		t.Fatal("file still writable after user lock")
	}
	// idempotent
	if err := l.Lock(p, LevelUser); err != nil {
		t.Fatalf("second lock: %v", err)
	}
	if err := l.Unlock(p); err != nil {
		t.Fatalf("unlock: %v", err)
	}
	st, _ = l.Status(p)
	if st.Level() != 0 || !st.Writable {
		t.Fatalf("after unlock: %+v", st)
	}
	if !PathWritableTest(p) {
		t.Fatal("file not writable after unlock")
	}
	if err := l.Unlock(p); err != nil {
		t.Fatalf("second unlock: %v", err)
	}
}

func TestSymlinkRejected(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "t.md")
	link := filepath.Join(dir, "l.md")
	os.WriteFile(target, nil, 0o644)
	if err := os.Symlink(target, link); err != nil {
		t.Skip("symlinks unsupported")
	}
	l := New()
	st, err := l.Status(link)
	if err != nil || !st.IsSymlink {
		t.Fatalf("status: %+v %v", st, err)
	}
	if err := l.Lock(link, LevelUser); !errors.Is(err, ErrSymlink) {
		t.Fatalf("lock symlink: %v", err)
	}
	if err := l.Unlock(link); !errors.Is(err, ErrSymlink) {
		t.Fatalf("unlock symlink: %v", err)
	}
}

// TestStrongLevelRoundTrip only runs with the required privilege; it is the
// real proof that the immutable flag works on this host.
func TestStrongLevelRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("-short")
	}
	if ok, why := StrongPrivilege(); !ok {
		t.Skip("strong privilege unavailable: " + why)
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(p, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	l := New()
	if ok, why := l.Supports(p, LevelStrong); !ok {
		t.Skip("fs unsupported: " + why)
	}
	t.Cleanup(func() { _ = l.Unlock(p) })

	if err := l.Lock(p, LevelStrong); err != nil {
		t.Fatalf("lock: %v", err)
	}
	st, err := l.Status(p)
	if err != nil || !st.Immutable || st.Level() != LevelStrong {
		t.Fatalf("after lock: %+v err=%v", st, err)
	}
	if PathWritableTest(p) {
		t.Fatal("root could still open for write after strong lock")
	}
	if err := os.Remove(p); err == nil {
		t.Fatal("root could unlink an immutable file")
	}
	if err := os.Rename(p, p+".x"); err == nil {
		t.Fatal("root could rename an immutable file")
	}
	if err := l.Lock(p, LevelStrong); err != nil {
		t.Fatalf("idempotent lock: %v", err)
	}
	if err := l.Unlock(p); err != nil {
		t.Fatalf("unlock: %v", err)
	}
	st, _ = l.Status(p)
	if st.Immutable || !st.Writable {
		t.Fatalf("after unlock: %+v", st)
	}
	if !PathWritableTest(p) {
		t.Fatal("not writable after unlock")
	}
}

// TestUserGuardRoundTrip proves the property the whole guard rests on: an
// append-only directory still accepts new entries, refuses to give up the ones
// it has, and refuses to be renamed itself — which is what closes the
// "rename the parent and recreate the path" bypass.
//
// It runs at LevelUser (uappnd on BSD/macOS), so it needs no privileges there.
// Linux has no user-clearable append flag, so the strong variant covers it.
func TestUserGuardRoundTrip(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "docs")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	kept := filepath.Join(sub, "kept.md")
	if err := os.WriteFile(kept, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	l := New()
	if err := l.Guard(sub, LevelUser); err != nil {
		if errors.Is(err, ErrUnsupportedFS) || errors.Is(err, ErrPermission) {
			t.Skip("user-level guard unavailable here: " + err.Error())
		}
		t.Fatalf("guard: %v", err)
	}
	t.Cleanup(func() { _ = l.Unguard(sub) })

	st, err := l.Status(sub)
	if err != nil || !st.Append {
		t.Fatalf("after guard: %+v err=%v", st, err)
	}
	if IsRoot() {
		t.Skip("root bypasses the user-level flag; see TestStrongGuardRoundTrip")
	}
	if err := os.WriteFile(filepath.Join(sub, "new.md"), []byte("y"), 0o644); err != nil {
		t.Errorf("an append-only directory must still accept new files: %v", err)
	}
	if err := os.Remove(kept); err == nil {
		t.Error("append-only directory allowed a delete")
	}
	if err := os.Rename(kept, kept+".x"); err == nil {
		t.Error("append-only directory allowed a rename of its entry")
	}
	if err := os.Rename(sub, sub+".locked"); err == nil {
		t.Error("append-only directory allowed itself to be renamed — the bypass is open")
		_ = os.Rename(sub+".locked", sub)
	}
	// idempotent, and unguard puts everything back
	if err := l.Guard(sub, LevelUser); err != nil {
		t.Errorf("second guard: %v", err)
	}
	if err := l.Unguard(sub); err != nil {
		t.Fatalf("unguard: %v", err)
	}
	if st, _ := l.Status(sub); st.Append {
		t.Fatal("still append-only after unguard")
	}
	if err := os.Remove(kept); err != nil {
		t.Errorf("delete should work again after unguard: %v", err)
	}
	if err := l.Unguard(sub); err != nil {
		t.Errorf("second unguard: %v", err)
	}
}

// TestStrongGuardRoundTrip is the privileged proof: sappnd / chattr +a, the
// flags an agent running as the user cannot clear.
func TestStrongGuardRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("-short")
	}
	if ok, why := StrongPrivilege(); !ok {
		t.Skip("strong privilege unavailable: " + why)
	}
	dir := t.TempDir()
	sub := filepath.Join(dir, "docs")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	l := New()
	if ok, why := l.Supports(sub, LevelStrong); !ok {
		t.Skip("fs unsupported: " + why)
	}
	locked := filepath.Join(sub, "SSOT.md")
	if err := os.WriteFile(locked, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := l.Lock(locked, LevelStrong); err != nil {
		t.Fatalf("lock: %v", err)
	}
	t.Cleanup(func() { _ = l.Unguard(sub); _ = l.Unlock(locked) })
	if err := l.Guard(sub, LevelStrong); err != nil {
		t.Fatalf("guard: %v", err)
	}
	st, err := l.Status(sub)
	if err != nil || !st.Append {
		t.Fatalf("after guard: %+v err=%v", st, err)
	}
	// Even as root: this is the case that matters, because the agent's own
	// `sudo mv` is what the user is defending against.
	if err := os.Rename(sub, sub+".locked"); err == nil {
		t.Error("root renamed an append-only directory — the bypass is open")
		_ = os.Rename(sub+".locked", sub)
	}
	if err := os.Remove(locked); err == nil {
		t.Error("root removed a file from an append-only directory")
	}
	if err := os.WriteFile(filepath.Join(sub, "new.md"), []byte("y"), 0o644); err != nil {
		t.Errorf("new files must still be allowed: %v", err)
	}
	// The lock itself survives the guard being applied and released.
	if err := l.Unguard(sub); err != nil {
		t.Fatalf("unguard: %v", err)
	}
	if st, _ := l.Status(locked); !st.Immutable {
		t.Error("unguard cleared the file's immutable flag")
	}
	if st, _ := l.Status(sub); st.Append {
		t.Fatal("still append-only after unguard")
	}
}
