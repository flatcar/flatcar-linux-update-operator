package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/coreos/go-systemd/v22/login1"
	"github.com/coreos/pkg/flagutil"
	"k8s.io/klog/v2"

	"k8s.io/kubectl/pkg/drain"

	"github.com/flatcar-linux/flatcar-linux-update-operator/pkg/agent"
	"github.com/flatcar-linux/flatcar-linux-update-operator/pkg/dbus"
	"github.com/flatcar-linux/flatcar-linux-update-operator/pkg/k8sutil"
	"github.com/flatcar-linux/flatcar-linux-update-operator/pkg/updateengine"
	"github.com/flatcar-linux/flatcar-linux-update-operator/pkg/version"
)

var (
	node         = flag.String("node", "", "Kubernetes node name")
	printVersion = flag.Bool("version", false, "Print version and exit")

	reapTimeout = flag.Int("grace-period", 600,
		"Period of time in seconds given to a pod to terminate when rebooting for an update")
	useKubectlDrain = flag.Bool("use-kubectl-drain", false, "Use kubectl drain implementation to drain nodes")
)

func main() {
	klog.InitFlags(nil)

	if err := flag.Set("logtostderr", "true"); err != nil {
		klog.Fatalf("Failed to set %q flag value: %v", "logtostderr", err)
	}

	flag.Parse()

	if err := flagutil.SetFlagsFromEnv(flag.CommandLine, "UPDATE_AGENT"); err != nil {
		klog.Fatalf("Failed to parse environment variables: %v", err)
	}

	if *printVersion {
		fmt.Println(version.Format())
		os.Exit(0)
	}

	clientset, err := k8sutil.GetClient("")
	if err != nil {
		klog.Fatalf("Failed creating Kubernetes client: %v", err)
	}

	updateEngineClient, err := updateengine.New(dbus.SystemPrivateConnector)
	if err != nil {
		klog.Fatalf("Failed establishing connection to update_engine dbus: %v", err)
	}

	defer func() {
		if err := updateEngineClient.Close(); err != nil {
			klog.Warningf("Failed gracefully closing update_engine client: %v", err)
		}
	}()

	rebooter, err := login1.New()
	if err != nil {
		klog.Fatalf("Failed establishing connection to logind dbus: %v", err)
	}

	dh := drain.Helper{
		Ctx:                 nil,
		Client:              clientset,
		Force:               false,
		GracePeriodSeconds:  -1,
		IgnoreAllDaemonSets: true,
		DeleteEmptyDirData:  true,
		Out:                 KlogOutWriter{},
		ErrOut:              KlogErrWriter{},
	}

	config := &agent.Config{
		NodeName:               *node,
		PodDeletionGracePeriod: time.Duration(*reapTimeout) * time.Second,
		Clientset:              clientset,
		StatusReceiver:         updateEngineClient,
		Rebooter:               rebooter,
		DrainHelper:            &dh,
	}

	agent, err := agent.New(config)
	if err != nil {
		klog.Fatalf("Failed to initialize %s: %v", os.Args[0], err)
	}

	klog.Infof("%s running", os.Args[0])

	// Run agent until the context is cancelled.
	if err := agent.Run(context.Background()); err != nil {
		klog.Fatalf("Error running agent: %v", err)
	}
}

type KlogOutWriter struct{}

func (r KlogOutWriter) Write(data []byte) (n int, err error) {
	klog.Info(string(data))
	return 0, err
}

type KlogErrWriter struct{}

func (r KlogErrWriter) Write(data []byte) (n int, err error) {
	klog.Error(string(data))
	return 0, err
}
