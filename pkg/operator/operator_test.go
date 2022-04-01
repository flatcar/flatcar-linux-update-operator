package operator_test

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/klog/v2"

	"github.com/flatcar-linux/flatcar-linux-update-operator/pkg/constants"
	"github.com/flatcar-linux/flatcar-linux-update-operator/pkg/operator"
)

const (
	testBeforeRebootAnnotation        = "test-before-annotation"
	testAnotherBeforeRebootAnnotation = "test-another-after-annotation"
	testAfterRebootAnnotation         = "test-after-annotation"
	testAnotherAfterRebootAnnotation  = "test-another-after-annotation"
	testNamespace                     = "default"
)

//nolint:funlen
func Test_Creating_new_operator(t *testing.T) {
	t.Parallel()

	t.Run("succeeds_with", func(t *testing.T) {
		t.Parallel()

		t.Run("only_required_fields_are_set", func(t *testing.T) {
			t.Parallel()

			if _, err := operator.New(validOperatorConfig()); err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}
		})

		t.Run("valid_reboot_window_configured", func(t *testing.T) {
			t.Parallel()

			config := validOperatorConfig()
			config.RebootWindowStart = "Mon 14:00"
			config.RebootWindowLength = "0s"

			if _, err := operator.New(config); err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}
		})
	})

	t.Run("fails_when", func(t *testing.T) {
		t.Parallel()

		t.Run("Kubernetes_client_is_not_set", func(t *testing.T) {
			t.Parallel()

			config := validOperatorConfig()
			config.Client = nil

			if _, err := operator.New(config); err == nil {
				t.Fatalf("Expected error")
			}
		})

		t.Run("namespace_is_not_set", func(t *testing.T) {
			t.Parallel()

			config := validOperatorConfig()
			config.Namespace = ""

			if _, err := operator.New(config); err == nil {
				t.Fatalf("Expected error")
			}
		})

		t.Run("lockID_is_not_set", func(t *testing.T) {
			config := validOperatorConfig()
			config.LockID = ""

			if _, err := operator.New(config); err == nil {
				t.Fatalf("Expected error")
			}
		})

		t.Run("invalid_reboot_window_is_configured", func(t *testing.T) {
			t.Parallel()

			config := validOperatorConfig()
			config.RebootWindowStart = "Mon 14"
			config.RebootWindowLength = "0s"

			if _, err := operator.New(config); err == nil {
				t.Fatalf("Expected error")
			}
		})
	})
}

func Test_Operator_exits_gracefully_when_user_requests_shutdown(t *testing.T) {
	t.Parallel()

	rebootCancelledNode := rebootCancelledNode()

	config, fakeClient := testConfig(rebootCancelledNode)

	ctx := contextWithDeadline(t)

	<-process(ctx, t, config, fakeClient)

	updatedNode := node(contextWithDeadline(t), t, config.Client.CoreV1().Nodes(), rebootCancelledNode.Name)

	if _, ok := updatedNode.Labels[constants.LabelBeforeReboot]; ok {
		t.Fatalf("Expected label %q to be removed from Node", constants.LabelBeforeReboot)
	}
}

//nolint:funlen // Should likely be refactored.
func Test_Operator_shuts_down_leader_election_process_when_user_requests_shutdown(t *testing.T) {
	t.Parallel()

	rebootCancelledNode := rebootCancelledNode()

	config, fakeClient := testConfig(rebootCancelledNode)
	config.BeforeRebootAnnotations = []string{testBeforeRebootAnnotation}
	config.ReconciliationPeriod = 1 * time.Second
	config.LeaderElectionLease = 2 * time.Second
	testKontroller := kontrollerWithObjects(t, config)
	nodeUpdated := nodeUpdatedNTimes(fakeClient, 1)

	stop := make(chan struct{})
	stopped := make(chan struct{})

	go func() {
		if err := testKontroller.Run(stop); err != nil {
			fmt.Printf("Error running operator: %v\n", err)
			t.Fail()
		}
		stopped <- struct{}{}
	}()

	// Wait for one reconciliation cycle to run.
	<-nodeUpdated

	ctx := contextWithDeadline(t)
	updatedNode := node(ctx, t, config.Client.CoreV1().Nodes(), rebootCancelledNode.Name)

	if _, ok := updatedNode.Labels[constants.LabelBeforeReboot]; ok {
		t.Fatalf("Expected label %q to be removed from Node after waiting the reconciliation period",
			constants.LabelBeforeReboot)
	}

	close(stop)

	<-stopped

	updatedNode.Labels[constants.LabelBeforeReboot] = constants.True
	updatedNode.Annotations[testBeforeRebootAnnotation] = constants.True

	if _, err := config.Client.CoreV1().Nodes().Update(ctx, updatedNode, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("Updating Node object: %v", err)
	}

	config.LockID = "bar"

	parallelKontroller := kontrollerWithObjects(t, config)

	stop = make(chan struct{})

	t.Cleanup(func() {
		close(stop)
	})

	go func() {
		if err := parallelKontroller.Run(stop); err != nil {
			fmt.Printf("Error running operator: %v\n", err)
			t.Fail()
		}
	}()

	time.Sleep(config.LeaderElectionLease * 2)

	updatedNode = node(ctx, t, config.Client.CoreV1().Nodes(), rebootCancelledNode.Name)

	if _, ok := updatedNode.Labels[constants.LabelBeforeReboot]; ok {
		t.Fatalf("Expected label %q to be removed from Node after waiting the reconciliation period",
			constants.LabelBeforeReboot)
	}
}

func Test_Operator_emits_events_about_leader_election_to_configured_namespace(t *testing.T) {
	t.Parallel()

	config, fakeClient := testConfig()

	<-process(contextWithDeadline(t), t, config, fakeClient)

	events, err := config.Client.CoreV1().Events(config.Namespace).List(contextWithDeadline(t), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("Failed listing events: %v", err)
	}

	if len(events.Items) == 0 {
		t.Fatalf("Expected at least one event to be published")
	}
}

