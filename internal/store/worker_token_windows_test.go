package store

import (
	"bytes"
	"os"
	"testing"
)

func TestWorkerPilotTokenIsProtectedAndBoundToEndpoint(t *testing.T) {
	SetDir(t.TempDir())
	t.Cleanup(func() { SetDir("") })
	const endpoint = "wss://relay.example.workers.dev/"
	const token = "test-only-worker-key"
	if err := SaveWorkerPilotToken(endpoint, token); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path(workerTokenFile))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(token)) {
		t.Fatal("pilot key was saved in plaintext")
	}
	if LoadWorkerPilotToken(endpoint) != token {
		t.Fatal("pilot key did not round trip")
	}
	if LoadWorkerPilotToken("wss://relay.example.workers.dev") != token {
		t.Fatal("the root URL without a trailing slash lost the saved key")
	}
	if LoadWorkerPilotToken("wss://other.example.workers.dev/") != "" {
		t.Fatal("another endpoint could read the saved key")
	}
}
