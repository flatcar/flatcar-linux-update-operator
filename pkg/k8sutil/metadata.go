package k8sutil

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/watch"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	watchtools "k8s.io/client-go/tools/watch"
	"k8s.io/client-go/util/retry"
)

// NodeAnnotationCondition returns a condition function that succeeds when a
// node being watched has an annotation of key equal to value.
func NodeAnnotationCondition(selector fields.Selector) watchtools.ConditionFunc {
	return func(event watch.Event) (bool, error) {
		if event.Type == watch.Modified {
			node, ok := event.Object.(*corev1.Node)
			if !ok {
				return false, fmt.Errorf("received event object is not Node, got: %#v", event.Object)
			}

			return selector.Matches(fields.Set(node.Annotations)), nil
		}

		return false, fmt.Errorf("unhandled watch case for %#v", event)
	}
}

// GetNodeRetry gets a node object, retrying up to DefaultBackoff number of times if it fails.
func GetNodeRetry(ctx context.Context, nc corev1client.NodeInterface, node string) (*corev1.Node, error) {
	var apiNode *corev1.Node

	err := retry.OnError(retry.DefaultBackoff, func(error) bool { return true }, func() error {
		n, getErr := nc.Get(ctx, node, metav1.GetOptions{})
		if getErr != nil {
			return fmt.Errorf("failed to get node %q: %w", node, getErr)
		}

		apiNode = n

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("getting node: %w", err)
	}

	return apiNode, nil
}

// UpdateNodeRetry calls f to update a node object in Kubernetes.
// It will attempt to update the node by applying f to it up to DefaultBackoff
// number of times.
// f will be called each time since the node object will likely have changed if
// a retry is necessary.
func UpdateNodeRetry(ctx context.Context, nc corev1client.NodeInterface, node string, f func(*corev1.Node)) error {
	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		n, getErr := nc.Get(ctx, node, metav1.GetOptions{})
		if getErr != nil {
			return fmt.Errorf("failed to get node %q: %w", node, getErr)
		}

		f(n)

		_, err := nc.Update(ctx, n, metav1.UpdateOptions{})

		return err
	})
	if err != nil {
		// May be conflict if max retries were hit.
		return fmt.Errorf("unable to update node %q: %w", node, err)
	}

	return nil
}

// SetNodeLabels sets all keys in m to their respective values in
// node's labels.
func SetNodeLabels(ctx context.Context, nc corev1client.NodeInterface, node string, m map[string]string) error {
	return UpdateNodeRetry(ctx, nc, node, func(n *corev1.Node) {
		for k, v := range m {
			n.Labels[k] = v
		}
	})
}

// SetNodeAnnotations sets all keys in m to their respective values in
// node's annotations.
func SetNodeAnnotations(ctx context.Context, nc corev1client.NodeInterface, node string, m map[string]string) error {
	return UpdateNodeRetry(ctx, nc, node, func(n *corev1.Node) {
		for k, v := range m {
			n.Annotations[k] = v
		}
	})
}

// SetNodeAnnotationsLabels sets all keys in a and l to their values in
// node's annotations and labels, respectively.
func SetNodeAnnotationsLabels(ctx context.Context, nc corev1client.NodeInterface, node string, a, l map[string]string) error { //nolint:lll
	return UpdateNodeRetry(ctx, nc, node, func(n *corev1.Node) {
		for k, v := range a {
			n.Annotations[k] = v
		}

		for k, v := range l {
			n.Labels[k] = v
		}
	})
}

// Unschedulable marks node as schedulable or unschedulable according to sched.
func Unschedulable(ctx context.Context, nc corev1client.NodeInterface, node string, sched bool) error {
	return UpdateNodeRetry(ctx, nc, node, func(n *corev1.Node) {
		n.Spec.Unschedulable = sched
	})
}
