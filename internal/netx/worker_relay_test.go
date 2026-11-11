package netx

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestWorkerRelayUsesBinaryStreamAndSurfacesRefusal(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer pilot-test" {
			http.Error(w, "not authorized", http.StatusUnauthorized)
			return
		}
		if r.URL.Query().Get("target") != "accounts.firefox.com:443" {
			http.Error(w, "not allowed", http.StatusForbidden)
			return
		}
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		for {
			kind, data, err := ws.ReadMessage()
			if err != nil || kind != websocket.BinaryMessage || len(data) > workerFrameSize {
				return
			}
			if err := ws.WriteMessage(kind, data); err != nil {
				return
			}
		}
	}))
	defer server.Close()
	endpoint, err := url.Parse("wss" + strings.TrimPrefix(server.URL, "https"))
	if err != nil {
		t.Fatal(err)
	}
	dial := workerDialer(*endpoint, websocket.Dialer{
		TLSClientConfig: server.Client().Transport.(*http.Transport).TLSClientConfig,
	}, "pilot-test", false)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if c, err := dial(ctx, "tcp", "example.com:443"); err == nil {
		c.Close()
		t.Fatal("forbidden destination opened a connection")
	} else if !strings.Contains(err.Error(), "HTTP 403") {
		t.Fatal(err)
	}
	conn, err := dial(ctx, "tcp", "accounts.firefox.com:443")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	payload := bytes.Repeat([]byte{0, 0xff, 1}, workerFrameSize)
	if _, err := conn.Write(payload); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, got); err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("binary stream did not round trip: %v", err)
	}
}

func TestManagedWorkerDoesNotSendPilotKey(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/managed" || r.URL.Query().Get("target") != "vpn.mozilla.org:443" ||
			r.Header.Get("Authorization") != "" || r.Header.Get("Origin") != "" {
			http.Error(w, "unexpected handshake", http.StatusForbidden)
			return
		}
		ws, err := upgrader.Upgrade(w, r, nil)
		if err == nil {
			ws.Close()
		}
	}))
	defer server.Close()
	endpoint, err := url.Parse("wss" + strings.TrimPrefix(server.URL, "https") + "/managed")
	if err != nil {
		t.Fatal(err)
	}
	dial := workerDialer(*endpoint, websocket.Dialer{
		TLSClientConfig: server.Client().Transport.(*http.Transport).TLSClientConfig,
	}, "private-key", true)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	conn, err := dial(ctx, "tcp", "vpn.mozilla.org:443")
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
}
