package bootstrap

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// Seeding puts config where git could not. Setup is the other half: what has to
// be built rather than copied — an install, a codegen step, a native extension.
// It runs as the first phase of `opentree chat`, which is where the screen and
// the status socket already are.

// killGrace is how long a cancelled command has to stop on its own before it is
// killed outright. Short: cancelling is the user deciding to abandon this, and
// the only work left is whatever the process does on its way out.
const killGrace = 2 * time.Second

// RunSetup runs a project's setup commands in a worktree, in order, and stops
// at the first failure — a build whose install did not finish is not worth
// starting.
//
// Every line either stream prints goes to emit as it arrives, because these are
// the commands people watch. Each command is announced the way a shell would
// echo it, so the output reads as a transcript rather than as text from an
// unknown source.
//
// No timeout, by design: any number would be wrong for somebody. A warm
// `pnpm install` is two seconds and a cold `cargo build` is twenty minutes, and
// a build killed at minute nineteen is worse than one the user chose to
// abandon. Cancelling is the caller's to offer, through ctx.
func RunSetup(ctx context.Context, dir string, commands []string, emit func(string)) error {
	for _, command := range commands {
		if strings.TrimSpace(command) == "" {
			continue
		}
		emit("$ " + command)
		if err := runCommand(ctx, dir, command, emit); err != nil {
			return fmt.Errorf("%s: %w", command, err)
		}
	}
	return nil
}

// runCommand runs one setup command to completion.
func runCommand(ctx context.Context, dir, command string, emit func(string)) error {
	// Through a shell, because that is what the config says: `pnpm install &&
	// pnpm build` and `FOO=1 ./script` are ordinary things to write in it, and
	// splitting the string ourselves would honour neither.
	//
	// #nosec G204 -- the command is the project's own opentree.toml, which this
	// machine approved through the trust gate before anything here runs.
	cmd := exec.Command("sh", "-c", command)
	cmd.Dir = dir
	cmd.Env = os.Environ()

	// Its own process group, so cancelling reaches the whole tree. pnpm and
	// friends spawn children and hand back a pid that is not the one doing the
	// work; killing only that leaves an install running against a worktree
	// nobody is watching.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// One pipe for both streams: an installer's progress goes to stderr and its
	// summary to stdout, and reading them separately would interleave them
	// wrongly on screen.
	pr, pw, err := os.Pipe()
	if err != nil {
		return err
	}
	cmd.Stdout, cmd.Stderr = pw, pw

	if err := cmd.Start(); err != nil {
		_ = pr.Close()
		_ = pw.Close()
		return err
	}
	// The child holds its own copy of the write end; this process must let go
	// of it or the reader below never sees EOF.
	_ = pw.Close()

	drained := make(chan struct{})
	go func() {
		defer close(drained)
		stream(pr, emit)
	}()

	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			killGroup(cmd.Process.Pid)
		case <-done:
		}
	}()

	err = cmd.Wait()
	close(done)
	<-drained // whatever it printed on the way out belongs on screen
	_ = pr.Close()

	// A cancelled command failed because it was cancelled, which is a different
	// thing to tell the user than "exit status 143".
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return err
}

// stream turns a command's output into lines, as they arrive.
func stream(r io.Reader, emit func(string)) {
	sc := bufio.NewScanner(r)
	// Installers print long lines — a resolved dependency tree, a webpack
	// bundle report — and the default 64KB token would end the scan on one.
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		emit(lastProgress(sc.Text()))
	}
}

// lastProgress keeps only what a carriage-returned line finally said. A
// progress bar redraws itself in place with \r, and rendered whole it is a
// hundred copies of the same line with the interesting one at the end.
func lastProgress(line string) string {
	if i := strings.LastIndexByte(line, '\r'); i >= 0 {
		line = line[i+1:]
	}
	return strings.TrimRight(line, " \t")
}

// killGroup stops a command and everything it started. SIGTERM first, so a
// package manager can unlink its temporary directory; SIGKILL after, so a
// process that ignores it still goes.
func killGroup(pid int) {
	// The negative pid addresses the group, which is the whole point of having
	// put the command in one.
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	time.Sleep(killGrace)
	_ = syscall.Kill(-pid, syscall.SIGKILL)
}
