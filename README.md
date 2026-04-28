# OBS-Board (obs-dashboard)

Unified observability dashboard for Kubernetes clusters, infrastructure, and CI/CD pipelines.
- **Backend**: Go (k8s client-go, node-exporter scraping, GitLab API integration)
- **Frontend**: Single HTML/JS, no build required
- **Data sources**:
  - Kubernetes: native API + metrics-server (no external Prometheus needed)
  - Infrastructure: node-exporter (/metrics scraping with alert rules)
  - CI/CD: GitLab Runners & Pipelines (GraphQL + REST API)
  - Observability: Links to Grafana, OpenSearch, Jaeger

## Quick Start

### 1. Kubernetes + metrics-server

```bash
# Verify metrics-server is running
kubectl top nodes

# Start the dashboard (single cluster)
make run
# → http://localhost:8080
```

Environment variables:
```bash
make run KUBECONFIG=~/.kube/homelab ADDR=:8080
```

### 2. Multiple clusters (optional)

Create `clusters.yaml`:
```yaml
clusters:
  - name: homelab
    kubeconfig: ~/.kube/homelab
  - name: prod
    kubeconfig: ~/.kube/prod
```

Run:
```bash
make run-multi CLUSTERS=./clusters.yaml
```

### 3. Infrastructure monitoring (optional)

Create `servers.yaml`:
```yaml
servers:
  - name: web-1
    url: http://192.168.1.10:9100
  - name: db-1
    url: http://192.168.1.11:9100
```

Run:
```bash
make run-multi CLUSTERS=./clusters.yaml SERVERS=./servers.yaml
```

### 4. GitLab integration (optional)

```bash
# Auto-detect from glab config
glab auth login

# Then start the dashboard
make run-multi CLUSTERS=./clusters.yaml

# Or explicitly provide credentials
make run GITLAB_URL=https://gitlab.example.com GITLAB_TOKEN=glpat-xxxxx
```

### 5. External observability tools (optional)

Link to existing observability infrastructure:
```bash
make run GRAFANA_URL=https://grafana.example.com \
         OPENSEARCH_URL=https://opensearch.example.com \
         JAEGER_URL=https://jaeger.example.com
```

## Testing without infrastructure

### Stub mode (synthetic data)
```bash
make run-stub
# → http://localhost:8080
```

All data is mocked — no Kubernetes access or external dependencies required.

### API health check
```bash
# These work in any mode (live or stub)
curl -s localhost:8080/api/health   | jq    # cluster reachability
curl -s localhost:8080/api/overview | jq    # aggregated metrics
curl -s localhost:8080/api/about    | jq    # app info + all endpoints
curl -s localhost:8080/api/tools    | jq    # configured external tools
```

## API Endpoints

### Kubernetes
| Path                    | Description                                         |
|-------------------------|--------------------------------------------------|
| `GET /api/health`       | Cluster + metrics-server reachability                 |
| `GET /api/overview`     | Summary stats (nodes, pods, avg CPU/MEM)            |
| `GET /api/nodes`        | Node list with per-node CPU/MEM/kernel info         |
| `GET /api/pods/status`  | Pod phase counts (Running/Failed/Pending/etc)       |
| `GET /api/workloads`    | Deployments + StatefulSets + DaemonSets            |
| `GET /api/events`       | Recent cluster events (time-sorted)                |
| `GET /api/namespaces`   | Namespace list with pod counts                     |
| `GET /api/metrics/cluster` | CPU/MEM time-series ring buffer (30 min, in-memory) |
| `GET /api/clusters`     | Cluster list + per-cluster health                  |

### Infrastructure (node-exporter)
| Path                    | Description                                         |
|-------------------------|--------------------------------------------------|
| `GET /api/servers`      | Host list + alert severity + status                |
| `GET /api/servers/detail?name=N` | Single host detail (uname/cpu/mem/fs/net/alerts) |

### GitLab CI/CD
| Path                    | Description                                         |
|-------------------------|--------------------------------------------------|
| `GET /api/runners`      | GitLab CI runners + status (online/paused/dead)     |
| `GET /api/pipelines`    | Pipelines grouped by project (30s cache)           |
| `GET /api/pipelines/trends?project=<id>` | 24h success/fail/other trend      |
| `POST /api/pipelines/action` | Retry or cancel a pipeline               |

### Meta / Info
| Path                    | Description                                         |
|-------------------------|--------------------------------------------------|
| `GET /api/tools`        | Configured Grafana/OpenSearch/Jaeger links         |
| `GET /api/about`        | App info, uptime, config, all endpoints            |

**Query parameters:**
- All Kubernetes endpoints accept optional `?cluster=<name>` to target a specific cluster
- Without it, defaults to the first cluster in config

## Metrics

### CPU / Memory calculation
- **Per-node**: `Usage / Capacity` from `metrics.k8s.io/v1beta1` and `corev1.Node`
- **Cluster average**: `sum(used) / sum(capacity)` across all nodes
- **30-minute history**: Backend samples avg every 60 seconds into a ring buffer (30 points per cluster)
  - Stored in-memory, cleared on restart (no external database needed)

