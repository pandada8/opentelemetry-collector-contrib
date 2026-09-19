// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package otlphttpexporter

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/config/configcompression"
	"go.opentelemetry.io/collector/consumer/consumererror"
	"go.opentelemetry.io/collector/exporter/exportertest"
	"go.opentelemetry.io/collector/exporter/otlphttpexporter/internal/metadata"
)

func TestRequestSizeDiagnostics(t *testing.T) {
	for _, useHTTP2 := range []bool{false, true} {
		for _, compression := range []configcompression.Type{configcompression.TypeGzip, ""} {
			for _, code := range []int{http.StatusRequestEntityTooLarge, http.StatusServiceUnavailable, http.StatusOK} {
				t.Run(fmt.Sprintf("http2=%t/%s/%s", useHTTP2, compression, http.StatusText(code)), func(t *testing.T) {
					payload := bytes.Repeat([]byte("test-payload-"), 1000)
					var received []byte
					server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if useHTTP2 {
							require.Equal(t, 2, r.ProtoMajor)
						}
						var err error
						received, err = io.ReadAll(r.Body)
						require.NoError(t, err)
						w.Header().Add("Eagleeye-Traceid", "test-trace-id")
						w.Header().Add("X-Request-Id", "first")
						w.Header().Add("X-Request-Id", "second")
						w.WriteHeader(code)
					}))
					if useHTTP2 {
						server.EnableHTTP2 = true
						server.StartTLS()
					} else {
						server.Start()
					}
					defer server.Close()
					cfg := createDefaultConfig().(*Config)
					cfg.ClientConfig.Compression = compression
					cfg.ClientConfig.TLS.InsecureSkipVerify = useHTTP2
					set := exportertest.NewNopSettings(metadata.Type)
					core, logs := observer.New(zap.ErrorLevel)
					set.Logger = zap.New(core)
					exp, err := newExporter(cfg, set)
					require.NoError(t, err)
					require.NoError(t, exp.start(context.Background(), componenttest.NewNopHost()))
					defer exp.client.CloseIdleConnections()
					err = exp.export(context.Background(), server.URL, payload, exp.tracesPartialSuccessHandler)
					if code == http.StatusOK {
						require.NoError(t, err)
					} else {
						require.Error(t, err)
						require.Equal(t, code == http.StatusRequestEntityTooLarge, consumererror.IsPermanent(err))
					}
					if code != http.StatusRequestEntityTooLarge {
						require.Zero(t, logs.Len())
						return
					}
					require.Equal(t, 1, logs.Len())
					fields := logs.All()[0].ContextMap()
					require.EqualValues(t, len(payload), fields["request_body_uncompressed_bytes"])
					require.EqualValues(t, len(received), fields["request_body_bytes"])
					require.Equal(t, []any{int64(len(received))}, fields["request_body_bytes_per_attempt"])
					headers := fields["response_headers"].(http.Header)
					require.Equal(t, "test-trace-id", headers.Get("Eagleeye-Traceid"))
					require.Equal(t, []string{"first", "second"}, headers.Values("X-Request-Id"))
					if compression == configcompression.TypeGzip {
						require.Equal(t, "gzip", fields["request_content_encoding"])
						reader, err := gzip.NewReader(bytes.NewReader(received))
						require.NoError(t, err)
						decoded, err := io.ReadAll(reader)
						require.NoError(t, err)
						require.NoError(t, reader.Close())
						require.Equal(t, payload, decoded)
						require.Less(t, len(received), len(payload))
					} else {
						require.Equal(t, "", fields["request_content_encoding"])
						require.Equal(t, payload, received)
					}
				})
			}
		}
	}
}
