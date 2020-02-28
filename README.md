# velero-pvc-watcher

[![Build Status](https://cloud.drone.io/api/badges/bitsbeats/velero-pvc-watcher/status.svg)](https://cloud.drone.io/bitsbeats/velero-pvc-watcher)
[![Go Report](https://goreportcard.com/badge/github.com/bitsbeats/velero-pvc-watcher)](https://goreportcard.com/badge/github.com/bitsbeats/velero-pvc-watcher)

Kubernetes controller for velero that detects PVCs with no restic backup and exposes a prometheus metric

If you use restic with velero you must annotate all pods to enable volume backups.

This controller will expose prometheus metrics with a static value '1' to generate alerts for volumes that are not in the backup or backup-exclusion annotation.

The controller supports in and out of cluster operation.

You can easily develop via minikube and 'go run .'

For real world usage use the docker image and helm chart.

## Environment Variables


| variable | description  | default value |
|---|---|---|
| METRICSPATH          | url path of the prom metrics | /metrics  |
| PORT                 | port of the metrics webserver | 2112 |
| EXCLUDEANNOTATION    | pod annotation for backup excluded volumes | backup.velero.io/backup-volumes-excludes |
| BACKUPANNOTATION     | pod annotation for backup volumes  | backup.velero.io/backup-volumes |

## Installation

Helm Chart
https://github.com/bitsbeats/helm-charts

```console
helm repo add bitsbeats https://bitsbeats.github.io/helm-charts/
helm upgrade -i --namespace <YOUR NAMESPACE> bitsbeats/velero-pvc-watcher
```
You can now scrape the metrics directly via prometheus kubernetes discovery, annotations:

| annotation | default value |
|---|---|
| prometheus.io/scrape | true     |
| prometheus.io/port   | 2112     |
| prometheus.io/path   | /metrics |

## Example StatefulSet config
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
    {{ $labels.instance }} {{ $labels.namespace }} {{ $labels.owner_kind }}/{{ $labels.owner_name }}
    pvc backup is not configured or deactivated.
```

## Build
```console
CGO_ENABLED=0 go build .
```

## Run / Dev

Start minikube.

```console
go run .
```
