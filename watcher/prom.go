package watcher

import (
	"github.com/prometheus/client_golang/prometheus"
)

func (w *Watcher) Describe(ch chan<- *prometheus.Desc) {
	w.promMissingBackups.Describe(ch)
}

func (w *Watcher) Collect(ch chan<- prometheus.Metric) {
	w.promMissingBackups.Reset()
	nsList, _ := w.ListNamespaces()
	for _, namespace := range nsList {
		for _, missing := range w.Update(namespace.GetName()) {
			w.promMissingBackups.With(prometheus.Labels{
				"namespace": missing.Namespace,
				"pvc_name":  missing.PVCName,
			}).Set(1)
		}
	}
	w.promMissingBackups.Collect(ch)
}
