package plugin

// RV2 (#140) regression tests for the /proxy response path.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime/debug"
	"strings"
	"testing"
	"time"
)

// TestProxyHostileParseRefused413 covers RV2: pages that make an unbounded
// HTML parser build a huge tree, do quadratic work or hold a huge tag are
// refused with 413 (none of the upstream bytes) and promptly, through /proxy.
// The memory side is measured in rewrite_memory_test.go.
func TestProxyHostileParseRefused413(t *testing.T) {
	pages := adversarialPages(1 << 20)
	bigTag := "<p" + strings.Repeat(" a=1", 1) + func() string {
		var b strings.Builder
		for i := 0; b.Len() < htmlParseLimits.MaxTokenBytes+1024; i++ {
			fmt.Fprintf(&b, " %x", i)
		}
		return b.String()
	}() + ">"
	cases := map[string]string{
		"formatting reconstruction": string(pages["reconstruction"]),
		"quadratic text appends":    string(adversarialPages(4 << 20)["text-appends"]),
		"tag over the token limit":  bigTag,
	}
	for name, page := range cases {
		t.Run(name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				_, _ = io.WriteString(w, page)
			}))
			defer upstream.Close()
			cfg := settingsWith(allowExample(DomainOptions{}))
			cfg.MaxResponseBytes = 8 << 20
			p := newTestHandler(t, cfg, upstream)

			start := time.Now()
			rec := doProxy(p, "http://example.com/hostile")
			if rec.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("status = %d, want 413", rec.Code)
			}
			if strings.Contains(rec.Body.String(), "<p") {
				t.Errorf("413 body leaks upstream markup: %.100q", rec.Body.String())
			}
			if d := time.Since(start); d > 5*time.Second {
				t.Errorf("refusal took %v, want a prompt stop", d)
			}
		})
	}
}

// TestRewriteSlotsBoundConcurrency covers RV2: at most
// maxConcurrentHTMLRewrites HTML rewrites run at once. A request that cannot
// get a slot before its deadline is a 504; a freed slot is reused.
func TestRewriteSlotsBoundConcurrency(t *testing.T) {
	var held []func()
	for i := 0; i < maxConcurrentHTMLRewrites; i++ {
		release, err := acquireRewriteSlot(context.Background())
		if err != nil {
			t.Fatalf("slot %d: %v", i, err)
		}
		held = append(held, release)
	}
	releaseAll := func() {
		for _, r := range held {
			r()
		}
		held = nil
	}
	defer releaseAll()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := acquireRewriteSlot(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("acquire with all slots held: err = %v, want DeadlineExceeded", err)
	}

	// Through /proxy: the HTML response waits for a slot until the request
	// deadline, then fails with 504 and none of the page.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, "<html><body>slot-test</body></html>")
	}))
	defer upstream.Close()
	cfg := settingsWith(allowExample(DomainOptions{}))
	cfg.RequestTimeoutSec = 1
	p := newTestHandler(t, cfg, upstream)
	if rec := doProxy(p, "http://example.com/"); rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("status with all slots held = %d, want 504", rec.Code)
	}

	// Non-HTML needs no slot.
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, "plain")
	}))
	defer plain.Close()
	if rec := doProxy(newTestHandler(t, cfg, plain), "http://example.com/"); rec.Code != http.StatusOK {
		t.Fatalf("non-HTML with all slots held = %d, want 200", rec.Code)
	}

	releaseAll()
	if rec := doProxy(p, "http://example.com/"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "slot-test") {
		t.Fatalf("after release: status %d body %q, want 200 with the page", rec.Code, rec.Body.String())
	}
}

// TestRenderLimitedStopsAtLimit covers RV2: rendering stops with
// errResponseTooLarge as soon as the output would pass the limit; the buffer
// never holds more than the limit.
func TestRenderLimitedStopsAtLimit(t *testing.T) {
	var buf bytes.Buffer
	w := &limitedWriter{buf: &buf, max: 10}
	if n, err := w.Write([]byte("0123456789")); n != 10 || err != nil {
		t.Fatalf("write to the limit = %d, %v", n, err)
	}
	if n, err := w.Write([]byte("x")); n != 0 || !errors.Is(err, errResponseTooLarge) {
		t.Fatalf("write past the limit = %d, %v; want 0, errResponseTooLarge", n, err)
	}
	if buf.Len() != 10 {
		t.Fatalf("buffer holds %d bytes, want 10", buf.Len())
	}

	doc := "<html><body>" + strings.Repeat("<p>hello</p>", 1000) + "</body></html>"
	out, err := rewriteHTMLLimited([]byte(doc), mustParseURL(t, examplePage), "text/html", nil, int64(len(doc)))
	if !errors.Is(err, errResponseTooLarge) || out != nil {
		t.Fatalf("rewrite over the limit = %d bytes, %v; want errResponseTooLarge", len(out), err)
	}
	if out, err = rewriteHTMLLimited([]byte(doc), mustParseURL(t, examplePage), "text/html", nil, int64(len(doc))*2); err != nil || !strings.Contains(string(out), "hello") {
		t.Fatalf("rewrite within the limit: %v", err)
	}
}

