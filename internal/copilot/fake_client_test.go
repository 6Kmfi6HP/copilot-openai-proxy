package copilot

import (
	"crypto/tls"
	"net/http/httptest"
	"strings"
	"testing"
)

func newClientForTestServer(t *testing.T, server *httptest.Server, cfg ClientConfig) *Client {
	t.Helper()

	warmSessions := cfg.WarmSessions
	cfg.WarmSessions = 0
	cfg.StartURL = server.URL + "/c/api/start"
	cfg.WSURL = "wss" + strings.TrimPrefix(server.URL, "https") + "/c/api/chat"

	client, err := NewClient(cfg)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	t.Cleanup(func() {
		closeTestClient(t, client)
	})

	client.http.Transport = server.Client().Transport
	client.wsDialer.Proxy = nil
	client.wsDialer.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}

	if warmSessions < 0 {
		warmSessions = 0
	}
	if warmSessions > client.maxSessions {
		warmSessions = client.maxSessions
	}
	if warmSessions > 0 {
		client.warmSessions = warmSessions
		client.sessionMgr = newSessionManagerWithWarmPool(client.maxSessions, warmSessions, client.startAnon)
		client.startJanitor()
	}

	return client
}
