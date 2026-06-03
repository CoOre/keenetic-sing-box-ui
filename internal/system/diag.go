package system

import (
	"context"
	"os"
	"strings"

	"github.com/CoOre/keenetic-sing-box-ui/internal/cmdrun"
)

// DiagStep is the result of one diagnostic command.
type DiagStep struct {
	Name   string `json:"name"`
	Cmd    string `json:"cmd"`
	Output string `json:"output"`
	Err    string `json:"err,omitempty"`
}

// ToolPresence records whether a binary exists at a known absolute path.
type ToolPresence struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	Found bool   `json:"found"`
}

// probeTools looks for the binaries TProxy needs at absolute paths, bypassing
// the (restricted) process PATH. KeeneticOS keeps system tools in /sbin etc.
func probeTools() []ToolPresence {
	candidates := map[string][]string{
		"iptables": {"/sbin/iptables", "/usr/sbin/iptables", "/opt/sbin/iptables", "/opt/bin/iptables"},
		"ip":       {"/sbin/ip", "/usr/sbin/ip", "/opt/sbin/ip", "/opt/bin/ip"},
		"ipset":    {"/sbin/ipset", "/usr/sbin/ipset", "/opt/sbin/ipset", "/opt/bin/ipset"},
		"iptables-mod-tproxy": {
			"/lib/modules", "/opt/lib/modules",
		},
	}
	order := []string{"iptables", "ip", "ipset", "iptables-mod-tproxy"}
	var out []ToolPresence
	for _, name := range order {
		tp := ToolPresence{Name: name, Found: false}
		for _, p := range candidates[name] {
			if st, err := os.Stat(p); err == nil {
				tp.Path = p
				tp.Found = true
				_ = st
				break
			}
		}
		out = append(out, tp)
	}
	return out
}

// procFlag reports whether a /proc path exists (kernel feature probe).
func procFlag(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// NetReport summarizes whether transparent proxying (TProxy) is feasible.
type NetReport struct {
	Tools   []ToolPresence `json:"tools"`
	Kernel  []DiagStep     `json:"kernel"`
	Runtime []DiagStep     `json:"runtime"`
}

// NetDiagnostics probes, without changing anything, whether the router can do
// selective transparent proxying: required userspace tools (by absolute path,
// since the process PATH is restricted), kernel netfilter/TPROXY features (via
// /proc), and a couple of best-effort tool invocations at their found paths.
func NetDiagnostics(ctx context.Context, runner cmdrun.Runner) NetReport {
	tools := probeTools()

	// Kernel features via /proc (read-only, no module load).
	kernel := []DiagStep{
		procStep("ip_tables", "/proc/net/ip_tables_names"),
		procStep("ip_tables_targets", "/proc/net/ip_tables_targets"),
		modulesStep(),
	}

	// Best-effort runtime checks using absolute paths we actually found.
	var runtime []DiagStep
	pathOf := func(name string) string {
		for _, t := range tools {
			if t.Name == name && t.Found {
				return t.Path
			}
		}
		return ""
	}
	if ipt := pathOf("iptables"); ipt != "" {
		runtime = append(runtime, runStep(ctx, runner, "iptables-mangle", ipt, "-t", "mangle", "-L", "-n"))
	}
	if ip := pathOf("ip"); ip != "" {
		runtime = append(runtime, runStep(ctx, runner, "ip-rule", ip, "rule", "list"))
	}
	if ips := pathOf("ipset"); ips != "" {
		runtime = append(runtime, runStep(ctx, runner, "ipset-version", ips, "--version"))
	}

	return NetReport{Tools: tools, Kernel: kernel, Runtime: runtime}
}

func runStep(ctx context.Context, runner cmdrun.Runner, name, bin string, args ...string) DiagStep {
	res, err := runner.Run(ctx, bin, args...)
	step := DiagStep{
		Name:   name,
		Cmd:    bin + " " + strings.Join(args, " "),
		Output: strings.TrimRight(string(res.Stdout)+string(res.Stderr), "\n"),
	}
	if err != nil {
		step.Err = err.Error()
	}
	return step
}

func procStep(name, path string) DiagStep {
	step := DiagStep{Name: name, Cmd: "read " + path}
	b, err := os.ReadFile(path)
	if err != nil {
		step.Err = err.Error()
		return step
	}
	step.Output = strings.TrimRight(string(b), "\n")
	return step
}

// modulesStep extracts tun/tproxy/nf-related lines from /proc/modules.
func modulesStep() DiagStep {
	step := DiagStep{Name: "modules", Cmd: "grep tun|tproxy|nf /proc/modules"}
	b, err := os.ReadFile("/proc/modules")
	if err != nil {
		step.Err = err.Error()
		return step
	}
	var keep []string
	for _, line := range strings.Split(string(b), "\n") {
		low := strings.ToLower(line)
		if strings.Contains(low, "tproxy") || strings.HasPrefix(low, "tun ") ||
			strings.Contains(low, "nf_tproxy") || strings.Contains(low, "xt_tproxy") {
			keep = append(keep, strings.SplitN(line, " ", 2)[0])
		}
	}
	step.Output = strings.Join(keep, "\n")
	return step
}

var _ = procFlag // reserved for future /proc feature probes

// execAllow maps a short action name to an absolute binary path. Only these
// can be run via RunAction — no arbitrary commands. Used to drive the REDIRECT
// PoC (opkg install, iptables/ipset rules) from the root backend, since the
// router has no usable shell over KCommand.
var execAllow = map[string]string{
	"opkg":     "/opt/bin/opkg",
	"iptables": "/opt/sbin/iptables",
	"ipset":    "/opt/sbin/ipset",
	"ip":       "/opt/sbin/ip",
	"sh":       "/opt/bin/sh",
}

// RunAction executes one whitelisted action with the given args. The action
// must be a key of execAllow; args are passed through verbatim. Returns a
// DiagStep with combined output.
func RunAction(ctx context.Context, runner cmdrun.Runner, action string, args []string) (DiagStep, bool) {
	bin, ok := execAllow[action]
	if !ok {
		return DiagStep{Name: action, Err: "action not allowed"}, false
	}
	// Fall back to bare name if the absolute path doesn't exist (PATH may
	// still resolve it in some setups).
	if _, err := os.Stat(bin); err != nil {
		bin = action
	}
	return runStep(ctx, runner, action, bin, args...), true
}
