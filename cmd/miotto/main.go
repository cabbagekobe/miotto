// miotto is the CLI that drives the printer over USB. Only print for now.
//
//	miotto print <image-file>
package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	miotto "github.com/cabbagekobe/miotto"
	"github.com/cabbagekobe/miotto/session"
)

// pendingLimit caps how many polls to wait while pending (waiting for paper);
// at the 2s interval this is about 5 minutes.
const pendingLimit = 150

func main() {
	if len(os.Args) != 3 || os.Args[1] != "print" {
		fmt.Fprintln(os.Stderr, "usage: miotto print <image-file>")
		os.Exit(2)
	}
	if err := printFile(os.Args[2]); err != nil {
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
