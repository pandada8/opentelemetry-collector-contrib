# Production 413 diagnostics (PAN-121)

This distribution uses the official Collector Contrib v0.160.0 manifest and a
local copy of `go.opentelemetry.io/collector/exporter/otlphttpexporter@v0.160.0`
in `internal/diagnostics/otlphttpexporter`. The copy removes upstream relative
Go module replacements so it can be tested independently. The core repository
next to this checkout is not used.

Only HTTP 413 responses emit the additional structured error log:

- `request_body_uncompressed_bytes`: serialized OTLP body size before compression.
- `request_body_bytes`: last Content-Length actually emitted by the transport,
  captured through `httptrace.WroteHeaderField` after compression;
  `-1` means unavailable. This is the intended body size, not an acknowledgement
  that the upstream read every byte.
- `request_body_bytes_per_attempt`: all emitted Content-Length values, to expose
  transport retries or redirects.
- `response_request_content_length`: separate response request metadata for
  comparison; this is not used as the sent body size.
- `request_content_encoding`: transport request Content-Encoding.
- `response_headers`: response headers exposed by Go's HTTP client, including
  all values of repeated headers.

Payload content and request headers are not logged. Existing permanent-error and
retry behavior is preserved. Response headers are logged as requested for this
temporary production diagnosis.

From the repository root:

```sh
go install go.opentelemetry.io/collector/cmd/builder@v0.160.0
bash cmd/otelcontribcol-diagnostics/prepare-obi.sh otelcol-contrib
builder --skip-compilation --config cmd/otelcontribcol-diagnostics/manifest.yaml
cd cmd/otelcontribcol-diagnostics/_build
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -p 3 -trimpath -tags grpcnotrace -ldflags '-s -w' -o otelcol-contrib .
```

Run `go test ./...` inside `internal/diagnostics/otlphttpexporter`.
Build the container from `cmd/otelcontribcol-diagnostics` using its Dockerfile.

## Intermediate redirect diagnostics

`redirectLoggingTransport` wraps the configured HTTP transport and emits
`OTLP HTTP redirect response` at WARN for 301, 302, 303, 307 and 308, before
`http.Client` follows the redirect. Fields include the source request method/URL,
precompression and transport body lengths, Content-Encoding, Location and all
response headers. This captures correlation IDs from intermediate responses,
even when the final response succeeds. Request payloads are not logged.

Automatic redirect behavior, the default ten-redirect limit and response bodies
are preserved. Tests exercise 302 POST-to-GET conversion, empty gzip bodies,
307/308 POST/body preservation, intermediate headers and the redirect limit.
These source additions require rebuilding and deploying an image to take effect;
they are not present in the previously deployed `0.160.0-pan121.1` image.

## Combined SLS image (PAN-226)

`manifest-sls.yaml` builds `0.160.0-pan226.1` with the same full v0.160.0
component set and both PAN-121 diagnostics above. It also includes the current
`exporter/alibabacloudlogserviceexporter` source: opt-in XTrace formatting,
service-to-PID overrides and hourly discovery, and Metricstore label/histogram
alignment. XTrace and PID discovery still require explicit configuration.

The development checkout targets v0.161.0. `build-sls.sh` stages an ignored
copy of the SLS exporter, pins its Collector/Contrib dependencies to
v0.160.0/v1.66.0, and removes development-only relative replacements. The
original source and development module are unchanged. Both the staged exporter
and HTTP diagnostics must pass race tests before the distribution is built.

From the repository root, with Go 1.26 and builder v0.160.0 installed:

```sh
bash cmd/otelcontribcol-diagnostics/build-sls.sh
docker build -f cmd/otelcontribcol-diagnostics/Dockerfile.sls \
  -t steam-cn-shanghai-registry.cn-shanghai.cr.aliyuncs.com/public/otel-contrib:pan226-0.160.0-1 \
  cmd/otelcontribcol-diagnostics
```

The binary targets Linux amd64, uses no CGO, and retains the pinned official
image's entrypoint, user and default configuration. This is a diagnostic/PoC
build; successful compilation and local tests do not establish ARMS feature or
billing equivalence. Publishing it does not change a running deployment.
