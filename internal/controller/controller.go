package controller

import (
	"fmt"
	"strings"
	"time"

	"k8s.io/klog"

	"github.com/prometheus/client_golang/prometheus"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
)

// Controller default
type (
	OwnerName  string
	VolumeName string

	Controller struct {
		indexer           cache.Indexer
		queue             workqueue.RateLimitingInterface
		informer          cache.Controller
		deletedIndexer    cache.Indexer
		volumeMissing     prometheus.GaugeVec
		excludeAnnotation string
		backupAnnotation  string
		podCache          map[OwnerName]map[VolumeName]bool
	}
)

// New default
func New(queue workqueue.RateLimitingInterface, indexer cache.Indexer, informer cache.Controller, deletedIndexer cache.Indexer, volumeMissing prometheus.GaugeVec, excludeAnnotation string, backupAnnotation string) *Controller {
	return &Controller{
		informer:          informer,
		indexer:           indexer,
		queue:             queue,
		deletedIndexer:    deletedIndexer,
		volumeMissing:     volumeMissing,
		excludeAnnotation: excludeAnnotation,
		backupAnnotation:  backupAnnotation,
		podCache:          map[OwnerName]map[VolumeName]bool{},
	}
}

func (c *Controller) processNextItem() bool {
	// Wait until there is a new item in the working queue
	key, quit := c.queue.Get()
	if quit {
		return false
	}
	// Tell the queue that we are done with processing this key. This unblocks the key for other workers
	// This allows safe parallel processing because two pods with the same key are never processed in
	// parallel.
	defer c.queue.Done(key)

	// Invoke the method containing the business logic
	err := c.validateBackupAnnotations(key.(string), c.volumeMissing, c.excludeAnnotation, c.backupAnnotation)
	// Handle the error if something went wrong during the execution of the business logic
	c.handleErr(err, key)
	return true
}

// validateBackupAnnotations is the business logic of the controller. It
// validates that all volumes have a backup annotation assinged.
func (c *Controller) validateBackupAnnotations(key string, volumeMissing prometheus.GaugeVec, excludeAnnotation string, backupAnnotation string) error {

	obj, exists, err := c.indexer.GetByKey(key)
	if err != nil {
		klog.Errorf("fetching object with key %s from store failed with %v", key, err)
		return err
	}
	if !exists {
		klog.Infof("pod %s does not exist anymore", key)
		if obj, exists, err = c.deletedIndexer.GetByKey(key); err == nil && exists {

			pod := obj.(*v1.Pod)
			ownerInfo := getPodOwnerInfo(pod)
			klog.Infof("disabling metric %s", ownerInfo.name)
			c.disableMetric(ownerInfo)
			_ = c.deletedIndexer.Delete(key)
		}

	} else {
		pod := obj.(*v1.Pod)
		ownerInfo := getPodOwnerInfo(pod)

		if _, ok := c.podCache[ownerInfo.name]; !ok {
			c.podCache[ownerInfo.name] = map[VolumeName]bool{}
		}
		klog.Infof("controlling backup config for %s", pod.GetName())
		missings := c.getMissingBackups(pod)
		if len(missings) > 0 {
			klog.Infof("backup missing enable metric for %s", ownerInfo.name)
			c.enableMetric(ownerInfo, missings)
		}
	}

	return nil
}

// handleErr checks if an error happened and makes sure we will retry later.
func (c *Controller) handleErr(err error, key interface{}) {
	if err == nil {
		// Forget about the #AddRateLimited history of the key on every successful synchronization.
		// This ensures that future processing of updates for this key is not delayed because of
		// an outdated error history.
		c.queue.Forget(key)
		return
	}

	// This controller retries 5 times if something goes wrong. After that, it stops trying.
	if c.queue.NumRequeues(key) < 5 {
		klog.Infof("error syncing pod %v: %v", key, err)

		// Re-enqueue the key rate limited. Based on the rate limiter on the
		// queue and the re-enqueue history, the key will be processed later again.
		c.queue.AddRateLimited(key)
		return
	}

	c.queue.Forget(key)
	// Report to an external entity that, even after several retries, we could not successfully process this key
	runtime.HandleError(err)
	klog.Infof("dropping pod %q out of the queue: %v", key, err)
}

