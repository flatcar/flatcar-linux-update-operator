package k8sutil

import (
	corev1 "k8s.io/api/core/v1"
)

// FilterPods filters given list of pods using given function.
func FilterPods(pods []corev1.Pod, filter func(*corev1.Pod) bool) []corev1.Pod {
	result := []corev1.Pod{}

	for _, p := range pods {
		p := p

		if filter(&p) {
			result = append(result, p)
		}
	}

	return result
}
