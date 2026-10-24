package singbox

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

const glibcLoader = "/lib/ld-linux-aarch64.so.1"

// writeELF writes a minimal ELF64 executable with one PT_INTERP program
// header pointing at interp ("" — a static binary without PT_INTERP).
func writeELF(t *testing.T, path, interp string) {
	t.Helper()
	const ehsize, phentsize = 64, 56
	hdr := make([]byte, ehsize)
	copy(hdr, "\x7fELF")
	hdr[4], hdr[5], hdr[6] = 2, 1, 1 // ELFCLASS64, little endian, EV_CURRENT
	le := binary.LittleEndian
	le.PutUint16(hdr[16:], 2)   // ET_EXEC
	le.PutUint16(hdr[18:], 183) // EM_AARCH64
	le.PutUint32(hdr[20:], 1)
	le.PutUint16(hdr[52:], ehsize)
	le.PutUint16(hdr[54:], phentsize)
	out := hdr
	if interp != "" {
		le.PutUint64(hdr[32:], ehsize) // e_phoff
		le.PutUint16(hdr[56:], 1)      // e_phnum
		ph := make([]byte, phentsize)
		le.PutUint32(ph[0:], 3) // PT_INTERP
		le.PutUint32(ph[4:], 4) // PF_R
		le.PutUint64(ph[8:], ehsize+phentsize)
		le.PutUint64(ph[32:], uint64(len(interp)+1))
		le.PutUint64(ph[40:], uint64(len(interp)+1))
		le.PutUint64(ph[48:], 1)
		out = append(append(out, ph...), append([]byte(interp), 0)...)
	}
	if err := os.WriteFile(path, out, 0o755); err != nil {
		t.Fatal(err)
	}
}

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestAdaptInterpreterPatchesForEntware(t *testing.T) {
	root := t.TempDir()
	touch(t, filepath.Join(root, "opt/lib/ld-linux-aarch64.so.1"))
	bin := filepath.Join(root, "sing-box")
	writeELF(t, bin, glibcLoader)

	patched, err := adaptInterpreter(root, bin)
	if err != nil || !patched {
		t.Fatalf("patched=%v err=%v", patched, err)
	}
	if _, got := readInterp(bin); got != "/opt/ld-linux-aarch64.so.1" {
		t.Errorf("interpreter %q", got)
	}
	if l, err := os.Readlink(filepath.Join(root, "opt/ld-linux-aarch64.so.1")); err != nil || l != "lib/ld-linux-aarch64.so.1" {
		t.Errorf("symlink %q, %v", l, err)
	}
	// Idempotent: the interpreter is under /opt now.
	if patched, err := adaptInterpreter(root, bin); err != nil || patched {
		t.Errorf("second run: patched=%v err=%v", patched, err)
	}
}

func TestAdaptInterpreterNoop(t *testing.T) {
	root := t.TempDir()
	touch(t, filepath.Join(root, "opt/lib/ld-linux-aarch64.so.1"))
	cases := map[string]func(bin string){
		"system has the loader": func(bin string) { writeELF(t, bin, glibcLoader) },
		"static":                func(bin string) { writeELF(t, bin, "") },
		"entware":               func(bin string) { writeELF(t, bin, "/opt/lib/ld-linux-aarch64.so.1") },
		"not elf": func(bin string) {
			if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			bin := filepath.Join(t.TempDir(), "sing-box")
			setup(bin)
			before, _ := os.ReadFile(bin)
			if name == "system has the loader" {
				touch(t, filepath.Join(root, glibcLoader))
				defer os.Remove(filepath.Join(root, glibcLoader))
			}
			patched, err := adaptInterpreter(root, bin)
			if err != nil || patched {
				t.Fatalf("patched=%v err=%v", patched, err)
			}
			if after, _ := os.ReadFile(bin); string(after) != string(before) {
				t.Error("binary changed")
			}
		})
	}
}

func TestAdaptInterpreterNoEntwareLoader(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "sing-box")
	writeELF(t, bin, glibcLoader)
	if _, err := adaptInterpreter(root, bin); err == nil {
		t.Fatal("want an error without any loader")
	}
}
