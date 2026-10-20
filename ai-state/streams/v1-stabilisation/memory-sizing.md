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
   - estimated tree size at most **48 MiB**: 112 bytes for every node the tree builder inserts
     (cloned formatting elements and implied elements, such as the `<tbody>` that `<tr>` makes,
     count), 48 bytes per attribute slot, and the strings of every token rounded up to the
     allocator's size classes. The estimate is an upper bound on the heap the tree holds: 1.0 to
     1.8 times the measured tree for the hostile pages (1.10 for `<tr><table>`), 1.1 to 1.2 times
     for real pages (`TestTreeEstimateIsUpperBound`). Real pages need 2x to 6.9x their size (measured on
     Wikipedia, BBC, the Guardian, the WHATWG spec and the BOM radar page; the 33 KiB Hacker News
     front page needs 7.9x), so an ordinary page of up to about 7 MiB fits;
   - **16 MiB** of quadratic work: bytes of text appended to a text node, attribute comparisons,
     and 32 units for each attribute merged for a repeated `<html>` or `<body>` tag (a merge takes
     about 40 ns, a byte appended well under 1 ns). Real pages use almost none (at most 40 units
     measured).
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
pages of 5 MiB (the big tag: 2 MiB), with the 256 MiB soft limit. Highest of three runs on a
desktop (2026-10-19, after the review fixes below):

| Page | One request | 10 concurrent `/proxy` requests | Result |
|---|---|---|---|
| `<p>x` repeated | 43 MiB, 42 ms | 180 MiB | 413 |
| `<br>` repeated | 44 MiB, 42 ms | 188 MiB | 413 |
| 26 attributes per tag | 59 MiB, 63 ms | 182 MiB | 413 |
| `<img src=x>` repeated | 46 MiB, 67 ms | 182 MiB | 413 |
| formatting-element clones | 47 MiB, 26 ms | 191 MiB | 413 |
| `x</q>` repeated | 16 MiB, 10 ms | 176 MiB | 413 |
| `<tr><table>` repeated (implied elements) | 44 MiB, 50 ms | 184 MiB | 413 |
| `<body aN>` repeated (attribute merging) | 42 MiB, 34 ms | 174 MiB | 413 |
| one 2 MiB tag, 300,000 attributes | 120 MiB, 98 ms | 203 MiB | 200 |
| 390,000 `<p>` (1.1 MiB, about the most elements the parse limits allow) with 20 hide selectors that each match every element | 86 MiB, 471 ms | 173 MiB (2.7 s) | 413 |

Worst case with defaults: **about 205 MiB** for 10 concurrent hostile requests (the target was
300 MiB). Without the soft limit (GOMEMLIMIT=off) the same runs peak at 180 to 375 MiB, most of it
uncollected garbage. `TestRewriteMemoryBounded` (one request at most 160 MiB) and
`TestProxyConcurrentHostilePagesMemoryBounded` (10 requests at most 300 MiB) check these bounds on
every test run without `-race` or `-short`; both include the hide-selector page.

Hide-selector fixes (2026-10-19, review of the CR5 hardening). Each of the 20 selectors that
matched an element appended the hide declaration to its style attribute again, copying the growing
value each time, and each selector built a new slice of its matches. On the hide-selector page above
that measured 251 to 258 MiB for one rewrite (budget 160) and 561 MiB in 27.9 s for 10 concurrent
requests (the review measured 253 MiB and 589 MiB in 29 s). Now an element whose style already ends
with the declaration is left alone, and the matcher reuses one slice for the matches of every
selector: 174 to 182 MiB with the first change alone, 86 MiB with both (the table row). The rewrite
still ends in 413, because the rewritten page (a style attribute on every element) is over
`maxResponseBytes`.

Review fixes (2026-10-19). The tree estimate first charged each token, not each node, so nodes
that the tree builder creates without a token were not counted: the `<tr><table>` page's tree was
1.45 times its estimate, and with 26 attributes per tag (attribute arrays grow in steps) 1.37
times. Merging the attributes of a repeated `<body>` tag counted 1 unit of work per attribute
although it is about 250 times slower than appending a byte. Measured on the same desktop before
the fixes: `<tr><table>` 71 MiB for one request and 242 MiB for 10; 26 attributes per tag 93 MiB
and 244 MiB; `<body aN>` 0.8 s of CPU for one request and 4.4 s for 10. The 2 MiB tag measured
120 MiB before and after the fixes (it was 109 MiB on the machine of the first RV2 measurement).

## Sizing guidance (for DOC1)

Worst-case heap of the plugin process is roughly:

    maxConcurrentRequests x maxResponseBytes        (bodies read, waiting for a rewrite slot)
  + maxConcurrentRequests x maxResponseBytes        (rewritten pages being sent)
  + 2 x about 120 MiB                               (the two rewrite slots, at the parse limits)

With the defaults (10 x 5 MiB) that is about 340 MiB before the soft limit trims garbage, and about
205 MiB measured with it.

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

- Any signed-in user can pass `hide` parameters to `/proxy`. Since the CR5 hardening, `:has()` and
  the other cascadia extensions are refused (400) and the matching work on one page is bounded by
  `hideMatchBudget`, which charges every simple selector, attribute byte and sibling a test reads
  (the simple selectors were not charged at first: 20 long `:not()` lists of type selectors took 3.7
  to 5 s on the hide-selector page and were served). The whole budget is 0.05 to 0.8 s of CPU on a
  desktop, inside one rewrite slot; the slowest are walks over sibling lists too long for the CPU
  cache (`:nth-last-child(2n)` over 150,000 items).
- A parse cannot be cancelled. The work and tree budgets refuse every hostile page measured within
  about 70 ms of rewrite time on a desktop CPU, and the largest allowed tag takes about 100 ms
  (expect up to 10x on a Raspberry Pi); during that time it holds a rewrite slot. The work limit
  stops a page within about 25 ms of merging or appending: a merged `<html>` or `<body>` attribute
  (about 40 ns) counts 32 units, so 16 Mi units are about 20 ms of merges, and 16 MiB of appended
  text is a few ms. Before merges were weighted, 5,700 `<body aN>` tags took 0.7 s.
