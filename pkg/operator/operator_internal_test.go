package operator

import (
	"context"
	"fmt"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"

	"github.com/coreos/locksmith/pkg/timeutil"
	"github.com/kinvolk/flatcar-linux-update-operator/pkg/constants"
)

const (
	testBeforeRebootAnnotation        = "test-before-annotation"
	testAnotherBeforeRebootAnnotation = "test-another-after-annotation"
	testAfterRebootAnnotation         = "test-after-annotation"
	testAnotherAfterRebootAnnotation  = "test-another-after-annotation"
)

//nolint:funlen
func Test_Operator_waits_for_leader_election_before_reconciliation(t *testing.T) {
	t.Parallel()

	rebootCancelledNode := rebootCancelledNode()

	k := kontrollerWithObjects(rebootCancelledNode)
	k.beforeRebootAnnotations = []string{testBeforeRebootAnnotation}
	k.reconciliationPeriod = 1 * time.Second

	stop := make(chan struct{})
	stopped := make(chan struct{})

	go func() {
		if err := k.Run(stop); err != nil {
			fmt.Printf("Error running operator: %v\n", err)
			t.Fail()
		}
		stopped <- struct{}{}
	}()

	time.Sleep(k.reconciliationPeriod)

	close(stop)

	<-stopped

	n := node(t, k.nc, rebootCancelledNode.Name)

	if _, ok := n.Labels[constants.LabelBeforeReboot]; ok {
		t.Fatalf("Expected label %q to be removed from Node after waiting the reconciliation period",
			constants.LabelBeforeReboot)
	}

	n.Labels[constants.LabelBeforeReboot] = constants.True
	n.Annotations[testBeforeRebootAnnotation] = constants.True

	if _, err := k.nc.Update(contextWithDeadline(t), n, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("Updating Node object: %v", err)
	}

	ak := kontrollerWithObjects()
	ak.kc = k.kc
	ak.nc = k.nc
	ak.beforeRebootAnnotations = k.beforeRebootAnnotations
	ak.reconciliationPeriod = k.reconciliationPeriod
	ak.lockID = "bar"

	stop = make(chan struct{})

	t.Cleanup(func() {
		close(stop)
	})

	runOperator(t, ak, stop)

	time.Sleep(ak.reconciliationPeriod)

	n = node(t, k.nc, rebootCancelledNode.Name)

	if _, ok := n.Labels[constants.LabelBeforeReboot]; !ok {
		t.Fatalf("Expected label %q to remain on Node", constants.LabelBeforeReboot)
	}
}

func Test_Operator_stops_reconciliation_loop_when_control_channel_is_closed(t *testing.T) {
	t.Parallel()

	rebootCancelledNode := rebootCancelledNode()

	k := kontrollerWithObjects(rebootCancelledNode)
	k.beforeRebootAnnotations = []string{testBeforeRebootAnnotation}
	k.reconciliationPeriod = 1 * time.Second

	stop := make(chan struct{})

	runOperator(t, k, stop)

	time.Sleep(k.reconciliationPeriod)

	n := node(t, k.nc, rebootCancelledNode.Name)

	if _, ok := n.Labels[constants.LabelBeforeReboot]; ok {
		t.Fatalf("Expected label %q to be removed from Node after waiting the reconciliation period",
			constants.LabelBeforeReboot)
	}

	close(stop)

	time.Sleep(k.reconciliationPeriod * 2)

	n.Labels[constants.LabelBeforeReboot] = constants.True
	n.Annotations[testBeforeRebootAnnotation] = constants.True

	if _, err := k.nc.Update(contextWithDeadline(t), n, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("Updating Node object: %v", err)
	}

	time.Sleep(k.reconciliationPeriod * 2)

	n = node(t, k.nc, rebootCancelledNode.Name)

	if _, ok := n.Labels[constants.LabelBeforeReboot]; !ok {
		t.Fatalf("Expected label %q to remain on Node", constants.LabelBeforeReboot)
	}
}

