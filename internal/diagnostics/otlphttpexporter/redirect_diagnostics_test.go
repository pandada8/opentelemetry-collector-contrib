// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package otlphttpexporter

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/config/configcompression"
	"go.opentelemetry.io/collector/exporter/exportertest"
	"go.opentelemetry.io/collector/exporter/otlphttpexporter/internal/metadata"
)

func TestRedirectDiagnostics(t *testing.T) {
	for _, code := range []int{301, 302, 303, 307, 308} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			payload := bytes.Repeat([]byte("trace"), 1000)
			var methods []string
			var sizes []int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				methods = append(methods, r.Method)
				sizes = append(sizes, r.ContentLength)
				w.Header().Add("EagleEye-TraceId", "original-hop-id")
				w.Header().Add("X-Test", "first")
				w.Header().Add("X-Test", "second")
				switch r.URL.Path {
				case "/start":
					w.Header().Set("Location", "/middle")
					w.WriteHeader(code)
				case "/middle":
					w.Header().Set("Location", "/end")
					w.WriteHeader(code)
				default:
					w.WriteHeader(http.StatusOK)
				}
			}))
			defer server.Close()
			cfg := createDefaultConfig().(*Config)
			cfg.ClientConfig.Compression = configcompression.TypeGzip
			set := exportertest.NewNopSettings(metadata.Type)
			core, logs := observer.New(zap.WarnLevel)
			set.Logger = zap.New(core)
			exp, err := newExporter(cfg, set)
			require.NoError(t, err)
			require.NoError(t, exp.start(context.Background(), componenttest.NewNopHost()))
			defer exp.client.CloseIdleConnections()
			require.NoError(t, exp.export(context.Background(), server.URL+"/start", payload, exp.tracesPartialSuccessHandler))
			entries := logs.FilterMessage("OTLP HTTP redirect response").All()
			require.Len(t, entries, 2)
			for i, entry := range entries {
				fields := entry.ContextMap()
				require.EqualValues(t, code, fields["http_status_code"])
				require.Equal(t, methods[i], fields["request_method"])
				require.Equal(t, sizes[i], fields["request_body_bytes"])
				require.Equal(t, "gzip", fields["request_content_encoding"])
				headers := fields["response_headers"].(http.Header)
				require.Equal(t, "original-hop-id", headers.Get("EagleEye-TraceId"))
				require.Equal(t, []string{"first", "second"}, headers.Values("X-Test"))
			}
			require.Equal(t, server.URL+"/start", entries[0].ContextMap()["request_url"])
			require.Equal(t, "/middle", entries[0].ContextMap()["redirect_location"])
			require.Equal(t, server.URL+"/middle", entries[1].ContextMap()["request_url"])
			require.Equal(t, "/end", entries[1].ContextMap()["redirect_location"])
			if code == 307 || code == 308 {
				require.Equal(t, []string{"POST", "POST", "POST"}, methods)
				require.Equal(t, sizes[0], sizes[1])
			} else {
				require.Equal(t, []string{"POST", "GET", "GET"}, methods)
				require.EqualValues(t, 23, sizes[1])
			}
		})
	}
}

func TestRedirectLimitPreserved(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/loop", http.StatusFound)
	}))
	defer server.Close()
	core, logs := observer.New(zap.WarnLevel)
	client := &http.Client{Transport: &redirectLoggingTransport{next: http.DefaultTransport, logger: zap.New(core)}}
	defer client.CloseIdleConnections()
	resp, err := client.Get(server.URL)
	if resp != nil {
		_ = resp.Body.Close()
	}
	// The wrapper must not disable automatic redirects or their default limit.
	require.ErrorContains(t, err, "stopped after 10 redirects")
	require.Len(t, logs.All(), 10)
}
