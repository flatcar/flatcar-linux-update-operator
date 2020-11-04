package k8sutil

//go:generate go run github.com/golang/mock/mockgen -build_flags=-mod=vendor -destination ./mocks/node_interface_mock.go k8s.io/client-go/kubernetes/typed/core/v1 NodeInterface

import (
	// Import model package to force it to be included in vendor/ directory,
	// so go generate can run with -mod vendor.
	_ "github.com/golang/mock/mockgen/model"
)
