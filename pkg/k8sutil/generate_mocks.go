// +build generate

package k8sutil

//go:generate go run -mod=vendor github.com/golang/mock/mockgen -build_flags=-mod=vendor -destination ./mocks/node_interface_mock.go k8s.io/client-go/kubernetes/typed/core/v1 NodeInterface

import (
	// Import mockgen package to force it to be included in vendor/ directory,
	// so go generate can run with -mod vendor.
	_ "github.com/golang/mock/mockgen"
)
