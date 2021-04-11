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
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-systemd/login1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	watchtools "k8s.io/client-go/tools/watch"
	"k8s.io/klog/v2"

	"github.com/kinvolk/flatcar-linux-update-operator/pkg/constants"
	"github.com/kinvolk/flatcar-linux-update-operator/pkg/k8sutil"
	"github.com/kinvolk/flatcar-linux-update-operator/pkg/updateengine"
)

// Klocksmith implements agent part of FLUO.
type Klocksmith struct {
	node        string
	kc          kubernetes.Interface
	nc          corev1client.NodeInterface
	ue          *updateengine.Client
	lc          *login1.Conn
	reapTimeout time.Duration
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

// New returns initialized Klocksmith.
func New(node string, reapTimeout time.Duration) (*Klocksmith, error) {
	// Set up kubernetes in-cluster client.
	kc, err := k8sutil.GetClient("")
	if err != nil {
		return nil, fmt.Errorf("error creating kubernetes client: %w", err)
	}

	// Node interface.
	nc := kc.CoreV1().Nodes()

	// Set up update_engine client.
	ue, err := updateengine.New()
	if err != nil {
		return nil, fmt.Errorf("error establishing connection to update_engine dbus: %w", err)
	}

	// Set up login1 client for our eventual reboot.
	lc, err := login1.New()
	if err != nil {
		return nil, fmt.Errorf("error establishing connection to logind dbus: %w", err)
	}

	return &Klocksmith{
		node:        node,
		kc:          kc,
		nc:          nc,
		ue:          ue,
		lc:          lc,
		reapTimeout: reapTimeout,
	}, nil
}

// Run starts the agent to listen for an update_engine reboot signal and react
// by draining pods and rebooting. Runs until the stop channel is closed.
func (k *Klocksmith) Run(stop <-chan struct{}) {
	klog.V(5).Info("Starting agent")

	// Agent process should reboot the node, no need to loop.
	if err := k.process(stop); err != nil {
		klog.Errorf("Error running agent process: %w", err)
	}

	klog.V(5).Info("Stopping agent")
}

// process performs the agent reconciliation to reboot the node or stops when
// the stop channel is closed.
func (k *Klocksmith) process(stop <-chan struct{}) error {
	klog.Info("Setting info labels")

	if err := k.setInfoLabels(); err != nil {
		return fmt.Errorf("failed to set node info: %w", err)
	}

	klog.Info("Checking annotations")

	node, err := k8sutil.GetNodeRetry(k.nc, k.node)
	if err != nil {
		return fmt.Errorf("getting node %q: %w", k.node, err)
	}

	// Only make a node schedulable if a reboot was in progress. This prevents a node from being made schedulable
	// if it was made unschedulable by something other than the agent.
	//
	//nolint:lll
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

	if err := k8sutil.SetNodeAnnotationsLabels(k.nc, k.node, anno, labels); err != nil {
		return fmt.Errorf("setting node %q labels and annotations: %w", k.node, err)
	}

	// Since we set 'reboot-needed=false', 'ok-to-reboot' should clear.
	// Wait for it to do so, else we might start reboot-looping.
	if err := k.waitForNotOkToReboot(); err != nil {
		return err
	}

	if makeSchedulable {
		// We are schedulable now.
		klog.Info("Marking node as schedulable")

		if err := k8sutil.Unschedulable(k.nc, k.node, false); err != nil {
			return fmt.Errorf("marking node %q as unschedulable: %w", k.node, err)
		}

		anno = map[string]string{
			constants.AnnotationAgentMadeUnschedulable: constants.False,
		}

		klog.Infof("Setting annotations %#v", anno)

		if err := k8sutil.SetNodeAnnotations(k.nc, k.node, anno); err != nil {
			return fmt.Errorf("setting node %q annotations: %w", k.node, err)
		}
	} else if madeUnschedulableAnnotationExists { // Annotation exists so node was marked unschedulable by external source.
		klog.Info("Skipping marking node as schedulable -- node was marked unschedulable by an external source")
	}

	// Watch update engine for status updates.
	go k.watchUpdateStatus(k.updateStatusCallback, stop)

	// Block until constants.AnnotationOkToReboot is set.
	for {
		klog.Infof("Waiting for ok-to-reboot from controller...")

		err := k.waitForOkToReboot()
		if err == nil {
			// Time to reboot.
			break
		}

		klog.Warningf("error waiting for an ok-to-reboot: %w", err)
	}

	klog.Info("Checking if node is already unschedulable")

	node, err = k8sutil.GetNodeRetry(k.nc, k.node)
	if err != nil {
		return fmt.Errorf("getting node %q: %w", k.node, err)
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

	if err := k8sutil.SetNodeAnnotations(k.nc, k.node, anno); err != nil {
		return fmt.Errorf("setting node %q annotations: %w", k.node, err)
	}

	// Drain self equates to:
	// 1. set Unschedulable if necessary
	// 2. delete all pods
	// unlike `kubectl drain`, we do not care about emptyDir or orphan pods
	// ('any pods that are neither mirror pods nor managed by
	// ReplicationController, ReplicaSet, DaemonSet or Job').

	if !alreadyUnschedulable {
		klog.Info("Marking node as unschedulable")

		if err := k8sutil.Unschedulable(k.nc, k.node, true); err != nil {
			return fmt.Errorf("marking node %q as unschedulable: %w", k.node, err)
		}
	} else {
		klog.Info("Node already marked as unschedulable")
	}

	klog.Info("Getting pod list for deletion")

	pods, err := k.getPodsForDeletion()
	if err != nil {
		return err
	}

	// Delete the pods.
	// TODO(mischief): explicitly don't terminate self? we'll probably just be a
	// Mirror pod or daemonset anyway..
	klog.Infof("Deleting %d pods", len(pods))

	for _, pod := range pods {
		klog.Infof("Terminating pod %q...", pod.Name)

		if err := k.kc.CoreV1().Pods(pod.Namespace).Delete(context.TODO(), pod.Name, metav1.DeleteOptions{}); err != nil {
			// Continue anyways, the reboot should terminate it.
			klog.Errorf("failed terminating pod %q: %v", pod.Name, err)
		}
	}

	// Wait for the pods to delete completely.
	wg := sync.WaitGroup{}

	for _, pod := range pods {
		wg.Add(1)

		go func(pod corev1.Pod) {
			klog.Infof("Waiting for pod %q to terminate", pod.Name)

			if err := k.waitForPodDeletion(pod); err != nil {
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
func (k *Klocksmith) updateStatusCallback(s updateengine.Status) {
	klog.Info("Updating status")
	// update our status
	anno := map[string]string{
		constants.AnnotationStatus:          s.CurrentOperation,
		constants.AnnotationLastCheckedTime: fmt.Sprintf("%d", s.LastCheckedTime),
		constants.AnnotationNewVersion:      s.NewVersion,
	}

	labels := map[string]string{}

	// Indicate we need a reboot.
	if s.CurrentOperation == updateengine.UpdateStatusUpdatedNeedReboot {
		klog.Info("Indicating a reboot is needed")

		anno[constants.AnnotationRebootNeeded] = constants.True
		labels[constants.LabelRebootNeeded] = constants.True
	}

	err := wait.PollUntil(defaultPollInterval, func() (bool, error) {
		if err := k8sutil.SetNodeAnnotationsLabels(k.nc, k.node, anno, labels); err != nil {
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
func (k *Klocksmith) setInfoLabels() error {
	vi, err := getVersionInfo()
	if err != nil {
		return fmt.Errorf("failed to get version info: %w", err)
	}

	labels := map[string]string{
		constants.LabelID:      vi.id,
		constants.LabelGroup:   vi.group,
		constants.LabelVersion: vi.version,
	}

	if err := k8sutil.SetNodeLabels(k.nc, k.node, labels); err != nil {
		return fmt.Errorf("setting node %q labels: %w", k.node, err)
	}

	return nil
}

func (k *Klocksmith) watchUpdateStatus(update func(s updateengine.Status), stop <-chan struct{}) {
	klog.Info("Beginning to watch update_engine status")

	oldOperation := ""
	ch := make(chan updateengine.Status, 1)

	go k.ue.ReceiveStatuses(ch, stop)

	for status := range ch {
		if status.CurrentOperation != oldOperation && update != nil {
			update(status)
			oldOperation = status.CurrentOperation
		}
	}
}

// waitForOkToReboot waits for both 'ok-to-reboot' and 'needs-reboot' to be true.
func (k *Klocksmith) waitForOkToReboot() error {
	n, err := k.nc.Get(context.TODO(), k.node, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get self node (%q): %w", k.node, err)
	}

	okToReboot := n.Annotations[constants.AnnotationOkToReboot] == constants.True
	rebootNeeded := n.Annotations[constants.AnnotationRebootNeeded] == constants.True

	if okToReboot && rebootNeeded {
		return nil
	}

	// XXX: set timeout > 0?
	watcher, err := k.nc.Watch(context.TODO(), metav1.ListOptions{
		FieldSelector:   fields.OneTermEqualSelector("metadata.name", n.Name).String(),
		ResourceVersion: n.ResourceVersion,
	})
	if err != nil {
		return fmt.Errorf("failed to watch self node (%q): %w", k.node, err)
	}

	// Hopefully 24 hours is enough time between indicating we need a
	// reboot and the controller telling us to do it.
	ctx, _ := watchtools.ContextWithOptionalTimeout(context.Background(), maxOperatorResponseTime)

	ev, err := watchtools.UntilWithoutRetry(ctx, watcher, k8sutil.NodeAnnotationCondition(shouldRebootSelector))
	if err != nil {
		return fmt.Errorf("waiting for annotation %q failed: %w", constants.AnnotationOkToReboot, err)
	}

	// Sanity check.
	no, ok := ev.Object.(*corev1.Node)
	if !ok {
		panic("event contains a non-*api.Node object")
	}

	if no.Annotations[constants.AnnotationOkToReboot] != constants.True {
		panic("event did not contain annotation expected")
	}

	return nil
}

func (k *Klocksmith) waitForNotOkToReboot() error {
	n, err := k.nc.Get(context.TODO(), k.node, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get self node (%q): %w", k.node, err)
	}

	if n.Annotations[constants.AnnotationOkToReboot] != constants.True {
		return nil
	}

	// XXX: set timeout > 0?
	watcher, err := k.nc.Watch(context.TODO(), metav1.ListOptions{
		FieldSelector:   fields.OneTermEqualSelector("metadata.name", n.Name).String(),
		ResourceVersion: n.ResourceVersion,
	})
	if err != nil {
		return fmt.Errorf("failed to watch self node (%q): %w", k.node, err)
	}

	// Within 24 hours of indicating we don't need a reboot we should be given a not-ok.
	// If that isn't the case, it likely means the operator isn't running, and
	// we'll just crash-loop in that case, and hopefully that will help the user realize something's wrong.
	// Use a custom condition function to use the more correct 'OkToReboot !=
	// true' vs '== False'; due to the operator matching on '== True', and not
	// going out of its way to convert '' => 'False', checking the exact inverse
	// of what the operator checks is the correct thing to do.
	ctx, _ := watchtools.ContextWithOptionalTimeout(context.Background(), maxOperatorResponseTime)

	ev, err := watchtools.UntilWithoutRetry(ctx, watcher, watchtools.ConditionFunc(func(event watch.Event) (bool, error) {
		switch event.Type {
		case watch.Error:
			return false, fmt.Errorf("error watching node: %v", event.Object)
		case watch.Deleted:
			return false, fmt.Errorf("our node was deleted while we were waiting for ready")
		case watch.Added, watch.Modified:
			return event.Object.(*corev1.Node).Annotations[constants.AnnotationOkToReboot] != constants.True, nil
		default:
			return false, fmt.Errorf("unknown event type: %v", event.Type)
		}
	}))
	if err != nil {
		return fmt.Errorf("waiting for annotation %q failed: %w", constants.AnnotationOkToReboot, err)
	}

	// Sanity check.
	no, ok := ev.Object.(*corev1.Node)
	if !ok {
		return fmt.Errorf("object received in event is not Node, got: %#v", ev.Object)
	}

	if no.Annotations[constants.AnnotationOkToReboot] == constants.True {
		panic("event did not contain annotation expected")
	}

	return nil
}

func (k *Klocksmith) getPodsForDeletion() ([]corev1.Pod, error) {
	pods, err := k8sutil.GetPodsForDeletion(k.kc, k.node)
	if err != nil {
		return nil, fmt.Errorf("failed to get list of pods for deletion: %w", err)
	}

	// XXX: ignoring kube-system is a simple way to avoid eviciting
	// critical components such as kube-scheduler and
	// kube-controller-manager.

	pods = k8sutil.FilterPods(pods, func(p *corev1.Pod) bool {
		return p.Namespace != "kube-system"
	})

	return pods, nil
}

// waitForPodDeletion waits for a pod to be deleted.
func (k *Klocksmith) waitForPodDeletion(pod corev1.Pod) error {
	return wait.PollImmediate(defaultPollInterval, k.reapTimeout, func() (bool, error) {
		p, err := k.kc.CoreV1().Pods(pod.Namespace).Get(context.TODO(), pod.Name, metav1.GetOptions{})
		if errors.IsNotFound(err) || (p != nil && p.ObjectMeta.UID != pod.ObjectMeta.UID) {
			klog.Infof("Deleted pod %q", pod.Name)

			return true, nil
		}

		// Most errors will be transient. log the error and continue
		// polling.
		if err != nil {
			klog.Errorf("Failed to get pod %q: %v", pod.Name, err)
		}

		return false, nil
	})
}

// sleepOrDone pauses the current goroutine until the done channel receives
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

// splitNewlineEnv splits newline-delimited KEY=VAL pairs and update map.
func splitNewlineEnv(m map[string]string, envs string) {
	sc := bufio.NewScanner(strings.NewReader(envs))
	for sc.Scan() {
		spl := strings.SplitN(sc.Text(), "=", 2)

		// Just skip empty lines or lines without a value.
		if len(spl) == 1 {
			continue
		}

		m[spl[0]] = spl[1]
	}
}

// versionInfo contains Flatcar version and update information.
type versionInfo struct {
	name    string
	id      string
	group   string
	version string
}

func getUpdateMap() (map[string]string, error) {
	infomap := map[string]string{}

	// This file should always be present on Flatcar.
	b, err := ioutil.ReadFile(updateConfPath)
	if err != nil {
		return nil, fmt.Errorf("reading file %q: %w", updateConfPath, err)
	}

	splitNewlineEnv(infomap, string(b))

	updateConfOverride, err := ioutil.ReadFile(updateConfOverridePath)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("reading file %q: %w", updateConfOverridePath, err)
		}

		klog.Infof("Skipping missing update.conf: %w", err)
	}

	splitNewlineEnv(infomap, string(updateConfOverride))

	return infomap, nil
}

func getReleaseMap() (map[string]string, error) {
	infomap := map[string]string{}

	// This file should always be present on Flatcar.
	b, err := ioutil.ReadFile(osReleasePath)
	if err != nil {
		return nil, fmt.Errorf("reading file %q: %w", osReleasePath, err)
	}

	splitNewlineEnv(infomap, string(b))

	return infomap, nil
}

// GetVersionInfo returns VersionInfo from the current Flatcar system.
//
// Should probably live in a different package.
func getVersionInfo() (*versionInfo, error) {
	updateconf, err := getUpdateMap()
	if err != nil {
		return nil, fmt.Errorf("unable to get update configuration: %w", err)
	}

	osrelease, err := getReleaseMap()
	if err != nil {
		return nil, fmt.Errorf("unable to get os release info: %w", err)
	}

	vi := &versionInfo{
		name:    osrelease["NAME"],
		id:      osrelease["ID"],
		group:   updateconf["GROUP"],
		version: osrelease["VERSION"],
	}

	return vi, nil
}
