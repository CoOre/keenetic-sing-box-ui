package cmdrun

import (
	"bytes"
	"context"
	"os/exec"
)

type Result struct {
	Stdout []byte
	Stderr []byte
}

type Runner interface {
	Run(ctx context.Context, name string, args ...string) (Result, error)
}

type OS struct{}

func (OS) Run(ctx context.Context, name string, args ...string) (Result, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return Result{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}, err
}

// RunStdin runs a command feeding stdin from the given bytes. Used for bulk
// ipset loading via `ipset restore` (one process for thousands of entries
// instead of one process per entry, which pegs the router CPU).
func (OS) RunStdin(ctx context.Context, stdin []byte, name string, args ...string) (Result, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = bytes.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return Result{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}, err
}

type FakeCall struct {
	Name  string
	Args  []string
	Stdin string // set for RunStdin calls
}

type FakeResponse struct {
	Stdout string
	Stderr string
	Err    error
}

type Fake struct {
	Responses map[string]FakeResponse
	Default   FakeResponse
	Calls     []FakeCall
}

func (f *Fake) Run(_ context.Context, name string, args ...string) (Result, error) {
	f.Calls = append(f.Calls, FakeCall{Name: name, Args: append([]string(nil), args...)})
	key := name
	if r, ok := f.Responses[key]; ok {
		return Result{Stdout: []byte(r.Stdout), Stderr: []byte(r.Stderr)}, r.Err
	}
	return Result{Stdout: []byte(f.Default.Stdout), Stderr: []byte(f.Default.Stderr)}, f.Default.Err
}

func (f *Fake) RunStdin(_ context.Context, stdin []byte, name string, args ...string) (Result, error) {
	f.Calls = append(f.Calls, FakeCall{Name: name, Args: append([]string(nil), args...), Stdin: string(stdin)})
	if r, ok := f.Responses[name]; ok {
		return Result{Stdout: []byte(r.Stdout), Stderr: []byte(r.Stderr)}, r.Err
	}
	return Result{Stdout: []byte(f.Default.Stdout), Stderr: []byte(f.Default.Stderr)}, f.Default.Err
}
