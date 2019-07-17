# velero-pvc-watcher

[![Build Status](https://cloud.drone.io/api/badges/bitsbeats/velero-pvc-watcher/status.svg)](https://cloud.drone.io/bitsbeats/velero-pvc-watcher)
[![Gitter chat](https://badges.gitter.im/drone/drone.png)](https://gitter.im/drone/drone)
[![Join the discussion at https://discourse.drone.io](https://img.shields.io/badge/discourse-forum-orange.svg)](https://discourse.drone.io)
[![Drone questions at https://stackoverflow.com](https://img.shields.io/badge/drone-stackoverflow-orange.svg)](https://stackoverflow.com/questions/tagged/drone.io)
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

```console
git clone git@github.com:bitsbeats/velero-pvc-watcher.git
cd velero-pvc-watcher
kubectl create namespace velero-pvc-watcher
kubectl config set-context --current --namespace=velero-pvc-watcher
helm upgrade -i velero-pvc-watcher velero-pvc-watcher
```
You can now scrape the metrics directly via prometheus kubernetes discovery, annotations:
| annotation | default value |
|---|---|
| prometheus.io/scrape | true     |
| prometheus.io/port   | 2112     |
| prometheus.io/path   | /metrics |

## Build
```console
CGO_ENABLED=0 go build .
```

## Run / Dev

Start minikube.

```console
go run .
```
