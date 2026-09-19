// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package alibabacloudlogserviceexporter // import "github.com/open-telemetry/opentelemetry-collector-contrib/exporter/alibabacloudlogserviceexporter"

import (
	"errors"
	"fmt"
	"strings"

	"go.opentelemetry.io/collector/config/configopaque"
)

// Config defines configuration for AlibabaCloud Log Service exporter.
type Config struct {
	// Trace output format: empty or "legacy" for the original SLS format, or "xtrace" for logstore-tracing.
	TraceFormat string `mapstructure:"trace_format"`
	// XTrace application identifier to emit as pid. This is not an OS process ID.
	TracePID string `mapstructure:"trace_pid"`
	// XTrace application identifiers keyed by exact resource service.name; overrides trace_pid.
	TracePIDByService map[string]string `mapstructure:"trace_pid_by_service"`
	// Enable startup and hourly discovery of XTrace PIDs from SLS.
	TracePIDAutoDiscovery bool `mapstructure:"trace_pid_auto_discovery"`
	// Optional discovery source project; defaults to project.
	TracePIDDiscoveryProject string `mapstructure:"trace_pid_discovery_project"`
	// Optional discovery source logstore; defaults to logstore.
	TracePIDDiscoveryLogstore string `mapstructure:"trace_pid_discovery_logstore"`
	// LogService's Endpoint, https://www.alibabacloud.com/help/doc-detail/29008.htm
	// for AlibabaCloud Kubernetes(or ECS), set {region-id}-intranet.log.aliyuncs.com, eg cn-hangzhou-intranet.log.aliyuncs.com;
	//  others set {region-id}.log.aliyuncs.com, eg cn-hangzhou.log.aliyuncs.com
	Endpoint string `mapstructure:"endpoint"`
	// LogService's Project Name
	Project string `mapstructure:"project"`
	// LogService's Logstore Name
	Logstore string `mapstructure:"logstore"`
	// AlibabaCloud access key id
	AccessKeyID string `mapstructure:"access_key_id"`
	// AlibabaCloud access key secret
	AccessKeySecret configopaque.String `mapstructure:"access_key_secret"`
	// AlibabaCloud security token for STS credentials
	SecurityToken configopaque.String `mapstructure:"security_token"`
	// Set AlibabaCLoud ECS ram role if you are using ACK
	ECSRamRole string `mapstructure:"ecs_ram_role"`
	// Set Token File Path if you are using ACK
	TokenFilePath string `mapstructure:"token_file_path"`
}

func (c *Config) Validate() error {
	switch c.TraceFormat {
	case "", "legacy", "xtrace":
	default:
		return fmt.Errorf("unsupported trace_format %q: must be legacy or xtrace", c.TraceFormat)
	}
	if c.TracePID != "" && c.TraceFormat != "xtrace" {
		return errors.New("trace_pid requires trace_format xtrace")
	}
	if len(c.TracePIDByService) > 0 && c.TraceFormat != "xtrace" {
		return errors.New("trace_pid_by_service requires trace_format xtrace")
	}
	for service, pid := range c.TracePIDByService {
		if strings.TrimSpace(service) == "" || strings.TrimSpace(pid) == "" {
			return errors.New("trace_pid_by_service requires nonempty service names and pids")
		}
	}
	if c.TracePIDAutoDiscovery && c.TraceFormat != "xtrace" {
		return errors.New("trace_pid_auto_discovery requires trace_format xtrace")
	}
	if !c.TracePIDAutoDiscovery && (c.TracePIDDiscoveryProject != "" || c.TracePIDDiscoveryLogstore != "") {
		return errors.New("trace_pid_discovery source requires trace_pid_auto_discovery")
	}
	return nil
}
