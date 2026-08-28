// Command tokenswim-supervisor is the gateway image's entrypoint. It runs
// tokenswim-gateway and, when the deployment has configured one, the Proof-pool
// relay of ADR 0039 beside it.
//
// Two processes in one image rather than two containers, because the relay has
// to answer on loopback: `relayProofOrigin` is a single global string that the
// Mesh node and every Container are addressed at, so the Container shape has to
// make http://127.0.0.1:9000 mean the relay the same way the Mesh shape's
// shared network namespace does. The image is distroless and has no shell, so
// there is nothing smaller than a binary that can start two things.
//
// It is a supervisor only in that it starts them and dies with the first one
// that stops. There is deliberately no restart logic: replacing a container
// that stopped working is the orchestrator's job, and a container that half
// works — a gateway serving traffic while the address its Proof-pool
// credentials point at is dead — is worse than one that is visibly gone.
package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
)

const (
	gatewayBinary = "/tokenswim-gateway"
	proverBinary  = "/usr/local/bin/prover"

	// Written by this process rather than mounted: a Cloudflare Container has
	// no volumes, and the two values below only reach it as environment.
	relayDir = "/tmp/tokenswim-relay"

	// Fixed, and the same address apps/prover/compose.yaml gives the Mesh
	// sidecar. See docs/ops/relay-proof-deployment.md.
	relayListen = "127.0.0.1:9000"
)

func main() {
	if err := run(); err != nil {
		log.Printf("tokenswim-supervisor: %v", err)
		os.Exit(1)
	}
}

func run() error {
	commands := []*exec.Cmd{exec.Command(gatewayBinary)}

	relay, errRelay := relayCommand()
	if errRelay != nil {
		return errRelay
	}
	if relay != nil {
		commands = append(commands, relay)
	}

	exited := make(chan error, len(commands))
	for _, command := range commands {
		command.Stdout = os.Stdout
		command.Stderr = os.Stderr
		if errStart := command.Start(); errStart != nil {
			terminate(commands)
			return fmt.Errorf("start %s: %w", command.Path, errStart)
		}
		go func(c *exec.Cmd) { exited <- c.Wait() }(command)
	}

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)

	select {
	case errExit := <-exited:
		terminate(commands)
		if errExit != nil {
			return fmt.Errorf("child process exited: %w", errExit)
		}
		return errors.New("child process exited cleanly; neither of them is meant to return")
	case sig := <-signals:
		log.Printf("tokenswim-supervisor: %s, stopping children", sig)
		terminate(commands)
		return nil
	}
}

// terminate asks every still-running child to stop. It does not wait: this
// process is PID 1 in the container's namespace, so whatever ignores the signal
// is killed when it exits anyway.
func terminate(commands []*exec.Cmd) {
	for _, command := range commands {
		if command.Process == nil {
			continue
		}
		if errSignal := command.Process.Signal(syscall.SIGTERM); errSignal != nil && !errors.Is(errSignal, os.ErrProcessDone) {
			log.Printf("tokenswim-supervisor: signal %s: %v", command.Path, errSignal)
		}
	}
}

// relayCommand builds the relay's command line, or returns nil when no relay is
// configured — which is the default, and what an ordinary gateway deployment
// looks like.
//
// PROVER_RELAY_SECRET is the switch. Once it is set the rest is required and a
// missing value fails the whole container at startup, rather than at the first
// Proof-pool request: by then the Worker has already written this address into
// a credential's base_url, and a relay that is not listening fails those
// Requests with nothing to say about why.
func relayCommand() (*exec.Cmd, error) {
	if strings.TrimSpace(os.Getenv("PROVER_RELAY_SECRET")) == "" {
		return nil, nil
	}

	var missing []string
	value := func(name string) string {
		v := strings.TrimSpace(os.Getenv(name))
		if v == "" {
			missing = append(missing, name)
		}
		return v
	}
	secret := value("PROVER_RELAY_SECRET")
	upstreams := value("PROVER_UPSTREAMS_JSON")
	ingest := value("PROVER_INGEST")
	attestor := value("PROVER_ATTESTOR")
	attestorVersion := value("PROVER_ATTESTOR_VERSION")
	registry := value("PROVER_REGISTRY")
	// Not passed on the command line: the prover reads it from the environment
	// it inherits, because argv is world-readable and this is the identity
	// every claim is filed under.
	value("PROVER_PRIVATE_KEY")
	if len(missing) > 0 {
		return nil, fmt.Errorf("PROVER_RELAY_SECRET is set, so the relay starts, but %s %s unset", strings.Join(missing, ", "), plural(len(missing)))
	}

	secretPath, errSecret := writeRelayFile("relay.secret", secret)
	if errSecret != nil {
		return nil, errSecret
	}
	upstreamsPath, errUpstreams := writeRelayFile("upstreams.json", upstreams)
	if errUpstreams != nil {
		return nil, errUpstreams
	}

	return exec.Command(proverBinary,
		"-serve",
		"-listen="+relayListen,
		"-secret-file="+secretPath,
		"-upstreams="+upstreamsPath,
		"-ingest="+ingest,
		"-attestor="+attestor,
		"-attestor-version="+attestorVersion,
		"-registry="+registry,
		// No -circuits, so no -work: this is ADR 0039's `lite` shape. The
		// Container has neither the 50 MB of proving keys nor the one to three
		// seconds of CPU a claim costs, so it spools every session and a
		// deployment that can hold them finishes the rows.
		//
		// Two sessions at once, not the binary's default of eight. The bound
		// times -max-session-bytes (8 MiB) is what a witnessed session can hold
		// in memory, and a `lite` instance has 256 MiB for this and the gateway
		// together. Past the ceiling a request is served unwitnessed, which is
		// the recorded failure. Raise it with the instance_type in
		// wrangler.jsonc, not on its own.
		"-max-inflight=2",
	), nil
}

func plural(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}

// writeRelayFile puts one environment value on disk for the relay to read.
// 0600 because the binary refuses a secret any group or other can read, and
// because a file is the one channel that keeps these two values out of `ps`.
func writeRelayFile(name, content string) (string, error) {
	if errDir := os.MkdirAll(relayDir, 0o700); errDir != nil {
		return "", fmt.Errorf("create %s: %w", relayDir, errDir)
	}
	path := relayDir + "/" + name
	if errWrite := os.WriteFile(path, []byte(content), 0o600); errWrite != nil {
		return "", fmt.Errorf("write %s: %w", path, errWrite)
	}
	return path, nil
}