func Test_Operator_reconciles_objects_every_configured_period(t *testing.T) {
	t.Parallel()

	rebootCancelledNode := rebootCancelledNode()

	k := kontrollerWithObjects(rebootCancelledNode)
	k.beforeRebootAnnotations = []string{testBeforeRebootAnnotation}
	k.reconciliationPeriod = 1 * time.Second

	stop := make(chan struct{})

	t.Cleanup(func() {
		close(stop)
	})

	runOperator(t, k, stop)

	time.Sleep(k.reconciliationPeriod)

	n := node(t, k.nc, rebootCancelledNode.Name)

	if _, ok := n.Labels[constants.LabelBeforeReboot]; ok {
		t.Fatalf("Expected label %q to be removed from Node after waiting the reconciliation period",
			constants.LabelBeforeReboot)
	}

	n.Labels[constants.LabelBeforeReboot] = constants.True
	n.Annotations[testBeforeRebootAnnotation] = constants.True

	if _, err := k.nc.Update(contextWithDeadline(t), n, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("Updating Node object: %v", err)
	}

	time.Sleep(k.reconciliationPeriod * 2)

	n = node(t, k.nc, rebootCancelledNode.Name)

	if _, ok := n.Labels[constants.LabelBeforeReboot]; ok {
		t.Fatalf("Expected label %q to be removed from Node after waiting another reconciliation period",
			constants.LabelBeforeReboot)
	}
}

// before-reboot label is intended to be used as a selector for pre-reboot hooks, so it should only
// be set for nodes, which are ready to start rebooting any minute.
//
//nolint:funlen
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

	k := kontrollerWithObjects(rebootCancelledNode, toBeRebootedNode)
	k.beforeRebootAnnotations = []string{testBeforeRebootAnnotation}

	k.process(contextWithDeadline(t))

	n := node(t, k.nc, rebootCancelledNode.Name)

	t.Run("by", func(t *testing.T) {
		t.Parallel()

		t.Run("removing_before_reboot_label", func(t *testing.T) {
			t.Parallel()

			if _, ok := n.Labels[constants.LabelBeforeReboot]; ok {
				t.Fatalf("Unexpected label %q found", constants.LabelBeforeReboot)
			}
		})

		t.Run("removing_configured_before_reboot_annotations", func(t *testing.T) {
			t.Parallel()

			if _, ok := n.Annotations[testBeforeRebootAnnotation]; ok {
				t.Fatalf("Unexpected annotation %q found for node %q", testBeforeRebootAnnotation, rebootCancelledNode.Name)
			}

			nn := node(t, k.nc, toBeRebootedNode.Name)

			if _, ok := nn.Annotations[testBeforeRebootAnnotation]; !ok {
				t.Fatalf("Annotation %q has been removed from wrong node %q",
					testBeforeRebootAnnotation, toBeRebootedNode.Name)
			}
		})
	})

	// To avoid rebooting nodes which executed before-reboot hooks, but don't need a reboot anymore.
	t.Run("before_reboot_is_approved", func(t *testing.T) {
		t.Parallel()

		if v, ok := n.Annotations[constants.AnnotationOkToReboot]; ok && v == "true" {
			t.Fatalf("Unexpected reboot approval")
		}
	})
}

func Test_Operator_does_not_count_nodes_as_rebooting_which(t *testing.T) {
	t.Parallel()

	ctx := contextWithDeadline(t)

	cases := map[string]*corev1.Node{
		"has_finished_rebooting": finishedRebootingNode(),
		"are_idle":               idleNode(),
	}

	for name, c := range cases { //nolint:paralleltest
		c := c

		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rebootableNode := rebootableNode()

			extraNode := c

			k := kontrollerWithObjects(extraNode, rebootableNode)

			k.process(ctx)

			n := node(t, k.nc, rebootableNode.Name)

			v, ok := n.Labels[constants.LabelBeforeReboot]
			if !ok {
				t.Fatalf("Expected label %q", constants.LabelBeforeReboot)
			}
			if v != constants.True {
				t.Fatalf("Expected value %q for label %q, got: %q", constants.True, constants.LabelBeforeReboot, v)
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

	for name, c := range cases { //nolint:paralleltest
		c := c

		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rebootableNode := rebootableNode()

			extraNode := c

			k := kontrollerWithObjects(extraNode, rebootableNode)
			// Required to test selecting rebooting nodes only with before-reboot label, otherwise
			// it gets removed before we schedule nodes for rebooting.
			k.beforeRebootAnnotations = []string{testBeforeRebootAnnotation}

			k.process(ctx)

			n := node(t, k.nc, rebootableNode.Name)

			if _, ok := n.Labels[constants.LabelBeforeReboot]; ok {
				t.Fatalf("Unexpected node %q scheduled for rebooting", rebootableNode.Name)
			}
		})
	}
}