func Test_Operator_returns_error_when_leadership_is_lost(t *testing.T) {
	t.Parallel()

	rebootCancelledNode := rebootCancelledNode()

	config, fakeClient := testConfig(rebootCancelledNode)
	config.BeforeRebootAnnotations = []string{testBeforeRebootAnnotation}
	config.ReconciliationPeriod = 1 * time.Second
	config.LeaderElectionLease = 2 * time.Second
	testKontroller := kontrollerWithObjects(t, config)
	nodeUpdated := nodeUpdatedNTimes(fakeClient, 1)

	stop := make(chan struct{})

	t.Cleanup(func() {
		close(stop)
	})

	errCh := make(chan error, 1)

	go func() {
		errCh <- testKontroller.Run(stop)
	}()

	// Wait for one reconciliation cycle to run.
	<-nodeUpdated

	ctx := contextWithDeadline(t)

	// Ensure operator is functional.
	updatedNode := node(ctx, t, config.Client.CoreV1().Nodes(), rebootCancelledNode.Name)

	if _, ok := updatedNode.Labels[constants.LabelBeforeReboot]; ok {
		t.Fatalf("Expected label %q to be removed from Node after waiting the reconciliation period",
			constants.LabelBeforeReboot)
	}

	stealLeaderElection(ctx, t, config)

	// Wait lease time to ensure operator lost it.
	time.Sleep(config.LeaderElectionLease)

	// Patch node object again to verify if operator is functional.
	updatedNode.Labels[constants.LabelBeforeReboot] = constants.True
	updatedNode.Annotations[testBeforeRebootAnnotation] = constants.True

	if _, err := config.Client.CoreV1().Nodes().Update(ctx, updatedNode, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("Updating Node object: %v", err)
	}

	time.Sleep(config.ReconciliationPeriod)

	updatedNode = node(ctx, t, config.Client.CoreV1().Nodes(), rebootCancelledNode.Name)

	if _, ok := updatedNode.Labels[constants.LabelBeforeReboot]; !ok {
		t.Fatalf("Expected label %q to remain on Node", constants.LabelBeforeReboot)
	}

	if err := <-errCh; err == nil {
		t.Fatalf("Expected operator to return error when leader election is lost")
	}
}

func stealLeaderElection(ctx context.Context, t *testing.T, config operator.Config) {
	t.Helper()

	configMapClient := config.Client.CoreV1().ConfigMaps(config.Namespace)

	configMaps, err := configMapClient.List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatalf("Failed listing ConfigMaps: %v", err)
	}

	if c := len(configMaps.Items); c != 1 {
		t.Fatalf("Expected exactly one ConfigMap to exist, got %d", c)
	}

	lock := configMaps.Items[0]

	leaderAnnotation := "control-plane.alpha.kubernetes.io/leader"

	leader, ok := lock.Annotations[leaderAnnotation]
	if !ok {
		t.Fatalf("expected annotation %q not found", leaderAnnotation)
	}

	leaderLease := &struct {
		HolderIdentity       string
		LeaseDurationSeconds int
		AcquireTime          time.Time
		RenewTime            time.Time
		LeaderTransitions    int
	}{}

	if err := json.Unmarshal([]byte(leader), leaderLease); err != nil {
		t.Fatalf("Decoding leader annotation data %q: %v", leader, err)
	}

	leaderLease.HolderIdentity = "baz"

	leaderBytes, err := json.Marshal(leaderLease)
	if err != nil {
		t.Fatalf("Encoding leader annotation data: %q: %v", leader, err)
	}

	lock.Annotations[leaderAnnotation] = string(leaderBytes)

	if _, err := configMapClient.Update(ctx, &lock, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("Updating lock ConfigMap: %v", err)
	}
}

func Test_Operator_waits_for_leader_election_before_reconciliation(t *testing.T) {
	t.Parallel()

	rebootCancelledNode := rebootCancelledNode()

	config, _ := testConfig(rebootCancelledNode)
	config.BeforeRebootAnnotations = []string{testBeforeRebootAnnotation}
	config.ReconciliationPeriod = 1 * time.Second
	testKontroller := kontrollerWithObjects(t, config)

	stop := make(chan struct{})
	stopped := make(chan struct{})

	go func() {
		if err := testKontroller.Run(stop); err != nil {
			fmt.Printf("Error running operator: %v\n", err)
			t.Fail()
		}
		stopped <- struct{}{}
	}()

	time.Sleep(config.ReconciliationPeriod)

	close(stop)

	<-stopped

	ctx := contextWithDeadline(t)
	updatedNode := node(ctx, t, config.Client.CoreV1().Nodes(), rebootCancelledNode.Name)

	if _, ok := updatedNode.Labels[constants.LabelBeforeReboot]; ok {
		t.Fatalf("Expected label %q to be removed from Node after waiting the reconciliation period",
			constants.LabelBeforeReboot)
	}

	updatedNode.Labels[constants.LabelBeforeReboot] = constants.True
	updatedNode.Annotations[testBeforeRebootAnnotation] = constants.True

	if _, err := config.Client.CoreV1().Nodes().Update(ctx, updatedNode, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("Updating Node object: %v", err)
	}

	config.LockID = "bar"
	parallelKontroller := kontrollerWithObjects(t, config)

	stop = make(chan struct{})

	t.Cleanup(func() {
		close(stop)
	})

	runOperator(ctx, t, parallelKontroller, stop)

	time.Sleep(config.ReconciliationPeriod)

	updatedNode = node(ctx, t, config.Client.CoreV1().Nodes(), rebootCancelledNode.Name)

	if _, ok := updatedNode.Labels[constants.LabelBeforeReboot]; !ok {
		t.Fatalf("Expected label %q to remain on Node", constants.LabelBeforeReboot)
	}
}

func Test_Operator_stops_reconciliation_loop_when_control_channel_is_closed(t *testing.T) {
	t.Parallel()

	rebootCancelledNode := rebootCancelledNode()

	config, fakeClient := testConfig(rebootCancelledNode)
	config.BeforeRebootAnnotations = []string{testBeforeRebootAnnotation}
	config.ReconciliationPeriod = 1 * time.Second
	testKontroller := kontrollerWithObjects(t, config)

	nodeUpdated := nodeUpdatedNTimes(fakeClient, 1)

	stop := make(chan struct{})

	ctx := contextWithDeadline(t)

	runOperator(ctx, t, testKontroller, stop)

	<-nodeUpdated

	updatedNode := node(ctx, t, config.Client.CoreV1().Nodes(), rebootCancelledNode.Name)

	if _, ok := updatedNode.Labels[constants.LabelBeforeReboot]; ok {
		t.Fatalf("Expected label %q to be removed from Node after waiting the reconciliation period",
			constants.LabelBeforeReboot)
	}

	close(stop)

	time.Sleep(config.ReconciliationPeriod * 2)

	updatedNode.Labels[constants.LabelBeforeReboot] = constants.True
	updatedNode.Annotations[testBeforeRebootAnnotation] = constants.True

	if _, err := config.Client.CoreV1().Nodes().Update(ctx, updatedNode, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("Updating Node object: %v", err)
	}

	time.Sleep(config.ReconciliationPeriod * 2)

	updatedNode = node(ctx, t, config.Client.CoreV1().Nodes(), rebootCancelledNode.Name)

	if _, ok := updatedNode.Labels[constants.LabelBeforeReboot]; !ok {
		t.Fatalf("Expected label %q to remain on Node", constants.LabelBeforeReboot)
	}
}

