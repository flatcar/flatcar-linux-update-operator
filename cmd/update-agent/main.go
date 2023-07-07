// Package main provides executable for FLUO agent.
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

	"github.com/flatcar/flatcar-linux-update-operator/pkg/agent"
	"github.com/flatcar/flatcar-linux-update-operator/pkg/dbus"
	"github.com/flatcar/flatcar-linux-update-operator/pkg/k8sutil"
	"github.com/flatcar/flatcar-linux-update-operator/pkg/updateengine"
	"github.com/flatcar/flatcar-linux-update-operator/pkg/version"
)

const defaultGracePeriodSeconds = 600

var (
	node         = flag.String("node", "", "Kubernetes node name")
	printVersion = flag.Bool("version", false, "Print version and exit")

	reapTimeout = flag.Int("grace-period", defaultGracePeriodSeconds,
		"Period of time in seconds given to a pod to terminate when rebooting for an update")
	forceNodeDrain = flag.Bool("force-drain", false, "Force removal of pods with custom or no owners while draining node")
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

	config := &agent.Config{
		NodeName:               *node,
		PodDeletionGracePeriod: time.Duration(*reapTimeout) * time.Second,
		Clientset:              clientset,
		StatusReceiver:         updateEngineClient,
		Rebooter:               rebooter,
		ForceNodeDrain:         *forceNodeDrain,
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
