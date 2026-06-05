package system

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"regexp"
	"runtime"
	"strings"

	"github.com/CoOre/keenetic-sing-box-ui/internal/cmdrun"
)

type Info struct {
	OS      string   `json:"os"`
	Arch    string   `json:"arch"`
	Paths   Paths    `json:"paths"`
	Entware *Entware `json:"entware,omitempty"`
	SingBox *SingBox `json:"sing_box,omitempty"`
	Service Service  `json:"service"`
}

type Entware struct {
	OpkgPath string `json:"opkg_path"`
}

type SingBox struct {
	Path    string `json:"path"`
	Version string `json:"version,omitempty"`
}

type Service struct {
	InitPath string `json:"init_path"`
	Present  bool   `json:"present"`
	// Enabled reports autostart (the init script's executable bit), NOT whether
	// the process is alive. Running reports the actual process liveness.
	Enabled bool `json:"enabled"`
	Running bool `json:"running"`
}

type Detector struct {
	Paths  Paths
	Runner cmdrun.Runner
	FS     fs.StatFS
}

func NewDetector(paths Paths) *Detector {
	return &Detector{Paths: paths, Runner: cmdrun.OS{}, FS: osFS{}}
}

func (d *Detector) Detect(ctx context.Context) (Info, error) {
	info := Info{
		OS:    runtime.GOOS,
		Arch:  runtime.GOARCH,
		Paths: d.Paths,
	}

	if st, err := d.FS.Stat(d.Paths.Opkg); err == nil && !st.IsDir() {
		info.Entware = &Entware{OpkgPath: d.Paths.Opkg}
	}

	if st, err := d.FS.Stat(d.Paths.SingBoxBin); err == nil && !st.IsDir() {
		sb := &SingBox{Path: d.Paths.SingBoxBin}
		if v, err := singBoxVersion(ctx, d.Runner, d.Paths.SingBoxBin); err == nil {
			sb.Version = v
		}
		info.SingBox = sb
	}

	svc := Service{InitPath: d.Paths.SingBoxInit}
	if st, err := d.FS.Stat(d.Paths.SingBoxInit); err == nil && !st.IsDir() {
		svc.Present = true
		svc.Enabled = st.Mode()&0o111 != 0
		svc.Running = d.serviceRunning(ctx)
	}
	info.Service = svc

	return info, nil
}

// serviceRunning probes actual process liveness by running the init script's
// "status" action, which prints "<name> is alive" / "is dead" / "not running".
// Best-effort: any error (script missing, exit code) means "not running".
func (d *Detector) serviceRunning(ctx context.Context) bool {
	res, err := d.Runner.Run(ctx, "sh", d.Paths.SingBoxInit, "status")
	if err != nil {
		return false
	}
	out := strings.ToLower(string(res.Stdout) + string(res.Stderr))
	if strings.Contains(out, "not running") {
		return false
	}
	return strings.Contains(out, "alive") || strings.Contains(out, "running")
}

var versionRe = regexp.MustCompile(`(?m)version\s+([0-9][^\s]*)`)

func singBoxVersion(ctx context.Context, runner cmdrun.Runner, bin string) (string, error) {
	res, err := runner.Run(ctx, bin, "version")
	if err != nil {
		return "", err
	}
	if m := versionRe.FindSubmatch(res.Stdout); len(m) == 2 {
		return strings.TrimSpace(string(m[1])), nil
	}
	first := strings.SplitN(strings.TrimSpace(string(res.Stdout)), "\n", 2)[0]
	if first == "" {
		return "", errors.New("empty version output")
	}
	return first, nil
}

type osFS struct{}

func (osFS) Stat(name string) (fs.FileInfo, error)         { return os.Stat(name) }
func (osFS) Open(name string) (fs.File, error)             { return os.Open(name) }
func (osFS) ReadFile(name string) ([]byte, error)          { return os.ReadFile(name) }
func (osFS) ReadDir(name string) ([]fs.DirEntry, error)    { return os.ReadDir(name) }
