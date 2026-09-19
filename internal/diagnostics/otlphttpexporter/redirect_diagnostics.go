// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package otlphttpexporter

import (
	"net/http"

	"go.uber.org/zap"
)

// Log intermediate responses before http.Client follows their Location. Logging
// only after Client.Do returns misses these responses and their correlation IDs.
type redirectLoggingTransport struct {
	next   http.RoundTripper
	logger *zap.Logger
}

func (t *redirectLoggingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.next.RoundTrip(req)
	if err != nil || resp == nil {
		return resp, err
	}
	switch resp.StatusCode {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther,
		http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		bodySize := int64(-1)
		encoding := ""
		if resp.Request != nil {
			bodySize = resp.Request.ContentLength
			encoding = resp.Request.Header.Get("Content-Encoding")
		}
		t.logger.Warn("OTLP HTTP redirect response",
			zap.Int("http_status_code", resp.StatusCode),
			zap.String("request_method", req.Method),
			zap.String("request_url", req.URL.Redacted()),
			zap.Int64("request_body_uncompressed_bytes", req.ContentLength),
			zap.Int64("request_body_bytes", bodySize),
			zap.String("request_content_encoding", encoding),
			zap.String("redirect_location", resp.Header.Get("Location")),
			zap.Any("response_headers", resp.Header),
		)
	}
	return resp, err
}

func (t *redirectLoggingTransport) CloseIdleConnections() {
	if closer, ok := t.next.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}
