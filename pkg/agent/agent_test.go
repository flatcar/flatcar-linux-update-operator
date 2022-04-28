package agent_test

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	policyv1beta1 "k8s.io/api/policy/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes/fake"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/klog/v2"

	"github.com/flatcar-linux/flatcar-linux-update-operator/pkg/agent"
	"github.com/flatcar-linux/flatcar-linux-update-operator/pkg/constants"
	"github.com/flatcar-linux/flatcar-linux-update-operator/pkg/updateengine"
)

const (
	agentRunTimeLimit     = 15 * time.Second
	agentShutdownLimit    = 10 * time.Second
	errorResponseThrottle = 100 * time.Millisecond
)

//nolint:funlen,cyclop,gocognit // Just many test cases.
func Test_Creating_new_agent(t *testing.T) {
	t.Parallel()

	t.Run("succeeds_when_all_dependencies_are_satisfied", func(t *testing.T) {
		t.Parallel()

		validConfig, _, _ := validTestConfig(t, testNode())

		client, err := agent.New(validConfig)
		if err != nil {
			t.Fatalf("Unexpected error creating new agent: %v", err)
		}

		if client == nil {
			t.Fatalf("Client should be returned when creating agent succeeds")
		}
	})

	t.Run("use_default_poll_interval_when_none_is_configured", func(t *testing.T) {
		t.Parallel()

		testConfig, _, fakeClient := validTestConfig(t, testNode())
		testConfig.PollInterval = 0

		getErrorCalls := make(chan bool, 2)
		firstCallMutex := &sync.Mutex{}
		firstCall := true

		fakeClient.PrependReactor("update", "nodes", func(action k8stesting.Action) (bool, runtime.Object, error) {
			node := updateActionToNode(t, action)

			if node.Annotations[constants.AnnotationRebootNeeded] == constants.True {
				firstCallMutex.Lock()
				getErrorCalls <- firstCall

				if firstCall {
					firstCall = false
				}
				firstCallMutex.Unlock()

				return true, nil, fmt.Errorf(t.Name())
			}

			return false, nil, nil
		})

		done := runAgent(contextWithTimeout(t, agentRunTimeLimit), t, testConfig)

		for {
			select {
			case <-contextWithTimeout(t, time.Second).Done():
				firstCallMutex.Lock()
				if firstCall {
					t.Fatalf("Expected to observe at least one update call")
				}

				return
			case <-done:
				t.Fatalf("Agent exited prematurely")
			case firstCall := <-getErrorCalls:
				if !firstCall {
					t.Fatalf("Got unexpected premature attempt to update node object")
				}
			}
		}
	})

	t.Run("use_default_max_operator_response_time_time_when_none_is_configured", func(t *testing.T) {
		t.Parallel()

		testConfig, _, _ := validTestConfig(t, okToRebootNode())

		done := runAgent(contextWithTimeout(t, agentRunTimeLimit), t, testConfig)

		select {
		case <-contextWithTimeout(t, time.Second).Done():
		case err := <-done:
			if err == nil {
				t.Fatalf("Expected to get agent runtime error")
			}
		}
	})

	t.Run("fails_when", func(t *testing.T) {
		t.Parallel()

		cases := map[string]func(*agent.Config){
			"no_clientset_is_configured":       func(c *agent.Config) { c.Clientset = nil },
			"no_status_receiver_is_configured": func(c *agent.Config) { c.StatusReceiver = nil },
			"no_rebooter_is_configured":        func(c *agent.Config) { c.Rebooter = nil },
			"empty_node_name_is_given":         func(c *agent.Config) { c.NodeName = "" },
		}

		for n, mutateConfigF := range cases {
			mutateConfigF := mutateConfigF

			t.Run(n, func(t *testing.T) {
				t.Parallel()

				testConfig, _, _ := validTestConfig(t, testNode())
				mutateConfigF(testConfig)

				client, err := agent.New(testConfig)
				if err == nil {
					t.Fatalf("Expected error creating new agent")
				}

				if client != nil {
					t.Fatalf("No client should be returned when New failed")
				}
			})
		}
	})
}

