package k8sutil

import (
	"bufio"
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"strings"

	v1api "k8s.io/api/core/v1"
	v1meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/watch"
	v1core "k8s.io/client-go/kubernetes/typed/core/v1"
	watchtools "k8s.io/client-go/tools/watch"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
)

const (
	updateConfPath         = "/usr/share/flatcar/update.conf"
	updateConfOverridePath = "/etc/flatcar/update.conf"
	osReleasePath          = "/etc/os-release"
)

// NodeAnnotationCondition returns a condition function that succeeds when a
// node being watched has an annotation of key equal to value.
func NodeAnnotationCondition(selector fields.Selector) watchtools.ConditionFunc {
	return func(event watch.Event) (bool, error) {
		if event.Type == watch.Modified {
			node := event.Object.(*v1api.Node)

			return selector.Matches(fields.Set(node.Annotations)), nil
		}

		return false, fmt.Errorf("unhandled watch case for %#v", event)
	}
}

// GetNodeRetry gets a node object, retrying up to DefaultBackoff number of times if it fails.
func GetNodeRetry(nc v1core.NodeInterface, node string) (*v1api.Node, error) {
	var apiNode *v1api.Node

	err := retry.OnError(retry.DefaultBackoff, func(error) bool { return true }, func() error {
		n, getErr := nc.Get(context.TODO(), node, v1meta.GetOptions{})
		if getErr != nil {
			return fmt.Errorf("failed to get node %q: %w", node, getErr)
		}

		apiNode = n

		return nil
	})

	return apiNode, err
}

// UpdateNodeRetry calls f to update a node object in Kubernetes.
// It will attempt to update the node by applying f to it up to DefaultBackoff
// number of times.
// f will be called each time since the node object will likely have changed if
// a retry is necessary.
func UpdateNodeRetry(nc v1core.NodeInterface, node string, f func(*v1api.Node)) error {
	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		n, getErr := nc.Get(context.TODO(), node, v1meta.GetOptions{})
		if getErr != nil {
			return fmt.Errorf("failed to get node %q: %w", node, getErr)
		}

		f(n)

		_, err := nc.Update(context.TODO(), n, v1meta.UpdateOptions{})

		return err //nolint:wrapcheck
	})
	if err != nil {
		// May be conflict if max retries were hit.
		return fmt.Errorf("unable to update node %q: %w", node, err)
	}

	return nil
}

// SetNodeLabels sets all keys in m to their respective values in
// node's labels.
func SetNodeLabels(nc v1core.NodeInterface, node string, m map[string]string) error {
	return UpdateNodeRetry(nc, node, func(n *v1api.Node) {
		for k, v := range m {
			n.Labels[k] = v
		}
	})
}

// SetNodeAnnotations sets all keys in m to their respective values in
// node's annotations.
func SetNodeAnnotations(nc v1core.NodeInterface, node string, m map[string]string) error {
	return UpdateNodeRetry(nc, node, func(n *v1api.Node) {
		for k, v := range m {
			n.Annotations[k] = v
		}
	})
}

// SetNodeAnnotationsLabels sets all keys in a and l to their values in
// node's annotations and labels, respectively.
func SetNodeAnnotationsLabels(nc v1core.NodeInterface, node string, a, l map[string]string) error {
	return UpdateNodeRetry(nc, node, func(n *v1api.Node) {
		for k, v := range a {
			n.Annotations[k] = v
		}

		for k, v := range l {
			n.Labels[k] = v
		}
	})
}

// DeleteNodeLabels deletes all keys in ks.
func DeleteNodeLabels(nc v1core.NodeInterface, node string, ks []string) error {
	return UpdateNodeRetry(nc, node, func(n *v1api.Node) {
		for _, k := range ks {
			delete(n.Labels, k)
		}
	})
}

// DeleteNodeAnnotations deletes all annotations with keys in ks.
func DeleteNodeAnnotations(nc v1core.NodeInterface, node string, ks []string) error {
	return UpdateNodeRetry(nc, node, func(n *v1api.Node) {
		for _, k := range ks {
			delete(n.Annotations, k)
		}
	})
}

// Unschedulable marks node as schedulable or unschedulable according to sched.
func Unschedulable(nc v1core.NodeInterface, node string, sched bool) error {
	n, err := nc.Get(context.TODO(), node, v1meta.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get node %q: %w", node, err)
	}

	n.Spec.Unschedulable = sched

	if err := retry.RetryOnConflict(retry.DefaultBackoff, func() (err error) {
		n, err = nc.Update(context.TODO(), n, v1meta.UpdateOptions{})

		return
	}); err != nil {
		return fmt.Errorf("unable to set 'Unschedulable' property of node %q to %t: %w", node, sched, err)
	}

	return nil
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

// VersionInfo contains CoreOS version and update information.
type VersionInfo struct {
	Name    string
	ID      string
	Group   string
	Version string
}

func getUpdateMap() (map[string]string, error) {
	infomap := map[string]string{}

	// This file should always be present on CoreOS.
	uconf, err := os.Open(updateConfPath)
	if err != nil {
		return nil, fmt.Errorf("opening file %q: %w", updateConfPath, err)
	}

	b, err := ioutil.ReadAll(uconf)

	uconf.Close()

	if err != nil {
		return nil, fmt.Errorf("reading file %q: %w", updateConfPath, err)
	}

	splitNewlineEnv(infomap, string(b))

	// If present and readable, this file has overrides.
	econf, err := os.Open(updateConfOverridePath)
	if err != nil {
		klog.Infof("Skipping missing update.conf: %w", err)
	}

	b, err = ioutil.ReadAll(econf)

	econf.Close()

	if err == nil {
		splitNewlineEnv(infomap, string(b))
	}

	return infomap, nil
}

func getReleaseMap() (map[string]string, error) {
	infomap := map[string]string{}

	// This file should always be present on CoreOS.
	osrelease, err := os.Open(osReleasePath)
	if err != nil {
		return nil, fmt.Errorf("opening file %q: %w", osReleasePath, err)
	}

	defer osrelease.Close()

	b, err := ioutil.ReadAll(osrelease)

	osrelease.Close()

	if err != nil {
		return nil, fmt.Errorf("reading file %q: %w", osReleasePath, err)
	}

	splitNewlineEnv(infomap, string(b))

	return infomap, nil
}

// GetVersionInfo returns VersionInfo from the current CoreOS system.
//
// Should probably live in a different package.
func GetVersionInfo() (*VersionInfo, error) {
	updateconf, err := getUpdateMap()
	if err != nil {
		return nil, fmt.Errorf("unable to get update configuration: %w", err)
	}

	osrelease, err := getReleaseMap()
	if err != nil {
		return nil, fmt.Errorf("unable to get os release info: %w", err)
	}

	vi := &VersionInfo{
		Name:    osrelease["NAME"],
		ID:      osrelease["ID"],
		Group:   updateconf["GROUP"],
		Version: osrelease["VERSION"],
	}

	return vi, nil
}
