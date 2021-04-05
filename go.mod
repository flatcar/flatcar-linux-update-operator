module github.com/kinvolk/flatcar-linux-update-operator

go 1.16

require (
	github.com/blang/semver/v4 v4.0.0
	github.com/coreos/go-systemd v0.0.0-20191104093116-d3cd4ed1dbcf
	github.com/coreos/locksmith v0.6.2
	github.com/coreos/pkg v0.0.0-20180928190104-399ea9e2e55f
	github.com/godbus/dbus v4.1.0+incompatible
	github.com/golang/mock v1.5.0
	k8s.io/api v0.20.5
	k8s.io/apimachinery v0.20.5
	k8s.io/client-go v0.20.5
	k8s.io/klog/v2 v2.8.0
)
