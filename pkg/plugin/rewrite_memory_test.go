package plugin

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"runtime"
	"runtime/debug"
	"runtime/metrics"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend/log"
)

// Memory tests for the HTML rewrite path. A hostile page of
// DefaultMaxResponseBytes can make an unbounded HTML parser build a tree many
// times larger than the page. These tests measure the peak heap while the
// proxy serves such pages and check it against the sizing in
// ai-state/streams/v1-stabilisation/memory-sizing.md.
//
// The heap is sampled, so the measured peak is a lower bound of the true
// peak. The race detector multiplies allocations, so the assertions skip under
// -race; -short skips them too.

// adversarialPages returns hostile pages of about size bytes, each aimed at
// one way a parse or rewrite can grow.
func adversarialPages(size int) map[string][]byte {
	fill := func(prefix, unit string) []byte {
		var b strings.Builder
		b.Grow(size)
		b.WriteString(prefix)
		for b.Len()+len(unit) <= size {
			b.WriteString(unit)
		}
		for b.Len() < size {
			b.WriteByte(' ')
		}
		return []byte(b.String())
	}
	// Formatting elements with distinct attributes, so none is dropped as a
	// duplicate, then "<p>x" repeated: each paragraph clones all 42.
	var reconstruction strings.Builder
	reconstruction.WriteString("<!doctype html><body><p>")
	for _, tag := range []string{"a", "b", "big", "code", "em", "font", "i", "nobr", "s", "small", "strike", "strong", "tt", "u"} {
		for k := 0; k < 3; k++ {
			fmt.Fprintf(&reconstruction, "<%s x0=%d x1=%d x2=%d x3=%d>", tag, k, k, k, k)
		}
	}
	// One tag just under the 2 MiB token limit, with short distinct
	// attributes: the most tokenizer memory one token can take.
	var bigTag strings.Builder
	bigTag.WriteString("<!doctype html><body><p")
	for i := 0; bigTag.Len() < htmlParseLimits.MaxTokenBytes-64; i++ {
		fmt.Fprintf(&bigTag, " %x", i)
	}
	bigTag.WriteString(">")
	// Repeated <body> tags, each with a new attribute: each tag merges all the
	// attributes so far into the first <body>, which is quadratic work.
	var merge strings.Builder
	merge.WriteString("<!doctype html>")
	for i := 0; ; i++ {
		tag := fmt.Sprintf("<body a%d>", i)
		if merge.Len()+len(tag) > size {
			break
		}
		merge.WriteString(tag)
	}
	return map[string][]byte{
		"void-elements":  fill("<!doctype html><body>", "<br>"),
		"attributes":     fill("<!doctype html><body>", "<br a b c d e f g h i j k l m n o p q r s t u v w x y z>"),
		"text-nodes":     fill("<!doctype html><body>", "<p>x"),
		"urls":           fill("<!doctype html><body>", `<img src=x>`),
		"reconstruction": fill(reconstruction.String(), "<p>x"),
		"text-appends":   fill("<!doctype html><body>", "x</q>"),
		"big-tag":        []byte(bigTag.String()),
		// Each "<tr><table>" makes three elements from two tokens: an implied
		// <tbody>, the <tr> and a new <table>.
		"implied-tables":  fill("<!doctype html><body><table>", "<tr><table>"),
		"attribute-merge": []byte(merge.String()),
	}
}

// hostileRewrite is a hostile page and the hide selectors it is requested
// with.
type hostileRewrite struct {
	page []byte
	hide []string
}

// hostileRewrites returns the pages of adversarialPages(size) without hide
// selectors, and "hide-selectors": the largest page of elements the parse
// limits allow, with 20 hide selectors that each match every element. Before
// an element was hidden at most once, each of its 20 matches appended to its
// style attribute, copying the attribute each time.
func hostileRewrites(size int) map[string]hostileRewrite {
	out := make(map[string]hostileRewrite)
	for name, page := range adversarialPages(size) {
		out[name] = hostileRewrite{page: page}
	}
	out["hide-selectors"] = hostileRewrite{
		page: []byte(bareParagraphs(largestElementPage)),
		hide: matchAllSelectors(maxHideSelectors),
	}
	return out
}

