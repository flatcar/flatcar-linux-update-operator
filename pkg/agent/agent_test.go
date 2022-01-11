package agent_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
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

//nolint:funlen // Just many test cases.
func Test_Running_agent(t *testing.T) {
	t.Parallel()

	t.Run("returns_error_when", func(t *testing.T) {
		t.Parallel()

		t.Run("Flatcar_update_configuration_file_does_not_exist", func(t *testing.T) {
			t.Parallel()

			configWithNoHostFiles := testConfig()
			configWithNoHostFiles.HostFilesPrefix = t.TempDir()

			client, err := agent.New(configWithNoHostFiles)
			if err != nil {
				t.Fatalf("Unexpected error creating new agent: %v", err)
			}

			ctx, cancel := context.WithTimeout(contextWithDeadline(t), 500*time.Millisecond)
			defer cancel()

			done := make(chan error)
			go func() {
				done <- client.Run(ctx.Done())
			}()

			select {
			case <-ctx.Done():
				t.Fatalf("Expected agent to exit before deadline")
			case err := <-done:
				if err == nil {
					t.Fatalf("Expected agent to return an error")
				}
			}
		})

		t.Run("configured_Node_does_not_exist", func(t *testing.T) {
			configWithTestFilesPrefix := testConfig()

			configWithTestFilesPrefix.HostFilesPrefix = t.TempDir()

			files := map[string]string{
				"/usr/share/flatcar/update.conf": "GROUP=imageGroup",
				"/etc/os-release":                "ID=testID\nVERSION=testVersion",
			}

			createTestFiles(t, files, configWithTestFilesPrefix.HostFilesPrefix)

			client, err := agent.New(configWithTestFilesPrefix)
			if err != nil {
				t.Fatalf("Unexpected error creating new agent: %v", err)
			}

			ctx, cancel := context.WithTimeout(contextWithDeadline(t), 500*time.Millisecond)
			defer cancel()

			done := make(chan error)
			go func() {
				done <- client.Run(ctx.Done())
			}()

			select {
			case <-ctx.Done():
				t.Fatalf("Expected agent to exit before deadline")
			case err := <-done:
				if err == nil {
					t.Fatalf("Expected agent to return an error")
				}

				if !apierrors.IsNotFound(err) {
					t.Fatalf("Expected Node not found error when running agent, got: %v", err)
				}
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
