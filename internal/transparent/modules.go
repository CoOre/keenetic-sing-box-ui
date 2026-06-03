package transparent

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/CoOre/keenetic-sing-box-ui/internal/cmdrun"
)

// kernelRelease returns `uname -r`. The firmware module tree is keyed by it.
func kernelRelease() string {
	if b, err := os.ReadFile("/proc/sys/kernel/osrelease"); err == nil {
		return strings.TrimSpace(string(b))
	}
	return ""
}

// moduleLoaded reports whether a module (without .ko) is already in the kernel.
func moduleLoaded(name string) bool {
	return dirExists("/sys/module/" + strings.TrimSuffix(name, ".ko"))
}

// ownerModuleWorking checks the xt_owner match, which on many Keenetic kernels
// is built in (no .ko to load). Mirrors SKeen's is_owner_module_working.
func ownerModuleWorking(ctx context.Context, r cmdrun.Runner) bool {
	if moduleLoaded("xt_owner") {
		return true
	}
	if out, _ := run(ctx, r, "iptables", "-m", "owner", "--help"); strings.Contains(out, "owner match options") {
		return true
	}
	// Last resort: try inserting and removing a harmless owner rule.
	if ok(ctx, r, "iptables", "-w", "-t", "mangle", "-I", "OUTPUT", "1",
		"-m", "owner", "--gid-owner", "65534", "-j", "RETURN") {
		_, _ = run(ctx, r, "iptables", "-w", "-t", "mangle", "-D", "OUTPUT", "1")
		return true
	}
	return false
}

// loadModule loads one module, copying it from the firmware tree into the
// Entware tree first (so it persists for Entware's own module path). Returns
// nil if already loaded. Mirrors SKeen's load_module.
func loadModule(ctx context.Context, r cmdrun.Runner, module string) error {
	if moduleLoaded(module) {
		return nil
	}
	rel := kernelRelease()
	osPath := filepath.Join(modulesOSDir, rel, module)
	entPath := filepath.Join(modulesEntwareDir, module)

	target := ""
	switch {
	case fileExists(osPath):
		target = osPath
		if !fileExists(entPath) {
			_ = os.MkdirAll(modulesEntwareDir, 0o755)
			_ = copyFile(osPath, entPath)
		}
	case fileExists(entPath):
		target = entPath
	}
	if target == "" {
		return fmt.Errorf("module %s not found under %s/%s or %s", module, modulesOSDir, rel, modulesEntwareDir)
	}
	if !ok(ctx, r, "insmod", target) {
		return fmt.Errorf("insmod %s failed", target)
	}
	return nil
}

// LoadModules ensures every required netfilter module is loaded. It is lenient
// for ip_set_* / xt_comment (nice-to-have) but strict for xt_TPROXY/xt_socket,
// which transparent TPROXY cannot work without. Returns the list of modules it
// could not load (empty on full success).
func LoadModules(ctx context.Context, r cmdrun.Runner) (missing []string, err error) {
	for _, m := range requiredModules {
		if m == "xt_owner.ko" && ownerModuleWorking(ctx, r) {
			continue
		}
		if lerr := loadModule(ctx, r, m); lerr != nil {
			missing = append(missing, m)
		}
	}
	for _, m := range missing {
		if m == "xt_TPROXY.ko" || m == "xt_socket.ko" {
			return missing, fmt.Errorf("required modules missing: %s — install Keenetic component 'Kernel modules for Netfilter'", strings.Join(missing, " "))
		}
	}
	return missing, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
