FROM alpine

ADD ./velero-pvc-watcher /usr/local/bin/velero-pvc-watcher

ENTRYPOINT /usr/local/bin/velero-pvc-watcher