func Test_Operator_does_not_count_nodes_as_rebootable_which(t *testing.T) {
	t.Parallel()

	ctx := contextWithDeadline(t)

	cases := map[string]func(*corev1.Node){
		"do_not_require_reboot": func(n *corev1.Node) {
			n.Annotations[constants.AnnotationRebootNeeded] = constants.False
		},
		"are_already_rebooting": func(n *corev1.Node) {
			*n = *rebootingNode()
			n.Annotations[testBeforeRebootAnnotation] = constants.True
			n.Annotations[testAnotherBeforeRebootAnnotation] = constants.True
		},
		"has_reboot_paused": func(n *corev1.Node) {
			n.Annotations[constants.AnnotationRebootPaused] = constants.True
		},
		"has_reboot_already_scheduled": func(n *corev1.Node) {
			n.Labels[constants.LabelBeforeReboot] = constants.True
			n.Annotations[testAnotherBeforeRebootAnnotation] = constants.False
		},
	}

	for name, c := range cases { //nolint:paralleltest
		c := c

		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rebootableNode := rebootableNode()
			rebootableNode.Annotations[testBeforeRebootAnnotation] = constants.True
			rebootableNode.Annotations[testAnotherBeforeRebootAnnotation] = constants.True

			c(rebootableNode)

			k := kontrollerWithObjects(rebootableNode)

			// To test filter on before-reboot label.
			k.maxRebootingNodes = 2
			k.beforeRebootAnnotations = []string{testBeforeRebootAnnotation, testAnotherBeforeRebootAnnotation}

			k.process(ctx)

			n := node(t, k.nc, rebootableNode.Name)

			if _, ok := n.Annotations[testBeforeRebootAnnotation]; !ok {
				t.Fatalf("Unexpected node %q scheduled for rebooting", rebootableNode.Name)
			}
		})
	}
}

func Test_Operator_counts_nodes_as_rebootable_which_needs_reboot_and_has_all_other_conditions_met(t *testing.T) {
	t.Parallel()

	rebootableNode := rebootableNode()

	k := kontrollerWithObjects(rebootableNode)

	k.process(contextWithDeadline(t))

	n := node(t, k.nc, rebootableNode.Name)

	v, ok := n.Labels[constants.LabelBeforeReboot]
	if !ok || v != constants.True {
		t.Fatalf("Expected node %q to be scheduled for rebooting", rebootableNode.Name)
	}
}

func Test_Operator_does_not_schedules_reboot_process_outside_reboot_window(t *testing.T) {
	t.Parallel()

	rebootableNode := rebootableNode()

	k := kontrollerWithObjects(rebootableNode)

	rw, err := timeutil.ParsePeriodic("Mon 14:00", "0s")
	if err != nil {
		t.Fatalf("Parsing reboot window: %v", err)
	}

	k.rebootWindow = rw

	k.process(contextWithDeadline(t))

	n := node(t, k.nc, rebootableNode.Name)
	if v, ok := n.Labels[constants.LabelBeforeReboot]; ok && v == constants.True {
		t.Fatalf("Unexpected node %q scheduled for reboot", rebootableNode.Name)
	}
}

