package k8sutil

import (
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
)

// FilterNodesByAnnotation takes a node list and a field selector, and returns
// a node list that matches the field selector.
func FilterNodesByAnnotation(list []corev1.Node, sel fields.Selector) []corev1.Node {
	var ret []corev1.Node

	for _, n := range list {
		if sel.Matches(fields.Set(n.Annotations)) {
			ret = append(ret, n)
		}
	}

	return ret
}

// FilterNodesByRequirement filters a list of nodes and returns nodes matching the
// given label requirement.
func FilterNodesByRequirement(nodes []corev1.Node, req *labels.Requirement) []corev1.Node {
	var matches []corev1.Node

	for _, node := range nodes {
		if req.Matches(labels.Set(node.Labels)) {
			matches = append(matches, node)
		}
	}

	return matches
}

// FilterContainerLinuxNodes filters a list of nodes and returns nodes with a
// Flatcar Container Linux OSImage, as reported by the node's /etc/os-release.
func FilterContainerLinuxNodes(nodes []corev1.Node) []corev1.Node {
	var matches []corev1.Node

	for _, node := range nodes {
		if strings.HasPrefix(node.Status.NodeInfo.OSImage, "Flatcar Container Linux") {
			matches = append(matches, node)
		}
	}

	return matches
}
