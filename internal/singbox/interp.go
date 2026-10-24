package singbox

import (
	"debug/elf"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Upstream linux builds of sing-box are dynamically linked against glibc
// (interpreter /lib/ld-linux-aarch64.so.1). KeeneticOS 5 firmware is
// musl-based and has no such loader, so the binary fails to start with
// "not found". Entware ships the same glibc loader under /opt/lib, and
// "/lib/" and "/opt/" have the same length: the interpreter path is
// rewritten in place to /opt/<loader>, a symlink to /opt/lib/<loader>. The
// process keeps its name, and every way of starting it keeps working.

// AdaptInterpreter makes bin runnable on firmware without its glibc loader
// and reports whether it patched the binary. It is a no-op for static
// binaries, when the system has the loader, and for Entware builds (their
// interpreter is already under /opt).
func AdaptInterpreter(bin string) (bool, error) {
	return adaptInterpreter("/", bin)
}

func adaptInterpreter(root, bin string) (bool, error) {
	off, interp := readInterp(bin)
	name, ok := strings.CutPrefix(interp, "/lib/")
	if !ok || name == "" || strings.Contains(name, "/") {
		return false, nil
	}
	if _, err := os.Stat(filepath.Join(root, "lib", name)); err == nil {
		return false, nil
	}
	if _, err := os.Stat(filepath.Join(root, "opt", "lib", name)); err != nil {
		return false, fmt.Errorf("%s needs %s: the firmware has none and Entware has no /opt/lib/%s", bin, interp, name)
	}
	if err := ensureSymlink(filepath.Join(root, "opt", name), "lib/"+name); err != nil {
		return false, err
	}
	// In place: the binary cannot be running (it has no loader), and a copy
	// of ~80 MB may not fit on the router's internal storage.
	f, err := os.OpenFile(bin, os.O_WRONLY, 0)
	if err != nil {
		return false, err
	}
	if _, err := f.WriteAt([]byte("/opt/"+name), off); err != nil {
		f.Close()
		return false, err
	}
	return true, f.Close()
}

// readInterp returns the file offset and value of the ELF PT_INTERP path,
// or "" for anything that is not a dynamically linked ELF.
func readInterp(bin string) (int64, string) {
	f, err := elf.Open(bin)
	if err != nil {
		return 0, ""
	}
	defer f.Close()
	for _, p := range f.Progs {
		if p.Type != elf.PT_INTERP {
			continue
		}
		buf := make([]byte, p.Filesz)
		if _, err := p.ReadAt(buf, 0); err != nil {
			return 0, ""
		}
		return int64(p.Off), strings.TrimRight(string(buf), "\x00")
	}
	return 0, ""
}

func ensureSymlink(link, target string) error {
	if cur, err := os.Readlink(link); err == nil && cur == target {
		return nil
	}
	_ = os.Remove(link)
	return os.Symlink(target, link)
}
