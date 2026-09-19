// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package alibabacloudlogserviceexporter

import (
	"encoding/json"
	"testing"
	"time"

	sls "github.com/aliyun/aliyun-log-go-sdk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"
)

func xtraceFields(t *testing.T, log *sls.Log) map[string]string {
	t.Helper()
	fields := make(map[string]string, len(log.Contents))
	for _, content := range log.Contents {
		require.NotContains(t, fields, content.GetKey(), "duplicate output field")
		fields[content.GetKey()] = content.GetValue()
	}
	return fields
}

func TestTraceDataToXTrace(t *testing.T) {
	td := constructSpanData()
	rs := td.ResourceSpans().At(0)
	rs.Resource().Attributes().PutInt("process.pid", 1)
	span := rs.ScopeSpans().At(0).Spans().At(0)
	span.Attributes().PutBool("sampled", true)
	span.Attributes().PutDouble("ratio", 1.5)
	span.Attributes().PutEmptySlice("array").AppendEmpty().SetStr("item")
	span.Attributes().PutEmptyMap("object").PutInt("count", 2)
	span.Attributes().PutStr("otel.scope.name", "stale")
	span.Events().At(0).Attributes().PutInt("count", 2)
	span.Links().At(0).SetTraceID(newTraceID())
	span.Links().At(0).SetSpanID(newSegmentID())
	before, err := (&ptrace.JSONMarshaler{}).MarshalTraces(td)
	require.NoError(t, err)
	logs := traceDataToXTrace(td, "test-application-id", nil)
	require.Len(t, logs, 2)
	fields := xtraceFields(t, logs[0])
	// Literal field names and values reflect the observed logstore-tracing schema.
	assert.Equal(t, "000000000000000052969a8955571a3f", fields["traceId"])
	assert.Equal(t, "0000000000647d98", fields["spanId"])
	assert.Equal(t, "0000000000647d98", fields["parentSpanId"])
	assert.Equal(t, "/users/junit", fields["spanName"])
	assert.Equal(t, "3", fields["kind"])
	assert.Equal(t, "1", fields["statusCode"])
	assert.Equal(t, "OK", fields["statusMessage"])
	assert.Equal(t, "x:y", fields["traceState"])
	assert.Equal(t, "12210123456789", fields["startTime"])
	assert.Equal(t, "12300123456789", fields["endTime"])
	assert.Equal(t, "90000000000", fields["duration"])
	assert.Equal(t, uint32(12300), logs[0].GetTime())
	assert.Equal(t, "signup_aggregator", fields["serviceName"])
	assert.Equal(t, "xxx.et15", fields["hostname"])
	assert.Equal(t, "test-application-id", fields["pid"])
	var resources map[string]string
	require.NoError(t, json.Unmarshal([]byte(fields["resources"]), &resources))
	assert.Equal(t, "signup_aggregator", resources["service.name"])
	assert.Equal(t, "xxx.et15", resources["host.name"])
	assert.Equal(t, "1", resources["process.pid"])
	assert.Len(t, resources, 10)
	assert.JSONEq(t, `{"http.method":"GET","http.url":"https://api.example.com/users/junit","http.status_code":"200","sampled":"true","ratio":"1.5","array":"[\"item\"]","object":"{\"count\":2}","otel.scope.name":"golang-sls-exporter","otel.scope.version":"v0.1.0"}`, fields["attributes"])
	assert.JSONEq(t, `[{"name":"event","timestamp":1024,"attributes":{"key":"value","count":"2"}}]`, fields["events"])
	assert.JSONEq(t, `[{"traceId":"000000000000000052969a8955571a3f","spanId":"0000000000647d98","traceState":"link:state","attributes":{"link":"true"}}]`, fields["links"])
	assert.Len(t, fields, 18)
	second := xtraceFields(t, logs[1])
	assert.Equal(t, "2", second["kind"])
	assert.Equal(t, "2", second["statusCode"])
	assert.NotContains(t, second, "events")
	assert.NotContains(t, second, "links")
	after, err := (&ptrace.JSONMarshaler{}).MarshalTraces(td)
	require.NoError(t, err)
	assert.Equal(t, before, after, "conversion must not mutate pdata")
}

