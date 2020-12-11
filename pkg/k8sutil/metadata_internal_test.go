package k8sutil

import (
	"reflect"
	"testing"
)

func Test_slitNewlineEnv_retain_map_values_when_given_empty_input(t *testing.T) {
	t.Parallel()

	expected := map[string]string{"foo": "bar"}

	input := map[string]string{}

	splitNewlineEnv(input, "foo=bar")

	splitNewlineEnv(input, "")

	if !reflect.DeepEqual(expected, input) {
		t.Fatalf("Should retain original map when given empty content")
	}
}