// heapSampler records the largest heap-object size seen while it runs.
type heapSampler struct {
	stop chan struct{}
	done chan struct{}
	peak atomic.Uint64
}

const heapObjectsMetric = "/memory/classes/heap/objects:bytes"

func readHeapObjects() uint64 {
	s := []metrics.Sample{{Name: heapObjectsMetric}}
	metrics.Read(s)
	return s[0].Value.Uint64()
}

func startHeapSampler() *heapSampler {
	h := &heapSampler{stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(h.done)
		t := time.NewTicker(200 * time.Microsecond)
		defer t.Stop()
		for {
			if v := readHeapObjects(); v > h.peak.Load() {
				h.peak.Store(v)
			}
			select {
			case <-h.stop:
				return
			case <-t.C:
			}
		}
	}()
	return h
}

func (h *heapSampler) finish() uint64 {
	close(h.stop)
	<-h.done
	if v := readHeapObjects(); v > h.peak.Load() {
		h.peak.Store(v)
	}
	return h.peak.Load()
}

// peakHeapDuring returns the peak heap growth, in bytes, above the post-GC
// baseline while f runs.
func peakHeapDuring(f func()) uint64 {
	runtime.GC()
	base := readHeapObjects()
	s := startHeapSampler()
	f()
	peak := s.finish()
	if peak < base {
		return 0
	}
	return peak - base
}

const mib = 1 << 20

// serveConcurrently serves page through a default-settings proxy handler n
// times at once, with the hide selectors, and returns the status codes.
func serveConcurrently(t *testing.T, page []byte, hide []string, n int) []int {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write(page)
	}))
	defer upstream.Close()

	cfg := settingsWith(allowExample(DomainOptions{}))
	cfg.MaxConcurrentRequests = DefaultMaxConcurrentRequests
	cfg.RequestTimeoutSec = 60
	p := newTestHandler(t, cfg, upstream)
	p.logger = log.NewNullLogger()

	target := proxyPath + "?" + url.Values{"url": {"http://example.com/"}, hideParam: hide}.Encode()
	statuses := make([]int, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			req := httptest.NewRequest(http.MethodGet, target, nil)
			w := &discardResponseWriter{header: http.Header{}}
			p.ServeHTTP(w, req)
			statuses[i] = w.status
		}(i)
	}
	close(start)
	wg.Wait()
	return statuses
}

// discardResponseWriter keeps no body, so the measurement does not include a
// recorder's copy of each response.
type discardResponseWriter struct {
	header http.Header
	status int
}

func (d *discardResponseWriter) Header() http.Header { return d.header }
func (d *discardResponseWriter) WriteHeader(s int) {
	if d.status == 0 {
		d.status = s
	}
}
func (d *discardResponseWriter) Write(b []byte) (int, error) {
	if d.status == 0 {
		d.status = http.StatusOK
	}
	return io.Discard.Write(b)
}

func skipMemoryAssertions(t *testing.T) {
	t.Helper()
	if raceEnabled {
		t.Skip("heap measurements are not meaningful under -race")
	}
	if testing.Short() {
		t.Skip("memory measurements skipped in -short mode")
	}
}

// Budgets checked by the tests below (see memory-sizing.md).
const (
	singleRewriteHeapBudget = 160 * mib
	concurrentHeapBudget    = 300 * mib
)

// TestRewriteMemoryBounded covers RV2 and the CR5 hardening: with the default
// limits, rewriting any hostile page of DefaultMaxResponseBytes, or the
// largest page with 20 hide selectors that match every element, either
// succeeds or is refused as too large, and its peak heap stays within
// singleRewriteHeapBudget.
func TestRewriteMemoryBounded(t *testing.T) {
	skipMemoryAssertions(t)
	withProductionMemoryLimit(t)
	page := mustParseURL(t, "http://example.com/")
	for name, hostile := range hostileRewrites(DefaultMaxResponseBytes) {
		t.Run(name, func(t *testing.T) {
			var rerr error
			start := time.Now()
			peak := peakHeapDuring(func() {
				_, rerr = rewriteHTMLLimited(hostile.page, page, "text/html", hostile.hide, DefaultMaxResponseBytes)
			})
			if rerr != nil && !errors.Is(rerr, errResponseTooLarge) {
				t.Errorf("rewrite error = %v, want success or errResponseTooLarge", rerr)
			}
			if peak > singleRewriteHeapBudget {
				t.Errorf("peak heap %d MiB, want <= %d MiB", peak/mib, singleRewriteHeapBudget/mib)
			}
			t.Logf("peak %d MiB in %v (err=%v)", peak/mib, time.Since(start).Round(time.Millisecond), rerr)
		})
	}
}

