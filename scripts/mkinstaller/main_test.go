package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fakeEntware writes a minimal Entware-like installer archive.
func fakeEntware(t *testing.T, dir string) string {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	add := func(h *tar.Header, body string) {
		h.Size = int64(len(body))
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(tw, body); err != nil {
			t.Fatal(err)
		}
	}
	add(&tar.Header{Typeflag: tar.TypeDir, Name: "bin", Mode: 0o755}, "")
	add(&tar.Header{Typeflag: tar.TypeReg, Name: "bin/busybox", Mode: 0o755, Uid: 1000}, "bb")
	add(&tar.Header{Typeflag: tar.TypeSymlink, Name: "bin/sh", Linkname: "busybox", Mode: 0o777}, "")
	add(&tar.Header{Typeflag: tar.TypeReg, Name: "etc/opkg.conf", Mode: 0o644}, "src/gz entware x\n")
	add(&tar.Header{Typeflag: tar.TypeReg, Name: "etc/dropbear/dropbear_rsa_host_key", Mode: 0o644}, "")
	add(&tar.Header{Typeflag: tar.TypeReg, Name: "etc/init.d/doinstall", Mode: 0o755}, "#!/bin/sh\n")
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "entware.tar.gz")
	if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRun(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "ui")
	if err := os.WriteFile(bin, []byte("ELF"), 0o755); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.tar.gz")
	t.Setenv("SOURCE_DATE_EPOCH", "1700000000")
	if err := run(fakeEntware(t, dir), bin, "../../packaging", "v9.9.9", out); err != nil {
		t.Fatal(err)
	}

	f, err := os.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(zr)
	got := map[string]*tar.Header{}
	var order []string
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		// The firmware's unpacker accepts GNU headers only.
		if h.Format != tar.FormatGNU {
			t.Errorf("%s: format %v, want GNU", h.Name, h.Format)
		}
		if h.Uid != 0 || h.Uname != "root" {
			t.Errorf("%s: owner %d/%s, want root", h.Name, h.Uid, h.Uname)
		}
		got[h.Name] = h
		order = append(order, h.Name)
	}

	for _, n := range []string{
		"bin/busybox", "bin/sh", "etc/init.d/doinstall",
		"etc/init.d/S00keenetic-sing-box-ui-setup",
		stageDir + "/opkg.conf", stageDir + "/setup.sh", stageDir + "/version",
		stageDir + "/S99keenetic-sing-box-ui", stageDir + "/keenetic-sing-box-ui",
	} {
		if got[n] == nil {
			t.Errorf("missing %s", n)
		}
	}
	for _, n := range []string{"etc/opkg.conf", "etc/dropbear/dropbear_rsa_host_key"} {
		if got[n] != nil {
			t.Errorf("%s must not be in the archive", n)
		}
	}
	if h := got["etc/init.d/S00keenetic-sing-box-ui-setup"]; h != nil && h.Mode&0o111 == 0 {
		t.Error("setup hook is not executable")
	}
	if h := got["bin/sh"]; h != nil && h.Linkname != "busybox" {
		t.Errorf("symlink lost: %q", h.Linkname)
	}
	if h := got[stageDir+"/version"]; h != nil && !h.ModTime.Equal(time.Unix(1700000000, 0)) {
		t.Errorf("mtime %v, want SOURCE_DATE_EPOCH", h.ModTime)
	}
	// Directories precede the opkg.conf that copyEntware puts into them.
	if order[0] != "share" {
		t.Errorf("first entry %q, want share", order[0])
	}
}

func TestRunRejectsNonEntware(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	_ = tw.Close()
	_ = zw.Close()
	src := filepath.Join(dir, "empty.tar.gz")
	if err := os.WriteFile(src, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run(src, src, "../../packaging", "v", filepath.Join(dir, "o.tar.gz")); err == nil {
		t.Fatal("want error for an archive without doinstall")
	}
}