func Test_Operator_reconciles_objects_every_configured_period(t *testing.T) {
	t.Parallel()

	rebootCancelledNode := rebootCancelledNode()

	config, _ := testConfig(rebootCancelledNode)
	config.BeforeRebootAnnotations = []string{testBeforeRebootAnnotation}
	config.ReconciliationPeriod = 1 * time.Second
	testKontroller := kontrollerWithObjects(t, config)

	stop := make(chan struct{})

	t.Cleanup(func() {
		close(stop)
	})

	ctx := contextWithDeadline(t)

	runOperator(ctx, t, testKontroller, stop)

	time.Sleep(config.ReconciliationPeriod)

	updatedNode := node(ctx, t, config.Client.CoreV1().Nodes(), rebootCancelledNode.Name)

	if _, ok := updatedNode.Labels[constants.LabelBeforeReboot]; ok {
		t.Fatalf("Expected label %q to be removed from Node after waiting the reconciliation period",
			constants.LabelBeforeReboot)
	}

	updatedNode.Labels[constants.LabelBeforeReboot] = constants.True
	updatedNode.Annotations[testBeforeRebootAnnotation] = constants.True

	if _, err := config.Client.CoreV1().Nodes().Update(ctx, updatedNode, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("Updating Node object: %v", err)
	}

	time.Sleep(config.ReconciliationPeriod * 2)

	updatedNode = node(ctx, t, config.Client.CoreV1().Nodes(), rebootCancelledNode.Name)

	if _, ok := updatedNode.Labels[constants.LabelBeforeReboot]; ok {
		t.Fatalf("Expected label %q to be removed from Node after waiting another reconciliation period",
			constants.LabelBeforeReboot)
	}
}

// before-reboot label is intended to be used as a selector for pre-reboot hooks, so it should only
// be set for nodes, which are ready to start rebooting any minute.
func Test_Operator_cleans_up_nodes_which_cannot_be_rebooted(t *testing.T) {
	t.Parallel()

	rebootCancelledNode := rebootCancelledNode()

	toBeRebootedNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "bar",
			Annotations: map[string]string{
				testBeforeRebootAnnotation: "",
			},
		},
	}

	config, fakeClient := testConfig(rebootCancelledNode, toBeRebootedNode)
	config.BeforeRebootAnnotations = []string{testBeforeRebootAnnotation}

	ctx := contextWithDeadline(t)

	<-process(ctx, t, config, fakeClient)

	updatedNode := node(ctx, t, config.Client.CoreV1().Nodes(), rebootCancelledNode.Name)

	t.Run("by", func(t *testing.T) {
		t.Parallel()

		t.Run("removing_before_reboot_label", func(t *testing.T) {
			t.Parallel()

			if _, ok := updatedNode.Labels[constants.LabelBeforeReboot]; ok {
				t.Fatalf("Unexpected label %q found", constants.LabelBeforeReboot)
			}
		})

		t.Run("removing_configured_before_reboot_annotations", func(t *testing.T) {
			t.Parallel()

			if _, ok := updatedNode.Annotations[testBeforeRebootAnnotation]; ok {
				t.Fatalf("Unexpected annotation %q found for node %q", testBeforeRebootAnnotation, rebootCancelledNode.Name)
			}

			updatedToBeRebootedNode := node(ctx, t, config.Client.CoreV1().Nodes(), toBeRebootedNode.Name)

			if _, ok := updatedToBeRebootedNode.Annotations[testBeforeRebootAnnotation]; !ok {
				t.Fatalf("Annotation %q has been removed from wrong node %q",
					testBeforeRebootAnnotation, toBeRebootedNode.Name)
			}
		})
	})

	// To avoid rebooting nodes which executed before-reboot hooks, but don't need a reboot anymore.
	t.Run("before_reboot_is_approved", func(t *testing.T) {
		t.Parallel()

		if v, ok := updatedNode.Annotations[constants.AnnotationOkToReboot]; ok && v == "true" {
			t.Fatalf("Unexpected reboot approval")
		}
	})
}

func Test_Operator_does_not_count_nodes_as_rebooting_which(t *testing.T) {
	t.Parallel()

	ctx := contextWithDeadline(t)

	cases := map[string]struct {
		expectedNodeUpdates int
		extraNode           *corev1.Node
	}{
		"has_finished_rebooting": {
			expectedNodeUpdates: 3,
			extraNode:           finishedRebootingNode(),
		},
		"are_idle": {
			expectedNodeUpdates: 2,
			extraNode:           idleNode(),
		},
	}

	for name, testCase := range cases { //nolint:paralleltest // False positive.
		testCase := testCase

		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rebootableNode := rebootableNode()

			config, fakeClient := testConfig(testCase.extraNode, rebootableNode)

			nodeUpdated := nodeUpdatedNTimes(fakeClient, testCase.expectedNodeUpdates)
			<-process(ctx, t, config, fakeClient)
			<-nodeUpdated

			updatedNode := node(ctx, t, config.Client.CoreV1().Nodes(), rebootableNode.Name)

			beforeReboot, ok := updatedNode.Labels[constants.LabelBeforeReboot]
			if !ok {
				t.Fatalf("Expected label %q", constants.LabelBeforeReboot)
			}

			if beforeReboot != constants.True {
				t.Fatalf("Expected value %q for label %q, got: %q", constants.True, constants.LabelBeforeReboot, beforeReboot)
			}
		})
	}
}

