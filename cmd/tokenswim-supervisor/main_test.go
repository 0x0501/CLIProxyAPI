package main

import (
	"os"
	"slices"
	"strings"
	"testing"
)

func TestRelayCommandAbsentWithoutASecret(t *testing.T) {
	t.Setenv("PROVER_RELAY_SECRET", "")

	relay, err := relayCommand()
	if err != nil {
		t.Fatalf("relayCommand returned error: %v", err)
	}
	if relay != nil {
		t.Fatalf("relayCommand = %v, want nil for an ordinary gateway deployment", relay.Args)
	}
}

func TestRelayCommandRefusesAHalfConfiguredRelay(t *testing.T) {
	t.Setenv("PROVER_RELAY_SECRET", "s3cret")
	t.Setenv("PROVER_UPSTREAMS_JSON", `{"codex":"https://chatgpt.com/backend-api/codex"}`)
	t.Setenv("PROVER_INGEST", "https://tokenswim.example")
	t.Setenv("PROVER_ATTESTOR", "ws://attestor:8001/ws")
	t.Setenv("PROVER_ATTESTOR_VERSION", "")
	t.Setenv("PROVER_REGISTRY", "https://registry.example.net")
	t.Setenv("PROVER_PRIVATE_KEY", "0xabc")

	relay, err := relayCommand()
	if err == nil {
		t.Fatalf("relayCommand = %v, want an error naming the unset variable", relay.Args)
	}
	if !strings.Contains(err.Error(), "PROVER_ATTESTOR_VERSION") {
		t.Errorf("error = %q, want it to name PROVER_ATTESTOR_VERSION", err)
	}
}

func TestRelayCommandIsTheLiteSpoolOnlyShape(t *testing.T) {
	t.Setenv("PROVER_RELAY_SECRET", "s3cret")
	t.Setenv("PROVER_UPSTREAMS_JSON", `{"codex":"https://chatgpt.com/backend-api/codex"}`)
	t.Setenv("PROVER_INGEST", "https://tokenswim.example")
	t.Setenv("PROVER_ATTESTOR", "ws://attestor:8001/ws")
	t.Setenv("PROVER_ATTESTOR_VERSION", "attestor-core@abc+overlay@def")
	t.Setenv("PROVER_REGISTRY", "https://registry.example.net")
	t.Setenv("PROVER_PRIVATE_KEY", "0xabc")

	relay, err := relayCommand()
	if err != nil {
		t.Fatalf("relayCommand returned error: %v", err)
	}

	for _, want := range []string{
		"-serve",
		"-listen=" + relayListen,
		"-ingest=https://tokenswim.example",
		"-attestor-version=attestor-core@abc+overlay@def",
		"-max-inflight=2",
	} {
		if !slices.Contains(relay.Args, want) {
			t.Errorf("relay args %v missing %q", relay.Args, want)
		}
	}
	// No circuits means SpoolOnly, which is the whole of the Container shape.
	for _, arg := range relay.Args {
		if strings.HasPrefix(arg, "-circuits=") || arg == "-work" {
			t.Errorf("relay args %v carry %q; a lite Container spools and proves nothing", relay.Args, arg)
		}
	}

	// The secret reaches the relay as a file the binary will accept: it refuses
	// one any group or other can read.
	info, errStat := os.Stat(relayDir + "/relay.secret")
	if errStat != nil {
		t.Fatalf("stat relay.secret: %v", errStat)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("relay.secret mode = %o, want 600", perm)
	}
	if err := os.RemoveAll(relayDir); err != nil {
		t.Errorf("clean up %s: %v", relayDir, err)
	}
}
