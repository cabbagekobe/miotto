// Tests for pendingGuard (the out-of-paper notice and give-up decision).
// The print flow itself is tested on the library side (miotto_test.go).
package main

import "testing"

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
