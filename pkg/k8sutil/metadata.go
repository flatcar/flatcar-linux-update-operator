package k8sutil

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
)

// NodeGetter is a subset of corev1client.NodeInterface used by this package for getting node objects.
type NodeGetter interface {
	Get(ctx context.Context, name string, opts metav1.GetOptions) (*corev1.Node, error)
}

// GetNodeRetry gets a node object, retrying up to DefaultBackoff number of times if it fails.
func GetNodeRetry(ctx context.Context, nc NodeGetter, node string) (*corev1.Node, error) {
	var apiNode *corev1.Node

	err := retry.OnError(retry.DefaultBackoff, func(error) bool { return true }, func() error {
		n, getErr := nc.Get(ctx, node, metav1.GetOptions{})
		if getErr != nil {
			return fmt.Errorf("getting node %q: %w", node, getErr)
		}

		apiNode = n

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("getting node: %w", err)
	}

	return apiNode, nil
}

// UpdateNode is a function updating properties of received node object.
type UpdateNode func(*corev1.Node)

// NodeUpdater is a subset of corev1client.NodeInterface used by this package for updating nodes.
type NodeUpdater interface {
	NodeGetter

	Update(ctx context.Context, node *corev1.Node, opts metav1.UpdateOptions) (*corev1.Node, error)
}

// UpdateNodeRetry calls f to update a node object in Kubernetes.
// It will attempt to update the node by applying f to it up to DefaultBackoff
// number of times.
// Given update function will be called each time since the node object will likely have changed if
// a retry is necessary.
func UpdateNodeRetry(ctx context.Context, nodeUpdater NodeUpdater, nodeName string, updateF UpdateNode) error {
	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		node, getErr := nodeUpdater.Get(ctx, nodeName, metav1.GetOptions{})
		if getErr != nil {
			return fmt.Errorf("getting node %q: %w", nodeName, getErr)
		}

		updateF(node)

		_, err := nodeUpdater.Update(ctx, node, metav1.UpdateOptions{})

		return err
	})
	if err != nil {
		// May be conflict if max retries were hit.
		return fmt.Errorf("updating node %q: %w", nodeName, err)
	}

	return nil
}

// SetNodeLabels sets all keys in m to their respective values in
// node's labels.
func SetNodeLabels(ctx context.Context, nc NodeUpdater, node string, m map[string]string) error {
	return UpdateNodeRetry(ctx, nc, node, func(n *corev1.Node) {
		for k, v := range m {
			n.Labels[k] = v
		}
	})
}

// SetNodeAnnotations sets all keys in m to their respective values in
// node's annotations.
func SetNodeAnnotations(ctx context.Context, nc NodeUpdater, node string, m map[string]string) error {
	return UpdateNodeRetry(ctx, nc, node, func(n *corev1.Node) {
		for k, v := range m {
			n.Annotations[k] = v
		}
	})
}

// SetNodeAnnotationsLabels sets all keys in a and l to their values in
// node's annotations and labels, respectively.
func SetNodeAnnotationsLabels(
	ctx context.Context, nc NodeUpdater, nodeName string, annotations, labels map[string]string,
) error {
	return UpdateNodeRetry(ctx, nc, nodeName, func(node *corev1.Node) {
		for k, v := range annotations {
			node.Annotations[k] = v
		}

		for k, v := range labels {
			node.Labels[k] = v
		}
	})
}

// Unschedulable marks node as schedulable or unschedulable according to sched.
func Unschedulable(ctx context.Context, nc NodeUpdater, node string, sched bool) error {
	return UpdateNodeRetry(ctx, nc, node, func(n *corev1.Node) {
		n.Spec.Unschedulable = sched
	})
}