//nolint:funlen,cyclop,gocognit,gocyclo,maintidx // Just many test cases.
func Test_Running_agent(t *testing.T) {
	t.Parallel()

	t.Run("reads_host_configuration_by", func(t *testing.T) {
		t.Parallel()

		testConfig, _, _ := validTestConfig(t, testNode())

		expectedGroup := "configuredGroup"
		expectedOSID := "testID"
		expectedVersion := "testVersion"

		files := map[string]string{
			"/usr/share/flatcar/update.conf": "GROUP=" + expectedGroup,
			"/etc/os-release":                fmt.Sprintf("ID=%s\nVERSION=%s", expectedOSID, expectedVersion),
		}

		createTestFiles(t, files, testConfig.HostFilesPrefix)

		ctx := contextWithTimeout(t, agentRunTimeLimit)

		done := runAgent(ctx, t, testConfig)

		t.Run("reading_OS_ID_from_etc_os_release_file", func(t *testing.T) {
			t.Parallel()

			// This is currently the only way to check that agent has read /etc/os-release file.
			assertNodeProperty(ctx, t, &assertNodePropertyContext{
				done:   done,
				config: testConfig,
				testF:  assertNodeLabelValue(constants.LabelID, expectedOSID),
			})
		})

		t.Run("reading_Flatcar_version_from_etc_os_release_file", func(t *testing.T) {
			t.Parallel()

			// This is currently the only way to check that agent has read /etc/os-release file.
			assertNodeProperty(ctx, t, &assertNodePropertyContext{
				done:   done,
				config: testConfig,
				testF:  assertNodeLabelValue(constants.LabelVersion, expectedVersion),
			})
		})

		t.Run("reading_Flatcar_group_from_update_configuration_file_in_usr_directory", func(t *testing.T) {
			t.Parallel()

			// This is currently the only way to check that agent
			// read /etc/flatcar/update.conf or /usr/share/flatcar/update.conf.
			assertNodeProperty(ctx, t, &assertNodePropertyContext{
				done:   done,
				config: testConfig,
				testF:  assertNodeLabelValue(constants.LabelGroup, expectedGroup),
			})
		})
	})

	t.Run("prefers_Flatcar_group_from_etc_over_usr", func(t *testing.T) {
		t.Parallel()

		testConfig, _, _ := validTestConfig(t, testNode())

		expectedGroup := "configuredGroup"

		files := map[string]string{
			"/etc/flatcar/update.conf": "GROUP=" + expectedGroup,
		}

		createTestFiles(t, files, testConfig.HostFilesPrefix)

		ctx := contextWithTimeout(t, agentRunTimeLimit)

		assertNodeProperty(ctx, t, &assertNodePropertyContext{
			done:   runAgent(ctx, t, testConfig),
			config: testConfig,
			testF:  assertNodeLabelValue(constants.LabelGroup, expectedGroup),
		})
	})

	t.Run("ignores_when_etc_update_configuration_file_does_not_exist", func(t *testing.T) {
		t.Parallel()

		testConfig, _, _ := validTestConfig(t, testNode())

		expectedGroup := "imageGroup"

		files := map[string]string{
			"/usr/lib/flatcar/update.conf": "GROUP=" + expectedGroup,
		}

		createTestFiles(t, files, testConfig.HostFilesPrefix)

		updateConfigFile := filepath.Join(testConfig.HostFilesPrefix, "/etc/flatcar/update.conf")

		if err := os.Remove(updateConfigFile); err != nil {
			t.Fatalf("Failed removing test file: %v", err)
		}

		ctx := contextWithTimeout(t, agentRunTimeLimit)

		assertNodeProperty(ctx, t, &assertNodePropertyContext{
			done:   runAgent(ctx, t, testConfig),
			config: testConfig,
			testF:  assertNodeLabelValue(constants.LabelGroup, expectedGroup),
		})
	})

	t.Run("on_start", func(t *testing.T) {
		t.Parallel()

		t.Run("updates_associated_Node_object_with_host_information_by", func(t *testing.T) {
			t.Parallel()

			testConfig, _, _ := validTestConfig(t, testNode())

			ctx := contextWithTimeout(t, agentRunTimeLimit)

			done := runAgent(ctx, t, testConfig)

			t.Run("setting_OS_ID_label", func(t *testing.T) {
				t.Parallel()

				assertNodeProperty(ctx, t, &assertNodePropertyContext{
					done:   done,
					config: testConfig,
					testF:  assertNodeLabelExists(constants.LabelID),
				})
			})

			t.Run("setting_Flatcar_group_label", func(t *testing.T) {
				t.Parallel()

				assertNodeProperty(ctx, t, &assertNodePropertyContext{
					done:   done,
					config: testConfig,
					testF:  assertNodeLabelExists(constants.LabelGroup),
				})
			})

			t.Run("setting_Flatcar_version_label", func(t *testing.T) {
				t.Parallel()

				assertNodeProperty(ctx, t, &assertNodePropertyContext{
					done:   done,
					config: testConfig,
					testF:  assertNodeLabelExists(constants.LabelVersion),
				})
			})
		})

		t.Run("resets_reboot_state_indicators_to_default_values_by", func(t *testing.T) {
			t.Parallel()

			testConfig, _, _ := validTestConfig(t, testNode())
			testConfig.StatusReceiver = &mockStatusReceiver{}

			ctx := contextWithTimeout(t, agentRunTimeLimit)

			done := runAgent(ctx, t, testConfig)

			t.Run("setting_reboot_in_progress_annotation_to_false", func(t *testing.T) {
				t.Parallel()

				assertNodeProperty(ctx, t, &assertNodePropertyContext{
					done:   done,
					config: testConfig,
					testF:  assertNodeAnnotationValue(constants.AnnotationRebootInProgress, constants.False),
				})
			})

			t.Run("setting_reboot_needed_annotation_to_false", func(t *testing.T) {
				t.Parallel()

				assertNodeProperty(ctx, t, &assertNodePropertyContext{
					done:   done,
					config: testConfig,
					testF:  assertNodeAnnotationValue(constants.AnnotationRebootNeeded, constants.False),
				})
			})

			t.Run("setting_reboot_needed_label_to_false", func(t *testing.T) {
				t.Parallel()

				assertNodeProperty(ctx, t, &assertNodePropertyContext{
					done:   done,
					config: testConfig,
					testF:  assertNodeLabelValue(constants.LabelRebootNeeded, constants.False),
				})
			})
		})
	})

	t.Run("waits_for_not_ok_to_reboot_annotation_from_operator_after_updating_node_information", func(t *testing.T) {
		t.Parallel()

		expectStatusPoll := false
		statusReceived := make(chan bool)

		testConfig, node, _ := validTestConfig(t, okToRebootNode())
		testConfig.StatusReceiver = &mockStatusReceiver{
			receiveStatusesF: func(chan<- updateengine.Status, <-chan struct{}) {
				statusReceived <- expectStatusPoll
			},
		}

		ctx := contextWithTimeout(t, time.Second)

		// Ensure we enter wait loop before setting not ok to reboot.
		assertNodeProperty(ctx, t, &assertNodePropertyContext{
			done:   runAgent(ctx, t, testConfig),
			config: testConfig,
			testF:  assertNodeLabelValue(constants.LabelRebootNeeded, constants.False),
		})

		expectStatusPoll = true

		notOkToReboot(ctx, t, testConfig.Clientset.CoreV1().Nodes(), node.Name)

		select {
		case <-ctx.Done():
			t.Fatal("Timed out waiting for receive statuses call")
		case expectedStatusPoll := <-statusReceived:
			if !expectedStatusPoll {
				t.Fatalf("Observed unexpected status poll")
			}
		}
	})

	t.Run("marks_node_as_schedulable_if_agent_made_it_unschedulable", func(t *testing.T) {
		t.Parallel()

		expectNodeSchedulableUpdateMutex := &sync.Mutex{}
		expectNodeSchedulableUpdate := false
		nodeUpdatedAsSchedulable := make(chan bool, 1)

		testConfig, node, fakeClient := validTestConfig(t, nodeMadeUnschedulable())

		fakeClient.PrependReactor("update", "nodes", func(action k8stesting.Action) (bool, runtime.Object, error) {
			node := updateActionToNode(t, action)

			if !node.Spec.Unschedulable {
				expectNodeSchedulableUpdateMutex.Lock()
				nodeUpdatedAsSchedulable <- expectNodeSchedulableUpdate
				expectNodeSchedulableUpdateMutex.Unlock()
			}

			return false, nil, nil
		})

		ctx := contextWithTimeout(t, agentRunTimeLimit)

		// Ensure we enter wait loop before setting not ok to reboot.
		assertNodeProperty(ctx, t, &assertNodePropertyContext{
			done:   runAgent(ctx, t, testConfig),
			config: testConfig,
			testF:  assertNodeLabelValue(constants.LabelRebootNeeded, constants.False),
		})

		notOkToReboot(ctx, t, testConfig.Clientset.CoreV1().Nodes(), node.Name)

		expectNodeSchedulableUpdateMutex.Lock()
		expectNodeSchedulableUpdate = true
		expectNodeSchedulableUpdateMutex.Unlock()

		select {
		case <-ctx.Done():
			t.Fatal("Timed out waiting for node being marked as schedulable")
		case expected := <-nodeUpdatedAsSchedulable:
			if !expected {
				t.Fatalf("Node was updated as schedulable before it was expected")
			}
		}
	})

	t.Run("leaves_node_unschedulable_if_it_was_made_unschedulable_by_external_source", func(t *testing.T) {
		t.Parallel()

		nodeUnschedulableByExternalSource := nodeMadeUnschedulable()
		nodeUnschedulableByExternalSource.Annotations[constants.AnnotationAgentMadeUnschedulable] = constants.False

		testConfig, node, fakeClient := validTestConfig(t, nodeUnschedulableByExternalSource)

		watchStatusStarted := make(chan struct{})

		testConfig.StatusReceiver = &mockStatusReceiver{
			receiveStatusesF: func(ch chan<- updateengine.Status, _ <-chan struct{}) {
				watchStatusStarted <- struct{}{}
			},
		}

		nodeUnschedulableUpdate := make(chan struct{})

		fakeClient.PrependReactor("update", "nodes", func(action k8stesting.Action) (bool, runtime.Object, error) {
			node := updateActionToNode(t, action)

			if !node.Spec.Unschedulable {
				nodeUnschedulableUpdate <- struct{}{}
			}

			return false, nil, nil
		})

		ctx := contextWithTimeout(t, agentRunTimeLimit)

		// Ensure we enter wait loop before setting not ok to reboot.
		assertNodeProperty(ctx, t, &assertNodePropertyContext{
			done:   runAgent(ctx, t, testConfig),
			config: testConfig,
			testF:  assertNodeLabelValue(constants.LabelRebootNeeded, constants.False),
		})

		notOkToReboot(ctx, t, testConfig.Clientset.CoreV1().Nodes(), node.Name)

		select {
		case <-ctx.Done():
			t.Fatal("Timed out waiting for node being marked as schedulable")
		case <-nodeUnschedulableUpdate:
			t.Fatalf("Unexpected node update as schedulable")
		case <-watchStatusStarted:
		}
	})

	t.Run("after_getting_not_ok_to_reboot_annotation", func(t *testing.T) {
		t.Parallel()

		testConfig, node, _ := validTestConfig(t, testNode())

		ctx := contextWithTimeout(t, agentRunTimeLimit)

		done := runAgent(ctx, t, testConfig)

		notOkToReboot(ctx, t, testConfig.Clientset.CoreV1().Nodes(), node.Name)

		t.Run("updates_node_information_when_update_enging_produces_updated_status", func(t *testing.T) {
			t.Parallel()

			assertNodeProperty(ctx, t, &assertNodePropertyContext{
				done:   done,
				config: testConfig,
				testF:  assertNodeLabelValue(constants.LabelRebootNeeded, constants.True),
			})
		})

		t.Run("waits_for_ok_to_reboot_annotation_from_operator", func(t *testing.T) {
			t.Parallel()

			assertNodeProperty(ctx, t, &assertNodePropertyContext{
				done:   done,
				config: testConfig,
				testF:  assertNodeLabelValue(constants.LabelRebootNeeded, constants.True),
			})

			okToReboot(ctx, t, testConfig.Clientset.CoreV1().Nodes(), node.Name)

			assertNodeProperty(ctx, t, &assertNodePropertyContext{
				done:   done,
				config: testConfig,
				testF:  assertNodeAnnotationValue(constants.AnnotationAgentMadeUnschedulable, constants.True),
			})
		})
	})

	t.Run("retries_updating_node_status_from_update_engine_until_it_succeeds", func(t *testing.T) {
		t.Parallel()

		testConfig, _, fakeClient := validTestConfig(t, testNode())

		firstCall := true

		fakeClient.PrependReactor("update", "nodes", func(action k8stesting.Action) (bool, runtime.Object, error) {
			node := updateActionToNode(t, action)

			if _, ok := node.Annotations[constants.AnnotationStatus]; ok {
				if firstCall {
					firstCall = false

					return true, nil, fmt.Errorf(t.Name())
				}
			}

			return false, nil, nil
		})

		ctx := contextWithTimeout(t, agentRunTimeLimit)

		assertNodeProperty(ctx, t, &assertNodePropertyContext{
			done:   runAgent(ctx, t, testConfig),
			config: testConfig,
			testF:  assertNodeAnnotationValue(constants.AnnotationStatus, updateengine.UpdateStatusUpdatedNeedReboot),
		})
	})

	t.Run("skips_node_status_update_when_update_engine_status_did_not_change", func(t *testing.T) {
		t.Parallel()

		testConfig, node, fakeClient := validTestConfig(t, testNode())

		status := updateengine.Status{
			CurrentOperation: updateengine.UpdateStatusUpdatedNeedReboot,
		}

		testConfig.StatusReceiver = &mockStatusReceiver{
			receiveStatusesF: func(ch chan<- updateengine.Status, _ <-chan struct{}) {
				status.NewVersion = "foo"
				ch <- status
				status.NewVersion = "bar"
				ch <- status
			},
		}

		newVersionReported := make(chan string, 2)

		fakeClient.PrependReactor("update", "nodes", func(action k8stesting.Action) (bool, runtime.Object, error) {
			node := updateActionToNode(t, action)

			if _, ok := node.Annotations[constants.AnnotationStatus]; ok {
				newVersionReported <- node.Annotations[constants.AnnotationNewVersion]
			}

			return false, nil, nil
		})

		ctx := contextWithTimeout(t, agentRunTimeLimit)

		runAgent(ctx, t, testConfig)

		notOkToReboot(ctx, t, testConfig.Clientset.CoreV1().Nodes(), node.Name)

		lastNewVersion := ""

		select {
		case <-ctx.Done():
			t.Fatalf("Timed out waiting for first node status update")
		case newVersion := <-newVersionReported:
			lastNewVersion = newVersion
		}

		select {
		case newVersion := <-newVersionReported:
			if newVersion != lastNewVersion {
				t.Fatalf("Unexpected node status update")
			}
		case <-contextWithTimeout(t, 500*time.Millisecond).Done():
		}
	})

	t.Run("after_getting_ok_to_reboot_annotation", func(t *testing.T) {
		t.Parallel()

		t.Run("marks_node_as_unschedulable_if_not_done_before_by", func(t *testing.T) {
			t.Parallel()

			testConfig, node, fakeClient := validTestConfig(t, testNode())

			nodeUpdatedAsUnschedulable := notifyOnNodeUnschedulableUpdate(t, fakeClient)

			ctx := contextWithTimeout(t, agentRunTimeLimit)

			done := runAgent(ctx, t, testConfig)

			assertNodeProperty(ctx, t, &assertNodePropertyContext{
				done:   done,
				config: testConfig,
				testF:  assertNodeAnnotationValue(constants.AnnotationRebootNeeded, constants.True),
			})

			okToReboot(ctx, t, testConfig.Clientset.CoreV1().Nodes(), node.Name)

			t.Run("setting_unschedulable_field_on_Node_object", func(t *testing.T) {
				t.Parallel()

				select {
				case <-ctx.Done():
					t.Fatal("Timed out waiting for node being marked as unschedulable")
				case <-nodeUpdatedAsUnschedulable:
				}
			})

			t.Run("setting_reboot_in_progress_annotation_to_true", func(t *testing.T) {
				t.Parallel()

				assertNodeProperty(ctx, t, &assertNodePropertyContext{
					done:   done,
					config: testConfig,
					testF:  assertNodeAnnotationValue(constants.AnnotationRebootInProgress, constants.True),
				})
			})

			t.Run("setting_agent_made_unschedulable_annotations_to_true", func(t *testing.T) {
				t.Parallel()

				assertNodeProperty(ctx, t, &assertNodePropertyContext{
					done:   done,
					config: testConfig,
					testF:  assertNodeAnnotationValue(constants.AnnotationAgentMadeUnschedulable, constants.True),
				})
			})
		})
	})

	t.Run("skips_marking_node_as_unschedulable_if_node_is_already_unschedulable", func(t *testing.T) {
		t.Parallel()

		nodeUpdatedAsUnschedulable := make(chan struct{})
		podsListed := make(chan struct{})

		nodeUnschedulable := testNode()
		nodeUnschedulable.Spec.Unschedulable = true

		testConfig, _, fakeClient := validTestConfig(t, nodeUnschedulable)

		fakeClient.PrependReactor("update", "nodes", func(action k8stesting.Action) (bool, runtime.Object, error) {
			node := updateActionToNode(t, action)

			if _, ok := node.Annotations[constants.AnnotationAgentMadeUnschedulable]; ok {
				nodeUpdatedAsUnschedulable <- struct{}{}
			}

			return false, nil, nil
		})

		fakeClient.PrependReactor("list", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
			podsListed <- struct{}{}

			return false, nil, nil
		})

		ctx := contextWithTimeout(t, agentRunTimeLimit)

		assertNodeProperty(ctx, t, &assertNodePropertyContext{
			done:   runAgent(ctx, t, testConfig),
			config: testConfig,
			testF:  assertNodeAnnotationValue(constants.AnnotationRebootNeeded, constants.True),
		})

		okToReboot(ctx, t, testConfig.Clientset.CoreV1().Nodes(), nodeUnschedulable.Name)

		select {
		case <-ctx.Done():
			t.Fatalf("Timed out waiting for pods listing operation")
		case <-nodeUpdatedAsUnschedulable:
			t.Fatalf("Expected node to not be marked as unschedulable")
		case <-podsListed:
		}
	})

	t.Run("after_marking_node_as_unschedulable", func(t *testing.T) {
		t.Parallel()

		t.Run("without_exceeding_pod_deletion_grace_period", func(t *testing.T) {
			t.Parallel()

			expectedPodsRemovedNames := map[string]struct{}{"pod-to-be-removed": {}}
			controllerTrue := true
			podsToCreate := []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pod-to-be-removed",
						Namespace: "default",
						OwnerReferences: []metav1.OwnerReference{
							{
								Name:       "fake-owner",
								Controller: &controllerTrue,
							},
						},
					},
					Spec: corev1.PodSpec{
						NodeName: testNode().Name,
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pod-filtered-from-removal-because-of-namespace",
						Namespace: "kube-system",
						OwnerReferences: []metav1.OwnerReference{
							{
								Name:       "fake-owner",
								Controller: &controllerTrue,
							},
						},
					},
					Spec: corev1.PodSpec{
						NodeName: testNode().Name,
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pod-on-another-node",
						Namespace: "another",
						OwnerReferences: []metav1.OwnerReference{
							{
								Name:       "fake-owner",
								Controller: &controllerTrue,
							},
						},
					},
					Spec: corev1.PodSpec{
						NodeName: "baz",
					},
				},
			}

			fakeClient := fake.NewSimpleClientset(podsToCreate[0], podsToCreate[1], podsToCreate[2], testNode())
			addEvictionSupport(t, fakeClient, "v1")

			testConfig, node, _ := validTestConfig(t, testNode())
			testConfig.Clientset = fakeClient
			testConfig.PodDeletionGracePeriod = time.Hour

			expectedPodRemovedMutex := &sync.Mutex{}
			expectedPodRemoved := len(expectedPodsRemovedNames)
			rebootTriggerred := make(chan bool, 1)

			testConfig.Rebooter = &mockRebooter{
				rebootF: func(auth bool) {
					expectedPodRemovedMutex.Lock()
					rebootTriggerred <- expectedPodRemoved < 0
					expectedPodRemovedMutex.Unlock()
				},
			}

			nodeUpdatedAsUnschedulable := notifyOnNodeUnschedulableUpdate(t, &fakeClient.Fake)

			fakeClient.PrependReactor("list", "pods", listPodsWithFieldSelector(podsToCreate))

			allExpectedPodsScheduledForRemoval := make(chan struct{}, 2)

			fakeClient.PrependReactor("delete", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
				deleteAction, ok := action.(k8stesting.DeleteActionImpl)
				if !ok {
					del := k8stesting.DeleteActionImpl{}

					return true, nil, fmt.Errorf("unexpected action, expected %T, got %T", del, action)
				}

				if _, ok := expectedPodsRemovedNames[deleteAction.Name]; !ok {
					t.Fatalf("Unexpected pod %q removed", deleteAction.Name)
				}

				expectedPodRemovedMutex.Lock()
				defer expectedPodRemovedMutex.Unlock()
				expectedPodRemoved--

				defer func(i int) {
					if i == 0 {
						allExpectedPodsScheduledForRemoval <- struct{}{}
						allExpectedPodsScheduledForRemoval <- struct{}{}
					}
				}(expectedPodRemoved)

				// After removal attempt of all pods which are expected to be removed, allow real removal by test code.
				return expectedPodRemoved >= 0, nil, nil
			})

			fakeClient.PrependReactor(
				"create",
				"pods/eviction",
				func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
					createAction, ok := action.(k8stesting.CreateActionImpl)
					if !ok {
						del := k8stesting.CreateActionImpl{}

						return true, nil, fmt.Errorf("unexpected action, expected %T, got %T", del, action)
					}

					if eviction, ok := createAction.Object.(*policyv1.Eviction); !ok {
						if _, ok := expectedPodsRemovedNames[eviction.GetObjectMeta().GetName()]; !ok {
							t.Fatalf("Unexpected eviction for %q created", createAction.Name)
						}
					}

					expectedPodRemovedMutex.Lock()
					defer expectedPodRemovedMutex.Unlock()
					expectedPodRemoved--

					defer func(i int) {
						if i == 0 {
							allExpectedPodsScheduledForRemoval <- struct{}{}
							allExpectedPodsScheduledForRemoval <- struct{}{}
						}
					}(expectedPodRemoved)

					// After removal attempt of all pods which are expected to be removed, allow real removal by test code.
					return expectedPodRemoved >= 0, nil, nil
				})

			expectedGetPodCalls := 1
			podGetRequest := make(chan struct{}, expectedGetPodCalls)

			fakeClient.PrependReactor("get", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
				if len(podGetRequest) < expectedGetPodCalls {
					podGetRequest <- struct{}{}
				}

				return false, nil, nil
			})

			ctx := contextWithTimeout(t, agentRunTimeLimit)

			assertNodeProperty(ctx, t, &assertNodePropertyContext{
				done:   runAgent(ctx, t, testConfig),
				config: testConfig,
				testF:  assertNodeAnnotationValue(constants.AnnotationRebootNeeded, constants.True),
			})

			okToReboot(ctx, t, testConfig.Clientset.CoreV1().Nodes(), node.Name)

			select {
			case <-ctx.Done():
				t.Fatal("Timed out waiting for node being marked as unschedulable")
			case <-nodeUpdatedAsUnschedulable:
			}

			t.Run("remove_pods_scheduled_on_running_node_without_pods_from_kube-system_namespace", func(t *testing.T) {
				t.Parallel()

				select {
				case <-ctx.Done():
					t.Fatal("Timed out waiting for expected number of pods to be removed")
				case <-allExpectedPodsScheduledForRemoval:
				}
			})

			t.Run("waits_for_removed_pods_to_terminate_before_rebooting", func(t *testing.T) {
				t.Parallel()

				select {
				case <-ctx.Done():
					t.Fatalf("Timed out waiting for all pods to be scheduled for removal")
				case <-allExpectedPodsScheduledForRemoval:
				}

				select {
				case <-ctx.Done():
					t.Fatalf("Timed out waiting pod GET request")
				case <-podGetRequest:
				}

				client := testConfig.Clientset.CoreV1().Pods(podsToCreate[0].Namespace)

				if err := client.Delete(ctx, podsToCreate[0].Name, metav1.DeleteOptions{}); err != nil {
					t.Fatalf("Failed removing test pod: %v", err)
				}

				select {
				case <-ctx.Done():
					t.Fatalf("Timed out waiting for reboot to be triggered")
				case expected := <-rebootTriggerred:
					if !expected {
						t.Fatalf("Got unexpected reboot call")
					}
				}
			})
		})

		t.Run("ignores_pods_which_has_not_terminated_when_pod_deletion_grace_period_is_reached", func(t *testing.T) {
			t.Parallel()

			rebootTriggerred := make(chan bool)

			controllerTrue := true
			podsToCreate := []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "foo",
						Namespace: "default",
						OwnerReferences: []metav1.OwnerReference{
							{
								Name:       "fake-owner",
								Controller: &controllerTrue,
							},
						},
					},
					Spec: corev1.PodSpec{
						NodeName: testNode().Name,
					},
				},
			}

			fakeClient := fake.NewSimpleClientset(podsToCreate[0], testNode())
			addEvictionSupport(t, fakeClient, "v1")

			testConfig, node, _ := validTestConfig(t, testNode())
			testConfig.Clientset = fakeClient
			testConfig.Rebooter = &mockRebooter{
				rebootF: func(auth bool) {
					rebootTriggerred <- auth
				},
			}

			fakeClient.PrependReactor("create", "pods/eviction", func(action k8stesting.Action) (bool, runtime.Object, error) {
				return true, nil, nil
			})

			ctx := contextWithTimeout(t, agentRunTimeLimit)

			assertNodeProperty(ctx, t, &assertNodePropertyContext{
				done:   runAgent(ctx, t, testConfig),
				config: testConfig,
				testF:  assertNodeAnnotationValue(constants.AnnotationRebootNeeded, constants.True),
			})

			okToReboot(ctx, t, testConfig.Clientset.CoreV1().Nodes(), node.Name)

			select {
			case <-ctx.Done():
				t.Fatal("Timed out waiting for reboot to be triggered")
			case <-rebootTriggerred:
			}
		})
	})

	t.Run("after_draining_node", func(t *testing.T) {
		t.Parallel()

		rebootTriggerred := make(chan bool, 1)

		ctx, cancel := context.WithCancel(contextWithTimeout(t, agentRunTimeLimit))

		testConfig, node, fakeClient := validTestConfig(t, testNode())
		testConfig.Rebooter = &mockRebooter{
			rebootF: func(auth bool) {
				rebootTriggerred <- auth
				cancel()
			},
		}

		nodeUpdatedAsUnschedulable := notifyOnNodeUnschedulableUpdate(t, fakeClient)

		done := runAgent(ctx, t, testConfig)

		assertNodeProperty(ctx, t, &assertNodePropertyContext{
			done:   done,
			config: testConfig,
			testF:  assertNodeAnnotationValue(constants.AnnotationRebootNeeded, constants.True),
		})

		okToReboot(ctx, t, testConfig.Clientset.CoreV1().Nodes(), node.Name)

		select {
		case <-ctx.Done():
			t.Fatal("Timed out waiting for node being marked as unschedulable")
		case <-nodeUpdatedAsUnschedulable:
		}

		t.Run("triggers_a_reboot_without_interactive_authentication", func(t *testing.T) {
			t.Parallel()

			select {
			case <-contextWithTimeout(t, 5*time.Second).Done():
				t.Fatalf("Timed out waiting for reboot to be triggered")
			case interactiveAuthRequested := <-rebootTriggerred:
				if interactiveAuthRequested {
					t.Fatalf("Got reboot call with interactive auth requested")
				}
			}
		})

		t.Run("waits_until_termination_signal_comes", func(t *testing.T) {
			t.Parallel()

			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("Got agent running error: %v", err)
				}
			case <-contextWithTimeout(t, 30*time.Second).Done():
				t.Fatalf("Timed out waiting for agent to exit gracefully after receiving exit signal")
			}
		})
	})

	t.Run("logs_error_but_continues_operating_when", func(t *testing.T) {
		t.Parallel()

		// TODO: Those are not hard errors, we should probably test that the tests are logged at least.
		// Alternatively we can test that those errors do not cause agent to exit.
		t.Run("waiting_for_ok_to_reboot_annotation_fails_by", func(t *testing.T) {
			t.Parallel()

			t.Run("getting_node_object_error", func(t *testing.T) {
				t.Parallel()

				testConfig, _, fakeClient := validTestConfig(t, testNode())

				errorReached, failOnSettingNodeAnnotations := failOnNthCall(4, fmt.Errorf(t.Name()))
				// 1. Updating info labels. TODO: Could be done with patch instead.
				// 2. Checking made unschedulable.
				// 3. Updating annotations and labels.
				// 4. Get initial set of annotations.
				fakeClient.PrependReactor("get", "nodes", failOnSettingNodeAnnotations)

				ctx, cancel := context.WithTimeout(contextWithDeadline(t), agentRunTimeLimit)

				done := runAgent(ctx, t, testConfig)

				select {
				case <-ctx.Done():
					t.Fatalf("Timed out waiting for failing get node call")
				case <-errorReached:
				}

				cancel()

				select {
				case <-contextWithTimeout(t, agentShutdownLimit).Done():
					t.Fatalf("Timed out waiting for node to stop")
				case err := <-done:
					if err != nil {
						t.Fatalf("Expected agent to shut down gracefully after waiting for OK error, got: %v", err)
					}
				}
			})

			t.Run("creating_watcher_error", func(t *testing.T) {
				t.Parallel()

				testConfig, _, fakeClient := validTestConfig(t, testNode())

				failingWatcherCreation := make(chan struct{}, 1)

				failOnWatchCreation := func(action k8stesting.Action) (handled bool, ret watch.Interface, err error) {
					if len(failingWatcherCreation) == 0 {
						failingWatcherCreation <- struct{}{}
					}

					// Agent may currently send requests without a backoff, so slow it down a bit to avoid exceesive logging,
					// which may make tests more flaky when there is not enough CPU available.
					//
					// TODO: This should not be needed once we implement backoff.
					time.Sleep(errorResponseThrottle)

					return true, nil, fmt.Errorf(t.Name())
				}

				fakeClient.PrependWatchReactor("*", failOnWatchCreation)

				ctx, cancel := context.WithCancel(contextWithTimeout(t, time.Second))

				done := runAgent(contextWithTimeout(t, time.Second), t, testConfig)

				select {
				case <-ctx.Done():
					t.Fatalf("Timed out waiting for watch creation call")
				case <-failingWatcherCreation:
				}

				cancel()

				select {
				case <-contextWithTimeout(t, agentShutdownLimit).Done():
					t.Fatalf("Timed out waiting for agent shutdown")
				case err := <-done:
					if err != nil {
						t.Fatalf("Expected agent to shut down gracefully after waiting for OK error, got: %v", err)
					}
				}
			})

			t.Run("watching_error", func(t *testing.T) {
				t.Parallel()

				testConfig, _, fakeClient := validTestConfig(t, testNode())

				watcher := watch.NewFakeWithChanSize(1, true)
				watcher.Error(nil)

				fakeClient.PrependWatchReactor("nodes", func(action k8stesting.Action) (bool, watch.Interface, error) {
					// Agent may currently send requests without a backoff, so slow it down a bit to avoid exceesive logging,
					// which may make tests more flaky when there is not enough CPU available.
					//
					// TODO: This should not be needed once we implement backoff.
					time.Sleep(errorResponseThrottle)

					return true, watcher, nil
				})

				if err := <-runAgent(contextWithTimeout(t, time.Second), t, testConfig); err != nil {
					t.Fatalf("Expected agent to shut down gracefully after waiting for OK error, got: %v", err)
				}
			})

			t.Run("max_operator_response_time_is_exceeded", func(t *testing.T) {
				t.Parallel()

				testConfig, _, _ := validTestConfig(t, testNode())
				testConfig.MaxOperatorResponseTime = 500 * time.Millisecond

				if err := <-runAgent(contextWithTimeout(t, time.Second), t, testConfig); err != nil {
					t.Fatalf("Expected agent to shut down gracefully after waiting for OK error, got: %v", err)
				}
			})
		})

		for testName, verb := range map[string]string{
			"removing_pod_on_node_fails":                       "delete",
			"getting_pods_while_waiting_for_termination_fails": "get",
		} {
			verb := verb

			t.Run(testName, func(t *testing.T) {
				t.Parallel()

				podsToCreate := []*corev1.Pod{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "foo",
							Namespace: "default",
						},
						Spec: corev1.PodSpec{
							NodeName: testNode().Name,
						},
					},
				}

				fakeClient := fake.NewSimpleClientset(podsToCreate[0], testNode())

				testConfig, node, _ := validTestConfig(t, testNode())
				testConfig.Clientset = fakeClient

				fakeClient.PrependReactor(verb, "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
					return true, nil, fmt.Errorf(t.Name())
				})

				ctx := contextWithTimeout(t, time.Second)

				done := runAgent(ctx, t, testConfig)

				assertNodeProperty(ctx, t, &assertNodePropertyContext{
					done:   done,
					config: testConfig,
					testF:  assertNodeAnnotationValue(constants.AnnotationRebootNeeded, constants.True),
				})

				okToReboot(ctx, t, testConfig.Clientset.CoreV1().Nodes(), node.Name)

				<-done
			})
		}
	})

	t.Run("stops_gracefully_when_shutdown_is_requested_and_agent_is", func(t *testing.T) {
		t.Parallel()

		t.Run("waiting_for_ok_to_reboot", func(t *testing.T) {
			t.Parallel()

			testConfig, _, _ := validTestConfig(t, testNode())

			ctx := contextWithTimeout(t, 500*time.Millisecond)

			if err := <-runAgent(ctx, t, testConfig); err != nil {
				t.Fatalf("Expected agent to shut down gracefully after waiting for OK error, got: %v", err)
			}
		})
	})

	t.Run("stops_with_error_when", func(t *testing.T) {
		t.Parallel()

		t.Run("configured_Node_does_not_exist", func(t *testing.T) {
			t.Parallel()

			configWithNoNodeObject, _, _ := validTestConfig(t, testNode())
			configWithNoNodeObject.Clientset = fake.NewSimpleClientset()

			err := getAgentRunningError(t, configWithNoNodeObject)
			if !apierrors.IsNotFound(err) {
				t.Fatalf("Expected Node not found error when running agent, got: %v", err)
			}
		})

		t.Run("reading_OS_information_fails_because", func(t *testing.T) {
			t.Parallel()

			t.Run("usr_update_config_file_is_not_available", func(t *testing.T) {
				t.Parallel()

				testConfig, _, _ := validTestConfig(t, testNode())

				usrUpdateConfigFile := filepath.Join(testConfig.HostFilesPrefix, "/usr/share/flatcar/update.conf")

				if err := os.Remove(usrUpdateConfigFile); err != nil {
					t.Fatalf("Failed removing test config file: %v", err)
				}

				err := getAgentRunningError(t, testConfig)
				if !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("Expected file not found error, got: %v", err)
				}
			})

			t.Run("etc_update_config_file_is_not_readable", func(t *testing.T) {
				t.Parallel()

				if os.Getuid() <= 0 {
					t.Skip("This test will produce false positive result when running as root")
				}

				testConfig, _, _ := validTestConfig(t, testNode())

				updateConfigFile := filepath.Join(testConfig.HostFilesPrefix, "/etc/flatcar/update.conf")

				if err := os.Chmod(updateConfigFile, 0o000); err != nil {
					t.Fatalf("Failed changing mode for test file: %v", err)
				}

				err := getAgentRunningError(t, testConfig)
				if !errors.Is(err, os.ErrPermission) {
					t.Fatalf("Expected file not found error, got: %v", err)
				}
			})

			t.Run("os_release_file_is_not_readable", func(t *testing.T) {
				t.Parallel()

				if os.Getuid() <= 0 {
					t.Skip("This test will produce false positive result when running as root")
				}

				testConfig, _, _ := validTestConfig(t, testNode())

				osReleasePath := filepath.Join(testConfig.HostFilesPrefix, "/etc/os-release")

				if err := os.Chmod(osReleasePath, 0o000); err != nil {
					t.Fatalf("Failed changing mode for test file: %v", err)
				}

				err := getAgentRunningError(t, testConfig)
				if !errors.Is(err, os.ErrPermission) {
					t.Fatalf("Expected file not found error, got: %v", err)
				}
			})
		})

		for name, method := range map[string]string{
			"getting_existing_Node_annotations_fails":                 "get",
			"setting_initial_set_of_Node_annotation_and_labels_fails": "update",
		} {
			method := method

			t.Run(name, func(t *testing.T) {
				t.Parallel()

				testConfig, _, fakeClient := validTestConfig(t, okToRebootNode())

				expectedError := errors.New("Error node operation " + method)

				_, f := failOnNthCall(1, expectedError)
				fakeClient.PrependReactor(method, "nodes", f)

				err := getAgentRunningError(t, testConfig)
				if !errors.Is(err, expectedError) {
					t.Fatalf("Expected error %q when running agent, got: %v", expectedError, err)
				}
			})
		}

		t.Run("waiting_for_not_ok_to_reboot_annotation_fails_because", func(t *testing.T) {
			t.Parallel()

			t.Run("getting_Node_object_fails", func(t *testing.T) {
				t.Parallel()

				testConfig, _, fakeClient := validTestConfig(t, testNode())

				expectedError := errors.New("Error getting node")

				// 1. Updating info labels. TODO: Could be done with patch instead.
				// 2. Checking made unschedulable.
				// 3. Updating annotations and labels.
				_, f := failOnNthCall(3, expectedError)
				fakeClient.PrependReactor("get", "*", f)

				err := getAgentRunningError(t, testConfig)
				if !errors.Is(err, expectedError) {
					t.Fatalf("Expected error %q when running agent, got: %v", expectedError, err)
				}
			})

			t.Run("creating_Node_watcher_fails", func(t *testing.T) {
				t.Parallel()

				testConfig, _, fakeClient := validTestConfig(t, okToRebootNode())

				expectedError := errors.New("creating watcher")
				f := func(action k8stesting.Action) (handled bool, ret watch.Interface, err error) {
					return true, nil, expectedError
				}

				fakeClient.PrependWatchReactor("*", f)

				err := getAgentRunningError(t, testConfig)
				if !errors.Is(err, expectedError) {
					t.Fatalf("Expected error %q running agent, got %q", expectedError, err)
				}
			})

			t.Run("watching_Node", func(t *testing.T) {
				t.Parallel()

				cases := map[string]struct {
					watchEvent    func(*watch.FakeWatcher)
					expectedError string
				}{
					"returns_watch_error": {
						watchEvent:    func(w *watch.FakeWatcher) { w.Error(nil) },
						expectedError: "watching node",
					},
					"returns_object_deleted_error": {
						watchEvent:    func(w *watch.FakeWatcher) { w.Delete(nil) },
						expectedError: "node was deleted",
					},
					"returns_unknown_event_type": {
						watchEvent:    func(w *watch.FakeWatcher) { w.Action(watch.Bookmark, nil) },
						expectedError: "unknown event type",
					},
				}

				for n, testCase := range cases {
					testCase := testCase

					t.Run(n, func(t *testing.T) {
						t.Parallel()

						testConfig, _, fakeClient := validTestConfig(t, okToRebootNode())

						// Mock sending custom watch event.
						watcher := watch.NewFakeWithChanSize(1, true)
						testCase.watchEvent(watcher)
						fakeClient.PrependWatchReactor("nodes", k8stesting.DefaultWatchReactor(watcher, nil))

						err := getAgentRunningError(t, testConfig)
						if err == nil {
							t.Fatalf("Expected error running agent")
						}

						if !strings.Contains(err.Error(), testCase.expectedError) {
							t.Fatalf("Expected error %q, got %q", testCase.expectedError, err)
						}
					})
				}
			})

			t.Run("max_operator_response_time_is_exceeded", func(t *testing.T) {
				t.Parallel()

				testConfig, _, _ := validTestConfig(t, okToRebootNode())
				testConfig.MaxOperatorResponseTime = 500 * time.Millisecond

				agentStopDeadline := contextWithTimeout(t, 5*time.Second)
				done := runAgent(agentStopDeadline, t, testConfig)

				select {
				case <-agentStopDeadline.Done():
					t.Fatalf("Timed out waiting for agent to exit prematurely")
				case err := <-done:
					if err == nil {
						t.Fatalf("Expected to get agent runtime error")
					}
				}
			})
		})

		t.Run("marking_Node_schedulable_fails", func(t *testing.T) {
			t.Parallel()

			testConfig, node, fakeClient := validTestConfig(t, nodeMadeUnschedulable())

			withOkToRebootFalseUpdate(fakeClient, node)

			expectedError := errors.New("Error marking node as schedulable")

			errorOnNodeSchedulable := func(action k8stesting.Action) (bool, runtime.Object, error) {
				node := updateActionToNode(t, action)

				if node.Spec.Unschedulable {
					return true, node, nil
				}

				// If node is about to be marked as schedulable, make error occur.
				return true, nil, expectedError
			}

			fakeClient.PrependReactor("update", "nodes", errorOnNodeSchedulable)

			err := getAgentRunningError(t, testConfig)
			if !errors.Is(err, expectedError) {
				t.Fatalf("Expected error %q, got %q", expectedError, err)
			}
		})

		t.Run("updating_Node_annotations_after_marking_Node_schedulable_fails", func(t *testing.T) {
			t.Parallel()

			testConfig, node, fakeClient := validTestConfig(t, nodeMadeUnschedulable())

			withOkToRebootFalseUpdate(fakeClient, node)

			expectedError := errors.New("Error marking node as schedulable")

			errorOnNodeSchedulable := func(action k8stesting.Action) (bool, runtime.Object, error) {
				node := updateActionToNode(t, action)

				if node.Annotations[constants.AnnotationAgentMadeUnschedulable] != constants.False {
					return true, node, nil
				}

				// If node is about to be annotated as no longer made unschedulable by agent, make error occur.
				return true, nil, expectedError
			}

			fakeClient.PrependReactor("update", "nodes", errorOnNodeSchedulable)

			err := getAgentRunningError(t, testConfig)
			if !errors.Is(err, expectedError) {
				t.Fatalf("Expected error %q, got %q", expectedError, err)
			}
		})

		t.Run("getting_Node_object_after_ok_to_reboot_is_given", func(t *testing.T) {
			t.Parallel()

			testConfig, node, fakeClient := validTestConfig(t, testNode())
			testConfig.StatusReceiver = &mockStatusReceiver{}

			withOkToRebootTrueUpdate(fakeClient, node)

			expectedError := errors.New("Error getting node")

			// 1. Updating info labels. TODO: Could be done with patch instead.
			// 2. Checking made unschedulable.
			// 3. Updating annotations and labels.
			// 4. Getting initial state while waiting for ok-to-reboot.
			// 5. Getting node object to mark it unschedulable etc.
			_, f := failOnNthCall(5, expectedError)
			fakeClient.PrependReactor("get", "*", f)

			err := getAgentRunningError(t, testConfig)
			if err == nil {
				t.Fatalf("Expected error %q, got %q", expectedError, err)
			}
		})

		t.Run("setting_reboot_in_progress_annotation_fails", func(t *testing.T) {
			t.Parallel()

			testConfig, node, fakeClient := validTestConfig(t, testNode())

			withOkToRebootTrueUpdate(fakeClient, node)

			expectedError := errors.New("Error setting reboot in progress annotation")

			fakeClient.PrependReactor("update", "nodes", func(action k8stesting.Action) (bool, runtime.Object, error) {
				node := updateActionToNode(t, action)

				if v, ok := node.Annotations[constants.AnnotationRebootInProgress]; ok && v == constants.True {
					// If node is about to be marked as reboot is in progress, make error occur.
					// For simplicity of logic, keep the happy path intended.
					return true, nil, expectedError
				}

				return true, node, nil
			})

			if err := getAgentRunningError(t, testConfig); !errors.Is(err, expectedError) {
				t.Fatalf("Expected error %q, got %q", expectedError, err)
			}
		})

		t.Run("marking_Node_unschedulable_fails", func(t *testing.T) {
			t.Parallel()

			testConfig, node, fakeClient := validTestConfig(t, testNode())

			withOkToRebootTrueUpdate(fakeClient, node)

			expectedError := errors.New("Error marking node as unschedulable")

			fakeClient.PrependReactor("update", "nodes", func(action k8stesting.Action) (bool, runtime.Object, error) {
				node := updateActionToNode(t, action)

				if !node.Spec.Unschedulable {
					return true, node, nil
				}

				// If node is about to be marked as unschedulable, make error occur.
				return true, nil, expectedError
			})

			if err := getAgentRunningError(t, testConfig); !errors.Is(err, expectedError) {
				t.Fatalf("Expected error %q, got %q", expectedError, err)
			}
		})

		t.Run("getting_nodes_for_deletion_fails", func(t *testing.T) {
			t.Parallel()

			testConfig, node, fakeClient := validTestConfig(t, testNode())

			withOkToRebootTrueUpdate(fakeClient, node)

			expectedError := errors.New("Error getting pods for deletion")

			_, f := failOnNthCall(0, expectedError)
			fakeClient.PrependReactor("list", "pods", f)

			expectedErrorWrapped := fmt.Errorf("processing: getting pods for deletion: %v", []error{expectedError})
			if err := getAgentRunningError(t, testConfig); err.Error() != expectedErrorWrapped.Error() {
				t.Fatalf("Expected error %q, got %q", expectedErrorWrapped, err)
			}
		})

		t.Run("agent_receives_termination_signal_while_waiting_for_all_pods_to_be_terminated", func(t *testing.T) {
			t.Parallel()

			rebootTriggerred := make(chan bool, 1)

			controllerTrue := true
			podsToCreate := []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "foo",
						Namespace: "default",
						OwnerReferences: []metav1.OwnerReference{
							{
								Name:       "fake-owner",
								Controller: &controllerTrue,
							},
						},
					},

					Spec: corev1.PodSpec{
						NodeName: testNode().Name,
					},
				},
			}

			fakeClient := fake.NewSimpleClientset(podsToCreate[0], testNode())
			addEvictionSupport(t, fakeClient, "v1")

			testConfig, node, _ := validTestConfig(t, testNode())
			testConfig.Clientset = fakeClient
			testConfig.PodDeletionGracePeriod = 30 * time.Second
			testConfig.Rebooter = &mockRebooter{
				rebootF: func(auth bool) {
					rebootTriggerred <- auth
				},
			}

			expectedGetPodCalls := 1
			getPodCalls := make(chan struct{}, expectedGetPodCalls)

			fakeClient.PrependReactor("get", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
				if len(getPodCalls) < expectedGetPodCalls {
					getPodCalls <- struct{}{}
				}

				return true, nil, fmt.Errorf(t.Name())
			})

			ctx, cancel := context.WithCancel(contextWithTimeout(t, agentRunTimeLimit))

			done := runAgent(ctx, t, testConfig)

			assertNodeProperty(ctx, t, &assertNodePropertyContext{
				done:   done,
				config: testConfig,
				testF:  assertNodeAnnotationValue(constants.AnnotationRebootNeeded, constants.True),
			})

			okToReboot(ctx, t, testConfig.Clientset.CoreV1().Nodes(), node.Name)

			<-getPodCalls
			cancel()

			select {
			case err := <-done:
				if err == nil {
					t.Fatalf("Expected error when stopping agent while waiting for pods to be terminated")
				}
			case <-rebootTriggerred:
				t.Fatalf("Unexpected reboot triggered")
			}
		})
	})
}

