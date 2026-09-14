# regtool-hub Helm chart

Deploys [`regtool-hub`](https://github.com/ZHallen122/RegTool) — the optional
service that serves the registry mirror list the `regtool` CLI reads, checks
every mirror in it on a schedule, and exports the results as JSON and as
Prometheus metrics.

The chart renders a Deployment, a Service, a ServiceAccount, a PVC for the
SQLite check history, an optional ConfigMap for an inline mirror list, and an
optional ServiceMonitor.

## Install

From the published OCI registry:

```sh
helm install regtool-hub oci://ghcr.io/zhallen122/charts/regtool-hub \
  --namespace regtool --create-namespace
```

From a checkout of the repository:

```sh
helm install regtool-hub ./deploy/helm/regtool-hub \
  --namespace regtool --create-namespace
```

Then reach it:

```sh
kubectl -n regtool port-forward svc/regtool-hub 8080:8080
curl http://localhost:8080/v1/sources
```

and point the CLI at it:

```sh
export REGTOOL_SOURCES_URL=http://localhost:8080/v1/sources
regtool list npm
```

`helm test regtool-hub -n regtool` runs a one-shot pod that fetches `/healthz`
through the Service.

## Single replica, and why

`replicaCount` defaults to `1` and should stay there. The check history lives
in a SQLite file on a ReadWriteOnce volume, and SQLite wants a single writer. A
second replica would either fail to schedule — most CSI drivers will not attach
an RWO volume to two nodes — or, on a driver that allows multi-attach, have two
processes writing the same database file. It would also buy nothing: the mirror
list is the same bytes on every pod, and the checks are cheap. To serve more
than one network, run a second release in the other cluster or region.

The same constraint is why the Deployment uses the `Recreate` strategy: a
rolling update would wait forever for a volume the outgoing pod is still
holding.

With `persistence.enabled=false` the pod gets an `emptyDir` instead. The mirror
list still serves and the checks still run; only `/v1/health/history` is reset
on every restart. That is fine for a demo or for CI, and not for anything else.

## Serving your own mirror list

Set `hub.sources` to the contents of a `sources.json` and the chart writes it
into a ConfigMap, mounts it read-only and passes `--sources`. It is served
verbatim, byte for byte, so whatever is in that value is exactly what the CLI
sees. A change to it rolls the pod, via a checksum annotation on the pod
template. Leave it empty to serve the list embedded in the binary.

```sh
helm install regtool-hub oci://ghcr.io/zhallen122/charts/regtool-hub \
  --set-file hub.sources=./source/sources.json
```

## Prometheus

Set `serviceMonitor.enabled=true` to have the Prometheus Operator scrape
`/metrics`. It is off by default because the `monitoring.coreos.com/v1` CRD is
not in a stock cluster and the install would fail without it. Most operators
only pick up ServiceMonitors carrying a particular label, so set
`serviceMonitor.labels` to match — with kube-prometheus-stack that is usually
`release: <the Prometheus release name>`:

```sh
helm upgrade regtool-hub oci://ghcr.io/zhallen122/charts/regtool-hub \
  --set serviceMonitor.enabled=true \
  --set serviceMonitor.labels.release=kube-prometheus-stack
```

## Values

### Image

| Key | Default | Description |
| --- | --- | --- |
| `image.repository` | `ghcr.io/zhallen122/regtool-hub` | Image to run. |
| `image.tag` | `""` | Image tag; empty means the chart's `appVersion`. |
| `image.pullPolicy` | `IfNotPresent` | |
| `imagePullSecrets` | `[]` | Secrets for a private registry. |

### Workload

| Key | Default | Description |
| --- | --- | --- |
| `replicaCount` | `1` | Leave at 1 — see "Single replica, and why". |
| `nameOverride` | `""` | Overrides the chart name in object names and labels. |
| `fullnameOverride` | `""` | Overrides the full name of every object. |
| `serviceAccount.create` | `true` | Create a ServiceAccount for the pod. |
| `serviceAccount.automount` | `false` | The hub never calls the API server. |
| `serviceAccount.annotations` | `{}` | |
| `serviceAccount.name` | `""` | Defaults to the full name when created. |
| `podAnnotations` | `{}` | |
| `podLabels` | `{}` | |
| `nodeSelector` | `{}` | |
| `tolerations` | `[]` | |
| `affinity` | `{}` | |
| `topologySpreadConstraints` | `[]` | |
| `priorityClassName` | `""` | |
| `terminationGracePeriodSeconds` | `30` | The hub drains for up to 10s on SIGTERM. |
| `extraVolumes` | `[]` | |
| `extraVolumeMounts` | `[]` | |

### Hub configuration

Each of these maps onto a flag of the binary.

| Key | Default | Flag |
| --- | --- | --- |
| `hub.checkInterval` | `5m` | `--check-interval` |
| `hub.checkTimeout` | `5s` | `--check-timeout` |
| `hub.retention` | `7d` | `--retention` |
| `hub.logLevel` | `info` | `--log-level` |
| `hub.sources` | `""` | `--sources`, via a ConfigMap, when set |
| `hub.extraArgs` | `[]` | Appended to the container's args. |
| `hub.extraEnv` | `[]` | Extra environment variables. |

`--db` is not a value: the image's entrypoint pins it to `/data/hub.db`, which
is where the volume is mounted.

### Service

| Key | Default | Description |
| --- | --- | --- |
| `service.type` | `ClusterIP` | |
| `service.port` | `8080` | Also the port the container listens on. |
| `service.annotations` | `{}` | |
| `service.nodePort` | `""` | Only honoured when `service.type` is `NodePort`. |

### Persistence

| Key | Default | Description |
| --- | --- | --- |
| `persistence.enabled` | `true` | `false` uses an `emptyDir` instead. |
| `persistence.size` | `1Gi` | |
| `persistence.storageClass` | `""` | `""` is the cluster default; `-` disables dynamic provisioning. |
| `persistence.accessModes` | `["ReadWriteOnce"]` | |
| `persistence.annotations` | `{}` | |
| `persistence.existingClaim` | `""` | Mount a claim the chart does not manage. |

### Resources and security

| Key | Default |
| --- | --- |
| `resources.requests.cpu` | `50m` |
| `resources.requests.memory` | `64Mi` |
| `resources.limits.cpu` | `200m` |
| `resources.limits.memory` | `128Mi` |
| `podSecurityContext` | `runAsNonRoot: true`, uid/gid/fsGroup `65532`, `seccompProfile: RuntimeDefault` |
| `containerSecurityContext` | `readOnlyRootFilesystem: true`, `allowPrivilegeEscalation: false`, all capabilities dropped |

The image is distroless and already runs as uid 65532; the chart states it so
the cluster can enforce it. `fsGroup` is what makes a freshly provisioned PVC
writable by that uid.

### Probes

| Key | Default | Description |
| --- | --- | --- |
| `livenessProbe.*` | delay 5s, period 20s, timeout 3s, 3 failures | `/healthz`, which touches nothing. |
| `readinessProbe.*` | delay 2s, period 5s, timeout 3s, 12 failures | `/readyz`, which is 503 until the first check finishes (or 30s passes) — hence the generous threshold. |

### ServiceMonitor

| Key | Default | Description |
| --- | --- | --- |
| `serviceMonitor.enabled` | `false` | Needs the Prometheus Operator CRD. |
| `serviceMonitor.namespace` | `""` | Defaults to the release namespace. |
| `serviceMonitor.interval` | `30s` | |
| `serviceMonitor.scrapeTimeout` | `10s` | |
| `serviceMonitor.labels` | `{}` | Must match the operator's `serviceMonitorSelector`. |
| `serviceMonitor.annotations` | `{}` | |
| `serviceMonitor.relabelings` | `[]` | |
| `serviceMonitor.metricRelabelings` | `[]` | |

### Test hook

| Key | Default | Description |
| --- | --- | --- |
| `tests.image.repository` | `busybox` | The hub's own image is distroless, so it has no shell or wget for `helm test`. |
| `tests.image.tag` | `1.37` | |
| `tests.image.pullPolicy` | `IfNotPresent` | |

## Developing the chart

```sh
make helm-lint      # helm lint against every file in ci/
make helm-template   # render the default, no-persistence and ServiceMonitor cases
make kind-e2e        # build the image, install it into a throwaway kind cluster, curl it
```

`ci/*-values.yaml` are the value combinations CI lints. Add one whenever a
template grows a branch that the existing files do not reach.
