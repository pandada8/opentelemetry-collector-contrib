// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package alibabacloudlogserviceexporter // import "github.com/open-telemetry/opentelemetry-collector-contrib/exporter/alibabacloudlogserviceexporter"

import (
	"context"
	"maps"

	sls "github.com/aliyun/aliyun-log-go-sdk"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/exporter"
	"go.opentelemetry.io/collector/exporter/exporterhelper"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.uber.org/zap"
)

// newTracesExporter return a new LogService trace exporter.
func newTracesExporter(set exporter.Settings, cfg component.Config) (exporter.Traces, error) {
	l := &logServiceTraceSender{
		logger:            set.Logger,
		traceFormat:       cfg.(*Config).TraceFormat,
		tracePID:          cfg.(*Config).TracePID,
		tracePIDByService: maps.Clone(cfg.(*Config).TracePIDByService),
	}

	var err error
	if l.client, err = newLogServiceClient(cfg.(*Config), set.Logger); err != nil {
		return nil, err
	}

	if cfg.(*Config).TracePIDAutoDiscovery {
		l.pidDiscovery = newPIDDiscovery(cfg.(*Config), set.Logger)
	}
	return exporterhelper.NewTraces(
		context.TODO(),
		set,
		cfg,
		l.pushTraceData,
		exporterhelper.WithStart(l.start),
		exporterhelper.WithShutdown(l.shutdown),
	)
}

type logServiceTraceSender struct {
	logger            *zap.Logger
	client            logServiceClient
	traceFormat       string
	tracePID          string
	tracePIDByService map[string]string
	pidDiscovery      *pidDiscovery
}

func (s *logServiceTraceSender) pushTraceData(
	_ context.Context,
	td ptrace.Traces,
) error {
	var err error
	var slsLogs []*sls.Log
	if s.traceFormat == "xtrace" {
		lookup := s.tracePIDByService
		if s.pidDiscovery != nil {
			lookup = s.pidDiscovery.lookup()
			maps.Copy(lookup, s.tracePIDByService)
		}
		slsLogs = traceDataToXTrace(td, s.tracePID, lookup)
	} else {
		slsLogs = traceDataToLogServiceData(td)
	}
	if len(slsLogs) > 0 {
		err = s.client.sendLogs(slsLogs)
	}
	return err
}

func (s *logServiceTraceSender) start(context.Context, component.Host) error {
	if s.pidDiscovery != nil {
		s.pidDiscovery.start()
	}
	return nil
}

func (s *logServiceTraceSender) shutdown(ctx context.Context) error {
	if s.pidDiscovery != nil {
		return s.pidDiscovery.shutdown(ctx)
	}
	return nil
}
