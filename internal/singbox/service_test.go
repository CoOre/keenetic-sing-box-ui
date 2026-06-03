package singbox

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/CoOre/keenetic-sing-box-ui/internal/cmdrun"
)

func writeInit(t *testing.T, mode os.FileMode) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "S99sing-box")
	if err := os.WriteFile(p, []byte("#!/bin/sh\necho ok\n"), mode); err != nil {
		t.Fatalf("write: %v", err)
	}
	return p
}

func TestDo_InvalidAction(t *testing.T) {
	s := &Service{InitPath: writeInit(t, 0o755), Runner: &cmdrun.Fake{}}
	if _, err := s.Do(context.Background(), Action("nuke")); err == nil {
		t.Fatal("expected error")
	}
}

func TestDo_RunsInit(t *testing.T) {
	p := writeInit(t, 0o755)
	fake := &cmdrun.Fake{Default: cmdrun.FakeResponse{Stdout: "Starting sing-box\n"}}
	s := &Service{InitPath: p, Runner: fake}

	res, err := s.Do(context.Background(), ActionStart)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	if res.Stdout == "" {
		t.Errorf("expected stdout")
	}
	if len(fake.Calls) != 1 || fake.Calls[0].Name != "sh" {
		t.Errorf("unexpected calls: %+v", fake.Calls)
	}
	if fake.Calls[0].Args[0] != p || fake.Calls[0].Args[1] != "start" {
		t.Errorf("unexpected args: %+v", fake.Calls[0].Args)
	}
}

func TestEnableDisable(t *testing.T) {
	p := writeInit(t, 0o644)
	s := &Service{InitPath: p, Runner: &cmdrun.Fake{}}

	if enabled, _ := s.IsEnabled(); enabled {
		t.Fatal("expected disabled initially")
	}
	if err := s.Enable(); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if enabled, _ := s.IsEnabled(); !enabled {
		t.Fatal("expected enabled")
	}
	if err := s.Disable(); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if enabled, _ := s.IsEnabled(); enabled {
		t.Fatal("expected disabled after Disable")
	}
}

func TestIsRunning(t *testing.T) {
	p := writeInit(t, 0o755)
	tests := []struct {
		out  string
		want bool
	}{
		{"sing-box is alive\n", true},
		{"sing-box is dead\n", false},
		{"sing-box is not running\n", false},
		{"", false},
	}
	for _, tc := range tests {
		fake := &cmdrun.Fake{Default: cmdrun.FakeResponse{Stdout: tc.out}}
		s := &Service{InitPath: p, Runner: fake}
		got, err := s.IsRunning(context.Background())
		if err != nil {
			t.Errorf("%q: %v", tc.out, err)
		}
		if got != tc.want {
			t.Errorf("%q: want %v, got %v", tc.out, tc.want, got)
		}
	}
}
