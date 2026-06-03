package singbox

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/CoOre/keenetic-sing-box-ui/internal/cmdrun"
)

type Action string

const (
	ActionStart   Action = "start"
	ActionStop    Action = "stop"
	ActionRestart Action = "restart"
	ActionCheck   Action = "check"
	ActionStatus  Action = "status"
)

func (a Action) Valid() bool {
	switch a {
	case ActionStart, ActionStop, ActionRestart, ActionCheck, ActionStatus:
		return true
	}
	return false
}

type Service struct {
	InitPath string
	Runner   cmdrun.Runner
}

func NewService(initPath string) *Service {
	return &Service{InitPath: initPath, Runner: cmdrun.OS{}}
}

type ActionResult struct {
	Stdout string `json:"stdout"`
	Stderr string `json:"stderr"`
}

func (s *Service) Do(ctx context.Context, a Action) (ActionResult, error) {
	if !a.Valid() {
		return ActionResult{}, fmt.Errorf("invalid action %q", a)
	}
	if _, err := os.Stat(s.InitPath); err != nil {
		return ActionResult{}, fmt.Errorf("init script %s: %w", s.InitPath, err)
	}
	res, err := s.Runner.Run(ctx, "sh", s.InitPath, string(a))
	out := ActionResult{Stdout: string(res.Stdout), Stderr: string(res.Stderr)}
	if err != nil {
		return out, fmt.Errorf("%s: %w", a, err)
	}
	return out, nil
}

func (s *Service) Enable() error {
	st, err := os.Stat(s.InitPath)
	if err != nil {
		return err
	}
	return os.Chmod(s.InitPath, st.Mode()|0o111)
}

func (s *Service) Disable() error {
	st, err := os.Stat(s.InitPath)
	if err != nil {
		return err
	}
	return os.Chmod(s.InitPath, st.Mode()&^fs.FileMode(0o111))
}

func (s *Service) IsEnabled() (bool, error) {
	st, err := os.Stat(s.InitPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return st.Mode()&0o111 != 0, nil
}

// IsRunning parses Entware rc.func "status" output, which is one of
// "<name> is alive" / "<name> is dead" / "<name> is not running".
func (s *Service) IsRunning(ctx context.Context) (bool, error) {
	res, err := s.Do(ctx, ActionStatus)
	if err != nil {
		return false, err
	}
	out := strings.ToLower(res.Stdout + res.Stderr)
	switch {
	case strings.Contains(out, "alive"), strings.Contains(out, "running"):
		return !strings.Contains(out, "not running"), nil
	case strings.Contains(out, "dead"), strings.Contains(out, "stopped"):
		return false, nil
	}
	return false, nil
}