// This test attempts to schedule a reboot for a schedulable node and depending on the
// state of the rebooting node, controller will either proceed with scheduling or will
// not do anything.
func Test_Operator_counts_nodes_as_rebooting_which(t *testing.T) {
	t.Parallel()

	ctx := contextWithDeadline(t)

	cases := map[string]*corev1.Node{
		"are_scheduled_for_reboot_already": scheduledForRebootNode(),
		"are_ready_to_reboot":              readyToRebootNode(),
		"has_reboot_approved":              rebootNotConfirmedNode(),
		"are_rebooting":                    rebootingNode(),
		"just_rebooted":                    justRebootedNode(),
	}

	for name, extraNode := range cases { //nolint:paralleltest // False positive.
		extraNode := extraNode

		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rebootableNode := rebootableNode()

			config, fakeClient := testConfig(extraNode, rebootableNode)

			// Required to test selecting rebooting nodes only with before-reboot label, otherwise
			// it gets removed before we schedule nodes for rebooting.
			config.BeforeRebootAnnotations = []string{testBeforeRebootAnnotation}

			<-process(ctx, t, config, fakeClient)

			updatedNode := node(ctx, t, config.Client.CoreV1().Nodes(), rebootableNode.Name)

			if _, ok := updatedNode.Labels[constants.LabelBeforeReboot]; ok {
				t.Fatalf("Unexpected node %q scheduled for rebooting", rebootableNode.Name)
			}
		})
	}
}

func Test_Operator_does_not_count_nodes_as_rebootable_which(t *testing.T) {
	t.Parallel()

	ctx := contextWithDeadline(t)

	cases := map[string]func(*corev1.Node){
		"do_not_require_reboot": func(updatedNode *corev1.Node) {
			updatedNode.Annotations[constants.AnnotationRebootNeeded] = constants.False
		},
		"are_already_rebooting": func(updatedNode *corev1.Node) {
			*updatedNode = *rebootingNode()
			updatedNode.Annotations[testBeforeRebootAnnotation] = constants.True
			updatedNode.Annotations[testAnotherBeforeRebootAnnotation] = constants.True
		},
		"has_reboot_paused": func(updatedNode *corev1.Node) {
			updatedNode.Annotations[constants.AnnotationRebootPaused] = constants.True
		},
		"has_reboot_already_scheduled": func(updatedNode *corev1.Node) {
			updatedNode.Labels[constants.LabelBeforeReboot] = constants.True
			updatedNode.Annotations[testAnotherBeforeRebootAnnotation] = constants.False
		},
	}

	for name, mutateF := range cases { //nolint:paralleltest // False positive.
		mutateF := mutateF

		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rebootableNode := rebootableNode()
			rebootableNode.Annotations[testBeforeRebootAnnotation] = constants.True
			rebootableNode.Annotations[testAnotherBeforeRebootAnnotation] = constants.True

			mutateF(rebootableNode)

			config, fakeClient := testConfig(rebootableNode)
			config.BeforeRebootAnnotations = []string{testBeforeRebootAnnotation, testAnotherBeforeRebootAnnotation}
			// To test filter on before-reboot label.
			config.MaxRebootingNodes = 2

			<-process(ctx, t, config, fakeClient)

			updatedNode := node(ctx, t, config.Client.CoreV1().Nodes(), rebootableNode.Name)

			if _, ok := updatedNode.Annotations[testBeforeRebootAnnotation]; !ok {
				t.Fatalf("Unexpected node %q scheduled for rebooting", rebootableNode.Name)
			}
		})
	}
}

func Test_Operator_counts_nodes_as_rebootable_which_needs_reboot_and_has_all_other_conditions_met(t *testing.T) {
	t.Parallel()

	rebootableNode := rebootableNode()

	config, fakeClient := testConfig(rebootableNode)

	ctx := contextWithDeadline(t)

	nodeUpdated := nodeUpdatedNTimes(fakeClient, 1)
	<-process(ctx, t, config, fakeClient)
	<-nodeUpdated

	updatedNode := node(contextWithDeadline(t), t, config.Client.CoreV1().Nodes(), rebootableNode.Name)

	v, ok := updatedNode.Labels[constants.LabelBeforeReboot]
	if !ok || v != constants.True {
		t.Fatalf("Expected node %q to be scheduled for rebooting", rebootableNode.Name)
	}
}

func Test_Operator_does_not_schedules_reboot_process_outside_reboot_window(t *testing.T) {
	t.Parallel()

	rebootableNode := rebootableNode()

	config, fakeClient := testConfig(rebootableNode)
	config.RebootWindowStart = "Mon 14:00"
	config.RebootWindowLength = "0s"

	ctx := contextWithDeadline(t)

	<-process(ctx, t, config, fakeClient)

	updatedNode := node(ctx, t, config.Client.CoreV1().Nodes(), rebootableNode.Name)
	if v, ok := updatedNode.Labels[constants.LabelBeforeReboot]; ok && v == constants.True {
		t.Fatalf("Unexpected node %q scheduled for reboot", rebootableNode.Name)
	}
}