// Expose klog flags to be able to increase verbosity for agent logs.
func TestMain(m *testing.M) {
	testFlags := flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	klog.InitFlags(testFlags)

	if err := testFlags.Parse([]string{"-v=10"}); err != nil {
		fmt.Printf("Failed parsing flags: %v\n", err)
		os.Exit(1)
	}

	os.Exit(m.Run())
}

func validTestConfig(t *testing.T, node *corev1.Node) (*agent.Config, *corev1.Node, *k8stesting.Fake) {
	t.Helper()

	files := map[string]string{
		"/usr/share/flatcar/update.conf": "GROUP=imageGroup",
		"/etc/flatcar/update.conf":       "GROUP=configuredGroup",
		"/etc/os-release":                "ID=testID\nVERSION=testVersion",
	}

	hostFilesPrefix := t.TempDir()

	createTestFiles(t, files, hostFilesPrefix)

	fakeClient := fake.NewSimpleClientset(node)

	return &agent.Config{
		Clientset:              fakeClient,
		StatusReceiver:         rebootNeededStatusReceiver(),
		Rebooter:               &mockRebooter{},
		NodeName:               node.Name,
		HostFilesPrefix:        hostFilesPrefix,
		PollInterval:           200 * time.Millisecond,
		PodDeletionGracePeriod: time.Second,
	}, node, &fakeClient.Fake
}