// To schedule pre-reboot hooks.
//
//nolint:funlen
func Test_Operator_schedules_reboot_process(t *testing.T) {
	t.Parallel()

	ctx := contextWithDeadline(t)

	t.Run("only_during_reboot_window", func(t *testing.T) {
		t.Parallel()

		rebootableNode := rebootableNode()

		k := kontrollerWithObjects(rebootableNode)

		rw, err := timeutil.ParsePeriodic("Mon 00:00", fmt.Sprintf("%ds", (7*24*60*60)-1))
		if err != nil {
			t.Fatalf("Parsing reboot window: %v", err)
		}

		k.rebootWindow = rw

		k.process(ctx)

		n := node(t, k.nc, rebootableNode.Name)
		if _, ok := n.Labels[constants.LabelBeforeReboot]; !ok {
			t.Fatalf("Expected node %q to be scheduled for reboot", rebootableNode.Name)
		}
	})

	t.Run("only_for_maximum_number_of_rebooting_nodes_in_parallel", func(t *testing.T) {
		t.Parallel()

		rebootableNode := rebootableNode()

		k := kontrollerWithObjects(rebootableNode, rebootNotConfirmedNode())

		k.process(ctx)

		n := node(t, k.nc, rebootableNode.Name)
		if v, ok := n.Labels[constants.LabelBeforeReboot]; ok && v == constants.True {
			t.Fatalf("Unexpected node %q scheduled for reboot", rebootableNode.Name)
		}
	})

	t.Run("for_nodes_which_are_rebootable", func(t *testing.T) {
		t.Parallel()

		scheduledForRebootNode := scheduledForRebootNode()

		k := kontrollerWithObjects(scheduledForRebootNode)

		k.process(ctx)

		n := node(t, k.nc, scheduledForRebootNode.Name)

		if _, ok := n.Labels[constants.LabelBeforeReboot]; ok {
			t.Fatalf("Unexpected node %q scheduled for reboot", n.Name)
		}
	})

	t.Run("by", func(t *testing.T) {
		t.Parallel()

		rebootableNode := rebootableNode()
		rebootableNode.Annotations[testBeforeRebootAnnotation] = constants.True

		k := kontrollerWithObjects(rebootableNode)
		k.beforeRebootAnnotations = []string{testBeforeRebootAnnotation}

		k.process(ctx)

		n := node(t, k.nc, rebootableNode.Name)

		t.Run("removing_all_before_reboot_annotations", func(t *testing.T) {
			t.Parallel()

			if _, ok := n.Annotations[testBeforeRebootAnnotation]; ok {
				t.Fatalf("Unexpected annotation %q found", testBeforeRebootAnnotation)
			}
		})

		t.Run("setting_before_reboot_label_to_true", func(t *testing.T) {
			t.Parallel()

			v, ok := n.Labels[constants.LabelBeforeReboot]
			if !ok {
				t.Fatalf("Expected label %q not found, got %v instead", constants.LabelBeforeReboot, n.Labels)
			}

			if v != constants.True {
				t.Fatalf("Unexpected label value: %q", v)
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
			mutateF: func(n *corev1.Node) {
				// Node without before-reboot label won't get ok-to-reboot.
				delete(n.Labels, constants.LabelBeforeReboot)
			},
		},
		"all_before_reboot_annotations_set_to_true": {
			mutateF: func(n *corev1.Node) {
				// Node without all before reboot annotations won't get ok-to-reboot.
				n.Annotations[testBeforeRebootAnnotation] = constants.False
			},
		},
	}

	for name, c := range cases { //nolint:paralleltest
		c := c

		t.Run(name, func(t *testing.T) {
			t.Parallel()

			readyToRebootNode := readyToRebootNode()
			if c.mutateF != nil {
				c.mutateF(readyToRebootNode)
			}

			k := kontrollerWithObjects(readyToRebootNode)
			// Use beforeRebootAnnotations to be able to test moment when node has before-reboot
			// label, but it cannot be removed yet.
			k.beforeRebootAnnotations = []string{testBeforeRebootAnnotation}

			k.process(ctx)

			n := node(t, k.nc, readyToRebootNode.Name)

			v, ok := n.Annotations[constants.AnnotationOkToReboot]
			if c.expectRebootOK && (!ok || v != constants.True) {
				t.Fatalf("Expected reboot-ok annotation, got %v", n.Annotations)
			}

			if !c.expectRebootOK && ok && v == constants.True {
				t.Fatalf("Unexpected reboot-ok annotation")
			}
		})
	}
}

// To inform agent it can proceed with node draining and rebooting.
func Test_Operator_approves_reboot_process_by(t *testing.T) {
	t.Parallel()

	readyToRebootNode := readyToRebootNode()

	k := kontrollerWithObjects(readyToRebootNode)
	k.beforeRebootAnnotations = []string{testBeforeRebootAnnotation}

	k.process(contextWithDeadline(t))

	n := node(t, k.nc, readyToRebootNode.Name)

	// To de-schedule hook pods.
	t.Run("removing_before_reboot_label", func(t *testing.T) {
		t.Parallel()

		if _, ok := n.Labels[constants.LabelBeforeReboot]; ok {
			t.Fatalf("Unexpected label %q found", constants.LabelBeforeReboot)
		}
	})

	t.Run("removing_all_before_reboot_annotations", func(t *testing.T) {
		t.Parallel()

		if _, ok := n.Annotations[testBeforeRebootAnnotation]; ok {
			t.Fatalf("Unexpected annotation %q found", testBeforeRebootAnnotation)
		}
	})

	// To inform agent that all hooks are executed and it can proceed with the reboot.
	// Right now by setting ok-to-reboot label to true.
	t.Run("informing_agent_to_proceed_with_reboot_process", func(t *testing.T) {
		t.Parallel()

		v, ok := n.Annotations[constants.AnnotationOkToReboot]

		if !ok {
			t.Fatalf("Expected annotation %q not found, got %v", constants.AnnotationOkToReboot, n.Annotations)
		}

		if v != constants.True {
			t.Fatalf("Expected annotation %q value to be %q, got %q", constants.AnnotationOkToReboot, constants.True, v)
		}
	})
}

// Test opposite conditions starting from base to make sure all cases are covered.
//
//nolint:funlen,cyclop
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
			mutateF: func(n *corev1.Node) {
				n.Annotations[constants.AnnotationOkToReboot] = constants.False
			},
		},
		// Nodes which already rebooted.
		"does_not_need_a_reboot": {
			mutateF: func(n *corev1.Node) {
				n.Annotations[constants.AnnotationRebootNeeded] = constants.True
			},
		},
		// Nodes which already reported that they are back from rebooting.
		"which_finished_the_reboot": {
			mutateF: func(n *corev1.Node) {
				n.Annotations[constants.AnnotationRebootInProgress] = constants.True
			},
		},
		// Nodes which do not have hooks scheduled yet.
		"has_no_after_reboot_label": {
			mutateF: func(n *corev1.Node) {
				n.Labels[constants.LabelAfterReboot] = constants.True
			},
		},
	}

	for name, c := range cases { //nolint:paralleltest
		c := c

		t.Run(name, func(t *testing.T) {
			t.Parallel()

			justRebootedNode := justRebootedNode()
			if c.mutateF != nil {
				c.mutateF(justRebootedNode)
			}

			k := kontrollerWithObjects(justRebootedNode)
			k.afterRebootAnnotations = []string{testAfterRebootAnnotation, testAnotherAfterRebootAnnotation}

			k.process(ctx)

			n := node(t, k.nc, justRebootedNode.Name)

			v, ok := n.Labels[constants.LabelAfterReboot]
			if c.expectJustRebooted {
				if !ok || v != constants.True {
					t.Errorf("Expected after reboot label, got %v", n.Labels)
				}

				if _, ok := n.Annotations[testAfterRebootAnnotation]; ok {
					t.Errorf("Expected annotation %q to be removed", testAfterRebootAnnotation)
				}

				if _, ok := n.Annotations[testAnotherAfterRebootAnnotation]; ok {
					t.Errorf("Expected annotation %q to be removed", testAnotherAfterRebootAnnotation)
				}
			}

			if !c.expectJustRebooted {
				v, ok := n.Annotations[testAfterRebootAnnotation]
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

	k := kontrollerWithObjects(justRebootedNode)
	k.afterRebootAnnotations = []string{testAfterRebootAnnotation, testAnotherAfterRebootAnnotation}

	k.process(contextWithDeadline(t))

	n := node(t, k.nc, justRebootedNode.Name)

	// To ensure all annotations are freshly set.
	t.Run("removing_all_after_reboot_annotations", func(t *testing.T) {
		t.Parallel()

		if _, ok := n.Annotations[testAfterRebootAnnotation]; ok {
			t.Fatalf("Unexpected annotation %q found", testAfterRebootAnnotation)
		}

		if _, ok := n.Annotations[testAnotherAfterRebootAnnotation]; ok {
			t.Fatalf("Unexpected annotation %q found", testAnotherAfterRebootAnnotation)
		}
	})

	// To schedule after-reboot hook pods.
	t.Run("setting_after_reboot_label_to_true", func(t *testing.T) {
		t.Parallel()

		v, ok := n.Labels[constants.LabelAfterReboot]
		if !ok {
			t.Fatalf("Expected label %q not found, not %v", constants.LabelAfterReboot, n.Labels)
		}

		if v != constants.True {
			t.Fatalf("Expected label value %q, got %q", constants.True, v)
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
			mutateF: func(n *corev1.Node) {
				delete(n.Labels, constants.LabelAfterReboot)
			},
		},
		// To verify all hooks executed successfully.
		"all_after_reboot_annotations_set_to_true": {
			mutateF: func(n *corev1.Node) {
				n.Annotations[testAfterRebootAnnotation] = constants.False
			},
		},
	}

	for name, c := range cases { //nolint:paralleltest
		c := c

		t.Run(name, func(t *testing.T) {
			t.Parallel()

			finishedRebootingNode := finishedRebootingNode()
			if c.mutateF != nil {
				c.mutateF(finishedRebootingNode)
			}

			k := kontrollerWithObjects(finishedRebootingNode)
			k.afterRebootAnnotations = []string{testAfterRebootAnnotation, testAnotherAfterRebootAnnotation}

			k.process(ctx)

			n := node(t, k.nc, finishedRebootingNode.Name)

			v, ok := n.Annotations[constants.AnnotationOkToReboot]
			if !c.expectFinishedRebooting && ok && v != constants.True {
				t.Fatalf("Expected after reboot label, got %v", n.Labels)
			}

			if c.expectFinishedRebooting && ok && v == constants.True {
				t.Fatalf("Unexpected after reboot label")
			}
		})
	}
}

// To de-schedule post-reboot hooks.
func Test_Operator_finishes_reboot_process_by(t *testing.T) {
	t.Parallel()

	finishedRebootingNode := finishedRebootingNode()

	k := kontrollerWithObjects(finishedRebootingNode)
	k.afterRebootAnnotations = []string{testAfterRebootAnnotation, testAnotherAfterRebootAnnotation}

	k.process(contextWithDeadline(t))

	n := node(t, k.nc, finishedRebootingNode.Name)

	// To de-schedule hook pods.
	t.Run("removing_after_reboot_label", func(t *testing.T) {
		t.Parallel()

		if _, ok := n.Labels[constants.LabelAfterReboot]; ok {
			t.Fatalf("Unexpected after reboot label found")
		}
	})

	// To cleanup the state before next runs.
	t.Run("removing_all_after_reboot_annotations", func(t *testing.T) {
		t.Parallel()

		if _, ok := n.Annotations[testAfterRebootAnnotation]; ok {
			t.Fatalf("Unexpected after reboot annotation %q found", testAfterRebootAnnotation)
		}

		if _, ok := n.Annotations[testAnotherAfterRebootAnnotation]; ok {
			t.Fatalf("Unexpected after reboot annotation %q found", testAnotherAfterRebootAnnotation)
		}
	})

	// To finalize reboot process. Implementation detail! setting ok-to-reboot label to false.
	t.Run("informing_agent_to_not_proceed_with_reboot_process", func(t *testing.T) {
		t.Parallel()

		v, ok := n.Annotations[constants.AnnotationOkToReboot]
		if !ok {
			t.Fatalf("Expected annotation %q not found, got %v", constants.AnnotationOkToReboot, n.Labels)
		}

		if v != constants.False {
			t.Fatalf("Expected annotation %q value %q, got %q", constants.AnnotationOkToReboot, constants.False, v)
		}
	})
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

func runOperator(t *testing.T, k *Kontroller, stopCh <-chan struct{}) {
	t.Helper()

	go func() {
		if err := k.Run(stopCh); err != nil {
			fmt.Printf("Error running operator: %v\n", err)
			t.Fail()
		}
	}()
}

func kontrollerWithObjects(objects ...runtime.Object) *Kontroller {
	fakeClient := fake.NewSimpleClientset(objects...)

	return &Kontroller{
		kc:                   fakeClient,
		nc:                   fakeClient.CoreV1().Nodes(),
		maxRebootingNodes:    1,
		reconciliationPeriod: defaultReconciliationPeriod,
		leaderElectionLease:  defaultLeaderElectionLease,
		lockID:               "foo",
	}
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

func node(t *testing.T, nodeClient corev1client.NodeInterface, name string) *corev1.Node {
	t.Helper()

	node, err := nodeClient.Get(contextWithDeadline(t), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Getting node %q: %v", name, err)
	}

	return node
}
