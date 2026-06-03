package transparent

import (
	"context"
	"os"
	"strings"

	"github.com/CoOre/keenetic-sing-box-ui/internal/cmdrun"
)

// toolPath returns the first existing absolute path for a tool, falling back to
// the bare name (letting PATH resolve it) if none of the candidates exist.
func toolPath(name string) string {
	for _, p := range toolPaths[name] {
		if fileExists(p) {
			return p
		}
	}
	return name
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// run invokes a resolved tool and returns combined trimmed output plus error.
func run(ctx context.Context, r cmdrun.Runner, tool string, args ...string) (string, error) {
	res, err := r.Run(ctx, toolPath(tool), args...)
	out := strings.TrimRight(string(res.Stdout)+string(res.Stderr), "\n")
	return out, err
}

// ok reports whether a tool invocation succeeded (exit 0), ignoring output.
func ok(ctx context.Context, r cmdrun.Runner, tool string, args ...string) bool {
	_, err := r.Run(ctx, toolPath(tool), args...)
	return err == nil
}

// stdinRunner is the optional capability for feeding a command stdin.
type stdinRunner interface {
	RunStdin(ctx context.Context, stdin []byte, name string, args ...string) (cmdrun.Result, error)
}

// runStdin invokes a resolved tool feeding it stdin. Returns false in the
// second result if the runner can't do stdin (caller should fall back).
func runStdin(ctx context.Context, r cmdrun.Runner, stdin []byte, tool string, args ...string) (string, bool, error) {
	sr, ok := r.(stdinRunner)
	if !ok {
		return "", false, nil
	}
	res, err := sr.RunStdin(ctx, stdin, toolPath(tool), args...)
	out := strings.TrimRight(string(res.Stdout)+string(res.Stderr), "\n")
	return out, true, err
}
