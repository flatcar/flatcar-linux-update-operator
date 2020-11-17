package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/coreos/pkg/flagutil"
	"k8s.io/klog/v2"

	"github.com/kinvolk/flatcar-linux-update-operator/pkg/agent"
	"github.com/kinvolk/flatcar-linux-update-operator/pkg/version"
)

var (
	node         = flag.String("node", "", "Kubernetes node name")
	printVersion = flag.Bool("version", false, "Print version and exit")

	reapTimeout = flag.Int("grace-period", 600,
		"Period of time in seconds given to a pod to terminate when rebooting for an update")
)

func main() {
	klog.InitFlags(nil)
	flag.Set("logtostderr", "true")
	flag.Parse()

	if err := flagutil.SetFlagsFromEnv(flag.CommandLine, "UPDATE_AGENT"); err != nil {
		klog.Fatalf("Failed to parse environment variables: %v", err)
	}

	if *printVersion {
		fmt.Println(version.Format())
		os.Exit(0)
	}

	if *node == "" {
		klog.Fatal("-node is required")
	}

	rt := time.Duration(*reapTimeout) * time.Second

	a, err := agent.New(*node, rt)
	if err != nil {
		klog.Fatalf("Failed to initialize %s: %v", os.Args[0], err)
	}

	klog.Infof("%s running", os.Args[0])

	// Run agent until the stop channel is closed
	stop := make(chan struct{})
	defer close(stop)
	a.Run(stop)
}
