package system

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/CoOre/keenetic-sing-box-ui/internal/cmdrun"
)

func TestDetect_EmptyRoot(t *testing.T) {
	root := t.TempDir()
	d := NewDetector(PathsRooted(root))
	d.Runner = &cmdrun.Fake{}

	info, err := d.Detect(context.Background())
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if info.Entware != nil {
		t.Errorf("expected no Entware, got %+v", info.Entware)
	}
	if info.SingBox != nil {
		t.Errorf("expected no sing-box, got %+v", info.SingBox)
	}
	if info.Service.Present {
		t.Errorf("expected service absent")
	}
}

func TestDetect_FullyInstalled(t *testing.T) {
	root := t.TempDir()
	p := PathsRooted(root)

	mustWrite(t, p.Opkg, "#!/bin/sh\n", 0o755)
	mustWrite(t, p.SingBoxBin, "#!/bin/sh\necho 'sing-box version 1.10.7'\n", 0o755)
	mustWrite(t, p.SingBoxInit, "#!/bin/sh\n", 0o755)

	d := NewDetector(p)
	d.Runner = &cmdrun.Fake{
		Responses: map[string]cmdrun.FakeResponse{
			p.SingBoxBin: {Stdout: "sing-box version 1.10.7\nEnvironment: linux/arm64\n"},
		},
	}

	info, err := d.Detect(context.Background())
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if info.Entware == nil || info.Entware.OpkgPath != p.Opkg {
		t.Errorf("expected Entware at %s, got %+v", p.Opkg, info.Entware)
	}
	if info.SingBox == nil || info.SingBox.Version != "1.10.7" {
		t.Errorf("expected version 1.10.7, got %+v", info.SingBox)
	}
	if !info.Service.Present || !info.Service.Enabled {
		t.Errorf("expected service present+enabled, got %+v", info.Service)
	}
}

func TestDetect_ServiceDisabledWhenNotExecutable(t *testing.T) {
	root := t.TempDir()
	p := PathsRooted(root)
	mustWrite(t, p.SingBoxInit, "#!/bin/sh\n", 0o644)

	d := NewDetector(p)
	d.Runner = &cmdrun.Fake{}

	info, err := d.Detect(context.Background())
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if !info.Service.Present {
		t.Errorf("expected present")
	}
	if info.Service.Enabled {
		t.Errorf("expected disabled (0644)")
	}
}

func mustWrite(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("write: %v", err)
	}
}
