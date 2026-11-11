# Local AnyTLS component

Source: [MetaCubeX/mihomo v1.19.34](https://github.com/MetaCubeX/mihomo/tree/v1.19.34/transport/anytls).
The upstream module is pinned in `backend/go.mod`; its checksum is recorded in `backend/go.sum`.
License: GNU GPL version 3, reproduced verbatim in `LICENSE`.

## Included source

Only these upstream files are retained locally:

- `transport/anytls/client.go`
- `transport/anytls/session/client.go`
- `transport/anytls/session/frame.go`
- `transport/anytls/session/session.go`
- `transport/anytls/session/stream.go`

The adapter in `backend/internal/pkg/proxychain/anytls_adapter.go` is derived from
upstream `adapter/outbound/anytls.go` and binds the application's supported AnyTLS
options to this client. The remaining TLS, padding, pipe, skiplist, utility,
metadata, buffer and logging packages come from the pinned upstream dependencies.

## Local differences

1. `transport/anytls/client.go` imports the local session package.
2. `session/stream.go` publishes the terminal `dieErr` through `atomic.Value`.
   `dieOnce` still selects the first terminal error. Concurrent reads, writes,
   local closes and peer closes access the published error atomically.

The second change addresses a race observed when closing a nested AnyTLS chain:
upstream `Stream.Read` read `dieErr` concurrently with `Stream.Close` writing it.
The upstream v1.19.34 source and the inspected Meta branch both used an ordinary
`error` field. TLS authentication, frame format, padding, reuse and idle-session
defaults retain their upstream implementations.

## Maintenance

For an engine upgrade, compare these five files with the new upstream tag and
review the native AnyTLS adapter against the application binding. Preserve the
small import and terminal-error synchronization differences while synchronizing
upstream changes. Run the real synthetic TLS, authentication, nested-chain,
reuse, retention, SSE and WebSocket regressions with the Go race detector.
