// Tests for the CLI's pure logic: pendingGuard (out-of-paper notice and
// give-up decision), the shell blocklist, and status formatting.
// The print flow itself is tested on the library side (miotto_test.go).
package main

import (
	"strings"
	"testing"

	miotto "github.com/cabbagekobe/miotto"
)

func TestIsBlockedRefusesDangerousCommands(t *testing.T) {
	for _, cmd := range []string{"producttest", "i2c read 0x10", "otp", "  otp  write"} {
		if !isBlocked(cmd) {
			t.Errorf("isBlocked(%q) = false, want true", cmd)
		}
	}
	for _, cmd := range []string{"apd", "bat soc", "echo producttest", ""} {
		if isBlocked(cmd) {
			t.Errorf("isBlocked(%q) = true, want false", cmd)
		}
	}
}

func TestFormatStatus(t *testing.T) {
	got := formatStatus(miotto.Status{BatteryPercent: 80, CoverOpen: false})
	if !strings.Contains(got, "80%") || !strings.Contains(got, "closed") {
		t.Errorf("formatStatus = %q", got)
	}
	got = formatStatus(miotto.Status{BatteryPercent: 5, CoverOpen: true})
	if !strings.Contains(got, "open") {
		t.Errorf("formatStatus = %q", got)
	}
}

func TestPendingGuardNotifiesOnce(t *testing.T) {
	g := &pendingGuard{limit: 10}
	notify, _ := g.observe("pending")
	if !notify {
		t.Error("first pending should notify")
	}
	notify, _ = g.observe("pending")
	if notify {
		t.Error("second pending should not notify again")
	}
}

func TestPendingGuardGivesUpAtLimit(t *testing.T) {
	g := &pendingGuard{limit: 3}
	for i := range 2 {
		if _, giveUp := g.observe("pending"); giveUp {
			t.Fatalf("gave up too early at %d", i+1)
		}
	}
	if _, giveUp := g.observe("pending"); !giveUp {
		t.Error("should give up at limit")
	}
	if !g.gaveUp {
		t.Error("gaveUp flag should be set")
	}
}

func TestPendingGuardResetsOnOtherStates(t *testing.T) {
	g := &pendingGuard{limit: 2}
	g.observe("pending")
	g.observe("printing") // the streak resets once printing resumes
	if _, giveUp := g.observe("pending"); giveUp {
		t.Error("streak should reset after resume")
	}
}