type mockStatusReceiver struct {
	receiveStatusesF func(chan<- updateengine.Status, <-chan struct{})
}

func (m *mockStatusReceiver) ReceiveStatuses(rcvr chan<- updateengine.Status, stop <-chan struct{}) {
	if m.receiveStatusesF != nil {
		m.receiveStatusesF(rcvr, stop)
	}
}

type mockRebooter struct {
	rebootF func(bool)
}

func (m *mockRebooter) Reboot(auth bool) {
	if m.rebootF != nil {
		m.rebootF(auth)
	}
}

func contextWithDeadline(t *testing.T) context.Context {
	t.Helper()

	deadline, ok := t.Deadline()
	if !ok {
		return context.Background()
	}

	gracefulDeadline := deadline.Truncate(agentShutdownLimit)

	if time.Now().After(gracefulDeadline) {
		t.Fatalf("Received deadline lower than termination grace period of %s", agentShutdownLimit)
	}

	ctx, cancel := context.WithDeadline(context.Background(), gracefulDeadline)
	t.Cleanup(cancel)

	return ctx
}

func createTestFiles(t *testing.T, filesContentByPath map[string]string, prefix string) {
	t.Helper()

	for path, content := range filesContentByPath {
		pathWithPrefix := filepath.Join(prefix, path)

		dir := filepath.Dir(pathWithPrefix)

		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("Failed creating directory %q: %v", dir, err)
		}

		if err := os.WriteFile(pathWithPrefix, []byte(content), 0o600); err != nil {
			t.Fatalf("Failed creating file %q: %v", pathWithPrefix, err)
		}
	}
}