// withProductionMemoryLimit runs the test under DefaultMemoryLimit, the soft
// limit the plugin process sets in main, and restores the previous limit.
func withProductionMemoryLimit(t *testing.T) {
	t.Helper()
	prev := debug.SetMemoryLimit(DefaultMemoryLimit)
	t.Cleanup(func() { debug.SetMemoryLimit(prev) })
}

// TestProxyConcurrentHostilePagesMemoryBounded covers RV2 and the CR5
// hardening: DefaultMaxConcurrentRequests requests for the worst hostile pages
// (and hide selectors) at once stay within concurrentHeapBudget in total,
// because only maxConcurrentHTMLRewrites documents are parsed at a time, each
// parse and each hide is bounded, and the process runs with
// DefaultMemoryLimit.
func TestProxyConcurrentHostilePagesMemoryBounded(t *testing.T) {
	skipMemoryAssertions(t)
	withProductionMemoryLimit(t)
	hostiles := hostileRewrites(DefaultMaxResponseBytes)
	for _, name := range []string{"text-nodes", "reconstruction", "big-tag", "hide-selectors"} {
		t.Run(name, func(t *testing.T) {
			var statuses []int
			start := time.Now()
			peak := peakHeapDuring(func() {
				statuses = serveConcurrently(t, hostiles[name].page, hostiles[name].hide, DefaultMaxConcurrentRequests)
			})
			for _, s := range statuses {
				if s != http.StatusOK && s != http.StatusRequestEntityTooLarge {
					t.Errorf("status %d, want 200 or 413", s)
				}
			}
			if peak > concurrentHeapBudget {
				t.Errorf("peak heap %d MiB for %d concurrent requests, want <= %d MiB", peak/mib, len(statuses), concurrentHeapBudget/mib)
			}
			t.Logf("peak %d MiB in %v, statuses %v", peak/mib, time.Since(start).Round(time.Millisecond), statuses)
		})
	}
}

// TestRewriteMemoryReport prints the single-rewrite and concurrent peaks for
// every hostile page. It runs only when WEBVIEW_MEMREPORT=1.
func TestRewriteMemoryReport(t *testing.T) {
	if os.Getenv("WEBVIEW_MEMREPORT") != "1" {
		t.Skip("set WEBVIEW_MEMREPORT=1 to print the memory report")
	}
	page := mustParseURL(t, "http://example.com/")
	hostiles := hostileRewrites(DefaultMaxResponseBytes)
	names := make([]string, 0, len(hostiles))
	for name := range hostiles {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, limit := range []string{"no memory limit", "DefaultMemoryLimit"} {
		if limit == "DefaultMemoryLimit" {
			withProductionMemoryLimit(t)
		}
		t.Logf("--- %s", limit)
		for _, name := range names {
			body, hide := hostiles[name].page, hostiles[name].hide
			var rerr error
			start := time.Now()
			single := peakHeapDuring(func() {
				_, rerr = rewriteHTMLLimited(body, page, "text/html", hide, DefaultMaxResponseBytes)
			})
			singleTime := time.Since(start)
			var statuses []int
			start = time.Now()
			concurrent := peakHeapDuring(func() {
				statuses = serveConcurrently(t, body, hide, DefaultMaxConcurrentRequests)
			})
			outcome := "ok"
			if rerr != nil {
				outcome = "refused"
			}
			t.Logf("%-14s %5d KiB  single rewrite: %4d MiB %6v %-7s  %d concurrent /proxy: %4d MiB %6v statuses=%v",
				name, len(body)>>10, single/mib, singleTime.Round(time.Millisecond), outcome,
				DefaultMaxConcurrentRequests, concurrent/mib, time.Since(start).Round(time.Millisecond), statuses)
		}
	}
}
