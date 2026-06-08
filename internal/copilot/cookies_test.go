package copilot

import (
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"testing"
)

func Test_collectCookies_includesAllStartResponseCookies_whenBuildingWebSocketHeader(t *testing.T) {
	cookies := []*http.Cookie{
		{Name: "__Host-copilot-anon", Value: "anon-token"},
		{Name: "__cf_bm", Value: "cloudflare-token"},
	}

	got := collectCookies(cookies)

	want := "__Host-copilot-anon=anon-token; __cf_bm=cloudflare-token"
	if got != want {
		t.Fatalf("collectCookies() = %q, want %q", got, want)
	}
}

func Test_cookiesWithJarFallback_usesJarCookies_whenStartOmitsSetCookie(t *testing.T) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New returned error: %v", err)
	}
	parsed, err := url.Parse(copilotStartURL)
	if err != nil {
		t.Fatalf("url.Parse returned error: %v", err)
	}
	jar.SetCookies(parsed, []*http.Cookie{
		{Name: "__Host-copilot-anon", Value: "anon-token"},
		{Name: "__cf_bm", Value: "cloudflare-token"},
	})

	got := cookiesWithJarFallback(jar, copilotStartURL, nil)

	if findCookie(got, CookieAnon) == nil {
		t.Fatalf("cookiesWithJarFallback() did not return %s: %v", CookieAnon, got)
	}
	if findCookie(got, "__cf_bm") == nil {
		t.Fatalf("cookiesWithJarFallback() did not return __cf_bm: %v", got)
	}
}
