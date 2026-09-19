// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package alibabacloudlogserviceexporter // import "github.com/open-telemetry/opentelemetry-collector-contrib/exporter/alibabacloudlogserviceexporter"

import (
	"encoding/json"
	"strconv"
	"time"

	sls "github.com/aliyun/aliyun-log-go-sdk"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/open-telemetry/opentelemetry-collector-contrib/internal/coreinternal/traceutil"
)

// traceDataToXTrace matches the schema used by XTrace's logstore-tracing.
func traceDataToXTrace(td ptrace.Traces, defaultPID string, pidByService map[string]string) []*sls.Log {
	logs := make([]*sls.Log, 0, td.SpanCount())
	for _, rs := range td.ResourceSpans().All() {
		resources := xtraceAttributes(rs.Resource().Attributes())
		resourceJSON := xtraceJSON(resources)
		pid := defaultPID
		if mappedPID, ok := pidByService[resources["service.name"]]; ok {
			pid = mappedPID
		}
		for _, ss := range rs.ScopeSpans().All() {
			for _, span := range ss.Spans().All() {
				log := &sls.Log{Contents: make([]*sls.LogContent, 0, 19)}
				add := func(key, value string) {
					log.Contents = append(log.Contents, &sls.LogContent{Key: new(key), Value: new(value)})
				}
				logTime := span.EndTimestamp()
				if logTime == 0 {
					logTime = pcommon.NewTimestampFromTime(time.Now())
				}
				log.Time = new(uint32(logTime / 1_000_000_000))
				add("traceId", traceutil.TraceIDToHexOrEmptyString(span.TraceID()))
				add("spanId", traceutil.SpanIDToHexOrEmptyString(span.SpanID()))
				add("parentSpanId", traceutil.SpanIDToHexOrEmptyString(span.ParentSpanID()))
				add("spanName", span.Name())
				add("kind", strconv.FormatInt(int64(span.Kind()), 10))
				add("traceState", span.TraceState().AsRaw())
				add("startTime", strconv.FormatUint(uint64(span.StartTimestamp()), 10))
				add("endTime", strconv.FormatUint(uint64(span.EndTimestamp()), 10))
				// Incomplete or invalid spans must not wrap to a huge unsigned duration.
				var duration uint64
				if span.EndTimestamp() >= span.StartTimestamp() {
					duration = uint64(span.EndTimestamp() - span.StartTimestamp())
				}
				add("duration", strconv.FormatUint(duration, 10))
				add("statusCode", strconv.FormatInt(int64(span.Status().Code()), 10))
				add("statusMessage", span.Status().Message())
				add("serviceName", resources["service.name"])
				add("hostname", resources["host.name"])
				add("resources", resourceJSON)
				if pid != "" {
					add("pid", pid)
				}
				attributes := xtraceAttributes(span.Attributes())
				if name := ss.Scope().Name(); name != "" {
					attributes["otel.scope.name"] = name
				}
				if version := ss.Scope().Version(); version != "" {
					attributes["otel.scope.version"] = version
				}
				add("attributes", xtraceJSON(attributes))
				if span.Events().Len() > 0 {
					events := make([]map[string]any, 0, span.Events().Len())
					for _, event := range span.Events().All() {
						events = append(events, map[string]any{
							"name": event.Name(), "timestamp": uint64(event.Timestamp()),
							"attributes": xtraceAttributes(event.Attributes()),
						})
					}
					add("events", xtraceJSON(events))
				}
				if span.Links().Len() > 0 {
					links := make([]map[string]any, 0, span.Links().Len())
					for _, link := range span.Links().All() {
						links = append(links, map[string]any{
							"traceId":    traceutil.TraceIDToHexOrEmptyString(link.TraceID()),
							"spanId":     traceutil.SpanIDToHexOrEmptyString(link.SpanID()),
							"traceState": link.TraceState().AsRaw(),
							"attributes": xtraceAttributes(link.Attributes()),
						})
					}
					add("links", xtraceJSON(links))
				}
				logs = append(logs, log)
			}
		}
	}
	return logs
}

func xtraceAttributes(attrs pcommon.Map) map[string]string {
	values := make(map[string]string, attrs.Len())
	for key, value := range attrs.All() {
		values[key] = value.AsString()
	}
	return values
}

// All callers pass strings, unsigned timestamps, or containers of these types.
func xtraceJSON(value any) string {
	data, _ := json.Marshal(value)
	return string(data)
}