type nodeAssertF func(*testing.T, *corev1.Node) bool

type assertNodePropertyContext struct {
	done   <-chan error
	config *agent.Config
	testF  nodeAssertF
}

func assertNodeProperty(ctx context.Context, t *testing.T, properties *assertNodePropertyContext) {
	t.Helper()

	ticker := time.NewTicker(100 * time.Millisecond)

	for {
		select {
		case err := <-properties.done:
			t.Fatalf("Agent stopped prematurely: %v", err)
		case <-ctx.Done():
			t.Fatal("Timed out waiting for node property")
		case <-ticker.C:
			nodeClient := properties.config.Clientset.CoreV1().Nodes()

			updatedNode, err := nodeClient.Get(ctx, properties.config.NodeName, metav1.GetOptions{})
			if err != nil {
				t.Fatalf("Failed getting Node object %q: %v", properties.config.NodeName, err)
			}

			if !properties.testF(t, updatedNode) {
				continue
			}

			return
		}
	}
}

func assertNodeLabelExists(key string) nodeAssertF {
	return func(t *testing.T, node *corev1.Node) bool {
		t.Helper()

		if _, ok := node.Labels[key]; !ok {
			t.Fatalf("Expected label %q to be set on node", key)
		}

		return true
	}
}

func assertNodeLabelValue(key, expectedValue string) nodeAssertF {
	return func(t *testing.T, node *corev1.Node) bool {
		t.Helper()

		value, ok := node.Labels[key]
		if !ok {
			return false
		}

		if value != expectedValue {
			t.Fatalf("Expected value %q for key %q, got %q", expectedValue, key, value)
		}

		return true
	}
}

