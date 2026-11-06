package main

import (
	"testing"
	"time"
)

func TestShutdownWatchdogUsesTheCapturedStartupDeadline(t *testing.T) {
	captured := time.Now().Add(-time.Second)
	expired := make(chan struct{}, 1)
	stop := startShutdownDeadlineWatchdog(captured, func() { expired <- struct{}{} })
	defer stop()

	select {
	case <-expired:
	case <-time.After(time.Second):
		t.Fatal("watchdog reset the expired startup deadline instead of enforcing it")
	}
}

// The coordinator returns its deadline error at the deadline itself; a
// watchdog firing at the same instant would exit before that is logged.
func TestShutdownWatchdogLeavesTheCoordinatorItsDeadline(t *testing.T) {
	expired := make(chan struct{}, 1)
	stop := startShutdownDeadlineWatchdog(time.Now(), func() { expired <- struct{}{} })
	defer stop()

	select {
	case <-expired:
		t.Fatal("watchdog fired at the coordinator deadline instead of after its grace")
	case <-time.After(shutdownWatchdogGrace / 2):
	}
	select {
	case <-expired:
	case <-time.After(shutdownWatchdogGrace + time.Second):
		t.Fatal("watchdog never fired after the grace elapsed")
	}
}
