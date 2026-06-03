package config

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/CoOre/keenetic-sing-box-ui/internal/cmdrun"
)

type Checker struct {
	SingBoxBin string
	Runner     cmdrun.Runner
}

func NewChecker(singBoxBin string) *Checker {
	return &Checker{SingBoxBin: singBoxBin, Runner: cmdrun.OS{}}
}

type CheckResult struct {
	OK     bool     `json:"ok"`
	Stdout string   `json:"stdout,omitempty"`
	Stderr string   `json:"stderr,omitempty"`
	Errors []string `json:"errors,omitempty"`
}

// Check runs `sing-box check -c <path>` against an existing config file.
func (c *Checker) Check(ctx context.Context, path string) (CheckResult, error) {
	if c.SingBoxBin == "" {
		return CheckResult{}, errors.New("empty sing-box bin path")
	}
	res, err := c.Runner.Run(ctx, c.SingBoxBin, "check", "-c", path)
	out := CheckResult{
		Stdout: string(res.Stdout),
		Stderr: string(res.Stderr),
	}
	out.Errors = extractErrorLines(out.Stderr)
	if err == nil {
		out.OK = true
	}
	return out, nil
}

// CheckContent writes content to a tempfile and runs Check against it; used
// to validate proposed config before committing it to the live path.
func (c *Checker) CheckContent(ctx context.Context, content []byte) (CheckResult, error) {
	f, err := os.CreateTemp("", "sing-box-check-*.json")
	if err != nil {
		return CheckResult{}, err
	}
	path := f.Name()
	defer os.Remove(path)
	if _, err := f.Write(content); err != nil {
		f.Close()
		return CheckResult{}, err
	}
	if err := f.Close(); err != nil {
		return CheckResult{}, err
	}
	return c.Check(ctx, path)
}

// extractErrorLines pulls out lines that look like FATAL/ERROR/error messages
// from sing-box stderr. The format varies by version, so we match leniently.
func extractErrorLines(stderr string) []string {
	if strings.TrimSpace(stderr) == "" {
		return nil
	}
	var out []string
	for _, line := range strings.Split(stderr, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		low := strings.ToLower(line)
		if strings.Contains(low, "fatal") ||
			strings.Contains(low, "error") ||
			strings.Contains(low, "invalid") ||
			strings.Contains(low, "fail") {
			out = append(out, line)
		}
	}
	return out
}
