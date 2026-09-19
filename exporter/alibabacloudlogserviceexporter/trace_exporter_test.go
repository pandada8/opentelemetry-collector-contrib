// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package alibabacloudlogserviceexporter

import (
	"errors"
	"testing"

	sls "github.com/aliyun/aliyun-log-go-sdk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/exporter/exportertest"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/open-telemetry/opentelemetry-collector-contrib/exporter/alibabacloudlogserviceexporter/internal/metadata"
)

func TestNewTracesExporter(t *testing.T) {
	got, err := newTracesExporter(exportertest.NewNopSettings(metadata.Type), &Config{
		Endpoint: "cn-hangzhou.log.aliyuncs.com",
		Project:  "demo-project",
		Logstore: "demo-logstore",
	})
	assert.NoError(t, err)
	require.NotNil(t, got)

	traces := ptrace.NewTraces()
	rs := traces.ResourceSpans().AppendEmpty()
	ss := rs.ScopeSpans().AppendEmpty()
	ss.Spans().AppendEmpty()

	// This will put trace data to send buffer and return success.
	err = got.ConsumeTraces(t.Context(), traces)
	assert.NoError(t, err)
	assert.NoError(t, got.Shutdown(t.Context()))
}

func TestNewFailsWithEmptyTracesExporterName(t *testing.T) {
	got, err := newTracesExporter(exportertest.NewNopSettings(metadata.Type), &Config{})
	assert.Error(t, err)
	require.Nil(t, got)
}

type captureTraceClient struct {
	logs []*sls.Log
	err  error
}

func (c *captureTraceClient) sendLogs(logs []*sls.Log) error {
	c.logs = logs
	return c.err
}

func TestPushTraceFormat(t *testing.T) {
	for _, format := range []string{"", "legacy", "xtrace"} {
		t.Run(format, func(t *testing.T) {
			client := &captureTraceClient{}
			sender := logServiceTraceSender{client: client, traceFormat: format, tracePID: "app"}
			require.NoError(t, sender.pushTraceData(t.Context(), ptrace.NewTraces()))
			assert.Nil(t, client.logs)
			require.NoError(t, sender.pushTraceData(t.Context(), constructSpanData()))
			require.Len(t, client.logs, 2)
			fields := xtraceFields(t, client.logs[0])
			if format == "xtrace" {
				assert.Equal(t, "app", fields["pid"])
				assert.Contains(t, fields, "traceId")
				assert.NotContains(t, fields, "traceID")
			} else {
				assert.Contains(t, fields, "traceID")
				assert.NotContains(t, fields, "traceId")
				assert.Equal(t, "90000000", fields["duration"])
			}
			client.err = errors.New("send failed")
			assert.ErrorIs(t, sender.pushTraceData(t.Context(), constructSpanData()), client.err)
		})
	}
}
