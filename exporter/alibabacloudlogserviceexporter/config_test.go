// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package alibabacloudlogserviceexporter

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/config/configopaque"
	"go.opentelemetry.io/collector/confmap"
	"go.opentelemetry.io/collector/confmap/confmaptest"

	"github.com/open-telemetry/opentelemetry-collector-contrib/exporter/alibabacloudlogserviceexporter/internal/metadata"
)

func TestLoadConfig(t *testing.T) {
	t.Parallel()

	cm, err := confmaptest.LoadConf(filepath.Join("testdata", "config.yaml"))
	require.NoError(t, err)

	// Endpoint doesn't have a default value so set it directly.
	defaultCfg := createDefaultConfig().(*Config)
	defaultCfg.Endpoint = "cn-hangzhou.log.aliyuncs.com"

	tests := []struct {
		id       component.ID
		expected component.Config
	}{
		{
			id:       component.NewIDWithName(metadata.Type, ""),
			expected: defaultCfg,
		},
		{
			id: component.NewIDWithName(metadata.Type, "2"),
			expected: &Config{
				Endpoint:        "cn-hangzhou.log.aliyuncs.com",
				Project:         "demo-project",
				Logstore:        "demo-logstore",
				AccessKeyID:     "test-id",
				AccessKeySecret: "test-secret",
				SecurityToken:   configopaque.String("test-token"),
			},
		},
		{
			id: component.NewIDWithName(metadata.Type, "xtrace"),
			expected: &Config{
				Endpoint:                  "cn-shanghai.log.aliyuncs.com",
				Project:                   "demo-project",
				Logstore:                  "logstore-tracing",
				TraceFormat:               "xtrace",
				TracePID:                  "demo-application-id",
				TracePIDAutoDiscovery:     true,
				TracePIDDiscoveryProject:  "reference-project",
				TracePIDDiscoveryLogstore: "reference-traces",
				TracePIDByService:         map[string]string{"orders.API": "orders-app-id", "payments": "payments-app-id"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.id.String(), func(t *testing.T) {
			factory := NewFactory()
			cfg := factory.CreateDefaultConfig()

			sub, err := cm.Sub(tt.id.String())
			require.NoError(t, err)
			require.NoError(t, sub.Unmarshal(cfg))

			assert.NoError(t, confmap.Validate(cfg))
			assert.Equal(t, tt.expected, cfg)
		})
	}
}

func TestValidateTraceFormat(t *testing.T) {
	for _, format := range []string{"", "legacy", "xtrace"} {
		assert.NoError(t, (&Config{TraceFormat: format}).Validate())
	}
	assert.ErrorContains(t, (&Config{TraceFormat: "typo"}).Validate(), "unsupported trace_format")
	assert.ErrorContains(t, (&Config{TracePID: "app"}).Validate(), "trace_pid requires")
	assert.NoError(t, (&Config{TraceFormat: "xtrace", TracePID: "app"}).Validate())
}

func TestValidateTracePIDLookup(t *testing.T) {
	for _, tc := range []struct {
		name, format, service, pid string
		valid                      bool
	}{
		{"valid", "xtrace", "orders", "app-1", true},
		{"legacy", "legacy", "orders", "app-1", false},
		{"empty name", "xtrace", "", "app-1", false},
		{"blank name", "xtrace", " ", "app-1", false},
		{"empty pid", "xtrace", "orders", "", false},
		{"blank pid", "xtrace", "orders", " ", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := (&Config{TraceFormat: tc.format, TracePIDByService: map[string]string{tc.service: tc.pid}}).Validate()
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestValidatePIDAutoDiscovery(t *testing.T) {
	require.ErrorContains(t, (&Config{TracePIDAutoDiscovery: true}).Validate(), "requires trace_format xtrace")
	require.ErrorContains(t, (&Config{TraceFormat: "xtrace", TracePIDDiscoveryProject: "source"}).Validate(), "requires trace_pid_auto_discovery")
	require.NoError(t, (&Config{TraceFormat: "xtrace", TracePIDAutoDiscovery: true}).Validate())
}
