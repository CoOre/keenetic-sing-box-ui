// Command mkinstaller builds the one-file installer archive for KeeneticOS:
// Entware's official installer for the architecture plus keenetic-sing-box-ui
// and its setup hook (packaging/installer).
//
// KeeneticOS installs it by itself, without SSH:
//
//	opkg disk <disk>:/ <archive URL>
//
// It downloads the archive, unpacks it into /opt and runs the init scripts:
// Entware's doinstall installs Entware, then our S00 hook installs iptables,
// ipset and the UI and starts it (see packaging/installer/setup.sh).
//
// The firmware's unpacker is picky: it wants a GNU-format tar (it rejects
// bsdtar's ustar output) served as application/octet-stream without
// redirects.
package main

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"time"
)

const (
	name     = "keenetic-sing-box-ui"
	stageDir = "share/" + name + "/install"
)

func main() {
	entware := flag.String("entware", "http://bin.entware.net/aarch64-k3.10/installer/aarch64-installer.tar.gz",
		"Entware installer archive: URL or local path")
	bin := flag.String("bin", "dist/"+name+"-linux-arm64", "keenetic-sing-box-ui binary for the router")
	pkg := flag.String("packaging", "packaging", "packaging directory of the repo")
	version := flag.String("version", "dev", "version recorded in the archive")
	out := flag.String("o", "dist/"+name+"-installer-aarch64.tar.gz", "output archive")
	flag.Parse()
	if err := run(*entware, *bin, *pkg, *version, *out); err != nil {
		fmt.Fprintln(os.Stderr, "mkinstaller:", err)
		os.Exit(1)
	}
}

func run(entware, bin, pkg, version, out string) error {
	src, err := openSource(entware)
	if err != nil {
		return err
	}
	defer src.Close()

	tmp := out + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	defer os.Remove(tmp)
	zw, _ := gzip.NewWriterLevel(f, gzip.BestCompression)
	tw := tar.NewWriter(zw)

	mtime := buildTime()
	// Our directories go first: copyEntware puts opkg.conf into stageDir.
	for _, d := range []string{"share", "share/" + name, stageDir} {
		if err := writeDir(tw, d, mtime); err != nil {
			return err
		}
	}
	if err := copyEntware(tw, src); err != nil {
		return fmt.Errorf("entware installer: %w", err)
	}
	files := []struct {
		dst, src string
		mode     int64
	}{
		{"etc/init.d/S00" + name + "-setup", path.Join(pkg, "installer", "S00"+name+"-setup"), 0o755},
		{stageDir + "/setup.sh", path.Join(pkg, "installer", "setup.sh"), 0o755},
		{stageDir + "/S99" + name, path.Join(pkg, "entware", "S99"+name), 0o755},
		{stageDir + "/" + name, bin, 0o755},
	}
	for _, e := range files {
		data, err := os.ReadFile(e.src)
		if err != nil {
			return err
		}
		if err := writeFile(tw, e.dst, e.mode, data, mtime); err != nil {
			return err
		}
	}
	if err := writeFile(tw, stageDir+"/version", 0o644, []byte(version+"\n"), mtime); err != nil {
		return err
	}

	if err := tw.Close(); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, out)
}

func openSource(src string) (io.ReadCloser, error) {
	if !strings.Contains(src, "://") {
		return os.Open(src)
	}
	resp, err := http.Get(src)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("GET %s: %s", src, resp.Status)
	}
	return resp.Body, nil
}

// copyEntware re-packs Entware's installer as is, except for:
//   - the empty dropbear host key placeholders, which would clobber the keys
//     of an existing install (its installer generates fresh ones anyway);
//   - opkg.conf, set aside for the hook to copy only onto a fresh disk.
func copyEntware(tw *tar.Writer, r io.Reader) error {
	zr, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	tr := tar.NewReader(zr)
	seen := false
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		n := strings.TrimPrefix(h.Name, "./")
		switch {
		case strings.HasPrefix(n, "etc/dropbear/dropbear_") && strings.HasSuffix(n, "_host_key"):
			continue
		case n == "lib/opkg/status":
			// The hook tells a fresh disk from an existing Entware by it.
			continue
		case n == "etc/opkg.conf":
			n = stageDir + "/opkg.conf"
		case n == "etc/init.d/doinstall":
			seen = true
		}
		h.Name = n
		normalize(h, h.ModTime)
		if err := tw.WriteHeader(h); err != nil {
			return err
		}
		if _, err := io.Copy(tw, tr); err != nil {
			return err
		}
	}
	if !seen {
		return errors.New("no etc/init.d/doinstall: not an Entware installer for KeeneticOS")
	}
	return nil
}

func writeDir(tw *tar.Writer, n string, mtime time.Time) error {
	h := &tar.Header{Typeflag: tar.TypeDir, Name: n, Mode: 0o755}
	normalize(h, mtime)
	return tw.WriteHeader(h)
}

func writeFile(tw *tar.Writer, n string, mode int64, data []byte, mtime time.Time) error {
	h := &tar.Header{Typeflag: tar.TypeReg, Name: n, Mode: mode, Size: int64(len(data))}
	normalize(h, mtime)
	if err := tw.WriteHeader(h); err != nil {
		return err
	}
	_, err := tw.Write(data)
	return err
}

func normalize(h *tar.Header, mtime time.Time) {
	h.Format = tar.FormatGNU
	h.Uid, h.Gid = 0, 0
	h.Uname, h.Gname = "root", "root"
	h.ModTime = mtime.Truncate(time.Second)
	h.AccessTime, h.ChangeTime = time.Time{}, time.Time{}
	h.PAXRecords = nil
}

// buildTime honours SOURCE_DATE_EPOCH for reproducible archives.
func buildTime() time.Time {
	if s := os.Getenv("SOURCE_DATE_EPOCH"); s != "" {
		if sec, err := strconv.ParseInt(s, 10, 64); err == nil {
			return time.Unix(sec, 0)
		}
	}
	return time.Now()
}
