package operator_test

import (
	"testing"

	"k8s.io/client-go/kubernetes/fake"

	"github.com/kinvolk/flatcar-linux-update-operator/pkg/operator"
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

func validOperatorConfig() operator.Config {
	return operator.Config{
		Client:    fake.NewSimpleClientset(),
		Namespace: "test-namespace",
		LockID:    "test-lock-id",
	}
}
