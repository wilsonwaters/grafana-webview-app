# Memory sizing of the HTML rewrite (RV2-be, #140)

**Decided:** 2026-10-19, RV2-be. **Input for:** DOC1 (`docs/administration.md` limits and sizing,
`docs/architecture.md` rewrite pipeline, troubleshooting for 413).

## The problem

`/proxy` reads the whole upstream body (at most `maxResponseBytes`, default 5 MiB) and, for HTML,
parses it into a DOM tree, rewrites it and renders it again. A parse tree is many times larger than
its document, and `golang.org/x/net/html` puts no bound on it:

| Hostile 5 MiB page (or as stated) | Before RV2 (measured) |
|---|---|
| `<p>x` repeated (2.6 million text and element nodes) | 330 MiB heap for one request; 3.4 GiB for 10 concurrent requests |
| `<br>` repeated / 26 attributes per tag / `<img src=x>` repeated | 175 / 270 / 96 MiB for one request; 1.8 / 2.9 / 1.1 GiB for 10 |
| 42 open formatting elements, then `<p>x` repeated (the tree builder clones all 42 for each paragraph) | 1 MiB of input: 10.5 million nodes, 1.1 GiB allocated; with 20 attributes per element, 10.8 GiB allocated |
| `x</q>` repeated (each `x` is appended to one text node, copying it) | quadratic CPU: 1 MiB took 2.6 s, 5 MiB about a minute |
| One tag with 800,000 distinct attributes | 135 MiB in the tokenizer alone, 222 MiB for the parse |

The dev server is a 1 GB Raspberry Pi, so any of these could take down Grafana. An allowlisted host
that serves user content is enough to deliver such a page, and any signed-in user can request it.

## Controls

1. **Bounded parser.** `pkg/internal/htmlparse` is the `x/net/html` v0.61.0 tree builder with a
   budget (see its package doc; a test keeps it identical to upstream for the html5lib corpus).
   The proxy's limits (`htmlParseLimits` in `pkg/plugin/rewrite.go`) are fixed and do not change
   with `maxResponseBytes`:
   - one token (a tag, a text run, a comment, an inline script or style) at most **2 MiB**;
   - estimated tree size at most **48 MiB** (112 bytes per node, 48 bytes per attribute, plus
     text; cloned formatting elements count). Real pages use 2x to 7.3x their size (measured on
     Wikipedia, BBC, the Guardian, the WHATWG spec and the BOM radar page), so an ordinary page of
     up to about 6.5 MiB fits;
   - **16 MiB** of quadratic work (text appended to a text node, attribute comparisons). Real
     pages use almost none (at most 40 bytes measured).
   A page over a limit is refused with **413** (`size-limit`), like a page over `maxResponseBytes`.
2. **Rewrite slots.** At most **2** HTML documents per plugin process are decoded, parsed,
   rewritten and rendered at once (`maxConcurrentHTMLRewrites`), whatever `maxConcurrentRequests`
   is. Other requests wait for a slot until their request deadline (then 504). Non-HTML responses
   need no slot.
3. **Bounded output.** The rewritten document is rendered into a buffer that stops at
   `maxResponseBytes` (413), instead of being built in full and checked afterwards.
4. **Pre-sized reads.** A body with a Content-Length is read into a buffer of that size.
5. **Soft memory limit.** The plugin process sets a Go soft memory limit of **256 MiB**
   (`runtime/debug.SetMemoryLimit`) unless `GOMEMLIMIT` is set in its environment. Parsing creates
   garbage several times the live tree; the limit makes the collector reclaim it early. It is a
   soft limit: the process is never stopped for exceeding it.
6. **Fail closed.** A document the parser rejects for another reason (for example more than 512
   nested elements) is a 502; the un-rewritten page is never served.

## After RV2 (measured)

Heap growth above the idle baseline, sampled every 0.2 ms (`TestRewriteMemoryReport`,
`WEBVIEW_MEMREPORT=1 go test ./pkg/plugin -run TestRewriteMemoryReport -v`), default settings,
pages of 5 MiB (the big tag: 2 MiB), with the 256 MiB soft limit:

| Page | One request | 10 concurrent `/proxy` requests | Result |
|---|---|---|---|
| `<p>x` repeated | 48 MiB, 40 ms | 186 MiB | 413 |
| `<br>` repeated | 48 MiB | 193 MiB | 413 |
| 26 attributes per tag | 93 MiB | 225 MiB | 413 |
| `<img src=x>` repeated | 51 MiB | 191 MiB | 413 |
| formatting-element clones | 47 MiB, 17 ms | 182 MiB | 413 |
| `x</q>` repeated | 16 MiB, 17 ms | 163 MiB | 413 |
| one 2 MiB tag, 300,000 attributes | 109 MiB | 198 MiB | 200 |

Worst case with defaults: **about 225 MiB** for 10 concurrent hostile requests (the target was
300 MiB). Without the soft limit (GOMEMLIMIT=off) the same runs peak at 210 to 380 MiB, most of it
uncollected garbage. `TestRewriteMemoryBounded` (one request at most 160 MiB) and
`TestProxyConcurrentHostilePagesMemoryBounded` (10 requests at most 300 MiB) check these bounds on
every test run without `-race` or `-short`.

## Sizing guidance (for DOC1)

Worst-case heap of the plugin process is roughly:

    maxConcurrentRequests x maxResponseBytes        (bodies read, waiting for a rewrite slot)
  + maxConcurrentRequests x maxResponseBytes        (rewritten pages being sent)
  + 2 x about 110 MiB                               (the two rewrite slots, at the parse limits)

With the defaults (10 x 5 MiB) that is about 320 MiB before the soft limit trims garbage, and about
225 MiB measured with it.

- Small hosts (1 GB Raspberry Pi): keep the defaults. Lower `maxResponseBytes` or
  `maxConcurrentRequests` to reduce the first two terms.
- Raising `maxResponseBytes` raises the first two terms linearly but not the parse limits: a page
  whose tree or largest token is over the parse limits is a 413 whatever `maxResponseBytes` is.
- `GOMEMLIMIT` in the environment of the plugin process replaces the 256 MiB soft limit, for
  example `GOMEMLIMIT=512MiB` on a large host or `GOMEMLIMIT=off` to disable it. (DOC1: check
  which environment variables the supported Grafana versions pass to backend plugin processes
  before documenting how to set it.)
- 413 from `/proxy` means one of: the body is over `maxResponseBytes`, its gzip-decoded size is,
  the rewritten page is, or the page is over a parse limit (very many elements or attributes, a
  token over 2 MiB, or deeply mis-nested formatting markup). The plugin log has the reason.

## Residual risks

- A hide selector with `:has()` (cascadia supports it) can make selector matching quadratic in the
  tree size, and any signed-in user can pass `hide` parameters to `/proxy`. The parse limits bound
  the tree, so this is CPU time inside one rewrite slot, not memory. Follow-up: reject `:has(` and
  the other cascadia extensions in hide selectors.
- A parse cannot be cancelled. The work and tree budgets stop every hostile page measured within
  about 120 ms on a desktop CPU (expect up to 10x on a Raspberry Pi), during which it holds a
  rewrite slot.
