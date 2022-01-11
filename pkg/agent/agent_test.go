package agent_test

import (
	"testing"

	"k8s.io/client-go/kubernetes/fake"

	"github.com/flatcar-linux/flatcar-linux-update-operator/pkg/agent"
	"github.com/flatcar-linux/flatcar-linux-update-operator/pkg/updateengine"
)

//nolint:funlen,cyclop // Just many subtests.
func Test_Creating_new_agent(t *testing.T) {
	t.Parallel()

	t.Run("returns_agent_when_all_dependencies_are_satisfied", func(t *testing.T) {
		t.Parallel()

		client, err := agent.New(testConfig())
		if err != nil {
			t.Fatalf("Unexpected error creating new agent: %v", err)
		}

		if client == nil {
			t.Fatalf("Client should be returned when creating agent succeeds")
		}
	})

	t.Run("returns_error_when", func(t *testing.T) {
		t.Parallel()

		t.Run("no_clientset_is_configured", func(t *testing.T) {
			t.Parallel()

			configWithoutClientset := testConfig()
			configWithoutClientset.Clientset = nil

			client, err := agent.New(configWithoutClientset)
			if err == nil {
				t.Fatalf("Expected error creating new agent")
			}

			if client != nil {
				t.Fatalf("No client should be returned when New failed")
			}
		})

		t.Run("no_status_receiver_is_configured", func(t *testing.T) {
			t.Parallel()

			configWithoutStatusReceiver := testConfig()
			configWithoutStatusReceiver.StatusReceiver = nil

			client, err := agent.New(configWithoutStatusReceiver)
			if err == nil {
				t.Fatalf("Expected error creating new agent")
			}

			if client != nil {
				t.Fatalf("No client should be returned when New failed")
			}
		})

		t.Run("no_rebooter_is_configured", func(t *testing.T) {
			t.Parallel()

			configWithoutStatusReceiver := testConfig()
			configWithoutStatusReceiver.Rebooter = nil

			client, err := agent.New(configWithoutStatusReceiver)
			if err == nil {
				t.Fatalf("Expected error creating new agent")
			}

			if client != nil {
				t.Fatalf("No client should be returned when New failed")
			}
		})

		t.Run("empty_node_name_is_given", func(t *testing.T) {
			t.Parallel()

			configWithoutStatusReceiver := testConfig()
			configWithoutStatusReceiver.NodeName = ""

			client, err := agent.New(configWithoutStatusReceiver)
			if err == nil {
				t.Fatalf("Expected error creating new agent")
			}

			if client != nil {
				t.Fatalf("No client should be returned when New failed")
			}
		})
	})
}

func testConfig() *agent.Config {
	return &agent.Config{
		Clientset:      fake.NewSimpleClientset(),
		StatusReceiver: &mockStatusReceiver{},
		Rebooter:       &mockRebooter{},
		NodeName:       "testNodeName",
	}
}

type mockStatusReceiver struct{}

func (m *mockStatusReceiver) ReceiveStatuses(rcvr chan<- updateengine.Status, stop <-chan struct{}) {}

type mockRebooter struct{}

func (m *mockRebooter) Reboot(bool) {}
