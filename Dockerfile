FROM alpine

ADD ./velero-pvc-watcher /velero-pvc-watcher

ENTRYPOINT /velero-pvc-watcher
