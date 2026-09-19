#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../.."

# Keep the deployed 0.160.0 distribution and compile the current SLS source
# against its API versions. Never modify the exporter's development go.mod.
python3 - <<'PY'
from pathlib import Path
import shutil
source = Path('exporter/alibabacloudlogserviceexporter')
target = Path('cmd/otelcontribcol-diagnostics/_sls-exporter')
if target.exists():
    shutil.rmtree(target)
shutil.copytree(source, target)
mod = (target / 'go.mod').read_text()
mod = mod.replace('v0.161.0', 'v0.160.0').replace('v1.67.0', 'v1.66.0')
mod = '\n'.join(line for line in mod.splitlines() if not line.startswith('replace ')) + '\n'
(target / 'go.mod').write_text(mod)
PY

(cd cmd/otelcontribcol-diagnostics/_sls-exporter && go mod tidy && go test -race ./...)
(cd internal/diagnostics/otlphttpexporter && go test -race ./...)
bash cmd/otelcontribcol-diagnostics/prepare-obi.sh otelcol-contrib
builder --skip-compilation --config cmd/otelcontribcol-diagnostics/manifest-sls.yaml
cd cmd/otelcontribcol-diagnostics/_build-sls
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -p 3 -trimpath -tags grpcnotrace -ldflags '-s -w' -o otelcol-contrib .
