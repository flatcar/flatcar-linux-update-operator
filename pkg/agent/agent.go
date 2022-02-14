// Package agent implements Flatcar Linux Update Operator agent, which role is to
// run on every Flatcar Node on the cluster, watch update_engine for status updates,
// propagate them to operator via Node labels and annotations and react on operator
// decisions about when to drain a node and reboot to finish upgrade process.
package agent

import (
	"bufio"
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	appsv1client "k8s.io/client-go/kubernetes/typed/apps/v1"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	watchtools "k8s.io/client-go/tools/watch"
	"k8s.io/klog/v2"

	"github.com/flatcar-linux/flatcar-linux-update-operator/pkg/constants"
	"github.com/flatcar-linux/flatcar-linux-update-operator/pkg/k8sutil"
	"github.com/flatcar-linux/flatcar-linux-update-operator/pkg/updateengine"
)

// Config represents configurable options for agent.
type Config struct {
	NodeName               string
	PodDeletionGracePeriod time.Duration
	Clientset              kubernetes.Interface
	StatusReceiver         StatusReceiver
	Rebooter               Rebooter
	HostFilesPrefix        string
}

// StatusReceiver describe dependency of object providing status updates from update_engine.
type StatusReceiver interface {
	ReceiveStatuses(rcvr chan<- updateengine.Status, stop <-chan struct{})
}

// Rebooter describes dependency of object providing capability of rebooting host machine.
type Rebooter interface {
	Reboot(bool)
}

// Klocksmith represents capabilities of agent.
type Klocksmith interface {
	Run(stop <-chan struct{}) error
}

// Klocksmith implements agent part of FLUO.
type klocksmith struct {
	nodeName        string
	pg              corev1client.PodsGetter
	nc              corev1client.NodeInterface
	dsg             appsv1client.DaemonSetsGetter
	ue              StatusReceiver
	lc              Rebooter
	reapTimeout     time.Duration
	hostFilesPrefix string
}

const (
	defaultPollInterval     = 10 * time.Second
	maxOperatorResponseTime = 24 * time.Hour

	updateConfPath         = "/usr/share/flatcar/update.conf"
	updateConfOverridePath = "/etc/flatcar/update.conf"
	osReleasePath          = "/etc/os-release"
)

var shouldRebootSelector = fields.Set(map[string]string{
	constants.AnnotationOkToReboot:   constants.True,
	constants.AnnotationRebootNeeded: constants.True,
}).AsSelector()

// New returns initialized klocksmith.
func New(config *Config) (Klocksmith, error) {
	if config.Clientset == nil {
		return nil, fmt.Errorf("no clientset configured")
	}

	if config.StatusReceiver == nil {
		return nil, fmt.Errorf("no status receiver configured")
	}

	if config.Rebooter == nil {
		return nil, fmt.Errorf("no rebooter given")
	}

	if config.NodeName == "" {
		return nil, fmt.Errorf("node name can't be empty")
	}

	return &klocksmith{
		nodeName:        config.NodeName,
		pg:              config.Clientset.CoreV1(),
		dsg:             config.Clientset.AppsV1(),
		nc:              config.Clientset.CoreV1().Nodes(),
		ue:              config.StatusReceiver,
		lc:              config.Rebooter,
		reapTimeout:     config.PodDeletionGracePeriod,
		hostFilesPrefix: config.HostFilesPrefix,
	}, nil
}

// Run starts the agent to listen for an update_engine reboot signal and react
// by draining pods and rebooting. Runs until the stop channel is closed.
func (k *klocksmith) Run(stop <-chan struct{}) error {
	klog.V(5).Info("Starting agent")

	defer klog.V(5).Info("Stopping agent")

	// Agent process should reboot the node, no need to loop.
	if err := k.process(stop); err != nil {
		klog.Errorf("Error running agent process: %v", err)

		return fmt.Errorf("processing: %w", err)
	}

	return nil
}

