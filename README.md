# velero-pvc-watcher

[![Build Status](https://cloud.drone.io/api/badges/bitsbeats/velero-pvc-watcher/status.svg)](https://cloud.drone.io/bitsbeats/velero-pvc-watcher)
[![Go Report](https://goreportcard.com/badge/github.com/bitsbeats/velero-pvc-watcher)](https://goreportcard.com/badge/github.com/bitsbeats/velero-pvc-watcher)


velero-pvc-watcher is a Prometheus exporter that monitores all PVCs in the cluster and verifies that a matching `backup.velero.io/backup-volumes` or a `backup.velero.io/backup-volumes-excludes` is set.

Note: Due to the design all unmounted PVCs will reported as not being backuped, since there is no configuration for them.

## Installation

Helm Chart
https://github.com/bitsbeats/helm-charts

```sh
helm repo add bitsbeats https://bitsbeats.github.io/helm-charts/
helm upgrade -i --namespace <YOUR NAMESPACE> bitsbeats/velero-pvc-watcher
```

You can now scrape the metrics directly via prometheus kubernetes discovery, annotations:

| annotation           | default value |
|----------------------|---------------|
| prometheus.io/scrape | true          |
| prometheus.io/port   | 2112          |
| prometheus.io/path   | /metrics      |

## Example StatefulSet config

**Note**: The names come from `pod.spec.volumes`, not the pvc name.

```
apiVersion: apps/v1
kind: StatefulSet
spec:
  template:
    metadata:
      annotations:
        backup.velero.io/backup-volumes: data,logging
        backup.velero.io/backup-volumes-excludes: tmp
```

## Example PVC config

To exclude a PVC that is not in use from backups annotate it as follows:

```yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  annotations:
    backup.velero.io/backup-excluded: "true"
```

## Example Alertmanager config

```

alert: Velero PVC Check
for: 10m
expr: |
  backupmonitor_missing != 0
labels:
  severity: warning
annotations:
  text: >-
    The pvc {{ $labels.pvc_name }} in namespace {{ $labels.namespace }} has no backup annotation.
  action: >-
    Either configure a backup or exclude the volume from backup. For more
    information visit https://github.com/bitsbeats/velero-pvc-watcher

```
