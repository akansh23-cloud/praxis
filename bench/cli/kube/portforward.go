/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package kube

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strconv"
	"time"
)

// forwardingLine matches kubectl's announcement of the local port it bound,
// e.g. "Forwarding from 127.0.0.1:40123 -> 8080".
var forwardingLine = regexp.MustCompile(`Forwarding from 127\.0\.0\.1:(\d+)`)

// PortForward starts `kubectl port-forward` to target (e.g. "svc/checkout-api")
// in namespace, forwarding a random free local port to remotePort. It
// returns the local port once kubectl reports the tunnel is up, plus a stop
// function that must be called to end the forward. Traffic through the
// tunnel enters the cluster at the node and reaches one backing pod — so a
// check that must exercise pod-to-pod networking has to make its request
// FROM inside that pod (e.g. netexec /dial), not merely to it.
func (t *Toolchain) PortForward(ctx context.Context, namespace, target string, remotePort int) (int, func(), error) {
	fctx, cancel := context.WithCancel(ctx)
	//nolint:gosec // the arguments are the benchmark's own kubeconfig and target
	cmd := exec.CommandContext(fctx, t.kubectl,
		"--kubeconfig", t.Kubeconfig, "-n", namespace,
		"port-forward", target, fmt.Sprintf(":%d", remotePort))
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return 0, nil, fmt.Errorf("pipe port-forward stdout: %w", err)
	}
	cmd.Stderr = cmd.Stdout // interleave kubectl's errors with its banner
	if err := cmd.Start(); err != nil {
		cancel()
		return 0, nil, fmt.Errorf("start kubectl port-forward %s/%s: %w", namespace, target, err)
	}
	stop := func() {
		cancel()
		_ = cmd.Wait()
	}

	ports := make(chan int, 1)
	fails := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			if m := forwardingLine.FindStringSubmatch(scanner.Text()); m != nil {
				port, _ := strconv.Atoi(m[1])
				ports <- port
				break
			}
		}
		// Drain so kubectl never blocks on a full pipe; report EOF-before-
		// banner as the failure it is.
		_, _ = io.Copy(io.Discard, stdout)
		fails <- fmt.Errorf("kubectl port-forward %s/%s exited before announcing a local port", namespace, target)
	}()

	select {
	case port := <-ports:
		return port, stop, nil
	case err := <-fails:
		stop()
		return 0, nil, err
	case <-time.After(30 * time.Second):
		stop()
		return 0, nil, fmt.Errorf("kubectl port-forward %s/%s did not come up within 30s", namespace, target)
	case <-ctx.Done():
		stop()
		return 0, nil, ctx.Err()
	}
}
