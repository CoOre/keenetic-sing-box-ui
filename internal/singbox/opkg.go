package singbox

import (
	"context"
	"fmt"

	"github.com/CoOre/keenetic-sing-box-ui/internal/cmdrun"
)

type Opkg struct {
	Bin    string
	Runner cmdrun.Runner
}

func NewOpkg(bin string) *Opkg {
	return &Opkg{Bin: bin, Runner: cmdrun.OS{}}
}

type OpkgStep struct {
	Command string `json:"command"`
	Stdout  string `json:"stdout"`
	Stderr  string `json:"stderr"`
	Err     string `json:"err,omitempty"`
}

type OpkgResult struct {
	Steps []OpkgStep `json:"steps"`
}

func (o *Opkg) Install(ctx context.Context, pkg string) (OpkgResult, error) {
	out := OpkgResult{}
	for _, args := range [][]string{
		{"update"},
		{"install", pkg},
	} {
		res, err := o.Runner.Run(ctx, o.Bin, args...)
		step := OpkgStep{
			Command: fmt.Sprintf("%s %s", o.Bin, joinArgs(args)),
			Stdout:  string(res.Stdout),
			Stderr:  string(res.Stderr),
		}
		if err != nil {
			step.Err = err.Error()
			out.Steps = append(out.Steps, step)
			return out, fmt.Errorf("opkg %s: %w", args[0], err)
		}
		out.Steps = append(out.Steps, step)
	}
	return out, nil
}

func joinArgs(args []string) string {
	s := ""
	for i, a := range args {
		if i > 0 {
			s += " "
		}
		s += a
	}
	return s
}
