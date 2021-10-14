package watcher

import (
	"log"

	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/informers"
	coreinformers "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/tools/cache"

	"github.com/prometheus/client_golang/prometheus"
)

type (
	Watcher struct {
		factory     informers.SharedInformerFactory
		podInformer coreinformers.PodInformer
		pvcInformer coreinformers.PersistentVolumeClaimInformer
		nsInformer  coreinformers.NamespaceInformer

		promMissingBackups *prometheus.GaugeVec
	}

	PVCInfo struct {
		Namespace string
		PVCName   string
	}
)

// NewWatcher creates a new Watcher
func NewWatcher(factory informers.SharedInformerFactory, stopper chan struct{}) *Watcher {
	podInformer := factory.Core().V1().Pods()
	pvcInformer := factory.Core().V1().PersistentVolumeClaims()
	nsInformer := factory.Core().V1().Namespaces()

	promMissingBackups := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "backupmonitor_missing",
		Help: "Unconfigured PXC Backups",
	}, []string{
		"namespace",
		"pvc_name",
	})

	return &Watcher{
		factory:            factory,
		podInformer:        podInformer,
		pvcInformer:        pvcInformer,
		nsInformer:         nsInformer,
		promMissingBackups: promMissingBackups,
	}
}

// Run starts all Informers and waits for the initial cache to sync
func (w *Watcher) Run(stopper chan struct{}) {
	go w.podInformer.Informer().Run(stopper)
	go w.pvcInformer.Informer().Run(stopper)
	go w.nsInformer.Informer().Run(stopper)

	if !cache.WaitForCacheSync(nil, w.podInformer.Informer().HasSynced) {
		log.Printf("failed to sync pods")
	}
	if !cache.WaitForCacheSync(nil, w.pvcInformer.Informer().HasSynced) {
		log.Printf("failed to sync pvcs")
	}
	if !cache.WaitForCacheSync(nil, w.nsInformer.Informer().HasSynced) {
		log.Printf("failed to sync namespaces")
	}
}

// Update verifies that all PVCs have a backup configured in a namespace
func (w *Watcher) Update(namespace string) []PVCInfo {
	handledPVCs := map[string]interface{}{}
	err := w.getHandledPVCs(namespace, &handledPVCs)
	if err != nil {
		log.Printf("unable to list pods: %s", err)
		return nil
	}

	missing := []PVCInfo{}
	pvcList, err := w.pvcInformer.Lister().PersistentVolumeClaims(namespace).List(labels.Everything())
	if err != nil {
		log.Printf("unable to list persistent volume claims: %s", err)
		return nil
	}
	for _, pvc := range pvcList {
		annotations := pvc.GetAnnotations()
		if v, ok := annotations[ExcludePVCAnnotation]; ok && v == "true" {
			continue
		}
		pvcName := pvc.GetName()
		if _, ok := handledPVCs[pvcName]; !ok {
			missing = append(missing, PVCInfo{
				Namespace: namespace,
				PVCName:   pvcName,
			})
		}
	}
	return missing
}
func (w *Watcher) ListNamespaces() ([]*v1.Namespace, error) {
	return w.nsInformer.Lister().List(labels.Everything())
}

// getHandledPVCs lists all PVCs that have a backup handling defined on a pod
func (w *Watcher) getHandledPVCs(namespace string, pvcNames *map[string]interface{}) error {
	podList, err := w.podInformer.Lister().Pods(namespace).List(labels.Everything())
	if err != nil {
		return err
	}
	knownParents := map[string]struct{}{}
pods:
	for _, pod := range podList {
		owners := pod.GetOwnerReferences()
		for _, owner := range owners {
			if _, ok := knownParents[string(owner.UID)]; ok && owner.Kind != "StatefulSet" {
				continue pods
			}
			knownParents[string(owner.UID)] = struct{}{}
		}
		listPodHandledPVCs(pod, pvcNames)

	}
	return nil
}