// To schedule pre-reboot hooks.
//
//nolint:funlen // Just many test cases.
func Test_Operator_schedules_reboot_process(t *testing.T) {
	t.Parallel()

	ctx := contextWithDeadline(t)

	t.Run("only_during_reboot_window", func(t *testing.T) {
		t.Parallel()

		rebootableNode := rebootableNode()

		config, fakeClient := testConfig(rebootableNode)
		config.RebootWindowStart = "Mon 00:00"
		config.RebootWindowLength = fmt.Sprintf("%ds", (7*24*60*60)-1)

		nodeUpdated := nodeUpdatedNTimes(fakeClient, 1)
		<-process(ctx, t, config, fakeClient)
		<-nodeUpdated

		updatedNode := node(ctx, t, config.Client.CoreV1().Nodes(), rebootableNode.Name)
		if _, ok := updatedNode.Labels[constants.LabelBeforeReboot]; !ok {
			t.Fatalf("Expected node %q to be scheduled for reboot", rebootableNode.Name)
		}
	})

	t.Run("only_for_maximum_number_of_rebooting_nodes_in_parallel", func(t *testing.T) {
		t.Parallel()

		rebootableNode := rebootableNode()

		config, fakeClient := testConfig(rebootableNode, rebootNotConfirmedNode())

		<-process(ctx, t, config, fakeClient)

		updatedNode := node(ctx, t, config.Client.CoreV1().Nodes(), rebootableNode.Name)
		if v, ok := updatedNode.Labels[constants.LabelBeforeReboot]; ok && v == constants.True {
			t.Fatalf("Unexpected node %q scheduled for reboot", rebootableNode.Name)
		}
	})

	t.Run("for_nodes_which_are_rebootable", func(t *testing.T) {
		t.Parallel()

		scheduledForRebootNode := scheduledForRebootNode()

		config, fakeClient := testConfig(scheduledForRebootNode)

		<-process(ctx, t, config, fakeClient)

		updatedNode := node(ctx, t, config.Client.CoreV1().Nodes(), scheduledForRebootNode.Name)

		if _, ok := updatedNode.Labels[constants.LabelBeforeReboot]; ok {
			t.Fatalf("Unexpected node %q scheduled for reboot", updatedNode.Name)
		}
	})

	t.Run("by", func(t *testing.T) {
		t.Parallel()

		rebootableNode := rebootableNode()
		rebootableNode.Annotations[testBeforeRebootAnnotation] = constants.True

		config, fakeClient := testConfig(rebootableNode)
		config.BeforeRebootAnnotations = []string{testBeforeRebootAnnotation}

		nodeUpdated := nodeUpdatedNTimes(fakeClient, 1)
		<-process(ctx, t, config, fakeClient)
		<-nodeUpdated

		updatedNode := node(ctx, t, config.Client.CoreV1().Nodes(), rebootableNode.Name)

		t.Run("removing_all_before_reboot_annotations", func(t *testing.T) {
			t.Parallel()

			if _, ok := updatedNode.Annotations[testBeforeRebootAnnotation]; ok {
				t.Fatalf("Unexpected annotation %q found", testBeforeRebootAnnotation)
			}
		})

		t.Run("setting_before_reboot_label_to_true", func(t *testing.T) {
			t.Parallel()

			beforeReboot, ok := updatedNode.Labels[constants.LabelBeforeReboot]
			if !ok {
				t.Fatalf("Expected label %q not found, got %v instead", constants.LabelBeforeReboot, updatedNode.Labels)
			}

			if beforeReboot != constants.True {
				t.Fatalf("Unexpected label value: %q", beforeReboot)
			}
		})
	})
}

func Test_Operator_approves_reboot_process_for_nodes_which_have(t *testing.T) {
	t.Parallel()

	ctx := contextWithDeadline(t)

	cases := map[string]struct {
		mutateF        func(*corev1.Node)
		expectRebootOK bool
	}{
		"all_conditions_met": {
			// Node without mutation should get ok-to-reboot.
			expectRebootOK: true,
		},
		"before_reboot_label": {
			mutateF: func(updatedNode *corev1.Node) {
				// Node without before-reboot label won't get ok-to-reboot.
				delete(updatedNode.Labels, constants.LabelBeforeReboot)
			},
		},
		"all_before_reboot_annotations_set_to_true": {
			mutateF: func(updatedNode *corev1.Node) {
				// Node without all before reboot annotations won't get ok-to-reboot.
				updatedNode.Annotations[testBeforeRebootAnnotation] = constants.False
			},
		},
	}

	for name, testCase := range cases { //nolint:paralleltest // False positive.
		testCase := testCase

		t.Run(name, func(t *testing.T) {
			t.Parallel()

			readyToRebootNode := readyToRebootNode()
			if testCase.mutateF != nil {
				testCase.mutateF(readyToRebootNode)
			}

			config, fakeClient := testConfig(readyToRebootNode)

			// Use beforeRebootAnnotations to be able to test moment when node has before-reboot
			// label, but it cannot be removed yet.
			config.BeforeRebootAnnotations = []string{testBeforeRebootAnnotation}

			<-process(ctx, t, config, fakeClient)

			updatedNode := node(ctx, t, config.Client.CoreV1().Nodes(), readyToRebootNode.Name)

			v, ok := updatedNode.Annotations[constants.AnnotationOkToReboot]
			if testCase.expectRebootOK && (!ok || v != constants.True) {
				t.Fatalf("Expected reboot-ok annotation, got %v", updatedNode.Annotations)
			}

			if !testCase.expectRebootOK && ok && v == constants.True {
				t.Fatalf("Unexpected reboot-ok annotation")
			}
		})
	}
}

// To inform agent it can proceed with node draining and rebooting.
func Test_Operator_approves_reboot_process_by(t *testing.T) {
	t.Parallel()

	readyToRebootNode := readyToRebootNode()

	config, fakeClient := testConfig(readyToRebootNode)
	config.BeforeRebootAnnotations = []string{testBeforeRebootAnnotation}

	ctx := contextWithDeadline(t)

	<-process(ctx, t, config, fakeClient)

	updatedNode := node(ctx, t, config.Client.CoreV1().Nodes(), readyToRebootNode.Name)

	// To de-schedule hook pods.
	t.Run("removing_before_reboot_label", func(t *testing.T) {
		t.Parallel()

		if _, ok := updatedNode.Labels[constants.LabelBeforeReboot]; ok {
			t.Fatalf("Unexpected label %q found", constants.LabelBeforeReboot)
		}
	})

	t.Run("removing_all_before_reboot_annotations", func(t *testing.T) {
		t.Parallel()

		if _, ok := updatedNode.Annotations[testBeforeRebootAnnotation]; ok {
			t.Fatalf("Unexpected annotation %q found", testBeforeRebootAnnotation)
		}
	})

	// To inform agent that all hooks are executed and it can proceed with the reboot.
	// Right now by setting ok-to-reboot label to true.
	t.Run("informing_agent_to_proceed_with_reboot_process", func(t *testing.T) {
		t.Parallel()

		okToReboot, ok := updatedNode.Annotations[constants.AnnotationOkToReboot]

		if !ok {
			t.Fatalf("Expected annotation %q not found, got %v", constants.AnnotationOkToReboot, updatedNode.Annotations)
		}

		if okToReboot != constants.True {
			t.Fatalf("Expected annotation %q value to be %q, got %q",
				constants.AnnotationOkToReboot, constants.True, okToReboot)
		}
	})
}