// process performs the agent reconciliation to reboot the node or stops when
// the stop channel is closed.
//
//nolint:funlen,cyclop // This will be refactored once we have tests in place.
func (k *klocksmith) process(stop <-chan struct{}) error {
	ctx := context.TODO()

	klog.Info("Setting info labels")

	if err := k.setInfoLabels(ctx); err != nil {
		return fmt.Errorf("setting node info: %w", err)
	}

	klog.Info("Checking annotations")

	node, err := k8sutil.GetNodeRetry(ctx, k.nc, k.nodeName)
	if err != nil {
		return fmt.Errorf("getting node %q: %w", k.nodeName, err)
	}

	// Only make a node schedulable if a reboot was in progress. This prevents a node from being made schedulable
	// if it was made unschedulable by something other than the agent.
	//
	//nolint:lll // To be addressed.
	madeUnschedulableAnnotation, madeUnschedulableAnnotationExists := node.Annotations[constants.AnnotationAgentMadeUnschedulable]
	makeSchedulable := madeUnschedulableAnnotation == constants.True

	// Set flatcar-linux.net/update1/reboot-in-progress=false and
	// flatcar-linux.net/update1/reboot-needed=false.
	anno := map[string]string{
		constants.AnnotationRebootInProgress: constants.False,
		constants.AnnotationRebootNeeded:     constants.False,
	}
	labels := map[string]string{
		constants.LabelRebootNeeded: constants.False,
	}

	klog.Infof("Setting annotations %#v", anno)

	if err := k8sutil.SetNodeAnnotationsLabels(ctx, k.nc, k.nodeName, anno, labels); err != nil {
		return fmt.Errorf("setting node %q labels and annotations: %w", k.nodeName, err)
	}

	// Since we set 'reboot-needed=false', 'ok-to-reboot' should clear.
	// Wait for it to do so, else we might start reboot-looping.
	if err := k.waitForNotOkToReboot(ctx); err != nil {
		return fmt.Errorf("waiting for not ok to reboot signal from operator: %w", err)
	}

	if makeSchedulable {
		// We are schedulable now.
		klog.Info("Marking node as schedulable")

		if err := k8sutil.Unschedulable(ctx, k.nc, k.nodeName, false); err != nil {
			return fmt.Errorf("marking node %q as unschedulable: %w", k.nodeName, err)
		}

		anno = map[string]string{
			constants.AnnotationAgentMadeUnschedulable: constants.False,
		}

		klog.Infof("Setting annotations %#v", anno)

		if err := k8sutil.SetNodeAnnotations(ctx, k.nc, k.nodeName, anno); err != nil {
			return fmt.Errorf("setting node %q annotations: %w", k.nodeName, err)
		}
	} else if madeUnschedulableAnnotationExists { // Annotation exists so node was marked unschedulable by external source.
		klog.Info("Skipping marking node as schedulable -- node was marked unschedulable by an external source")
	}

	// Watch update engine for status updates.
	go k.watchUpdateStatus(ctx, k.updateStatusCallback, stop)

	// Block until constants.AnnotationOkToReboot is set.
	for {
		klog.Infof("Waiting for ok-to-reboot from controller...")

		err := k.waitForOkToReboot(ctx)
		if err == nil {
			// Time to reboot.
			break
		}

		klog.Warningf("Error waiting for an ok-to-reboot: %v", err)
	}

	klog.Info("Checking if node is already unschedulable")

	node, err = k8sutil.GetNodeRetry(ctx, k.nc, k.nodeName)
	if err != nil {
		return fmt.Errorf("getting node %q: %w", k.nodeName, err)
	}

	alreadyUnschedulable := node.Spec.Unschedulable

	// Set constants.AnnotationRebootInProgress and drain self.
	anno = map[string]string{
		constants.AnnotationRebootInProgress: constants.True,
	}

	if !alreadyUnschedulable {
		anno[constants.AnnotationAgentMadeUnschedulable] = constants.True
	}

	klog.Infof("Setting annotations %#v", anno)

	if err := k8sutil.SetNodeAnnotations(ctx, k.nc, k.nodeName, anno); err != nil {
		return fmt.Errorf("setting node %q annotations: %w", k.nodeName, err)
	}

	// Drain self equates to:
	// 1. set Unschedulable if necessary
	// 2. delete all pods
	// unlike `kubectl drain`, we do not care about emptyDir or orphan pods
	// ('any pods that are neither mirror pods nor managed by
	// ReplicationController, ReplicaSet, DaemonSet or Job').

	if !alreadyUnschedulable {
		klog.Info("Marking node as unschedulable")

		if err := k8sutil.Unschedulable(ctx, k.nc, k.nodeName, true); err != nil {
			return fmt.Errorf("marking node %q as unschedulable: %w", k.nodeName, err)
		}
	} else {
		klog.Info("Node already marked as unschedulable")
	}

	klog.Info("Getting pod list for deletion")

	pods, err := k.getPodsForDeletion(ctx)
	if err != nil {
		return fmt.Errorf("getting list of pods for deletion: %w", err)
	}

	// Delete the pods.
	// TODO(mischief): Explicitly don't terminate self? we'll probably just be a
	// Mirror pod or daemonset anyway..
	klog.Infof("Deleting %d pods", len(pods))

	for _, pod := range pods {
		klog.Infof("Terminating pod %q...", pod.Name)

		if err := k.pg.Pods(pod.Namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{}); err != nil {
			// Continue anyways, the reboot should terminate it.
			klog.Errorf("Failed terminating pod %q: %v", pod.Name, err)
		}
	}

	// Wait for the pods to delete completely.
	//
	//nolint:varnamelen // Conventional name.
	wg := sync.WaitGroup{}

	for _, pod := range pods {
		wg.Add(1)

		go func(pod corev1.Pod) {
			klog.Infof("Waiting for pod %q to terminate", pod.Name)

			if err := k.waitForPodDeletion(ctx, pod); err != nil {
				klog.Errorf("Skipping wait on pod %q: %v", pod.Name, err)
			}

			wg.Done()
		}(pod)
	}

	wg.Wait()

	klog.Info("Node drained, rebooting")

	// Reboot.
	k.lc.Reboot(false)

	// Cross fingers.
	sleepOrDone(24*7*time.Hour, stop)

	return nil
}

