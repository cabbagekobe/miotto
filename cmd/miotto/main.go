// miotto is the CLI that drives the printer over USB.
//
//	miotto print <image-file>   print one photo
//	miotto status               show battery and cover state
//	miotto shell [command]      run a debug-shell command (REPL without args)
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	miotto "github.com/cabbagekobe/miotto"
	"github.com/cabbagekobe/miotto/session"
)

// pendingLimit caps how many polls to wait while pending (waiting for paper);
// at the 2s interval this is about 5 minutes.
const pendingLimit = 150

func main() {
	var err error
	switch {
	case len(os.Args) == 3 && os.Args[1] == "print":
		err = printFile(os.Args[2])
	case len(os.Args) == 2 && os.Args[1] == "status":
		err = showStatus()
	case len(os.Args) >= 2 && os.Args[1] == "shell":
		err = shellCmd(os.Args[2:])
	default:
		fmt.Fprintln(os.Stderr, "usage: miotto print <image-file> | status | shell [command]")
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func printFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	p, err := miotto.Open()
	if err != nil {
		return err
	}
	defer p.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	guard := &pendingGuard{limit: pendingLimit}
	err = p.Print(ctx, data, miotto.WithProgress(func(pr miotto.Progress) {
		show(pr)
		notify, giveUp := guard.observe(pr.JobState)
		if notify {
			fmt.Println("Out of paper or a feed error. Printing resumes automatically once paper is loaded (Ctrl-C to abort).")
		}
		if giveUp {
			cancel()
		}
	}))
	if guard.gaveUp && errors.Is(err, context.Canceled) {
		return errors.New("printing did not resume; check paper, ribbon, and cover, then power-cycle the printer")
	}
	if err != nil {
		return err
	}
	fmt.Println("Print finished (job_state: finished)")
	return nil
}

func showStatus() error {
	p, err := miotto.Open()
	if err != nil {
		return err
	}
	defer p.Close()

	status, err := p.Status()
	if err != nil {
		return err
	}
	fmt.Print(formatStatus(status))
	return nil
}

func formatStatus(s miotto.Status) string {
	cover := "closed"
	if s.CoverOpen {
		cover = "open"
	}
	return fmt.Sprintf("battery: %d%%\ncover:   %s\n", s.BatteryPercent, cover)
}

// blockedCommands have unpredictable side effects (see docs/protocol.md) and
// are never sent.
var blockedCommands = []string{"producttest", "i2c", "otp"}

func isBlocked(command string) bool {
	fields := strings.Fields(command)
	return len(fields) > 0 && slices.Contains(blockedCommands, fields[0])
}

func shellCmd(args []string) error {
	p, err := miotto.Open()
	if err != nil {
		return err
	}
	defer p.Close()

	if len(args) > 0 {
		return runShellCommand(p, strings.Join(args, " "), os.Stdout)
	}

	fmt.Println("miotto debug shell — type a command, `exit` or Ctrl-D to quit")
	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("> ")
		if !scanner.Scan() {
			fmt.Println()
			break
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if line == "exit" {
			break
		}
		if err := runShellCommand(p, line, os.Stdout); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func runShellCommand(p *miotto.Printer, command string, w io.Writer) error {
	if isBlocked(command) {
		_, err := fmt.Fprintf(w, "refusing %q: unpredictable side effects (see docs/protocol.md)\n",
			strings.Fields(command)[0])
		return err
	}
	out, err := p.Shell(command)
	if err != nil {
		return err
	}
	if len(out) == 0 {
		_, err := fmt.Fprintln(w, "(no response)")
		return err
	}
	_, err = w.Write(out)
	return err
}

func show(pr miotto.Progress) {
	if pr.JobState != "" {
		fmt.Printf("job_state: %s\n", pr.JobState)
		return
	}
	fmt.Printf("chunk sent (%d/%d)\n", pr.ChunksSent, pr.ChunksTotal)
}

// pendingGuard counts consecutive pendings and decides when to show the
// one-time notice and when to give up at the limit.
type pendingGuard struct {
	limit    int
	streak   int
	notified bool
	gaveUp   bool
}

func (g *pendingGuard) observe(state string) (notify, giveUp bool) {
	if state != session.JobStatePending {
		g.streak = 0
		return false, false
	}
	g.streak++
	notify = !g.notified
	g.notified = true
	if g.streak >= g.limit {
		g.gaveUp = true
	}
	return notify, g.gaveUp
}
