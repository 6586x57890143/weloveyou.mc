package main

import (
	"io"
	"os/exec"
	"strings"
)

// dockerCLI implements bench.Container by shelling out. Deliberately trivial:
// every decision this could get wrong lives in internal/bench, where it is
// tested against a replayed log rather than a real server.
type dockerCLI struct{}

func (dockerCLI) Start(name, image string, env []string) error {
	args := []string{"run", "-d", "--name", name, "--network", "host"}
	for _, e := range env {
		args = append(args, "-e", e)
	}
	return exec.Command("docker", append(args, image)...).Run()
}

func (dockerCLI) Logs(name string) (io.ReadCloser, error) {
	cmd := exec.Command("docker", "logs", "-f", name)
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &procReader{ReadCloser: pipe, cmd: cmd}, nil
}

func (dockerCLI) Exec(name string, args ...string) error {
	return exec.Command("docker", append([]string{"exec", name}, args...)...).Run()
}

func (dockerCLI) MemUsage(name string) (string, error) {
	out, err := exec.Command("docker", "stats", "--no-stream", "--format", "{{.MemUsage}}", name).Output()
	return strings.TrimSpace(string(out)), err
}

func (dockerCLI) Remove(name string) error {
	return exec.Command("docker", "rm", "-f", name).Run()
}

// procReader kills the log follower when the reader is closed, so a finished
// run does not leave a `docker logs -f` behind for every profile it measured.
type procReader struct {
	io.ReadCloser
	cmd *exec.Cmd
}

func (p *procReader) Close() error {
	err := p.ReadCloser.Close()
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
	_ = p.cmd.Wait()
	return err
}