// updateStatusCallback receives Status messages from update engine. If the
// status is UpdateStatusUpdatedNeedReboot, indicate that with a label on our
// node.
func (k *klocksmith) updateStatusCallback(ctx context.Context, status updateengine.Status) {
	klog.Info("Updating status")

	// update our status.
	anno := map[string]string{
		constants.AnnotationStatus:          status.CurrentOperation,
		constants.AnnotationLastCheckedTime: fmt.Sprintf("%d", status.LastCheckedTime),
		constants.AnnotationNewVersion:      status.NewVersion,
	}

	labels := map[string]string{}

	// Indicate we need a reboot.
	if status.CurrentOperation == updateengine.UpdateStatusUpdatedNeedReboot {
		klog.Info("Indicating a reboot is needed")

		anno[constants.AnnotationRebootNeeded] = constants.True
		labels[constants.LabelRebootNeeded] = constants.True
	}

	err := wait.PollImmediateUntil(defaultPollInterval, func() (bool, error) {
		if err := k8sutil.SetNodeAnnotationsLabels(ctx, k.nc, k.nodeName, anno, labels); err != nil {
			klog.Errorf("Failed to set annotation %q: %v", constants.AnnotationStatus, err)

			return false, nil
		}

		return true, nil
	}, wait.NeverStop)
	if err != nil {
		klog.Errorf("Failed updating node annotations and labels: %v", err)
	}
}

// setInfoLabels labels our node with helpful info about Flatcar Container Linux.
func (k *klocksmith) setInfoLabels(ctx context.Context) error {
	versionInfo, err := getVersionInfo(k.hostFilesPrefix)
	if err != nil {
		return fmt.Errorf("getting version info: %w", err)
	}

	labels := map[string]string{
		constants.LabelID:      versionInfo.id,
		constants.LabelGroup:   versionInfo.group,
		constants.LabelVersion: versionInfo.version,
	}

	if err := k8sutil.SetNodeLabels(ctx, k.nc, k.nodeName, labels); err != nil {
		return fmt.Errorf("setting node %q labels: %w", k.nodeName, err)
	}

	return nil
}

type statusUpdateF func(context.Context, updateengine.Status)

func (k *klocksmith) watchUpdateStatus(ctx context.Context, update statusUpdateF, stop <-chan struct{}) {
	klog.Info("Beginning to watch update_engine status")

	oldOperation := ""
	ch := make(chan updateengine.Status, 1)

	go k.ue.ReceiveStatuses(ch, stop)

	for status := range ch {
		if status.CurrentOperation != oldOperation && update != nil {
			update(ctx, status)
			oldOperation = status.CurrentOperation
		}
	}
}