func assertNodeAnnotationValue(key, expectedValue string) nodeAssertF {
	return func(t *testing.T, node *corev1.Node) bool {
		t.Helper()

		value, ok := node.Annotations[key]
		if !ok {
			return false
		}

		if value != expectedValue {
			t.Fatalf("Expected value %q for key %q, got %q", expectedValue, key, value)
		}

		return true
	}
}

func runAgent(ctx context.Context, t *testing.T, config *agent.Config) <-chan error {
	t.Helper()

	client, err := agent.New(config)
	if err != nil {
		t.Fatalf("Unexpected error creating new agent: %v", err)
	}

	done := make(chan error)

	go func() {
		done <- client.Run(ctx)
	}()

	return done
}

func getAgentRunningError(t *testing.T, config *agent.Config) error {
	t.Helper()

	testCtx := contextWithDeadline(t)

	ctx, cancel := context.WithTimeout(testCtx, agentRunTimeLimit)
	defer cancel()

	select {
	case <-testCtx.Done():
		t.Fatalf("Expected agent to exit before deadline")
	case err := <-runAgent(ctx, t, config):
		return err
	}

	return nil
}

func okToRebootNode() *corev1.Node {
	node := testNode()

	node.Annotations[constants.AnnotationOkToReboot] = constants.True

	return node
}

