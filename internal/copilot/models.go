package copilot

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	defaultModelsBaseURL = copilotOrigin
	defaultModelsTTL     = time.Hour
	maxModelIDLen        = 64
)

var (
	entrySSRScriptRe = regexp.MustCompile(`(?i)(?:src|href)=["']([^"']*entry-ssr[^"']*\.js[^"']*)["']`)
	anyScriptSrcRe   = regexp.MustCompile(`(?i)src=["']([^"']+\.js[^"']*)["']`)
	// Matches a dense quoted-identifier catalog that includes smart + reasoning (+ preferably coco).
	modeCatalogRe = regexp.MustCompile(`(?:"[a-zA-Z][a-zA-Z0-9]{0,63}"\s*,\s*)*"smart"(?:\s*,\s*"[a-zA-Z][a-zA-Z0-9]{0,63}"){2,}`)
	quotedIdentRe = regexp.MustCompile(`"([a-zA-Z][a-zA-Z0-9]{0,63})"`)
)

// ModelCatalog fetches Copilot conversation modes from the public web bundle.
type ModelCatalog struct {
	http    *http.Client
	baseURL string
	ttl     time.Duration

	mu        sync.RWMutex
	modes     []string
	fetchedAt time.Time
}

func modelsBaseURLFromStart(startURL string) string {
	parsed, err := url.Parse(startURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return defaultModelsBaseURL
	}
	return parsed.Scheme + "://" + parsed.Host
}

func newModelCatalog(httpClient *http.Client, baseURL string, ttl time.Duration) *ModelCatalog {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	if strings.TrimSpace(baseURL) == "" {
		baseURL = defaultModelsBaseURL
	}
	if ttl <= 0 {
		ttl = defaultModelsTTL
	}
	return &ModelCatalog{
		http:    httpClient,
		baseURL: strings.TrimRight(baseURL, "/"),
		ttl:     ttl,
	}
}

// ListModes returns cached Copilot conversation mode IDs, refreshing when stale.
// On refresh failure it returns the last successful list, or ["smart"] if none.
func (c *ModelCatalog) ListModes(ctx context.Context) ([]string, error) {
	if c == nil {
		return []string{"smart"}, nil
	}

	c.mu.RLock()
	if len(c.modes) > 0 && time.Since(c.fetchedAt) < c.ttl {
		out := append([]string(nil), c.modes...)
		c.mu.RUnlock()
		return out, nil
	}
	stale := append([]string(nil), c.modes...)
	c.mu.RUnlock()

	modes, err := c.refresh(ctx)
	if err != nil {
		if len(stale) > 0 {
			log.Printf("copilot models catalog refresh failed; using stale cache err=%v", err)
			return stale, nil
		}
		log.Printf("copilot models catalog refresh failed; using fallback smart err=%v", err)
		return []string{"smart"}, nil
	}
	return append([]string(nil), modes...), nil
}

func (c *ModelCatalog) refresh(ctx context.Context) ([]string, error) {
	homeHTML, err := c.getBytes(ctx, c.baseURL+"/")
	if err != nil {
		return nil, fmt.Errorf("fetch copilot home: %w", err)
	}
	scriptURLs := discoverScriptURLs(c.baseURL, string(homeHTML))
	if len(scriptURLs) == 0 {
		return nil, fmt.Errorf("no javascript bundles found on copilot home page")
	}

	var lastErr error
	for _, scriptURL := range scriptURLs {
		js, err := c.getBytes(ctx, scriptURL)
		if err != nil {
			lastErr = fmt.Errorf("fetch %s: %w", scriptURL, err)
			continue
		}
		modes := parseModesFromJS(string(js))
		if len(modes) == 0 {
			lastErr = fmt.Errorf("no conversation modes in %s", scriptURL)
			continue
		}
		c.mu.Lock()
		c.modes = modes
		c.fetchedAt = time.Now()
		c.mu.Unlock()
		log.Printf("copilot models catalog refreshed count=%d source=%s", len(modes), scriptURL)
		return modes, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no conversation modes found")
	}
	return nil, lastErr
}

func (c *ModelCatalog) getBytes(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", copilotUserAgent)
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", acceptLanguage)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	return body, nil
}

func discoverScriptURLs(baseURL, html string) []string {
	seen := make(map[string]struct{})
	var out []string
	add := func(raw string) {
		abs := resolveURL(baseURL, raw)
		if abs == "" {
			return
		}
		if _, ok := seen[abs]; ok {
			return
		}
		seen[abs] = struct{}{}
		out = append(out, abs)
	}

	for _, m := range entrySSRScriptRe.FindAllStringSubmatch(html, -1) {
		if len(m) > 1 {
			add(m[1])
		}
	}
	// Prefer entry-ssr first; then other scripts as fallback.
	for _, m := range anyScriptSrcRe.FindAllStringSubmatch(html, -1) {
		if len(m) > 1 {
			add(m[1])
		}
	}
	return out
}

func resolveURL(baseURL, ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ""
	}
	base, err := url.Parse(baseURL + "/")
	if err != nil {
		return ""
	}
	u, err := base.Parse(ref)
	if err != nil {
		return ""
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return ""
	}
	return u.String()
}

func parseModesFromJS(js string) []string {
	match := modeCatalogRe.FindString(js)
	if match == "" {
		// Fallback: locate a window containing the characteristic trio.
		smart := strings.Index(js, `"smart"`)
		reasoning := strings.Index(js, `"reasoning"`)
		coco := strings.Index(js, `"coco"`)
		if smart < 0 || reasoning < 0 || coco < 0 {
			return nil
		}
		start := smart
		if reasoning < start {
			start = reasoning
		}
		if coco < start {
			start = coco
		}
		end := smart
		if reasoning > end {
			end = reasoning
		}
		if coco > end {
			end = coco
		}
		// Expand a bit to capture neighboring quoted identifiers.
		start = max(0, start-256)
		end = min(len(js), end+256)
		match = js[start:end]
	}

	raw := quotedIdentRe.FindAllStringSubmatch(match, -1)
	if len(raw) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(raw))
	out := make([]string, 0, len(raw))
	hasSmart, hasReasoning := false, false
	for _, m := range raw {
		id := strings.ToLower(strings.TrimSpace(m[1]))
		if id == "" || len(id) > maxModelIDLen {
			continue
		}
		if !isPlausibleModeID(id) {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
		if id == "smart" {
			hasSmart = true
		}
		if id == "reasoning" {
			hasReasoning = true
		}
	}
	if !hasSmart || !hasReasoning || len(out) < 3 {
		return nil
	}
	return out
}

func isPlausibleModeID(id string) bool {
	if id == "" {
		return false
	}
	// Drop obvious non-mode noise that can appear near the catalog.
	switch id {
	case "type", "event", "ai", "text", "image", "null", "true", "false", "enum":
		return false
	}
	for _, r := range id {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

// ListModels returns Copilot conversation modes as OpenAI-compatible model IDs.
func (c *Client) ListModels(ctx context.Context) ([]string, error) {
	if c == nil || c.modelCatalog == nil {
		return []string{"smart"}, nil
	}
	return c.modelCatalog.ListModes(ctx)
}
