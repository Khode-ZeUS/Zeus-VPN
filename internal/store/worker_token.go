package store

import (
	"encoding/json"
	"net/url"
	"sync"
)

const workerTokenFile = "worker-pilot.bin"

var workerTokenMu sync.Mutex

type workerToken struct {
	Endpoint string
	Token    string
}

func LoadWorkerPilotToken(endpoint string) string {
	workerTokenMu.Lock()
	defer workerTokenMu.Unlock()
	var stored workerToken
	if readProtected(workerTokenFile, &stored) && workerTokenEndpoint(stored.Endpoint) == workerTokenEndpoint(endpoint) {
		return stored.Token
	}
	return ""
}

func SaveWorkerPilotToken(endpoint, token string) error {
	workerTokenMu.Lock()
	defer workerTokenMu.Unlock()
	raw, err := json.Marshal(workerToken{Endpoint: workerTokenEndpoint(endpoint), Token: token})
	if err != nil {
		return err
	}
	encrypted, err := protect(raw)
	if err != nil {
		return err
	}
	return writeAtomic(workerTokenFile, encrypted)
}

func workerTokenEndpoint(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err == nil && u.Scheme == "wss" && u.Host != "" && u.Path == "" {
		u.Path = "/"
		return u.String()
	}
	return endpoint
}