// Test opposite conditions starting from base to make sure all cases are covered.
//
//nolint:funlen,cyclop // Just many test cases.
func Test_Operator_counts_nodes_as_just_rebooted_which(t *testing.T) {
	t.Parallel()

	ctx := contextWithDeadline(t)

	cases := map[string]struct {
		mutateF            func(*corev1.Node)
		expectJustRebooted bool
	}{
		"has_all_conditions_met": {
			expectJustRebooted: true,
		},
		// Nodes which we allowed to reboot.
		"has_reboot_approved": {
			mutateF: func(updatedNode *corev1.Node) {
				updatedNode.Annotations[constants.AnnotationOkToReboot] = constants.False
			},
		},
		// Nodes which already rebooted.
		"does_not_need_a_reboot": {
			mutateF: func(updatedNode *corev1.Node) {
				updatedNode.Annotations[constants.AnnotationRebootNeeded] = constants.True
			},
		},
		// Nodes which already reported that they are back from rebooting.
		"which_finished_the_reboot": {
			mutateF: func(updatedNode *corev1.Node) {
				updatedNode.Annotations[constants.AnnotationRebootInProgress] = constants.True
			},
		},
		// Nodes which do not have hooks scheduled yet.
		"has_no_after_reboot_label": {
			mutateF: func(updatedNode *corev1.Node) {
				updatedNode.Labels[constants.LabelAfterReboot] = constants.True
			},
		},
	}

	for name, testCase := range cases { //nolint:paralleltest // False positive.
		testCase := testCase

		t.Run(name, func(t *testing.T) {
			t.Parallel()

			justRebootedNode := justRebootedNode()
			if testCase.mutateF != nil {
				testCase.mutateF(justRebootedNode)
			}

			config, fakeClient := testConfig(justRebootedNode)
			config.AfterRebootAnnotations = []string{testAfterRebootAnnotation, testAnotherAfterRebootAnnotation}

			<-process(ctx, t, config, fakeClient)

			updatedNode := node(ctx, t, config.Client.CoreV1().Nodes(), justRebootedNode.Name)

			v, ok := updatedNode.Labels[constants.LabelAfterReboot]
			if testCase.expectJustRebooted {
				if !ok || v != constants.True {
					t.Errorf("Expected after reboot label, got %v", updatedNode.Labels)
				}

				if _, ok := updatedNode.Annotations[testAfterRebootAnnotation]; ok {
					t.Errorf("Expected annotation %q to be removed", testAfterRebootAnnotation)
				}

				if _, ok := updatedNode.Annotations[testAnotherAfterRebootAnnotation]; ok {
					t.Errorf("Expected annotation %q to be removed", testAnotherAfterRebootAnnotation)
				}
			}

			if !testCase.expectJustRebooted {
				v, ok := updatedNode.Annotations[testAfterRebootAnnotation]
				if !ok || v != constants.False {
					t.Fatalf("Expected annotation %q to be left untouched", testAfterRebootAnnotation)
				}
			}
		})
	}
}

// To schedule post-reboot hooks.
func Test_Operator_confirms_reboot_process_by(t *testing.T) {
	t.Parallel()

	justRebootedNode := justRebootedNode()
	justRebootedNode.Annotations[testAfterRebootAnnotation] = constants.True
	justRebootedNode.Annotations[testAnotherAfterRebootAnnotation] = constants.True

	config, fakeClient := testConfig(justRebootedNode)
	config.AfterRebootAnnotations = []string{testAfterRebootAnnotation, testAnotherAfterRebootAnnotation}

	ctx := contextWithDeadline(t)

	<-process(ctx, t, config, fakeClient)

	updatedNode := node(ctx, t, config.Client.CoreV1().Nodes(), justRebootedNode.Name)

	// To ensure all annotations are freshly set.
	t.Run("removing_all_after_reboot_annotations", func(t *testing.T) {
		t.Parallel()

		if _, ok := updatedNode.Annotations[testAfterRebootAnnotation]; ok {
			t.Fatalf("Unexpected annotation %q found", testAfterRebootAnnotation)
		}

		if _, ok := updatedNode.Annotations[testAnotherAfterRebootAnnotation]; ok {
			t.Fatalf("Unexpected annotation %q found", testAnotherAfterRebootAnnotation)
		}
	})

	// To schedule after-reboot hook pods.
	t.Run("setting_after_reboot_label_to_true", func(t *testing.T) {
		t.Parallel()

		afterReboot, ok := updatedNode.Labels[constants.LabelAfterReboot]
		if !ok {
			t.Fatalf("Expected label %q not found, not %v", constants.LabelAfterReboot, updatedNode.Labels)
		}

		if afterReboot != constants.True {
			t.Fatalf("Expected label value %q, got %q", constants.True, afterReboot)
		}
	})
}

// Test opposite conditions starting from base to make sure all cases are covered.
func Test_Operator_counts_nodes_as_which_finished_rebooting_which_has(t *testing.T) {
	t.Parallel()

	ctx := contextWithDeadline(t)

	cases := map[string]struct {
		mutateF                 func(*corev1.Node)
		expectFinishedRebooting bool
	}{
		"all_conditions_met": {
			expectFinishedRebooting: true,
		},
		// Only consider nodes which runs the after-reboot hooks.
		"after_reboot_label_set": {
			mutateF: func(updatedNode *corev1.Node) {
				delete(updatedNode.Labels, constants.LabelAfterReboot)
			},
		},
		// To verify all hooks executed successfully.
		"all_after_reboot_annotations_set_to_true": {
			mutateF: func(updatedNode *corev1.Node) {
				updatedNode.Annotations[testAfterRebootAnnotation] = constants.False
			},
		},
	}

	for name, testCase := range cases { //nolint:paralleltest // False positive.
		testCase := testCase

		t.Run(name, func(t *testing.T) {
			t.Parallel()

			finishedRebootingNode := finishedRebootingNode()
			if testCase.mutateF != nil {
				testCase.mutateF(finishedRebootingNode)
			}

			config, fakeClient := testConfig(finishedRebootingNode)
			config.AfterRebootAnnotations = []string{testAfterRebootAnnotation, testAnotherAfterRebootAnnotation}

			<-process(ctx, t, config, fakeClient)

			updatedNode := node(ctx, t, config.Client.CoreV1().Nodes(), finishedRebootingNode.Name)

			v, ok := updatedNode.Annotations[constants.AnnotationOkToReboot]
			if !testCase.expectFinishedRebooting && ok && v != constants.True {
				t.Fatalf("Expected after reboot label, got %v", updatedNode.Labels)
			}

			if testCase.expectFinishedRebooting && ok && v == constants.True {
				t.Fatalf("Unexpected after reboot label")
			}
		})
	}
}