// waitForOkToReboot waits for both 'ok-to-reboot' and 'needs-reboot' to be true.
func (k *klocksmith) waitForOkToReboot(ctx context.Context) error {
	node, err := k.nc.Get(ctx, k.nodeName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("getting self node (%q): %w", k.nodeName, err)
	}

	okToReboot := node.Annotations[constants.AnnotationOkToReboot] == constants.True
	rebootNeeded := node.Annotations[constants.AnnotationRebootNeeded] == constants.True

	if okToReboot && rebootNeeded {
		return nil
	}

	// XXX: Set timeout > 0?
	watcher, err := k.nc.Watch(ctx, metav1.ListOptions{
		FieldSelector:   fields.OneTermEqualSelector("metadata.name", node.Name).String(),
		ResourceVersion: node.ResourceVersion,
	})
	if err != nil {
		return fmt.Errorf("creating watcher for self node (%q): %w", k.nodeName, err)
	}

	// Hopefully 24 hours is enough time between indicating we need a
	// reboot and the controller telling us to do it.
	ctx, _ = watchtools.ContextWithOptionalTimeout(ctx, maxOperatorResponseTime)

	event, err := watchtools.UntilWithoutRetry(ctx, watcher, k8sutil.NodeAnnotationCondition(shouldRebootSelector))
	if err != nil {
		return fmt.Errorf("waiting for annotation %q failed: %w", constants.AnnotationOkToReboot, err)
	}

	// Sanity check.
	no, ok := event.Object.(*corev1.Node)
	if !ok {
		panic("event contains a non-*api.Node object")
	}

	if no.Annotations[constants.AnnotationOkToReboot] != constants.True {
		panic("event did not contain annotation expected")
	}

	return nil
}

//nolint:cyclop // We will deal with complexity once we have proper tests.
func (k *klocksmith) waitForNotOkToReboot(ctx context.Context) error {
	node, err := k.nc.Get(ctx, k.nodeName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("getting self node (%q): %w", k.nodeName, err)
	}

	if node.Annotations[constants.AnnotationOkToReboot] != constants.True {
		return nil
	}

	// XXX: Set timeout > 0?
	watcher, err := k.nc.Watch(ctx, metav1.ListOptions{
		FieldSelector:   fields.OneTermEqualSelector("metadata.name", node.Name).String(),
		ResourceVersion: node.ResourceVersion,
	})
	if err != nil {
		return fmt.Errorf("creating watcher for self node (%q): %w", k.nodeName, err)
	}

	// Within 24 hours of indicating we don't need a reboot we should be given a not-ok.
	// If that isn't the case, it likely means the operator isn't running, and
	// we'll just crash-loop in that case, and hopefully that will help the user realize something's wrong.
	// Use a custom condition function to use the more correct 'OkToReboot !=
	// true' vs '== False'; due to the operator matching on '== True', and not
	// going out of its way to convert '' => 'False', checking the exact inverse
	// of what the operator checks is the correct thing to do.
	ctx, _ = watchtools.ContextWithOptionalTimeout(ctx, maxOperatorResponseTime)

	watchF := func(event watch.Event) (bool, error) {
		//nolint:exhaustive // Handle for event type Bookmark will be added once we have tests in place.
		switch event.Type {
		case watch.Error:
			return false, fmt.Errorf("watching node: %v", event.Object)
		case watch.Deleted:
			return false, fmt.Errorf("our node was deleted while we were waiting for ready")
		case watch.Added, watch.Modified:
			//nolint:forcetypeassert // Will be addressed once we have proper tests in place.
			return event.Object.(*corev1.Node).Annotations[constants.AnnotationOkToReboot] != constants.True, nil
		default:
			return false, fmt.Errorf("unknown event type: %v", event.Type)
		}
	}

	event, err := watchtools.UntilWithoutRetry(ctx, watcher, watchF)
	if err != nil {
		return fmt.Errorf("waiting for annotation %q: %w", constants.AnnotationOkToReboot, err)
	}

	// Sanity check.
	no, ok := event.Object.(*corev1.Node)
	if !ok {
		return fmt.Errorf("object received in event is not Node, got: %#v", event.Object)
	}

	if no.Annotations[constants.AnnotationOkToReboot] == constants.True {
		panic("event did not contain expected annotation")
	}

	return nil
}

