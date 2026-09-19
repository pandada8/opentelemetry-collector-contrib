FROM ghcr.io/open-telemetry/opentelemetry-collector-releases/opentelemetry-collector-contrib:0.160.0@sha256:799dc6cf12c96192af37b5bdba804da8c10b3bc563b43cb90c3f3c58d9572ad6
COPY --chmod=755 _build-sls/otelcol-contrib /otelcol-contrib
LABEL org.opencontainers.image.version="0.160.0-pan226.1" \
      org.opencontainers.image.description="Collector Contrib with SLS trace/metric alignment, PID discovery and PAN-121 HTTP diagnostics"
