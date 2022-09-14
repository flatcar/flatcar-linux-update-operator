// Package main provides executable for FLUO.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/coreos/pkg/flagutil"
	"k8s.io/klog/v2"

	"github.com/flatcar/flatcar-linux-update-operator/pkg/k8sutil"
	"github.com/flatcar/flatcar-linux-update-operator/pkg/operator"
	"github.com/flatcar/flatcar-linux-update-operator/pkg/version"
)

type flagsSet struct {
	beforeRebootAnnotations flagutil.StringSliceFlag
	afterRebootAnnotations  flagutil.StringSliceFlag
	kubeconfig              *string
	rebootWindowStart       *string
	rebootWindowLength      *string
	printVersion            *bool
}

func handleFlags() *flagsSet {
	flags := &flagsSet{
		kubeconfig: flag.String("kubeconfig", "",
			"Path to a kubeconfig file. Default to the in-cluster config if not provided."),

		rebootWindowStart: flag.String("reboot-window-start", "",
			"Day of week ('Sun', 'Mon', ...; optional) and time of day at which the reboot window starts. "+
				"E.g. 'Mon 14:00', '11:00'"),

		rebootWindowLength: flag.String("reboot-window-length", "", "Length of the reboot window. E.g. '1h30m'"),
		printVersion:       flag.Bool("version", false, "Print version and exit"),
	}

	flag.Var(&flags.beforeRebootAnnotations, "before-reboot-annotations",
		"List of comma-separated Kubernetes node annotations that must be set to 'true' before a reboot is allowed")

	flag.Var(&flags.afterRebootAnnotations, "after-reboot-annotations",
		"List of comma-separated Kubernetes node annotations that must be set to 'true' before a node is marked "+
			"schedulable and the operator lock is released")

	klog.InitFlags(nil)

	if err := flag.Set("logtostderr", "true"); err != nil {
		klog.Fatalf("Failed setting %q flag: %v", "logtostderr", err)
	}

	flag.Parse()

	if err := flagutil.SetFlagsFromEnv(flag.CommandLine, "UPDATE_OPERATOR"); err != nil {
		klog.Fatalf("Failed to parse environment variables: %v", err)
	}

	// Respect KUBECONFIG without the prefix as well.
	if *flags.kubeconfig == "" {
		*flags.kubeconfig = os.Getenv("KUBECONFIG")
	}

	return flags
}

func main() {
	flags := handleFlags()

	if *flags.printVersion {
		fmt.Println(version.Format())
		os.Exit(0)
	}

	// Create Kubernetes client (clientset).
	client, err := k8sutil.GetClient(*flags.kubeconfig)
	if err != nil {
		klog.Fatalf("Failed to create Kubernetes client: %v", err)
	}

	namespace := os.Getenv("POD_NAMESPACE")
	if namespace == "" {
		klog.Fatalf("Unable to determine operator namespace: please ensure POD_NAMESPACE environment variable is set")
	}

	// TODO: a better id might be necessary.
	// Currently, KVO uses env.POD_NAME and the upstream controller-manager uses this.
	// Both end up having the same value in general, but Hostname is
	// more likely to have a value.
	hostname, err := os.Hostname()
	if err != nil {
		klog.Fatalf("Getting hostname: %v", err)
	}

	// Construct update-operator.
	operatorInstance, err := operator.New(operator.Config{
		Client:                  client,
		BeforeRebootAnnotations: flags.beforeRebootAnnotations,
		AfterRebootAnnotations:  flags.afterRebootAnnotations,
		RebootWindowStart:       *flags.rebootWindowStart,
		RebootWindowLength:      *flags.rebootWindowLength,
		Namespace:               namespace,
		LockID:                  hostname,
	})
	if err != nil {
		klog.Fatalf("Failed to initialize %s: %v", os.Args[0], err)
	}

	klog.Infof("%s running", os.Args[0])

	// Run operator until the stop channel is closed.
	stop := make(chan struct{})
	defer close(stop)

	if err := operatorInstance.Run(stop); err != nil {
		klog.Fatalf("Error while running %s: %v", os.Args[0], err)
	}
}
