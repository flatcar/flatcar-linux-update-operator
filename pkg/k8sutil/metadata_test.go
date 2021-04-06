package k8sutil_test

import (
	"context"
	"fmt"
	"strconv"
	"testing"

	"github.com/golang/mock/gomock"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/kinvolk/flatcar-linux-update-operator/pkg/k8sutil"
	mock_v1 "github.com/kinvolk/flatcar-linux-update-operator/pkg/k8sutil/mocks"
)

func atomicCounterIncrement(t *testing.T) func(n *corev1.Node) {
	t.Helper()

	return func(n *corev1.Node) {
		counterAnno := "counter"
		s := n.Annotations[counterAnno]

		var i int

		if s == "" {
			i = 0
		} else {
			var err error
			i, err = strconv.Atoi(s)
			if err != nil {
				t.Fatalf("parsing %q to integer: %v", s, err)
			}
		}

		n.Annotations[counterAnno] = strconv.Itoa(i + 1)
	}
}

func TestUpdateNodeRetryHandlesConflict(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockNi := mock_v1.NewMockNodeInterface(ctrl)
	mockNode := &corev1.Node{}
	mockNode.SetName("mock_node")
	mockNode.SetNamespace("default")
	mockNode.SetAnnotations(map[string]string{"counter": "20"})
	mockNode.SetResourceVersion("20")

	mockNi.EXPECT().Get(context.TODO(), "mock_node", metav1.GetOptions{}).Return(mockNode, nil).AnyTimes()

	// Conflict once; mock that a third party incremented the counter from '20'
	// to '21' right after the node is returned
	gomock.InOrder(
		mockNi.EXPECT().Update(context.TODO(), mockNode, metav1.UpdateOptions{}).Do(func(
			ctx context.Context, n *corev1.Node, uo metav1.UpdateOptions) {
			// Fake conflict; the counter was incremented elsewhere; resourceVersion is now 21
			mockNode.SetAnnotations(map[string]string{"counter": "21"})
			mockNode.SetResourceVersion("21")
		},
		).Return(mockNode, errors.NewConflict(schema.GroupResource{}, "mock_node", fmt.Errorf("err"))),

		// And then the successful retry
		mockNi.EXPECT().Update(context.TODO(), mockNode, metav1.UpdateOptions{}).Return(mockNode, nil),
	)

	err := k8sutil.UpdateNodeRetry(mockNi, "mock_node", atomicCounterIncrement(t))
	if err != nil {
		t.Errorf("unexpected error: expected increment to succeed")
	}

	if mockNode.Annotations["counter"] != "22" {
		t.Errorf("expected the counter to hit 22; was %v", mockNode.Annotations["counter"])
	}
}