func nodeMadeUnschedulable() *corev1.Node {
	node := testNode()

	node.Annotations[constants.AnnotationOkToReboot] = constants.True
	node.Annotations[constants.AnnotationAgentMadeUnschedulable] = constants.True
	node.Spec.Unschedulable = true

	return node
}

func testNode() *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "testNodeName",
			// TODO: Fix code to handle Node objects with no labels and annotations?
			Labels:      map[string]string{},
			Annotations: map[string]string{},
		},
	}
}

func withOkToRebootTrueUpdate(fakeClient *k8stesting.Fake, node *corev1.Node) {
	watcher := watch.NewFakeWithChanSize(1, true)
	updatedNode := node.DeepCopy()
	updatedNode.Annotations[constants.AnnotationOkToReboot] = constants.True
	updatedNode.Annotations[constants.AnnotationRebootNeeded] = constants.True
	watcher.Modify(updatedNode)
	fakeClient.PrependWatchReactor("nodes", k8stesting.DefaultWatchReactor(watcher, nil))
}

func withOkToRebootFalseUpdate(fakeClient *k8stesting.Fake, node *corev1.Node) {
	watcher := watch.NewFakeWithChanSize(1, true)
	updatedNode := node.DeepCopy()
	updatedNode.Annotations[constants.AnnotationOkToReboot] = constants.False
	watcher.Modify(updatedNode)
	fakeClient.PrependWatchReactor("nodes", k8stesting.DefaultWatchReactor(watcher, nil))
}