func (k *klocksmith) getPodsForDeletion(ctx context.Context) ([]corev1.Pod, error) {
	pods, err := k8sutil.GetPodsForDeletion(ctx, k.pg, k.dsg, k.nodeName)
	if err != nil {
		return nil, fmt.Errorf("getting list of pods for deletion: %w", err)
	}

	// XXX: Ignoring kube-system is a simple way to avoid eviciting
	// critical components such as kube-scheduler and
	// kube-controller-manager.

	pods = k8sutil.FilterPods(pods, func(p *corev1.Pod) bool {
		return p.Namespace != "kube-system"
	})

	return pods, nil
}

// waitForPodDeletion waits for a pod to be deleted.
func (k *klocksmith) waitForPodDeletion(ctx context.Context, pod corev1.Pod) error {
	return wait.PollImmediate(defaultPollInterval, k.reapTimeout, func() (bool, error) {
		p, err := k.pg.Pods(pod.Namespace).Get(ctx, pod.Name, metav1.GetOptions{})
		if errors.IsNotFound(err) || (p != nil && p.ObjectMeta.UID != pod.ObjectMeta.UID) {
			klog.Infof("Deleted pod %q", pod.Name)

			return true, nil
		}

		// Most errors will be transient. Log the error and continue polling.
		if err != nil {
			klog.Errorf("Failed to get pod %q: %v", pod.Name, err)
		}

		return false, nil
	})
}

// sleepOrDone blocks until the done channel receives
// or until at least the duration d has elapsed, whichever comes first. This
// is similar to time.Sleep(d), except it can be interrupted.
func sleepOrDone(d time.Duration, done <-chan struct{}) {
	sleep := time.NewTimer(d)
	defer sleep.Stop()
	select {
	case <-sleep.C:
		return
	case <-done:
		return
	}
}

// splitNewlineEnv splits newline-delimited KEY=VAL pairs and puts values into given map.
func splitNewlineEnv(envVars map[string]string, envs string) {
	sc := bufio.NewScanner(strings.NewReader(envs))
	for sc.Scan() {
		//nolint:gomnd // TODO.
		spl := strings.SplitN(sc.Text(), "=", 2)

		// Just skip empty lines or lines without a value.
		if len(spl) == 1 {
			continue
		}

		envVars[spl[0]] = spl[1]
	}
}

// versionInfo contains Flatcar version and update information.
type versionInfo struct {
	id      string
	group   string
	version string
}

func getUpdateMap(filesPathPrefix string) (map[string]string, error) {
	infomap := map[string]string{}

	updateConfPathWithPrefix := filepath.Join(filesPathPrefix, updateConfPath)

	// This file should always be present on Flatcar.
	b, err := ioutil.ReadFile(updateConfPathWithPrefix)
	if err != nil {
		return nil, fmt.Errorf("reading file %q: %w", updateConfPathWithPrefix, err)
	}

	splitNewlineEnv(infomap, string(b))

	updateConfOverridePathWithPrefix := filepath.Join(filesPathPrefix, updateConfOverridePath)

	updateConfOverride, err := ioutil.ReadFile(updateConfOverridePathWithPrefix)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("reading file %q: %w", updateConfOverridePathWithPrefix, err)
		}

		klog.Infof("Skipping missing update.conf: %v", err)
	}

	splitNewlineEnv(infomap, string(updateConfOverride))

	return infomap, nil
}

func getReleaseMap(filesPathPrefix string) (map[string]string, error) {
	infomap := map[string]string{}

	osReleasePathWithPrefix := filepath.Join(filesPathPrefix, osReleasePath)

	// This file should always be present on Flatcar.
	b, err := ioutil.ReadFile(osReleasePathWithPrefix)
	if err != nil {
		return nil, fmt.Errorf("reading file %q: %w", osReleasePathWithPrefix, err)
	}

	splitNewlineEnv(infomap, string(b))

	return infomap, nil
}

// GetVersionInfo returns VersionInfo from the current Flatcar system.
//
// Should probably live in a different package.
func getVersionInfo(filesPathPrefix string) (*versionInfo, error) {
	updateconf, err := getUpdateMap(filesPathPrefix)
	if err != nil {
		return nil, fmt.Errorf("getting update configuration: %w", err)
	}

	osrelease, err := getReleaseMap(filesPathPrefix)
	if err != nil {
		return nil, fmt.Errorf("getting OS release info: %w", err)
	}

	return &versionInfo{
		id:      osrelease["ID"],
		group:   updateconf["GROUP"],
		version: osrelease["VERSION"],
	}, nil
}
