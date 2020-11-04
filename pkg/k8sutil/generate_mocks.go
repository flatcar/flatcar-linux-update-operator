package k8sutil

// Use -mod=mod as github.com/golang/mock/mockgen cannot be vendored.
// This means generation won't work in offline mode unless cache for
// mockgen is populated.
//
//go:generate go run -mod=mod github.com/golang/mock/mockgen -build_flags=-mod=vendor -destination ./mocks/node_interface_mock.go k8s.io/client-go/kubernetes/typed/core/v1 NodeInterface

import (
	// Import model package to force it to be included in vendor/ directory,
	// so go generate can run with -mod vendor.
	_ "github.com/golang/mock/mockgen/model"
)
