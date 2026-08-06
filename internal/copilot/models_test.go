package copilot

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func Test_parseModesFromJS_extractsConversationModeCatalog(t *testing.T) {
	js := `var rR=["chat"];var f1=[...rR,"smart","study","reasoning","research","computerUse","search","coco","browserAction","analyst","researcher"];Qi=H(f1)`
	got := parseModesFromJS(js)
	want := []string{
		"smart", "study", "reasoning", "research", "computeruse", "search",
		"coco", "browseraction", "analyst", "researcher",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("parseModesFromJS() = %v, want %v", got, want)
	}
}

func Test_parseModesFromJS_rejectsNoiseWithoutMarkers(t *testing.T) {
	js := `["foo","bar","baz","qux"]`
	if got := parseModesFromJS(js); got != nil {
		t.Fatalf("parseModesFromJS() = %v, want nil", got)
	}
}

func TestModelCatalog_ListModes_fetchesAndCaches(t *testing.T) {
	var homeHits, jsHits atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		homeHits.Add(1)
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><script src="/static/cmc/assets/entry-ssr-test.js"></script>`))
	})
	mux.HandleFunc("/static/cmc/assets/entry-ssr-test.js", func(w http.ResponseWriter, r *http.Request) {
		jsHits.Add(1)
		w.Header().Set("Content-Type", "application/javascript")
		_, _ = w.Write([]byte(`f1=["smart","study","reasoning","coco","search"]`))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	catalog := newModelCatalog(server.Client(), server.URL, time.Hour)
	ctx := context.Background()

	got, err := catalog.ListModes(ctx)
	if err != nil {
		t.Fatalf("ListModes() error = %v", err)
	}
	want := []string{"smart", "study", "reasoning", "coco", "search"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("ListModes() = %v, want %v", got, want)
	}
	if homeHits.Load() != 1 || jsHits.Load() != 1 {
		t.Fatalf("hits home=%d js=%d, want 1/1", homeHits.Load(), jsHits.Load())
	}

	got2, err := catalog.ListModes(ctx)
	if err != nil {
		t.Fatalf("ListModes() cache error = %v", err)
	}
	if strings.Join(got2, ",") != strings.Join(want, ",") {
		t.Fatalf("ListModes() cache = %v, want %v", got2, want)
	}
	if homeHits.Load() != 1 || jsHits.Load() != 1 {
		t.Fatalf("cache should not refetch; hits home=%d js=%d", homeHits.Load(), jsHits.Load())
	}
}

func TestModelCatalog_ListModes_usesStaleOnRefreshFailure(t *testing.T) {
	var failJS atomic.Bool
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<script src="/bundle.js"></script>`))
	})
	mux.HandleFunc("/bundle.js", func(w http.ResponseWriter, r *http.Request) {
		if failJS.Load() {
			http.Error(w, "nope", http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(`["smart","reasoning","coco"]`))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	catalog := newModelCatalog(server.Client(), server.URL, time.Nanosecond)
	ctx := context.Background()

	first, err := catalog.ListModes(ctx)
	if err != nil {
		t.Fatalf("first ListModes() error = %v", err)
	}
	if strings.Join(first, ",") != "smart,reasoning,coco" {
		t.Fatalf("first ListModes() = %v", first)
	}

	failJS.Store(true)
	time.Sleep(time.Millisecond)
	second, err := catalog.ListModes(ctx)
	if err != nil {
		t.Fatalf("stale ListModes() error = %v", err)
	}
	if strings.Join(second, ",") != "smart,reasoning,coco" {
		t.Fatalf("stale ListModes() = %v, want previous catalog", second)
	}
}

func TestModelCatalog_ListModes_fallsBackToSmartWhenEmpty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)

	catalog := newModelCatalog(server.Client(), server.URL, time.Hour)
	got, err := catalog.ListModes(context.Background())
	if err != nil {
		t.Fatalf("ListModes() error = %v", err)
	}
	if strings.Join(got, ",") != "smart" {
		t.Fatalf("ListModes() = %v, want [smart]", got)
	}
}
