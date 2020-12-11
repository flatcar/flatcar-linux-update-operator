package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/coreos/pkg/flagutil"
	"k8s.io/klog/v2"

	"github.com/kinvolk/flatcar-linux-update-operator/pkg/k8sutil"
	"github.com/kinvolk/flatcar-linux-update-operator/pkg/operator"
	"github.com/kinvolk/flatcar-linux-update-operator/pkg/version"
)

type flags struct {
	beforeRebootAnnotations flagutil.StringSliceFlag
	afterRebootAnnotations  flagutil.StringSliceFlag
	kubeconfig              *string
	autoLabelContainerLinux *bool
	rebootWindowStart       *string
	rebootWindowLength      *string
	printVersion            *bool
}

func handleFlags() *flags {
	f := &flags{
		kubeconfig: flag.String("kubeconfig", "",
			"Path to a kubeconfig file. Default to the in-cluster config if not provided."),

		autoLabelContainerLinux: flag.Bool("auto-label-flatcar-linux", false,
			"Auto-label Flatcar Container Linux nodes with agent=true (convenience)"),

		rebootWindowStart: flag.String("reboot-window-start", "",
			"Day of week ('Sun', 'Mon', ...; optional) and time of day at which the reboot window starts. "+
				"E.g. 'Mon 14:00', '11:00'"),

		rebootWindowLength: flag.String("reboot-window-length", "", "Length of the reboot window. E.g. '1h30m'"),
		printVersion:       flag.Bool("version", false, "Print version and exit"),
	}

	flag.Var(&f.beforeRebootAnnotations, "before-reboot-annotations",
		"List of comma-separated Kubernetes node annotations that must be set to 'true' before a reboot is allowed")

	flag.Var(&f.afterRebootAnnotations, "after-reboot-annotations",
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
	if *f.kubeconfig == "" {
		*f.kubeconfig = os.Getenv("KUBECONFIG")
	}

	return f
}

func main() {
	f := handleFlags()

	if *f.printVersion {
		fmt.Println(version.Format())
		os.Exit(0)
	}

	// Create Kubernetes client (clientset).
	client, err := k8sutil.GetClient(*f.kubeconfig)
	if err != nil {
		klog.Fatalf("Failed to create Kubernetes client: %v", err)
	}

	// Construct update-operator.
	o, err := operator.New(operator.Config{
		Client:                  client,
		AutoLabelContainerLinux: *f.autoLabelContainerLinux,
		BeforeRebootAnnotations: f.beforeRebootAnnotations,
		AfterRebootAnnotations:  f.afterRebootAnnotations,
		RebootWindowStart:       *f.rebootWindowStart,
		RebootWindowLength:      *f.rebootWindowLength,
	})
	if err != nil {
		klog.Fatalf("Failed to initialize %s: %v", os.Args[0], err)
	}

	klog.Infof("%s running", os.Args[0])

	// Run operator until the stop channel is closed.
	stop := make(chan struct{})
	defer close(stop)

	if err := o.Run(stop); err != nil {
		klog.Fatalf("Error while running %s: %v", os.Args[0], err)
	}
}
