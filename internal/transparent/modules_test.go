package transparent

import (
	"context"
	"errors"
	"testing"

	"github.com/CoOre/keenetic-sing-box-ui/internal/cmdrun"
)

// On many Keenetic firmwares there is no standalone insmod — it exists only as
// a busybox applet. insmodFile must fall back to `busybox insmod` when the
// bare insmod call fails, otherwise TPROXY module loading silently aborts.
func TestInsmodFile_FallsBackToBusybox(t *testing.T) {
	// Key responses by the RESOLVED tool path: on Linux CI toolPath("insmod")
	// finds /sbin/insmod, so a bare "insmod" key would never match and the
	// fallback would never be exercised.
	f := &cmdrun.Fake{
		Responses: map[string]cmdrun.FakeResponse{
			toolPath("insmod"):  {Err: errors.New("not found")},
			toolPath("busybox"): {}, // success
		},
	}
	if err := insmodFile(context.Background(), f, "/lib/system-modules/x/xt_TPROXY.ko"); err != nil {
		t.Fatalf("expected busybox fallback to succeed, got %v", err)
	}
	if !hasCall(f.Calls, "busybox insmod /lib/system-modules/x/xt_TPROXY.ko") {
		t.Errorf("expected a busybox insmod call, calls: %+v", f.Calls)
	}
}

func TestInsmodFile_BothFail(t *testing.T) {
	f := &cmdrun.Fake{Default: cmdrun.FakeResponse{Err: errors.New("boom")}}
	if err := insmodFile(context.Background(), f, "/x.ko"); err == nil {
		t.Error("expected error when both insmod and busybox insmod fail")
	}
}