//nolint:funlen // Just many sub-tests.
func Test_Operator_stops_current_reconciliation_when(t *testing.T) {
	t.Parallel()

	for name, testCase := range map[string]struct {
		node                  *corev1.Node
		failingListCall       int
		failingUpdateCall     int
		expectedNodeCondition func(*corev1.Node) bool
	}{
		"cleaning_up_node_state_fails_because": {
			node:              rebootCancelledNode(),
			failingListCall:   0,
			failingUpdateCall: 0,
			expectedNodeCondition: func(node *corev1.Node) bool {
				_, ok := node.Labels[constants.LabelBeforeReboot]

				return ok
			},
		},
		"evaluating_nodes_which_finished_rebooting_fails_because": {
			node:              finishedRebootingNode(),
			failingListCall:   1,
			failingUpdateCall: 0,
			expectedNodeCondition: func(node *corev1.Node) bool {
				_, ok := node.Labels[constants.LabelAfterReboot]

				return ok
			},
		},
		"evaluating_nodes_which_just_rebooted_fails_because": {
			node:              justRebootedNode(),
			failingListCall:   2,
			failingUpdateCall: 1,
			expectedNodeCondition: func(node *corev1.Node) bool {
				_, ok := node.Labels[constants.LabelAfterReboot]

				return !ok
			},
		},
		"evaluating_nodes_which_are_ready_to_reboot_fails_because": {
			node:              readyToRebootNode(),
			failingListCall:   3,
			failingUpdateCall: 1,
			expectedNodeCondition: func(node *corev1.Node) bool {
				v, ok := node.Labels[constants.LabelBeforeReboot]

				return ok || v != constants.True
			},
		},
		"evaluating_nodes_which_needs_to_reboot_fails_because": {
			node:              rebootableNode(),
			failingListCall:   4,
			failingUpdateCall: 1,
			expectedNodeCondition: func(node *corev1.Node) bool {
				v, ok := node.Labels[constants.LabelBeforeReboot]

				return ok || v != constants.True
			},
		},
	} {
		testCase := testCase

		t.Run(name, func(t *testing.T) {
			t.Parallel()

			for subName, subTestCase := range map[string]struct {
				failingCall int
				verb        string
			}{
				"listing_node_objects_fails": {
					failingCall: testCase.failingListCall,
					verb:        "list",
				},
				"updating_node_fails": {
					failingCall: testCase.failingUpdateCall,
					verb:        "update",
				},
			} {
				subTestCase := subTestCase

				t.Run(subName, func(t *testing.T) {
					t.Parallel()

					config, fakeClient := testConfig(testCase.node)
					requestFailed, failRequest := failOnNthCall(subTestCase.failingCall, fmt.Errorf(t.Name()))
					fakeClient.PrependReactor(subTestCase.verb, "nodes", failRequest)

					ctx, cancel := context.WithTimeout(contextWithDeadline(t), 5*time.Second)
					t.Cleanup(cancel)

					process(ctx, t, config, fakeClient)

					select {
					case <-requestFailed:
					case <-ctx.Done():
						t.Fatalf("Timed out waiting for request to fail")
					}

					if !testCase.expectedNodeCondition(node(ctx, t, config.Client.CoreV1().Nodes(), testCase.node.Name)) {
						t.Fatalf("Expected condition not met")
					}
				})
			}
		})
	}
}

// To de-schedule post-reboot hooks.
func Test_Operator_finishes_reboot_process_by(t *testing.T) {
	t.Parallel()

	finishedRebootingNode := finishedRebootingNode()

	config, fakeClient := testConfig(finishedRebootingNode)
	config.AfterRebootAnnotations = []string{testAfterRebootAnnotation, testAnotherAfterRebootAnnotation}

	ctx := contextWithDeadline(t)
	<-process(ctx, t, config, fakeClient)

	updatedNode := node(ctx, t, config.Client.CoreV1().Nodes(), finishedRebootingNode.Name)

	// To de-schedule hook pods.
	t.Run("removing_after_reboot_label", func(t *testing.T) {
		t.Parallel()

		if _, ok := updatedNode.Labels[constants.LabelAfterReboot]; ok {
			t.Fatalf("Unexpected after reboot label found")
		}
	})

	// To cleanup the state before next runs.
	t.Run("removing_all_after_reboot_annotations", func(t *testing.T) {
		t.Parallel()

		if _, ok := updatedNode.Annotations[testAfterRebootAnnotation]; ok {
			t.Fatalf("Unexpected after reboot annotation %q found", testAfterRebootAnnotation)
		}

		if _, ok := updatedNode.Annotations[testAnotherAfterRebootAnnotation]; ok {
			t.Fatalf("Unexpected after reboot annotation %q found", testAnotherAfterRebootAnnotation)
		}
	})

	// To finalize reboot process. Implementation detail! setting ok-to-reboot label to false.
	t.Run("informing_agent_to_not_proceed_with_reboot_process", func(t *testing.T) {
		t.Parallel()

		okToReboot, ok := updatedNode.Annotations[constants.AnnotationOkToReboot]
		if !ok {
			t.Fatalf("Expected annotation %q not found, got %v", constants.AnnotationOkToReboot, updatedNode.Labels)
		}

		if okToReboot != constants.False {
			t.Fatalf("Expected annotation %q value %q, got %q", constants.AnnotationOkToReboot, constants.False, okToReboot)
		}
	})
}

// Expose klog flags to be able to increase verbosity for operator logs.
func TestMain(m *testing.M) {
	testFlags := flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	klog.InitFlags(testFlags)

	if err := testFlags.Parse([]string{"-v=10"}); err != nil {
		fmt.Printf("Failed parsing flags: %v\n", err)
		os.Exit(1)
	}

	os.Exit(m.Run())
}

func contextWithDeadline(t *testing.T) context.Context {
	t.Helper()

	deadline, ok := t.Deadline()
	if !ok {
		return context.Background()
	}

	// Arbitrary amount of time to let tests exit cleanly before main process terminates.
	timeoutGracePeriod := 10 * time.Second

	ctx, cancel := context.WithDeadline(context.Background(), deadline.Truncate(timeoutGracePeriod))
	t.Cleanup(cancel)

	return ctx
}

func runOperator(_ context.Context, t *testing.T, k *operator.Kontroller, stopCh <-chan struct{}) {
	t.Helper()

	go func() {
		if err := k.Run(stopCh); err != nil {
			fmt.Printf("Error running operator: %v\n", err)
			t.Fail()
		}
	}()
}

func validOperatorConfig() operator.Config {
	return operator.Config{
		Client:    fake.NewSimpleClientset(),
		Namespace: "test-namespace",
		LockID:    "test-lock-id",
	}
}

