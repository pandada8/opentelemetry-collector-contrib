// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package alibabacloudlogserviceexporter

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	sls "github.com/aliyun/aliyun-log-go-sdk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.uber.org/zap"
)

func discoveryResponse(rows ...map[string]string) *sls.GetLogsResponse {
	return &sls.GetLogsResponse{Progress: "Complete", Logs: rows}
}

func TestParsePIDDiscovery(t *testing.T) {
	response := discoveryResponse(
		map[string]string{"serviceName": "unique", "pid": "one"},
		map[string]string{"serviceName": "ambiguous", "pid": "one"},
		map[string]string{"serviceName": "ambiguous", "pid": "two"},
		map[string]string{"serviceName": "ambiguous", "pid": "one"},
	)
	values, conflicts, err := parsePIDDiscovery(response)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"unique": "one"}, values)
	assert.Equal(t, 1, conflicts)
	for _, invalid := range []*sls.GetLogsResponse{
		nil,
		{Progress: "Incomplete"},
		discoveryResponse(map[string]string{"serviceName": "missing-pid"}),
		{Progress: "Complete", Logs: make([]map[string]string, pidDiscoveryLimit+1)},
	} {
		_, _, err := parsePIDDiscovery(invalid)
		require.Error(t, err)
	}
}

func TestPIDDiscoveryCacheAndExporter(t *testing.T) {
	now := time.Now()
	response := discoveryResponse(map[string]string{"serviceName": "signup_aggregator", "pid": "discovered"})
	var fetchErr error
	d := &pidDiscovery{logger: zap.NewNop(), now: func() time.Time { return now }, fetch: func(context.Context, time.Time) (*sls.GetLogsResponse, error) { return response, fetchErr }}
	assert.Empty(t, d.lookup())
	d.refresh(t.Context())
	client := &captureTraceClient{}
	sender := &logServiceTraceSender{client: client, traceFormat: "xtrace", tracePID: "fallback", pidDiscovery: d}
	require.NoError(t, sender.pushTraceData(t.Context(), constructSpanData()))
	assert.Equal(t, "discovered", xtraceFields(t, client.logs[0])["pid"])
	sender.tracePIDByService = map[string]string{"signup_aggregator": "manual"}
	require.NoError(t, sender.pushTraceData(t.Context(), constructSpanData()))
	assert.Equal(t, "manual", xtraceFields(t, client.logs[0])["pid"])
	assert.Equal(t, "discovered", d.lookup()["signup_aggregator"])
	fetchErr = errors.New("query failed")
	d.refresh(t.Context())
	assert.Equal(t, "discovered", d.lookup()["signup_aggregator"])
	now = now.Add(pidDiscoveryLookback)
	assert.Empty(t, d.lookup())
	fetchErr = nil
	response = discoveryResponse(map[string]string{"serviceName": "other", "pid": "new"})
	d.refresh(t.Context())
	assert.Equal(t, map[string]string{"other": "new"}, d.lookup())
	response = discoveryResponse()
	d.refresh(t.Context())
	assert.Empty(t, d.lookup(), "complete empty results remove old entries")
}

func TestPIDDiscoveryHourlyLifecycle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int64

		d := &pidDiscovery{
			logger: zap.NewNop(), interval: pidDiscoveryInterval, now: time.Now,
			fetch: func(ctx context.Context, now time.Time) (*sls.GetLogsResponse, error) {
				calls.Add(1)
				deadline, ok := ctx.Deadline()
				require.True(t, ok)
				assert.Equal(t, now.Add(pidDiscoveryTimeout), deadline)
				return discoveryResponse(map[string]string{"serviceName": "service", "pid": "pid"}), nil
			},
		}
		d.start()
		synctest.Wait()
		assert.Equal(t, int64(1), calls.Load())
		time.Sleep(time.Hour)
		synctest.Wait()
		assert.Equal(t, int64(2), calls.Load())
		require.NoError(t, d.shutdown(t.Context()))
		time.Sleep(time.Hour)
		assert.Equal(t, int64(2), calls.Load())
	})
}

func TestPIDDiscoveryShutdownCancelsQuery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := &pidDiscovery{
			logger: zap.NewNop(), interval: time.Hour, now: time.Now,
			fetch: func(ctx context.Context, _ time.Time) (*sls.GetLogsResponse, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			},
		}
		d.start()
		synctest.Wait()
		require.NoError(t, d.shutdown(t.Context()))
		assert.Nil(t, d.snapshot.Load())
	})
}

func TestPIDDiscoveryConcurrentReads(t *testing.T) {
	d := &pidDiscovery{
		logger: zap.NewNop(), now: time.Now,
		fetch: func(context.Context, time.Time) (*sls.GetLogsResponse, error) {
			return discoveryResponse(map[string]string{"serviceName": "service", "pid": "pid"}), nil
		},
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 100 {
				d.refresh(t.Context())
				values := d.lookup()
				values["service"] = "local"
			}
		})
	}
	wg.Wait()
	assert.Equal(t, "pid", d.lookup()["service"])
}

func TestDiscoveryDisabledByDefault(t *testing.T) {
	sender := &logServiceTraceSender{client: &captureTraceClient{}, traceFormat: "xtrace"}
	require.NoError(t, sender.start(t.Context(), nil))
	require.NoError(t, sender.pushTraceData(t.Context(), ptrace.NewTraces()))
	require.NoError(t, sender.shutdown(t.Context()))
}

func TestPIDDiscoverySDKQuery(t *testing.T) {
	requests := make(chan *http.Request, 1)
	bodies := make(chan sls.GetLogRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body sls.GetLogRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		requests <- r
		bodies <- body
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"meta":{"progress":"Complete","count":1,"hasSQL":true,"keys":[]},"data":[{"serviceName":"service","pid":"cloud-pid"}]}`)
	}))
	defer server.Close()
	// Empty project avoids SDK subdomain routing for the local test server.
	cfg := &Config{Endpoint: server.URL, Logstore: "destination", TracePIDDiscoveryLogstore: "reference", AccessKeyID: "test-key", AccessKeySecret: "test-secret", SecurityToken: "test-token"}
	d := newPIDDiscovery(cfg, zap.NewNop())
	now := time.Now().Truncate(time.Second)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second*5)
	defer cancel()
	response, err := d.fetch(ctx, now)
	require.NoError(t, err)
	values, _, err := parsePIDDiscovery(response)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"service": "cloud-pid"}, values)
	request := <-requests
	assert.Equal(t, http.MethodPost, request.Method)
	assert.Equal(t, "/logstores/reference/logs", request.URL.Path)
	assert.Equal(t, "test-token", request.Header.Get("x-acs-security-token"))
	assert.Contains(t, request.Header.Get("Authorization"), "test-key")
	body := <-bodies
	assert.Equal(t, pidDiscoveryQuery, body.Query)
	assert.Equal(t, now.Add(-24*time.Hour).Unix(), body.From)
	assert.Equal(t, now.Unix(), body.To)
	cancel()
	_, err = d.fetch(ctx, now)
	require.Error(t, err)
}
