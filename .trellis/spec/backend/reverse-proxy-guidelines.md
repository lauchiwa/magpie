# Gateway Networking and Reverse-Proxy Guidelines

## Scope and source of truth

The repository contains no Nginx configuration or generic reverse-proxy deployment template. These rules describe the gateway's implemented contract only. The primary sources are `internal/gateway/lan.go`, `internal/gateway/lan_proxy_test.go`, `internal/gateway/gateway.go`, `Dockerfile`, and the proxy/tunnel section of `docs/reference.md`.

## Gateway listeners

The model gateway defaults to `MAGPIE_ADDR` (normally `127.0.0.1:3425`); the Docker image sets `MAGPIE_ADDR=0.0.0.0:3425` and exposes 3425. The browser UI is a separate `magpie web` listener, conventionally `:3430`; Docker exposes it but the container command must explicitly run `magpie web --addr 0.0.0.0:3430 --no-open`. Development uses `DEV_ADDR=127.0.0.1:3426`, UI `3427`, backend `3428`, and control `3429` as defined in `Makefile`/`build/dev.sh`.

Keep the gateway and browser UI roles distinct. A proxy for model requests should target the gateway listener; a proxy for the management page should target the web listener and preserve the UI's cookie/key flow.

## Proxy detection and trust

`internal/gateway/lan.go` treats a request as proxied when it contains any non-empty header in `forwardHeaders`:

- `Forwarded`
- `X-Forwarded-For`
- `X-Real-IP`
- `Cf-Connecting-IP`
- `True-Client-IP`
- `Tailscale-User-Login`

A request arriving on loopback with one of these headers is not considered local. It is treated as another caller and requires LAN sharing plus an enabled Magpie gateway key. This protects the gateway when a tunnel or reverse proxy forwards external traffic to `127.0.0.1`. The behavior applies across model APIs, quotas, MCP sign-ins, and other gateway routes; it is tested by `internal/gateway/lan_proxy_test.go`.

`MAGPIE_TRUST_PROXY=1` disables this loopback-proxy classification. Set it only when the local proxy itself authenticates/signs its clients and the operator intentionally wants forwarded requests to retain local treatment. This is a trust-boundary override, not a general way to make a public proxy keyless.

When publishing through Nginx, Caddy, Cloudflare Tunnel, ngrok, Tailscale, or a similar HTTP proxy, enable Magpie's **Share on local network** setting and give each client an enabled gateway key. Point the proxy upstream at `http://127.0.0.1:3425` unless the gateway is deliberately bound elsewhere. Do not assume a bare TCP forward adds forwarding headers; the gateway cannot distinguish that from a local agent, so use the LAN address with sharing enabled as documented in `docs/reference.md`.

## Keys, CORS, and streaming

Forwarding headers do not authenticate callers. Clients still send the gateway key using `Authorization: Bearer <key>` or `x-api-key`; invalid/missing keys receive a protocol-shaped 401 response. Do not log or expose full keys. Gateway middleware (`internal/gateway/gateway.go`) owns CORS, caller identity, key limits, and request recording; do not bypass it by calling a handler directly in a new route.

The gateway streams SSE and other long responses. A reverse proxy used in front of it must be configured and tested so it does not buffer or truncate streaming responses. The project has no checked-in Nginx directive for this, so any deployment-specific `proxy_buffering`, timeout, HTTP/2, websocket, or header configuration belongs in deployment documentation and must not be presented as a Magpie repository convention without evidence.

## Outbound proxy and compression

`internal/netproxy` owns process/provider outbound proxy behavior and integrates with settings; reuse it rather than constructing ad-hoc proxy transports. `internal/upstream` handles vendor status/upstream helpers. `internal/davsync/dav.go` explicitly accounts for proxies weakening or changing compressed ETags; do not compare compressed and uncompressed validators as if they were interchangeable.

## Verification

Proxy changes need tests for at least: direct loopback, loopback with each relevant forwarding-header class, LAN/shared mode with a valid key, missing/invalid keys, `MAGPIE_TRUST_PROXY`, and the actual HTTP server middleware stack. Use `internal/gateway/lan_proxy_test.go` and gateway integration tests as patterns. For a real Nginx deployment, verify the model stream through the proxy and state the deployment directives and result separately from repository tests.
