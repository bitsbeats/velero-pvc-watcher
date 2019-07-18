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
type Controller struct {
	indexer           cache.Indexer
	queue             workqueue.RateLimitingInterface
	informer          cache.Controller
	deletedIndexer    cache.Indexer
	volumeMissing     prometheus.GaugeVec
	excludeAnnotation string
	backupAnnotation  string
	podCache          map[string]map[string]bool
}

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
		podCache:          map[string]map[string]bool{},
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
	err := c.syncToStdout(key.(string), c.volumeMissing, c.excludeAnnotation, c.backupAnnotation)
	// Handle the error if something went wrong during the execution of the business logic
	c.handleErr(err, key)
	return true
}

// syncToStdout is the business logic of the controller. In this controller it simply prints
// information about the pod to stdout. In case an error happened, it has to simply return the error.
// The retry logic should not be part of the business logic.
func (c *Controller) syncToStdout(key string, volumeMissing prometheus.GaugeVec, excludeAnnotation string, backupAnnotation string) error {

	obj, exists, err := c.indexer.GetByKey(key)
	if err != nil {
		klog.Errorf("fetching object with key %s from store failed with %v", key, err)
		return err
	}
	if !exists {
		// Below we will warm up our cache with a Pod, so that we will see a delete for one pod
		klog.Infof("pod %s does not exist anymore", key)

		// Handling cleanup steps
		if obj, exists, err = c.deletedIndexer.GetByKey(key); err == nil && exists {

			pod := obj.(*v1.Pod)
			podOwnerKind := "none"
			podOwnerName := "none"
			if len(pod.OwnerReferences) >= 1 {
				podOwnerKind = pod.OwnerReferences[0].Kind
				podOwnerName = pod.OwnerReferences[0].Name
			}
			ownerName := fmt.Sprintf("%s/%s/%s", pod.Namespace, podOwnerKind, podOwnerName)
			delete(c.podCache[ownerName], pod.Name)
			if len(c.podCache[ownerName]) == 0 {
				klog.Infof("disabling metric %s/%s/%s", pod.Namespace, podOwnerKind, podOwnerName)
				disableMetric(volumeMissing, pod.Namespace, podOwnerKind, podOwnerName)
			}
			_ = c.deletedIndexer.Delete(key)
		}

	} else {
		// Note that you also have to check the uid if you have a local controlled resource, which
		// is dependent on the actual instance, to detect that a Pod was recreated with the same name

		pod := obj.(*v1.Pod)
		podOwnerKind := "none"
		podOwnerName := "none"
		if len(pod.OwnerReferences) >= 1 {
			podOwnerKind = pod.OwnerReferences[0].Kind
			podOwnerName = pod.OwnerReferences[0].Name
		}
		ownerName := fmt.Sprintf("%s/%s/%s", pod.Namespace, podOwnerKind, podOwnerName)
		if _, ok := c.podCache[ownerName]; !ok {
			c.podCache[ownerName] = map[string]bool{}
		}

		c.podCache[ownerName][pod.Name] = true
		klog.Infof("controlling backup config for %s", pod.GetName())
		if checkBackup(pod, backupAnnotation, excludeAnnotation) {
			klog.Infof("backup missing enable metric for %s/%s/%s", pod.Namespace, podOwnerKind, podOwnerName)
			enableMetric(volumeMissing, pod.Namespace, podOwnerKind, podOwnerName)
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

func enableMetric(volumeMissing prometheus.GaugeVec, namespace string, ownerKind string, ownerName string) {

	volumeMissing.With(prometheus.Labels{
		"namespace":  namespace,
		"owner_kind": ownerKind,
		"owner_name": ownerName,
	}).Set(1)

}

func disableMetric(volumeMissing prometheus.GaugeVec, namespace string, ownerKind string, ownerName string) {

	volumeMissing.Delete(prometheus.Labels{
		"namespace":  namespace,
		"owner_kind": ownerKind,
		"owner_name": ownerName,
	})
}

func checkBackup(pod *v1.Pod, excludeAnnotation string, backupAnnotation string) bool {

	// get pod annotations
	annotations := pod.GetAnnotations()

	// range over all volumes
VolumeLoop:
	for _, volume := range pod.Spec.Volumes {

		// check if volume uses persistentVolumeClaim
		if volume.PersistentVolumeClaim != nil {

			klog.Infof("pod '%s' uses volume '%s' from pvc '%s'", pod.Name, volume.Name, volume.PersistentVolumeClaim.ClaimName)

			// get backup-volumes annotation if present
			if _, ok := annotations[backupAnnotation]; ok {

				// first, clean/remove the comma
				backupString := strings.Replace(annotations[backupAnnotation], ",", " ", -1)

				// convert 'clened' comma separated string to slice
				backupVolumes := strings.Fields(backupString)

				// check if backup is configured
				if contains(backupVolumes, volume.Name) {

					klog.Infof("backup configured for '%s'", pod.Name)
					continue VolumeLoop

				}

			}

			// get backup-volumes-excludes annotation if present
			if _, ok := annotations[excludeAnnotation]; ok {

				// first, clean/remove the comma
				backupExcludesString := strings.Replace(annotations[excludeAnnotation], ",", " ", -1)

				// convert 'clened' comma separated string to slice
				backupVolumesExcludes := strings.Fields(backupExcludesString)

				// check if backup is configured
				if contains(backupVolumesExcludes, volume.Name) {

					klog.Infof("backup exclusion configured for '%s'", volume.Name)
					continue VolumeLoop

				}

			}

			klog.Infof("%s used but not backed up or excluded", volume.Name)
			return true

		}

	}
	return false
}

func contains(s []string, e string) bool {
	for _, a := range s {
		if a == e {
			return true
		}
	}
	return false
}
