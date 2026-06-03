package transparent

import (
	"os"
	"path/filepath"
	"strings"
)

// hookBinCandidates returns the paths to try for the callback binary, most
// canonical first. KeeneticOS runs Entware in a chroot where /opt is the root,
// so a process sees itself at /bin/<name> (os.Executable) while NDM — which
// invokes netfilter.d hooks — sees it at /opt/bin/<name>. We emit both so the
// hook works regardless of which root it's called from.
func hookBinCandidates(bin string) []string {
	base := filepath.Base(bin)
	cands := []string{"/opt/bin/" + base, "/bin/" + base}
	// Keep the discovered path too, if it's something unusual (dev/test).
	if bin != "" && bin != cands[0] && bin != cands[1] {
		cands = append(cands, bin)
	}
	return cands
}

// hookPath is the netfilter.d script KeeneticOS runs on every firewall rebuild.
func hookPath() string { return filepath.Join(netfilterDir, HookFileName) }

// writeHook installs the netfilter.d shim. KeeneticOS executes it with $type
// (iptables/ip6tables) and $table set; the shim filters to our tables and calls
// back into this binary to (re)apply rules. Embedding the binary path keeps all
// rule logic in Go — the shim is just a trampoline that survives the router
// flushing its firewall.
func (e *Engine) writeHook(cfg Config) error {
	bin := e.Bin
	if bin == "" {
		if p, err := os.Executable(); err == nil {
			bin = p
		} else {
			bin = "/opt/bin/keenetic-sing-box-ui"
		}
	}
	tables := cfg.tables()
	if len(tables) == 0 {
		return e.removeHook()
	}

	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString("# keenetic-sing-box-ui transparent firewall hook (managed; do not edit)\n")
	b.WriteString("[ \"$type\" = \"iptables\" ] || exit 0\n")
	b.WriteString("case \"$table\" in\n")
	b.WriteString("  " + strings.Join(tables, "|") + ") ;;\n")
	b.WriteString("  *) exit 0 ;;\n")
	b.WriteString("esac\n")
	b.WriteString("logger -p notice -t " + Name + " \"applying $type rules for $table\" 2>/dev/null\n")
	// Try each candidate path; exec the first that's executable. Resilient to
	// the Entware chroot (/bin vs /opt/bin) in which NDM may run this hook.
	b.WriteString("for BIN in")
	for _, c := range hookBinCandidates(bin) {
		b.WriteString(" " + shellQuote(c))
	}
	b.WriteString("; do\n")
	b.WriteString("  [ -x \"$BIN\" ] && exec \"$BIN\" firewall apply --table \"$table\" >/dev/null 2>&1\n")
	b.WriteString("done\n")

	if err := os.MkdirAll(netfilterDir, 0o755); err != nil {
		return err
	}
	tmp := hookPath() + ".new"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o755); err != nil {
		return err
	}
	return os.Rename(tmp, hookPath())
}

func (e *Engine) removeHook() error {
	err := os.Remove(hookPath())
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// shellQuote single-quotes a path for safe embedding in the shim.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
