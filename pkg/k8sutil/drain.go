package k8sutil

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	appsv1client "k8s.io/client-go/kubernetes/typed/apps/v1"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
)

// GetPodsForDeletion finds pods on the given node that are candidates for
// deletion during a drain before a reboot.
// This code mimics pod filtering behavior in
// https://github.com/kubernetes/kubernetes/blob/v1.5.4/pkg/kubectl/cmd/drain.go#L234-L245
// See DrainOptions.getPodsForDeletion and callees.
func GetPodsForDeletion(ctx context.Context, pg corev1client.PodsGetter, dsg appsv1client.DaemonSetsGetter, node string) (pods []corev1.Pod, err error) { //nolint:lll
	podList, err := pg.Pods(corev1.NamespaceAll).List(ctx, metav1.ListOptions{
		FieldSelector: fields.SelectorFromSet(fields.Set{"spec.nodeName": node}).String(),
	})
	if err != nil {
		return nil, fmt.Errorf("listing pods on node %q: %w", node, err)
	}

	// Delete pods, even if they are lone pods without a controller. As an
	// exception, skip mirror pods and daemonset pods with an existing
	// daemonset (since the daemonset owner would recreate them anyway).
	for _, pod := range podList.Items {
		// skip mirror pods
		if _, ok := pod.Annotations[corev1.MirrorPodAnnotationKey]; ok {
			continue
		}

		// check if pod is a daemonset owner
		if _, err = getOwnerDaemonset(ctx, dsg, pod); err == nil {
			continue
		}

		pods = append(pods, pod)
	}

	return pods, nil
}

// getOwnerDaemonset returns an existing DaemonSet owner if it exists.
func getOwnerDaemonset(ctx context.Context, dsg appsv1client.DaemonSetsGetter, pod corev1.Pod) (interface{}, error) {
	if len(pod.OwnerReferences) == 0 {
		return nil, fmt.Errorf("pod %q has no owner objects", pod.Name)
	}

	for _, ownerRef := range pod.OwnerReferences {
		ownerRef := ownerRef

		// skip pod if it is owned by an existing daemonset
		if ownerRef.Kind == "DaemonSet" {
			ds, err := getDaemonsetController(ctx, dsg, pod.Namespace, ownerRef)
			if err == nil {
				// daemonset owner exists
				return ds, nil
			}

			if !errors.IsNotFound(err) {
				return nil, fmt.Errorf("failed to get controller of pod %q: %w", pod.Name, err)
			}
		}
	}
	// pod may have owners, but they don't exist or aren't daemonsets
	return nil, fmt.Errorf("pod %q has no existing damonset owner", pod.Name)
}

// Stripped down version of https://github.com/kubernetes/kubernetes/blob/1bc56825a2dff06f29663a024ee339c25e6e6280/pkg/kubectl/cmd/drain.go#L272
//
//nolint:lll
func getDaemonsetController(ctx context.Context, dsg appsv1client.DaemonSetsGetter, namespace string, controllerRef metav1.OwnerReference) (interface{}, error) {
	if controllerRef.Kind == "DaemonSet" {
		return dsg.DaemonSets(namespace).Get(ctx, controllerRef.Name, metav1.GetOptions{})
	}

	return nil, fmt.Errorf("unknown controller kind %q", controllerRef.Kind)
}
