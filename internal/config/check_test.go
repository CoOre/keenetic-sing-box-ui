package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/CoOre/keenetic-sing-box-ui/internal/cmdrun"
)

func TestCheck_OK(t *testing.T) {
	c := &Checker{SingBoxBin: "/opt/bin/sing-box", Runner: &cmdrun.Fake{}}
	res, err := c.Check(context.Background(), "/tmp/x.json")
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if !res.OK {
		t.Errorf("expected ok")
	}
	if len(res.Errors) != 0 {
		t.Errorf("unexpected errors: %v", res.Errors)
	}
}

func TestCheck_Failure_ParsesErrorLines(t *testing.T) {
	fake := &cmdrun.Fake{Default: cmdrun.FakeResponse{
		Stderr: "FATAL parse config: invalid character 'x'\nsomething else\nerror: missing outbound\n",
		Err:    errors.New("exit 1"),
	}}
	c := &Checker{SingBoxBin: "/opt/bin/sing-box", Runner: fake}
	res, err := c.Check(context.Background(), "/tmp/x.json")
	if err != nil {
		t.Fatalf("check returned err: %v", err)
	}
	if res.OK {
		t.Errorf("expected not OK")
	}
	if len(res.Errors) != 2 {
		t.Errorf("expected 2 error lines, got %d (%v)", len(res.Errors), res.Errors)
	}
}

func TestCheckContent_WritesTempfileAndPassesPath(t *testing.T) {
	fake := &cmdrun.Fake{}
	c := &Checker{SingBoxBin: "/opt/bin/sing-box", Runner: fake}
	if _, err := c.CheckContent(context.Background(), []byte(`{"a":1}`)); err != nil {
		t.Fatal(err)
	}
	if len(fake.Calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(fake.Calls))
	}
	args := fake.Calls[0].Args
	if args[0] != "check" || args[1] != "-c" {
		t.Errorf("unexpected args: %v", args)
	}
	// tempfile must have been removed.
	if _, err := os.Stat(args[2]); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected tempfile removed, stat=%v", err)
	}
	if filepath.Ext(args[2]) != ".json" {
		t.Errorf("expected .json tempfile, got %s", args[2])
	}
}