// TestApplyDefaultMemoryLimit covers RV2: the plugin sets DefaultMemoryLimit
// as the soft memory limit unless GOMEMLIMIT is set.
func TestApplyDefaultMemoryLimit(t *testing.T) {
	prev := debug.SetMemoryLimit(-1)
	t.Cleanup(func() { debug.SetMemoryLimit(prev) })

	t.Setenv("GOMEMLIMIT", "1GiB")
	debug.SetMemoryLimit(1 << 30)
	if got := ApplyDefaultMemoryLimit(); got != 1<<30 || debug.SetMemoryLimit(-1) != 1<<30 {
		t.Fatalf("with GOMEMLIMIT set: limit %d, want it left at 1 GiB", got)
	}

	if err := os.Unsetenv("GOMEMLIMIT"); err != nil {
		t.Fatal(err)
	}
	if got := ApplyDefaultMemoryLimit(); got != DefaultMemoryLimit || debug.SetMemoryLimit(-1) != DefaultMemoryLimit {
		t.Fatalf("without GOMEMLIMIT: limit %d, want %d", got, DefaultMemoryLimit)
	}
}

// rv2FailingResolver is a security.Resolver whose lookup fails with an error
// that names an internal DNS server address.
type rv2FailingResolver struct{}

func (rv2FailingResolver) LookupIPAddr(context.Context, string) ([]net.IPAddr, error) {
	return nil, errors.New("lookup example.com on 192.168.77.1:53: no such host")
}

// TestUpstreamFailureDetailOnlyInServerLog covers RV1-8: when the upstream
// cannot be reached, /check-frameable and /proxy tell the client only the
// class of failure. The dial error, which names resolved or internal IP
// addresses, is written to the server log only.
func TestUpstreamFailureDetailOnlyInServerLog(t *testing.T) {
	cases := []struct {
		name       string
		transport  func() http.RoundTripper
		leak       string
		wantReason string
		proxyCode  int
	}{
		{
			name:       "blocked resolved address",
			transport:  func() http.RoundTripper { return tc1ResolverTransport(tc1NewStubResolver("10.20.30.40")) },
			leak:       "10.20.30.40",
			wantReason: "upstream unreachable: the host resolves to a blocked address",
			proxyCode:  http.StatusForbidden,
		},
		{
			name:       "resolve failure",
			transport:  func() http.RoundTripper { return tc1ResolverTransport(rv2FailingResolver{}) },
			leak:       "192.168.77.1",
			wantReason: "upstream unreachable: the host could not be resolved",
			proxyCode:  http.StatusBadGateway,
		},
		{
			name: "connection refused",
			transport: func() http.RoundTripper {
				return &http.Transport{DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
					var d net.Dialer
					return d.DialContext(ctx, network, "127.0.0.1:1")
				}}
			},
			leak:       "127.0.0.1",
			wantReason: "upstream unreachable: the connection failed",
			proxyCode:  http.StatusBadGateway,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := newProxyHandler(settingsWith(allowExample(DomainOptions{})))
			p.transport = tc.transport()
			logs := &ac19CapturingLogger{}
			p.logger = logs

			got := decodeFrameable(t, doCheckFrameable(checkFrameableHandler{p: p}, "http://example.com/page"))
			if got.Reason != tc.wantReason || got.RecommendedMode != recommendedModeProxy {
				t.Errorf("/check-frameable verdict = %+v, want reason %q", got, tc.wantReason)
			}

			rec := doProxy(p, "http://example.com/page")
			if rec.Code != tc.proxyCode {
				t.Errorf("/proxy status = %d, want %d", rec.Code, tc.proxyCode)
			}
			if strings.Contains(rec.Body.String(), tc.leak) {
				t.Errorf("/proxy body %q contains %s", rec.Body.String(), tc.leak)
			}

			var logged []string
			for _, w := range logs.warnEntries() {
				logged = append(logged, w.msg+": "+fmt.Sprint(w.kv["error"]))
			}
			all := strings.Join(logged, "\n")
			if !strings.Contains(all, "check-frameable fetch failed") || !strings.Contains(all, "proxy upstream error") {
				t.Errorf("server log = %q, want a warning from each endpoint", all)
			}
			if !strings.Contains(all, tc.leak) {
				t.Errorf("server log = %q, want the error detail (%s)", all, tc.leak)
			}
		})
	}
}

// TestProxyEmptyEncodedBodyPassesThrough covers RV2: a response that declares
// Content-Encoding: gzip but has no body (304, 204, or an empty 200) is not a
// 502. It passes through with its status, an empty body and no
// Content-Encoding. An empty body with an unsupported coding passes too: there
// is nothing to decode.
func TestProxyEmptyEncodedBodyPassesThrough(t *testing.T) {
	cases := []struct {
		name        string
		status      int
		contentType string
		encoding    string
	}{
		{"304 html gzip", http.StatusNotModified, "text/html", "gzip"},
		{"204 gzip", http.StatusNoContent, "text/html", "gzip"},
		{"200 empty html gzip", http.StatusOK, "text/html; charset=utf-8", "gzip"},
		{"200 empty json gzip", http.StatusOK, "application/json", "gzip"},
		{"200 empty html br", http.StatusOK, "text/html", "br"},
		{"304 br", http.StatusNotModified, "text/css", "br"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			upstream := chunkedUpstream(t, tc.status, tc.contentType, tc.encoding, "", nil)
			defer upstream.Close()
			p := newTestHandler(t, settingsWith(allowExample(DomainOptions{})), upstream)

			rec := doProxy(p, "http://example.com/page")
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d (body=%q)", rec.Code, tc.status, rec.Body.String())
			}
			if rec.Body.Len() != 0 {
				t.Errorf("body = %q, want empty", rec.Body.String())
			}
			if ce := rec.Header().Get("Content-Encoding"); ce != "" {
				t.Errorf("Content-Encoding = %q, want none on an empty body", ce)
			}
		})
	}
}
