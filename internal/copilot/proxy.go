package copilot

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

func newProxyFunc(proxyURL string) (func(*http.Request) (*url.URL, error), error) {
	trimmed := strings.TrimSpace(proxyURL)
	if trimmed == "" {
		return http.ProxyFromEnvironment, nil
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return nil, fmt.Errorf("parse proxy url: %w", err)
	}

	return http.ProxyURL(parsed), nil
}
