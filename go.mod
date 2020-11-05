module github.com/kinvolk/flatcar-linux-update-operator

go 1.13

require (
	github.com/blang/semver v3.5.1+incompatible
	github.com/coreos/go-systemd v0.0.0-20191104093116-d3cd4ed1dbcf
	github.com/coreos/locksmith v0.6.2
	github.com/coreos/pkg v0.0.0-20180928190104-399ea9e2e55f
	github.com/go-logr/logr v0.3.0 // indirect
	github.com/godbus/dbus v4.1.0+incompatible
	github.com/golang/mock v1.4.4
	github.com/google/go-cmp v0.5.2 // indirect
	github.com/google/gofuzz v1.2.0 // indirect
	github.com/googleapis/gnostic v0.5.3 // indirect
	github.com/hashicorp/golang-lru v0.5.4 // indirect
	github.com/imdario/mergo v0.3.11 // indirect
	golang.org/x/crypto v0.0.0-20201016220609-9e8e0b390897 // indirect
	golang.org/x/net v0.0.0-20201031054903-ff519b6c9102 // indirect
	golang.org/x/oauth2 v0.0.0-20200902213428-5d25da1a8d43 // indirect
	golang.org/x/sys v0.0.0-20201101102859-da207088b7d1 // indirect
	golang.org/x/text v0.3.4 // indirect
	golang.org/x/time v0.0.0-20200630173020-3af7569d3a1e // indirect
	google.golang.org/appengine v1.6.7 // indirect
	k8s.io/api v0.19.3
	k8s.io/apimachinery v0.19.3
	k8s.io/client-go v11.0.0+incompatible
	k8s.io/klog/v2 v2.4.0
	k8s.io/kube-openapi v0.0.0-20201104192653-842b07581b16 // indirect
	k8s.io/utils v0.0.0-20201027101359-01387209bb0d // indirect
)

// Force using specific version of client-go matching other K8s packages.
replace k8s.io/client-go => k8s.io/client-go v0.19.3
