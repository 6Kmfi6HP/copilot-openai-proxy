package copilot

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"

	xproxy "golang.org/x/net/proxy"
)

type outboundProxy struct {
	// proxyFunc is used for HTTP(S) proxies via Transport.Proxy / Dialer.Proxy.
	// Nil for SOCKS proxies (those dial through dialContext instead).
	proxyFunc func(*http.Request) (*url.URL, error)
	// dialContext tunnels TCP through a SOCKS proxy when set.
	dialContext func(ctx context.Context, network, address string) (net.Conn, error)
	scheme      string
}

func newOutboundProxy(proxyURL string) (*outboundProxy, error) {
	trimmed := strings.TrimSpace(proxyURL)
	if trimmed == "" {
		return &outboundProxy{proxyFunc: http.ProxyFromEnvironment}, nil
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return nil, fmt.Errorf("parse proxy url: %w", err)
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("proxy url %q is missing host; use a full URL like http://host:port or socks5://user:pass@host:port", trimmed)
	}

	scheme := strings.ToLower(parsed.Scheme)
	switch scheme {
	case "http", "https":
		return &outboundProxy{
			proxyFunc: http.ProxyURL(parsed),
			scheme:    scheme,
		}, nil
	case "socks5", "socks5h", "socks":
		// Normalize socks -> socks5 for x/net/proxy.
		if scheme == "socks" {
			parsed.Scheme = "socks5"
			scheme = "socks5"
		}
		dialer, err := xproxy.FromURL(parsed, xproxy.Direct)
		if err != nil {
			return nil, fmt.Errorf("configure socks proxy: %w", err)
		}
		contextDialer, ok := dialer.(xproxy.ContextDialer)
		if !ok {
			return nil, fmt.Errorf("socks proxy dialer does not support DialContext")
		}
		return &outboundProxy{
			dialContext: contextDialer.DialContext,
			scheme:      scheme,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported proxy scheme %q (supported: http, https, socks5, socks5h)", parsed.Scheme)
	}
}

func newProxyFunc(proxyURL string) (func(*http.Request) (*url.URL, error), error) {
	cfg, err := newOutboundProxy(proxyURL)
	if err != nil {
		return nil, err
	}
	if cfg.proxyFunc != nil {
		return cfg.proxyFunc, nil
	}
	// SOCKS dials via DialContext; callers that only probe Proxy should not
	// treat SOCKS as HTTP CONNECT.
	return func(*http.Request) (*url.URL, error) { return nil, nil }, nil
}