func TestXTraceEmptyAndInvalidSpans(t *testing.T) {
	assert.Empty(t, traceDataToXTrace(ptrace.NewTraces(), "", nil))
	td := ptrace.NewTraces()
	spans := td.ResourceSpans().AppendEmpty().ScopeSpans().AppendEmpty().Spans()
	spans.AppendEmpty()
	invalid := spans.AppendEmpty()
	invalid.SetStartTimestamp(2000)
	invalid.SetEndTimestamp(1000)
	before := uint32(time.Now().Unix())
	logs := traceDataToXTrace(td, "", nil)
	require.Len(t, logs, 2)
	fields := xtraceFields(t, logs[0])
	assert.GreaterOrEqual(t, logs[0].GetTime(), before)
	assert.LessOrEqual(t, logs[0].GetTime(), uint32(time.Now().Unix()))
	for _, key := range []string{"traceId", "spanId", "parentSpanId", "serviceName", "hostname"} {
		assert.Empty(t, fields[key])
	}
	assert.Equal(t, "0", fields["kind"])
	assert.Equal(t, "0", fields["statusCode"])
	assert.Equal(t, "{}", fields["attributes"])
	assert.Equal(t, "{}", fields["resources"])
	assert.NotContains(t, fields, "pid")
	assert.Equal(t, "0", xtraceFields(t, logs[1])["duration"])
}

func TestXTraceResourceAndScopeIsolation(t *testing.T) {
	td := ptrace.NewTraces()
	for _, service := range []string{"one", "two"} {
		rs := td.ResourceSpans().AppendEmpty()
		rs.Resource().Attributes().PutStr("service.name", service)
		for _, scope := range []string{"first", "second"} {
			ss := rs.ScopeSpans().AppendEmpty()
			ss.Scope().SetName(scope)
			ss.Spans().AppendEmpty().SetStartTimestamp(pcommon.Timestamp(1))
		}
	}
	logs := traceDataToXTrace(td, "", nil)
	require.Len(t, logs, 4)
	for i, expected := range []struct{ service, scope string }{{"one", "first"}, {"one", "second"}, {"two", "first"}, {"two", "second"}} {
		fields := xtraceFields(t, logs[i])
		assert.Equal(t, expected.service, fields["serviceName"])
		assert.JSONEq(t, `{"otel.scope.name":"`+expected.scope+`"}`, fields["attributes"])
	}
}

func TestXTracePIDLookup(t *testing.T) {
	td := ptrace.NewTraces()
	for _, service := range []string{"orders.API", "payments", "unknown", ""} {
		rs := td.ResourceSpans().AppendEmpty()
		if service != "" {
			rs.Resource().Attributes().PutStr("service.name", service)
		}
		rs.ScopeSpans().AppendEmpty().Spans().AppendEmpty()
	}
	lookup := map[string]string{"orders.API": "orders-pid", "payments": "payments-pid"}
	for _, fallback := range []string{"", "default-pid"} {
		t.Run("fallback="+fallback, func(t *testing.T) {
			client := &captureTraceClient{}
			sender := logServiceTraceSender{client: client, traceFormat: "xtrace", tracePID: fallback, tracePIDByService: lookup}
			require.NoError(t, sender.pushTraceData(t.Context(), td))
			require.Len(t, client.logs, 4)
			for i, expected := range []string{"orders-pid", "payments-pid", fallback, fallback} {
				fields := xtraceFields(t, client.logs[i])
				if expected == "" {
					assert.NotContains(t, fields, "pid")
				} else {
					assert.Equal(t, expected, fields["pid"])
				}
			}
		})
	}
	assert.Equal(t, map[string]string{"orders.API": "orders-pid", "payments": "payments-pid"}, lookup)
	assert.Equal(t, "orders.API", td.ResourceSpans().At(0).Resource().Attributes().AsRaw()["service.name"])
}
