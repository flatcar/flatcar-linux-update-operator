package k8sutil_test

import (
	"context"
	"fmt"
	"strconv"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/flatcar-linux/flatcar-linux-update-operator/pkg/k8sutil"
)

//nolint:funlen // Just subtests.
func Test_Updating_node(t *testing.T) {
	t.Parallel()

	t.Run("retries_on_conflict_error", func(t *testing.T) {
		t.Parallel()

		annotationKey := "counter"

		node := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "testNodeName",
				Annotations: map[string]string{annotationKey: "20"},
			},
		}

		fakeClient := fake.NewSimpleClientset(node)

		fakeClient.PrependReactor("get", "nodes", func(action k8stesting.Action) (bool, runtime.Object, error) {
			return true, node, nil
		})

		sentConflict := false

		fakeClient.PrependReactor("update", "nodes", func(action k8stesting.Action) (bool, runtime.Object, error) {
			if sentConflict {
				return false, nil, nil
			}

			sentConflict = true

			node.Annotations[annotationKey] = "21"

			return true, node, errors.NewConflict(schema.GroupResource{}, node.Name, fmt.Errorf("test error"))
		})

		ctx := context.TODO()
		nc := fakeClient.CoreV1().Nodes()

		if err := k8sutil.UpdateNodeRetry(ctx, nc, node.Name, atomicCounterIncrement(t, annotationKey)); err != nil {
			t.Fatalf("Unexpected error updating node: %v", err)
		}

		expectedCounterValue := "22"

		if v := node.Annotations[annotationKey]; v != expectedCounterValue {
			t.Fatalf("Expected the counter to hit %q, got %q", expectedCounterValue, v)
		}
	})

	t.Run("returns_error_when", func(t *testing.T) {
		t.Parallel()

		t.Run("updated_node_does_not_exist", func(t *testing.T) {
			t.Parallel()

			fakeClient := fake.NewSimpleClientset()

			ctx := context.TODO()
			nc := fakeClient.CoreV1().Nodes()

			if err := k8sutil.UpdateNodeRetry(ctx, nc, "nonExistingNode", func(*corev1.Node) {}); err == nil {
				t.Fatalf("Expected error updating non existing node")
			}
		})

		t.Run("updating_node_returns_error_other_than_conflict", func(t *testing.T) {
			t.Parallel()

			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "testNodeName",
				},
			}

			fakeClient := fake.NewSimpleClientset(node)

			fakeClient.PrependReactor("update", "nodes", func(action k8stesting.Action) (bool, runtime.Object, error) {
				return true, nil, fmt.Errorf("test error")
			})

			ctx := context.TODO()
			nc := fakeClient.CoreV1().Nodes()

			if err := k8sutil.UpdateNodeRetry(ctx, nc, node.Name, func(*corev1.Node) {}); err == nil {
				t.Fatalf("Expected error updating node")
			}
		})
	})
}

func atomicCounterIncrement(t *testing.T, annotationKey string) func(n *corev1.Node) {
	t.Helper()

	return func(node *corev1.Node) {
		s := node.Annotations[annotationKey]

		i, err := strconv.Atoi(s)
		if err != nil {
			t.Fatalf("Parsing %q to integer: %v", s, err)
		}

		node.Annotations[annotationKey] = strconv.Itoa(i + 1)
	}
}