func listPodsWithFieldSelector(allPods []*corev1.Pod) func(action k8stesting.Action) (bool, runtime.Object, error) {
	return func(action k8stesting.Action) (bool, runtime.Object, error) {
		actionList, ok := action.(k8stesting.ListActionImpl)
		if !ok {
			return true, nil, fmt.Errorf("unexpected action type, expected %T, got %T", k8stesting.ListActionImpl{}, action)
		}

		listFieldsSelector := actionList.GetListRestrictions().Fields

		pods := []corev1.Pod{}

		for _, pod := range allPods {
			podSpecificFieldsSet := make(fields.Set, 8)
			podSpecificFieldsSet["spec.nodeName"] = pod.Spec.NodeName

			if listFieldsSelector.Matches(podSpecificFieldsSet) {
				pods = append(pods, *pod)
			}
		}

		return true, &corev1.PodList{
			Items: pods,
		}, nil
	}
}

func okToReboot(ctx context.Context, t *testing.T, client corev1client.NodeInterface, name string) {
	t.Helper()

	updateNode(ctx, t, client, name, constants.True)
}

func notOkToReboot(ctx context.Context, t *testing.T, client corev1client.NodeInterface, name string) {
	t.Helper()

	updateNode(ctx, t, client, name, constants.False)
}

func updateNode(ctx context.Context, t *testing.T, client corev1client.NodeInterface, name, value string) {
	t.Helper()

	updatedNode, err := client.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Failed getting node %q for marking as ok to reboot: %v", name, err)
	}

	updatedNode.Annotations[constants.AnnotationOkToReboot] = value

	if _, err := client.Update(ctx, updatedNode, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("Failed updating node: %v", err)
	}
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

		// Agent may currently send requests without a backoff, so slow it down a bit to avoid exceesive logging,
		// which may make tests more flaky when there is not enough CPU available.
		//
		// TODO: This should not be needed once we implement backoff.
		time.Sleep(errorResponseThrottle)

		if len(errorReached) == 0 {
			errorReached <- struct{}{}
		}

		return true, nil, err
	}
}

func contextWithTimeout(t *testing.T, timeout time.Duration) context.Context {
	t.Helper()

	ctx, cancel := context.WithTimeout(contextWithDeadline(t), timeout)
	t.Cleanup(cancel)

	return ctx
}

func rebootNeededStatusReceiver() *mockStatusReceiver {
	return &mockStatusReceiver{
		receiveStatusesF: func(ch chan<- updateengine.Status, _ <-chan struct{}) {
			ch <- updateengine.Status{
				CurrentOperation: updateengine.UpdateStatusUpdatedNeedReboot,
			}
		},
	}
}

func notifyOnNodeUnschedulableUpdate(t *testing.T, fakeClient *k8stesting.Fake) chan struct{} {
	t.Helper()

	chSize := 1

	updateCh := make(chan struct{}, chSize)

	fakeClient.PrependReactor("update", "nodes", func(action k8stesting.Action) (bool, runtime.Object, error) {
		node := updateActionToNode(t, action)

		if node.Spec.Unschedulable && len(updateCh) < chSize {
			updateCh <- struct{}{}
		}

		return false, nil, nil
	})

	return updateCh
}

func updateActionToNode(t *testing.T, action k8stesting.Action) *corev1.Node {
	t.Helper()

	updateAction, ok := action.(k8stesting.UpdateActionImpl) //nolint:varnamelen // Below it's different variable.
	if !ok {
		t.Fatalf("Expected action %T, got %T", k8stesting.UpdateActionImpl{}, action)
	}

	node, ok := updateAction.GetObject().(*corev1.Node)
	if !ok {
		t.Fatalf("Expected update for object %T, got %T", &corev1.Node{}, updateAction.GetObject())
	}

	return node
}

// lifted from https://github.com/kubernetes/kubectl/blob/master/pkg/drain/drain_test.go.
func addEvictionSupport(t *testing.T, clientset *fake.Clientset, version string) {
	t.Helper()

	podsEviction := metav1.APIResource{
		Name:    "pods/eviction",
		Kind:    "Eviction",
		Group:   "policy",
		Version: version,
	}
	coreResources := &metav1.APIResourceList{
		GroupVersion: "v1",
		APIResources: []metav1.APIResource{podsEviction},
	}

	policyResources := &metav1.APIResourceList{
		GroupVersion: "policy/v1",
	}
	clientset.Resources = append(clientset.Resources, coreResources, policyResources)

	// Delete pods when evict is called
	clientset.PrependReactor("create", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if action.GetSubresource() != "eviction" {
			return false, nil, nil
		}

		namespace := ""
		name := ""
		switch version {
		case "v1":
			var eviction *policyv1.Eviction
			if a, ok := action.(k8stesting.CreateAction); ok {
				if p, ok := a.GetObject().(*policyv1.Eviction); ok {
					eviction = p
				}
			}
			namespace = eviction.Namespace
			name = eviction.Name
		case "v1beta1":
			var eviction *policyv1beta1.Eviction
			if a, ok := action.(k8stesting.CreateAction); ok {
				if p, ok := a.GetObject().(*policyv1beta1.Eviction); ok {
					eviction = p
				}
			}
			namespace = eviction.Namespace
			name = eviction.Name
		default:
			t.Errorf("unknown version %s", version)
		}
		// Avoid the lock.
		go func() {
			err := clientset.CoreV1().Pods(namespace).Delete(context.TODO(), name, metav1.DeleteOptions{})
			if err != nil {
				// Errorf because we can't call Fatalf from another goroutine.
				t.Errorf("failed to delete pod: %s/%s", namespace, name)
			}
		}()

		return true, nil, nil
	})
}
