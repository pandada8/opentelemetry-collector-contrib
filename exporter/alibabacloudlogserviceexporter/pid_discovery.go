// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package alibabacloudlogserviceexporter // import "github.com/open-telemetry/opentelemetry-collector-contrib/exporter/alibabacloudlogserviceexporter"

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	sls "github.com/aliyun/aliyun-log-go-sdk"
	"go.uber.org/zap"
)

const (
	pidDiscoveryInterval = time.Hour
	pidDiscoveryLookback = 24 * time.Hour
	pidDiscoveryTimeout  = 30 * time.Second
	pidDiscoveryLimit    = 1000
	pidDiscoveryQuery    = "* | select serviceName, pid from log where serviceName != '' and pid != '' group by serviceName, pid limit 1001"
)

type pidSnapshot struct {
	values  map[string]string
	updated time.Time
}

type pidDiscovery struct {
	fetch    func(context.Context, time.Time) (*sls.GetLogsResponse, error)
	logger   *zap.Logger
	snapshot atomic.Pointer[pidSnapshot]
	interval time.Duration
	now      func() time.Time
	cancel   context.CancelFunc
	done     chan struct{}
}

// The SDK's GetLogsV2 has no context parameter. Bind HTTP requests to the
// refresh context so shutdown and the refresh deadline cancel in-flight I/O.
type pidQueryTransport struct{ ctx context.Context }

func (t pidQueryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return http.DefaultTransport.RoundTrip(req.Clone(t.ctx))
}

func newPIDDiscovery(cfg *Config, logger *zap.Logger) *pidDiscovery {
	producerConfig := newProducerConfig(cfg)
	provider := producerConfig.CredentialsProvider
	if provider == nil {
		provider = sls.NewStaticCredentialsProvider(cfg.AccessKeyID, string(cfg.AccessKeySecret), string(cfg.SecurityToken))
	}
	project, logstore := cfg.TracePIDDiscoveryProject, cfg.TracePIDDiscoveryLogstore
	if project == "" {
		project = cfg.Project
	}
	if logstore == "" {
		logstore = cfg.Logstore
	}
	endpoint := cfg.Endpoint
	return &pidDiscovery{
		logger: logger, interval: pidDiscoveryInterval, now: time.Now,
		fetch: func(ctx context.Context, now time.Time) (*sls.GetLogsResponse, error) {
			client := sls.CreateNormalInterfaceV2(endpoint, provider)
			client.SetHTTPClient(&http.Client{Timeout: pidDiscoveryTimeout, Transport: pidQueryTransport{ctx: ctx}})
			// Bound SDK retries as well as individual HTTP requests.
			client.SetRetryTimeout(pidDiscoveryTimeout)
			return client.GetLogsV2(project, logstore, &sls.GetLogRequest{
				From: now.Add(-pidDiscoveryLookback).Unix(), To: now.Unix(), Query: pidDiscoveryQuery,
			})
		},
	}
}

func (d *pidDiscovery) start() {
	ctx, cancel := context.WithCancel(context.Background())
	d.cancel = cancel
	d.done = make(chan struct{})
	go func() {
		defer close(d.done)
		d.refresh(ctx)
		ticker := time.NewTicker(d.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				d.refresh(ctx)
			}
		}
	}()
}

func (d *pidDiscovery) shutdown(ctx context.Context) error {
	if d.cancel == nil {
		return nil
	}
	d.cancel()
	select {
	case <-d.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (d *pidDiscovery) refresh(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, pidDiscoveryTimeout)
	defer cancel()
	now := d.now()
	response, err := d.fetch(ctx, now)
	if err == nil {
		err = ctx.Err()
	}
	var values map[string]string
	var conflicts int
	if err == nil {
		values, conflicts, err = parsePIDDiscovery(response)
	}
	if err != nil {
		if ctx.Err() != context.Canceled {
			d.logger.Warn("XTrace PID discovery failed; retaining previous cache", zap.Error(err))
		}
		return
	}
	d.snapshot.Store(&pidSnapshot{values: values, updated: now})
	if conflicts > 0 {
		d.logger.Warn("XTrace PID discovery omitted ambiguous service names", zap.Int("services", conflicts))
	}
	d.logger.Debug("Refreshed XTrace PID cache", zap.Int("services", len(values)))
}

func parsePIDDiscovery(response *sls.GetLogsResponse) (map[string]string, int, error) {
	if response == nil || !response.IsComplete() {
		return nil, 0, errors.New("incomplete PID discovery query")
	}
	// Fetch a sentinel row beyond the supported size so a truncated mapping is
	// never installed (it could hide conflicting PIDs for the same service).
	if len(response.Logs) > pidDiscoveryLimit {
		return nil, 0, errors.New("PID discovery exceeds 1000 service/PID pairs")
	}
	values := make(map[string]string, len(response.Logs))
	ambiguous := make(map[string]bool)
	for _, row := range response.Logs {
		service, pid := row["serviceName"], row["pid"]
		if strings.TrimSpace(service) == "" || strings.TrimSpace(pid) == "" {
			return nil, 0, errors.New("PID discovery returned an empty service name or PID")
		}
		if previous, ok := values[service]; ok && previous != pid {
			ambiguous[service] = true
		}
		values[service] = pid
	}
	for service := range ambiguous {
		delete(values, service)
	}
	return values, len(ambiguous), nil
}

func (d *pidDiscovery) lookup() map[string]string {
	snapshot := d.snapshot.Load()
	if snapshot == nil || d.now().Sub(snapshot.updated) >= pidDiscoveryLookback {
		return make(map[string]string)
	}
	return maps.Clone(snapshot.values)
}
