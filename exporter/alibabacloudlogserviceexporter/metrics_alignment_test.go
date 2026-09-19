// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package alibabacloudlogserviceexporter

import (
	"strings"
	"testing"

	sls "github.com/aliyun/aliyun-log-go-sdk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"
)

func metricFields(log *sls.Log) map[string]string {
	fields := make(map[string]string, len(log.Contents))
	for _, content := range log.Contents {
		fields[content.GetKey()] = content.GetValue()
	}
	return fields
}

func metricLabels(t *testing.T, raw string) map[string]string {
	t.Helper()
	labels := map[string]string{}
	for pair := range strings.SplitSeq(raw, "|") {
		if pair == "" {
			continue
		}
		key, value, ok := strings.Cut(pair, "#$#")
		require.True(t, ok)
		require.NotContains(t, labels, key, "duplicate normalized label")
		labels[key] = value
	}
	return labels
}

func TestMetricstoreIdentityAndScopes(t *testing.T) {
	md := pmetric.NewMetrics()
	rm := md.ResourceMetrics().AppendEmpty()
	attrs := rm.Resource().Attributes()
	attrs.PutStr("service.name", "collector")
	attrs.PutStr("service.namespace", "monitoring")
	attrs.PutStr("service.instance.id", "instance-1")
	attrs.PutStr("http.method", "resource")
	for _, scope := range []string{"first", "second"} {
		sm := rm.ScopeMetrics().AppendEmpty()
		sm.Scope().SetName(scope)
		sm.Scope().SetVersion("1.0")
		m := sm.Metrics().AppendEmpty()
		m.SetName("request.duration")
		dp := m.SetEmptyGauge().DataPoints().AppendEmpty()
		dp.SetTimestamp(1789811965985123456)
		dp.SetDoubleValue(1.25)
		dp.Attributes().PutStr("http_method", "GET")
	}
	before, err := (&pmetric.JSONMarshaler{}).MarshalMetrics(md)
	require.NoError(t, err)
	logs := metricsDataToLogServiceData(zap.NewNop(), md)
	require.Len(t, logs, 2)
	for i, scope := range []string{"first", "second"} {
		fields := metricFields(logs[i])
		assert.Len(t, fields, 4)
		assert.Equal(t, "request_duration", fields["__name__"])
		assert.Equal(t, "1789811965985123456", fields["__time_nano__"])
		assert.Equal(t, uint32(1789811965), logs[i].GetTime())
		assert.Equal(t, "1.25", fields["__value__"])
		assert.Equal(t, map[string]string{
			"job": "monitoring/collector", "instance": "instance-1",
			"service_name": "collector", "service_namespace": "monitoring",
			"service_instance_id": "instance-1", "http_method": "GET",
			"otel_scope_name": scope, "otel_scope_version": "1.0",
		}, metricLabels(t, fields["__labels__"]))
	}
	after, err := (&pmetric.JSONMarshaler{}).MarshalMetrics(md)
	require.NoError(t, err)
	assert.Equal(t, before, after)
	attrs.PutStr("job", "resource-job")
	attrs.PutStr("instance", "resource-instance")
	dp := rm.ScopeMetrics().At(0).Metrics().At(0).Gauge().DataPoints().At(0)
	dp.Attributes().PutStr("job", "scrape-job")
	dp.Attributes().PutStr("instance", "scrape-instance")
	logs = metricsDataToLogServiceData(zap.NewNop(), md)
	first := metricLabels(t, metricFields(logs[0])["__labels__"])
	second := metricLabels(t, metricFields(logs[1])["__labels__"])
	assert.Equal(t, "scrape-job", first["job"])
	assert.Equal(t, "scrape-instance", first["instance"])
	assert.Equal(t, "resource-job", second["job"])
	assert.Equal(t, "resource-instance", second["instance"])
}

func TestMetricstoreCumulativeBuckets(t *testing.T) {
	md := pmetric.NewMetrics()
	m := md.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
	m.SetName("latency")
	h := m.SetEmptyHistogram()
	h.SetAggregationTemporality(pmetric.AggregationTemporalityCumulative)
	dp := h.DataPoints().AppendEmpty()
	dp.SetCount(10)
	dp.ExplicitBounds().FromRaw([]float64{0.1, 0.5, 1})
	dp.BucketCounts().FromRaw([]uint64{2, 0, 3, 5})
	dp.Attributes().PutStr("le", "must-be-replaced")
	logs := metricsDataToLogServiceData(zap.NewNop(), md)
	require.Len(t, logs, 5)
	assert.Equal(t, "latency_count", metricFields(logs[0])["__name__"])
	assert.Equal(t, "10", metricFields(logs[0])["__value__"])
	for i, expected := range []struct{ bound, count string }{{"0.1", "2"}, {"0.5", "2"}, {"1", "5"}, {"+Inf", "10"}} {
		fields := metricFields(logs[i+1])
		assert.Equal(t, "latency_bucket", fields["__name__"])
		assert.Equal(t, expected.count, fields["__value__"])
		assert.Equal(t, expected.bound, metricLabels(t, fields["__labels__"])["le"])
	}
	dp.SetSum(0)
	logs = metricsDataToLogServiceData(zap.NewNop(), md)
	require.Len(t, logs, 6)
	assert.Equal(t, "latency_sum", metricFields(logs[0])["__name__"])
	assert.Equal(t, "0", metricFields(logs[0])["__value__"])
}

func TestMetricstoreScrapedLabelsWithoutResource(t *testing.T) {
	md := pmetric.NewMetrics()
	m := md.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
	m.SetName("requests_total")
	dp := m.SetEmptySum().DataPoints().AppendEmpty()
	dp.SetIntValue(42)
	dp.Attributes().PutStr("job", "scrape-job")
	dp.Attributes().PutStr("instance", "localhost:8080")
	logs := metricsDataToLogServiceData(zap.NewNop(), md)
	require.Len(t, logs, 1)
	assert.Equal(t, "instance#$#localhost:8080|job#$#scrape-job", metricFields(logs[0])["__labels__"])
}
