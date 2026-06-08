package copilot

import (
	"net/http"
	"net/url"
	"strings"
)

func setCommonHeaders(h http.Header) {
	h.Set("User-Agent", copilotUserAgent)
	h.Set("Accept", "application/json")
}

func collectCookies(cookies []*http.Cookie) string {
	pairs := make([]string, 0, len(cookies))
	for _, ck := range cookies {
		if ck == nil || ck.Name == "" || ck.Value == "" {
			continue
		}
		pairs = append(pairs, ck.Name+"="+ck.Value)
	}
	return strings.Join(pairs, "; ")
}

func sessionCookiesUsable(cookies []*http.Cookie) bool {
	return findCookie(cookies, CookieAnon) != nil
}

func cookiesWithJarFallback(jar http.CookieJar, rawURL string, responseCookies []*http.Cookie) []*http.Cookie {
	if sessionCookiesUsable(responseCookies) || jar == nil {
		return responseCookies
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return responseCookies
	}
	jarCookies := jar.Cookies(parsed)
	if !sessionCookiesUsable(jarCookies) {
		return responseCookies
	}
	return jarCookies
}

func findCookie(cookies []*http.Cookie, name string) *http.Cookie {
	for _, ck := range cookies {
		if ck != nil && ck.Name == name && ck.Value != "" {
			return ck
		}
	}
	return nil
}

func cookieNames(cookies []*http.Cookie) string {
	names := make([]string, 0, len(cookies))
	for _, ck := range cookies {
		if ck != nil && ck.Name != "" {
			names = append(names, ck.Name)
		}
	}
	return strings.Join(names, ",")
}