// Run default
func (c *Controller) Run(threadiness int, stopCh chan struct{}) {
	defer runtime.HandleCrash()

	// Let the workers stop when we are done
	defer c.queue.ShutDown()
	klog.Info("starting Pod controller")

	go c.informer.Run(stopCh)

	// Wait for all involved caches to be synced, before processing items from the queue is started
	if !cache.WaitForCacheSync(stopCh, c.informer.HasSynced) {
		runtime.HandleError(fmt.Errorf("timed out waiting for caches to sync"))
		return
	}

	for i := 0; i < threadiness; i++ {
		go wait.Until(c.runWorker, time.Second, stopCh)
	}

	<-stopCh
	klog.Info("stopping Pod controller")
}

func (c *Controller) runWorker() {
	for c.processNextItem() {
	}
}

func (c *Controller) enableMetric(o *PodOwnerInfo, volumeNames []VolumeName) {
	for _, volumeName := range volumeNames {
		c.podCache[o.name][volumeName] = true
		c.volumeMissing.With(prometheus.Labels{
			"namespace":   o.namespace,
			"owner_kind":  o.ownerKind,
			"owner_name":  o.ownerName,
			"volume_name": string(volumeName),
		}).Set(1)
	}

}

func (c *Controller) disableMetric(o *PodOwnerInfo) {
	for volumeName, _ := range c.podCache[o.name] {
		c.volumeMissing.Delete(prometheus.Labels{
			"namespace":   o.namespace,
			"owner_kind":  o.ownerKind,
			"owner_name":  o.ownerName,
			"volume_name": string(volumeName),
		})
	}
}

// getMissingBackups returns a list of all volume names that are missing a
// backup include or exclude configuration
func (c *Controller) getMissingBackups(pod *v1.Pod) []VolumeName {
	// get pod annotations
	annotations := pod.GetAnnotations()

	// gather excludes
	backupVolumesExcludesString := annotations[c.excludeAnnotation]
	excludes := map[string]interface{}{}
	for _, exclude := range strings.Split(backupVolumesExcludesString, ",") {
		excludes[exclude] = nil
	}

	// gather includes
	backupVolumesString := annotations[c.backupAnnotation]
	includes := map[string]interface{}{}
	for _, include := range strings.Split(backupVolumesString, ",") {
		includes[include] = nil
	}

	// range over all volumes
	missing := []VolumeName{}
	for _, volume := range pod.Spec.Volumes {
		if volume.PersistentVolumeClaim == nil {
			continue
		}

		klog.Infof("pod '%s' uses volume '%s' from pvc '%s'", pod.Name, volume.Name, volume.PersistentVolumeClaim.ClaimName)
		if _, ok := includes[volume.Name]; ok {
			klog.Infof("backup configured for '%s'", pod.Name)
			continue
		}
		if _, ok := excludes[volume.Name]; ok {
			klog.Infof("backup exclusion configured for '%s'", volume.Name)
			continue
		}

		klog.Infof("%s used but not backed up or excluded", volume.Name)
		missing = append(missing, VolumeName(volume.Name))

	}

	return missing
}

type PodOwnerInfo struct {
	name      OwnerName
	ownerKind string
	ownerName string
	namespace string
}

func getPodOwnerInfo(pod *v1.Pod) *PodOwnerInfo {
	o := &PodOwnerInfo{
		ownerKind: "none",
		ownerName: "none",
		namespace: pod.Namespace,
	}
	if len(pod.OwnerReferences) >= 1 {
		o.ownerKind = pod.OwnerReferences[0].Kind
		o.ownerName = pod.OwnerReferences[0].Name
	}
	o.name = OwnerName(fmt.Sprintf("%s/%s/%s", pod.Namespace, o.ownerKind, o.ownerName))
	return o
}
