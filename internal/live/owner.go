package live

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// The harness owns the call's process group, but is not part of it. Watching
// round's parent would instead watch a shell that exits normally after round.
type relayOwner struct {
	PID     int
	Started string
}

func findRelayOwner() (relayOwner, error) {
	pid, err := ownerPID()
	if err != nil {
		return relayOwner{}, err
	}
	started, err := processStart(pid)
	return relayOwner{PID: pid, Started: started}, err
}

func ownerPID() (int, error) {
	out, err := exec.Command("ps", "-o", "ppid=", "-p", strconv.Itoa(syscall.Getpgrp())).Output()
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || pid <= 1 {
		return 0, fmt.Errorf("cannot identify the call's owner")
	}
	return pid, nil
}

// ownerExecutable names the executable of the call's owner, or is empty when
// the owner cannot be identified.
func ownerExecutable() string {
	pid, err := ownerPID()
	if err != nil {
		return ""
	}
	out, err := exec.Command("ps", "-o", "comm=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func processStart(pid int) (string, error) {
	out, err := exec.Command("ps", "-o", "lstart=", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(out))
	if len(fields) != 6 || strings.HasPrefix(fields[5], "Z") {
		return "", fmt.Errorf("call owner is no longer running")
	}
	return strings.Join(fields[:5], " "), nil
}

func (o relayOwner) follow(stop <-chan struct{}) <-chan struct{} {
	gone := make(chan struct{})
	go func() {
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			started, err := processStart(o.PID)
			if err != nil || started != o.Started {
				close(gone)
				return
			}
			select {
			case <-stop:
				return
			case <-ticker.C:
			}
		}
	}()
	return gone
}