func testConfig(objects ...runtime.Object) (operator.Config, *k8stesting.Fake) {
	client := fake.NewSimpleClientset(objects...)

	return operator.Config{
		Client:    client,
		LockID:    "foo",
		Namespace: testNamespace,
	}, &client.Fake
}

func kontrollerWithObjects(t *testing.T, config operator.Config) *operator.Kontroller {
	t.Helper()

	kontroller, err := operator.New(config)
	if err != nil {
		t.Fatalf("Failed creating controller instance: %v", err)
	}

	return kontroller
}

// Node with no need for rebooting.
func idleNode() *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "idle",
			Labels: map[string]string{},
			Annotations: map[string]string{
				constants.AnnotationOkToReboot:       constants.False,
				constants.AnnotationRebootNeeded:     constants.False,
				constants.AnnotationRebootInProgress: constants.False,
			},
		},
	}
}

// Node with need for rebooting.
func rebootableNode() *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "rebootable",
			Labels: map[string]string{
				constants.LabelRebootNeeded: constants.True,
			},
			Annotations: map[string]string{
				constants.AnnotationRebootNeeded:     constants.True,
				constants.AnnotationOkToReboot:       constants.False,
				constants.AnnotationRebootInProgress: constants.False,
				testBeforeRebootAnnotation:           constants.False,
			},
		},
	}
}

// Node which has been scheduled for rebooting and runs before reboot hooks.
func scheduledForRebootNode() *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "scheduled-for-reboot",
			Labels: map[string]string{
				constants.LabelBeforeReboot: constants.True,
			},
			Annotations: map[string]string{
				constants.AnnotationRebootNeeded:     constants.True,
				constants.AnnotationOkToReboot:       constants.False,
				constants.AnnotationRebootInProgress: constants.False,
			},
		},
	}
}

// Node which has run pre-reboot hooks, but no longer needs a reboot.
func rebootCancelledNode() *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "before-reboot",
			Labels: map[string]string{
				constants.LabelBeforeReboot: constants.True,
			},
			Annotations: map[string]string{
				testBeforeRebootAnnotation: constants.True,
			},
		},
	}
}

// Node which has finished running before reboot hooks.
func readyToRebootNode() *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "ready-to-reboot",
			Labels: map[string]string{
				constants.LabelBeforeReboot: constants.True,
			},
			Annotations: map[string]string{
				constants.AnnotationRebootNeeded:     constants.True,
				testBeforeRebootAnnotation:           constants.True,
				constants.AnnotationOkToReboot:       constants.False,
				constants.AnnotationRebootInProgress: constants.False,
			},
		},
	}
}

// Node which reboot has been approved by operator, but not confirmed by agent.
func rebootNotConfirmedNode() *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "reboot-not-confirmed",
			Labels: map[string]string{},
			Annotations: map[string]string{
				constants.AnnotationOkToReboot:       constants.True,
				constants.AnnotationRebootNeeded:     constants.True,
				constants.AnnotationRebootInProgress: constants.False,
			},
		},
	}
}

// Node which reboot has been confirmed by agent.
func rebootingNode() *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "rebooting",
			Labels: map[string]string{},
			Annotations: map[string]string{
				constants.AnnotationOkToReboot:       constants.True,
				constants.AnnotationRebootNeeded:     constants.True,
				constants.AnnotationRebootInProgress: constants.True,
			},
		},
	}
}

// Node which agent just finished rebooting.
func justRebootedNode() *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "just-rebooted",
			Labels: map[string]string{},
			Annotations: map[string]string{
				constants.AnnotationOkToReboot:       constants.True,
				constants.AnnotationRebootNeeded:     constants.False,
				constants.AnnotationRebootInProgress: constants.False,

				// Test data.
				testAfterRebootAnnotation:        constants.False,
				testAnotherAfterRebootAnnotation: constants.False,
			},
		},
	}
}

// Node which runs after reboot hooks.
func finishedRebootingNode() *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "finished-rebooting",
			Labels: map[string]string{
				constants.LabelAfterReboot: constants.True,
			},
			Annotations: map[string]string{
				constants.AnnotationOkToReboot:       constants.True,
				testAfterRebootAnnotation:            constants.True,
				testAnotherAfterRebootAnnotation:     constants.True,
				constants.AnnotationRebootInProgress: constants.False,
			},
		},
	}
}

func node(ctx context.Context, t *testing.T, nodeClient corev1client.NodeInterface, name string) *corev1.Node {
	t.Helper()

	node, err := nodeClient.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Getting node %q: %v", name, err)
	}

	return node
}

func process(ctx context.Context, t *testing.T, config operator.Config, fakeClient *k8stesting.Fake) chan struct{} {
	t.Helper()

	reconcileCycleCh := make(chan struct{}, 1)

	listCallsCount := 0

	fakeClient.PrependReactor("list", "nodes", func(action k8stesting.Action) (bool, runtime.Object, error) {
		operatorListOperations := 4

		if listCallsCount == operatorListOperations {
			reconcileCycleCh <- struct{}{}
			listCallsCount = 0

			return false, nil, nil
		}

		listCallsCount++

		return false, nil, nil
	})

	stop := make(chan struct{})

	t.Cleanup(func() {
		close(stop)
	})

	runOperator(ctx, t, kontrollerWithObjects(t, config), stop)

	return reconcileCycleCh
}

func nodeUpdatedNTimes(fakeClient *k8stesting.Fake, expectedUpdateCalls int) chan struct{} {
	updateCallsCount := 0
	nodeUpdatedCh := make(chan struct{}, 1)

	fakeClient.PrependReactor("update", "nodes", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if updateCallsCount == expectedUpdateCalls {
			nodeUpdatedCh <- struct{}{}

			updateCallsCount = 0

			return false, nil, nil
		}

		updateCallsCount++

		return false, nil, nil
	})

	return nodeUpdatedCh
}

func failOnNthCall(failingCall int, err error) (chan struct{}, k8stesting.ReactionFunc) {
	callCounter := 0

	errorReached := make(chan struct{}, 1)

	return errorReached, func(action k8stesting.Action) (bool, runtime.Object, error) {
		// TODO: Make implementation smarter.
		if callCounter != failingCall {
			callCounter++

			return false, nil, nil
		}

		if len(errorReached) == 0 {
			errorReached <- struct{}{}
		}

		return true, nil, err
	}
}
