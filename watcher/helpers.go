package watcher

import (
	"strings"

	"k8s.io/api/core/v1"
)

const (
	BackupAnnotation  = "backup.velero.io/backup-volumes"
	ExcludeAnnotation = "backup.velero.io/backup-volumes-excludes"
	ExcludePVCAnnotation = "backup.velero.io/backup-excluded"
)

func listPodHandledPVCs(pod *v1.Pod, handledPvcNames *map[string]interface{}) {
	// fetch all annotations
	handledVolumeNames := map[string]struct{}{}
	if backuped, ok := pod.ObjectMeta.Annotations[BackupAnnotation]; ok {
		volumes := strings.Split(backuped, ",")
		for _, volume := range volumes {
			handledVolumeNames[volume] = struct{}{}
		}
	}
	if excluded, ok := pod.ObjectMeta.Annotations[ExcludeAnnotation]; ok {
		volumes := strings.Split(excluded, ",")
		for _, volume := range volumes {
			handledVolumeNames[volume] = struct{}{}
		}
	}

	// provide map for looking up volumeName -> pvcName
	volumeIndex := map[string]string{}
	for _, volume := range pod.Spec.Volumes {
		if volume.VolumeSource.PersistentVolumeClaim == nil {
			continue
		}
		volumeIndex[volume.Name] = volume.VolumeSource.PersistentVolumeClaim.ClaimName
	}

	// resolve all handled pvc-names
	for volumeName, pxcName := range volumeIndex {
		if _, ok := handledVolumeNames[volumeName]; ok {
			(*handledPvcNames)[pxcName] = nil
		}
	}
}
