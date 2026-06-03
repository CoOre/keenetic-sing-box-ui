package singbox

import (
	"context"
	"errors"
	"testing"

	"github.com/CoOre/keenetic-sing-box-ui/internal/cmdrun"
)

func TestOpkgInstall_Success(t *testing.T) {
	fake := &cmdrun.Fake{
		Responses: map[string]cmdrun.FakeResponse{
			"/opt/bin/opkg": {Stdout: "ok\n"},
		},
	}
	o := &Opkg{Bin: "/opt/bin/opkg", Runner: fake}
	res, err := o.Install(context.Background(), "sing-box")
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if len(res.Steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(res.Steps))
	}
	if len(fake.Calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(fake.Calls))
	}
	if fake.Calls[0].Args[0] != "update" {
		t.Errorf("first call not update: %+v", fake.Calls[0])
	}
	if fake.Calls[1].Args[0] != "install" || fake.Calls[1].Args[1] != "sing-box" {
		t.Errorf("second call not install sing-box: %+v", fake.Calls[1])
	}
}

func TestOpkgInstall_UpdateFails(t *testing.T) {
	fake := &cmdrun.Fake{Default: cmdrun.FakeResponse{Stderr: "no network\n", Err: errors.New("exit 1")}}
	o := &Opkg{Bin: "/opt/bin/opkg", Runner: fake}
	res, err := o.Install(context.Background(), "sing-box")
	if err == nil {
		t.Fatal("expected error")
	}
	if len(res.Steps) != 1 {
		t.Fatalf("expected 1 step (update failed), got %d", len(res.Steps))
	}
	if res.Steps[0].Err == "" {
		t.Errorf("expected err on step")
	}
}
