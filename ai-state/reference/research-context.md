# Grafana Web View Panel Plugin — Implementation Context

## Context Summary for Implementation Agent

### Project Goal

Build a generic **Grafana panel plugin** that displays web pages with viewport control (pan/zoom into a specific section). It should work across a variety of sites, handling browser security restrictions (X-Frame-Options, CSP, CORS, XSS) intelligently.

### Architectural Decisions Made

**1. Hybrid Direct/Proxy Approach (Chosen)**

- **Direct iframe** for sites that allow framing (no overhead, full functionality)
- **Backend proxy fallback** for sites that block framing via `X-Frame-Options` or CSP `frame-ancestors`
- **Auto-detection** via HEAD request to check headers, with manual override

**2. CSS Transform Viewport (Key Technique)**

- The "viewport into iframe section" is achieved by CSS transforms on the `<iframe>` element itself
- `transform: scale() translate()` works on cross-origin iframes (you're styling the element, not its contents)
- Container uses `overflow: hidden` to clip to the visible region
- Pan via mouse drag updates translate values
- Zoom via mouse wheel updates scale value
- Mouse events captured on parent container (`pointer-events: none` on iframe prevents capture)

**3. Backend Proxy Architecture**

- Grafana **backend data source plugin** (Go-based, gRPC sub-process) is the standard Grafana pattern
- Backend fetches target URL server-side (no CORS issues)
- **Strips** `X-Frame-Options` and CSP `frame-ancestors` headers
- **Injects** `<base>` tag for relative URLs
- **Rewrites** subresource URLs to route through proxy
- **Removes** frame-busting JavaScript patterns
- Proxy endpoint is same-origin as Grafana → enables full iframe DOM access when needed

### Alternatives Considered and Rejected

| Approach | Why Rejected |
|----------|--------------|
| BOM tile server direct (specific to BOM) | User wants generic solution |
| Third-party screenshot API (urlbox, apiflash) | Not real-time, costs money, third-party dependency |
| Browser extension companion | Poor UX, requires user installation |
| Grafana Image Renderer pattern (external service) | Overkill for this use case |
| DOM access via injected controller script | More complex; user prefers CSS viewport approach |

### Key Technical Insights

**Same-Origin Magic:** When proxied through `/api/plugins/.../resources/proxy`, the iframe is same-origin with the Grafana panel. This means direct DOM access is possible if needed (future enhancement), but the **CSS viewport approach works regardless of origin**.

**Security Considerations:**

- **Domain allowlist** required for the proxy (prevents SSRF and abuse)
- DOMPurify only needed if rendering HTML outside an iframe
- Sandbox attribute on iframe: `sandbox="allow-scripts allow-same-origin"`
- Strip dangerous headers, don't blindly forward

**Mouse Event Handling:**

- Iframe content captures mouse events by default
- Set `pointer-events: none` on iframe to let parent capture pan/zoom
- Or implement a toggle: "interact mode" vs "viewport mode"

**Limitations of Proxy Approach:**

- AJAX/fetch from the proxied page will fail CORS to original domain (works for static content)
- Cookies/auth sessions don't work without explicit handling
- Heavy SPAs may not function correctly
- Service workers won't register
- WebSockets won't connect

### Recommended File Structure

```
grafana-webview-panel/
├── src/
│   ├── components/
│   │   ├── WebViewPanel.tsx           # Main panel component
│   │   └── ViewportControls.tsx       # Pan/zoom UI controls
│   ├── hooks/
│   │   ├── useViewportTransform.ts    # Pan/zoom state logic
│   │   └── useFrameabilityCheck.ts    # Direct vs proxy detection
│   ├── module.ts                      # Plugin registration
│   ├── types.ts                       # TypeScript interfaces
│   └── plugin.json                    # Plugin metadata
├── pkg/
│   └── plugin/
│       ├── plugin.go                  # Backend entry point
│       ├── proxy.go                   # Page proxy with header stripping
│       ├── check_frameable.go         # HEAD request to check headers
│       └── allowlist.go               # Domain validation
├── provisioning/
│   └── docker-compose.yml             # Example deployment
├── Magefile.go                        # Build configuration
└── package.json
```

### Backend Resource Handlers Needed

1. `GET /resources/proxy?url=...` — Fetch, strip headers, rewrite URLs, return HTML
2. `GET /resources/proxy-resource?url=...` — Proxy subresources (CSS, JS, images)
3. `GET /resources/check-frameable?url=...` — HEAD request, return JSON with `useProxy: boolean`

### Frontend Component Behavior

1. On mount, call `check-frameable` endpoint (unless `forceProxy` is set)
2. Set iframe `src` to direct URL or proxy URL based on result
3. Apply CSS transform based on viewport state (X, Y, zoom)
4. On mouse down + drag, update X/Y
5. On mouse wheel, update zoom (with cursor-position-aware zooming)
6. On iframe error, fall back from direct to proxy
7. Provide reset button to return to initial viewport
8. Show mode indicator (DIRECT vs PROXIED) for debugging

### Panel Configuration Options

- `url` (string): Target URL
- `forceProxy` (boolean): Override auto-detection
- `initialX`, `initialY` (number): Initial viewport offset
- `initialZoom` (number): Initial zoom level (0.1–5.0)
- `iframeWidth`, `iframeHeight` (number): Virtual iframe dimensions (default 1920×1080)
- `allowPan` (boolean): Enable mouse pan
- `allowZoom` (boolean): Enable wheel zoom
- `interactive` (boolean): Allow clicks to pass through to iframe
- `refreshInterval` (number): Optional auto-refresh in seconds