## Infrastructure Monitoring (node-exporter)

Backend scrapes `/metrics` from each server every 30 seconds and evaluates alert rules (based on awesome-prometheus-alerts):

| Rule                  | Severity                                  |
|-----------------------|-------------------------------------------|
| HostHighMemoryUsage   | ⚠️ ≥ 85%, 🔴 ≥ 95%                          |
| HostHighLoadAverage   | ⚠️ ≥ 1.5/core, 🔴 ≥ 2.0/core (load5)       |
| HostOutOfDiskSpace    | ⚠️ < 15% free, 🔴 < 5% (any filesystem)   |
| HostOutOfInodes       | ⚠️ < 15% free, 🔴 < 5% (any filesystem)   |
| HostClockSkew         | ⚠️ ≥ 50ms, 🔴 ≥ 1s (NTP offset)            |
| NodeExporterDown      | 🔴 metrics endpoint unreachable            |

**UI features:**
- **Servers tab** in navigation shows aggregate alert severity
- Host list with status dot, name, URL
- Click row to expand inline alert details (severity + message like "memory used 88.3%")
- **OPEN ↗** button for full-screen host details: uname, uptime, load avg, memory breakdown, filesystems with inode usage, network counters, all alerts

## UI Design

### Color system
| Color | CSS var | Meaning |
|-------|---------|---------|
| 🟢 Green | `--accent` | OK / online / healthy |
| 🟡 Yellow | `--warn` | Warning / degraded / paused |
| 🔴 Red | `--err` | Problem / unreachable / error |
| 🟣 Purple | `--purple` | Static / not live-monitored |

### Navigation tabs
- Neutral styling at rest
- Dynamically assigned `.ok` / `.warn` / `.crit` / `.down` class based on live data
- Active tab with status class shows tinted background + colored border
- Tools tab always purple (not live-monitored)

### Runner dots
- 🟢 Online — accepting jobs
- ⚫ Paused — on pause, not accepting
- 🔴 Dead — no heartbeat / offline

Hover tooltip shows `last heartbeat <time>`

### Pipeline status
- 🟢 success — green
- 🔵 running — blue with pulse
- 🟡 pending — yellow
- 🔴 failed — red
- ⚫ canceled / skipped — gray / dimmed

## GitLab Runners & Pipelines

### Runners

The **Runners** tab shows all GitLab runners: status (online/paused/dead), type (shared/group/project), tags, and last heartbeat.

### Authorization

1. **Auto-detect from `glab` config** (recommended):
   ```bash
   glab auth login          # one-time setup
   make run                 # dashboard auto-loads GitLab URL + token
   ```

2. **Explicit flags**:
   ```bash
   make run GITLAB_URL=https://gitlab.example.com GITLAB_TOKEN=glpat-xxxxx
   ```

3. **Environment variables**:
   ```bash
   export GITLAB_URL=https://gitlab.example.com
   export GITLAB_TOKEN=glpat-xxxxx
   make run
   ```

**Priority**: flags > `GITLAB_TOKEN` env > glab config

**Token requirements**: Needs `read_api` scope. Admin token gives access to all runners; user token shows only runners you have access to.

### Pipelines

Panel below runners shows pipelines grouped by project, sorted by activity (refreshes every 30s):

- **Filter**: Project name chips (saved to localStorage)
- **Pipeline card**: `#<id> / <branch>` + metadata (status, author, duration, timestamp)
- **Actions**: Retry (↺) and cancel (✕) buttons — tries `glab pipeline` commands, falls back to REST API
- **Trends**: Shows 24h success/fail/other distribution per project

## In-Cluster Deployment

To run the dashboard inside a Kubernetes cluster, use an in-cluster ServiceAccount instead of kubeconfig:

```bash
make run -kubeconfig=""  # uses in-cluster ServiceAccount
```

RBAC manifest:

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: obs-dashboard
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: obs-dashboard-reader
rules:
  - apiGroups: [""]
    resources: [nodes, pods, services, namespaces, events]
    verbs: [get, list, watch]
  - apiGroups: ["apps"]
    resources: [deployments, statefulsets, daemonsets]
    verbs: [get, list]
  - apiGroups: ["metrics.k8s.io"]
    resources: [nodes, pods]
    verbs: [get, list]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: obs-dashboard-reader
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: obs-dashboard-reader
subjects:
  - kind: ServiceAccount
    name: obs-dashboard
    namespace: default
```

Use the Docker image:
```bash
docker build -t obs-dashboard .
kubectl create deployment obs-dashboard --image=obs-dashboard --port=8080
kubectl patch deployment obs-dashboard -p '{"spec":{"template":{"spec":{"serviceAccountName":"obs-dashboard"}}}}'
```

Or via Helm/Kustomize with the above manifests + Deployment with `serviceAccountName: obs-dashboard`.
