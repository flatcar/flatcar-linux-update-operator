module github.com/kinvolk/flatcar-linux-update-operator

go 1.13

require (
	github.com/blang/semver v3.5.1+incompatible
	github.com/coreos/go-systemd v0.0.0-20161114122254-48702e0da86b
	github.com/coreos/locksmith v0.6.2
	github.com/coreos/pkg v0.0.0-20180928190104-399ea9e2e55f
	github.com/godbus/dbus v4.1.0+incompatible
	github.com/gogo/protobuf v1.3.1 // indirect
	github.com/golang/groupcache v0.0.0-20191227052852-215e87163ea7 // indirect
	github.com/golang/mock v1.4.4
	github.com/google/go-cmp v0.4.0 // indirect
	github.com/googleapis/gnostic v0.3.1 // indirect
	github.com/hashicorp/golang-lru v0.5.3 // indirect
	github.com/imdario/mergo v0.3.8 // indirect
	github.com/json-iterator/go v1.1.9 // indirect
	golang.org/x/crypto v0.0.0-20200109152110-61a87790db17 // indirect
	golang.org/x/net v0.0.0-20191209160850-c0dbc17a3553 // indirect
	golang.org/x/oauth2 v0.0.0-20200107190931-bf48bf16ab8d // indirect
	golang.org/x/sys v0.0.0-20200107162124-548cf772de50 // indirect
	golang.org/x/text v0.3.4 // indirect
	golang.org/x/time v0.0.0-20191024005414-555d28b269f0 // indirect
	google.golang.org/appengine v1.6.5 // indirect
	gopkg.in/yaml.v2 v2.2.7 // indirect
	k8s.io/api v0.17.0
	k8s.io/apimachinery v0.17.0
	k8s.io/client-go v11.0.0+incompatible
	k8s.io/klog/v2 v2.4.0
	k8s.io/utils v0.0.0-20200109141947-94aeca20bf09 // indirect
)

// Force using specific version of client-go matching other K8s packages.
replace k8s.io/client-go => k8s.io/client-go v0.17.0
