package operator

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/klog/v2"

	"github.com/flatcar-linux/flatcar-linux-update-operator/pkg/constants"
	"github.com/flatcar-linux/flatcar-linux-update-operator/pkg/k8sutil"
)

var (
	// Labels nodes where update-agent should be scheduled.
	enableUpdateAgentLabel = map[string]string{
		constants.LabelUpdateAgentEnabled: constants.True,
	}

	// Label Requirement matching nodes which lack the update agent label.
	updateAgentLabelMissing = k8sutil.NewRequirementOrDie(
		constants.LabelUpdateAgentEnabled,
		selection.DoesNotExist,
		[]string{},
	)
)

// legacyLabeler finds Flatcar Container Linux nodes lacking the update-agent enabled
// label and adds the label set "true" so nodes opt-in to running update-agent.
//
// Important: This behavior supports clusters which may have nodes that do not
// have labels which an update-agent daemonset might node select upon. Even if
// all current nodes are labeled, auto-scaling groups may create nodes lacking
// the label. Retain this behavior to support upgrades of Tectonic clusters
// created at 1.6.
func (k *Kontroller) legacyLabeler(ctx context.Context) {
	klog.V(6).Infof("Starting Flatcar Container Linux node auto-labeler")

	nodelist, err := k.nc.List(ctx, metav1.ListOptions{})
	if err != nil {
		klog.Infof("Failed listing nodes %v", err)

		return
	}

	// Match nodes that don't have an update-agent label.
	nodesMissingLabel := k8sutil.FilterNodesByRequirement(nodelist.Items, updateAgentLabelMissing)
	// Match nodes that identify as Flatcar Container Linux.
	nodesToLabel := k8sutil.FilterContainerLinuxNodes(nodesMissingLabel)

	klog.V(6).Infof("Found Flatcar Container Linux nodes to label: %+v", nodelist.Items)

	for _, node := range nodesToLabel {
		klog.Infof("Setting label 'agent=true' on %q", node.Name)

		if err := k8sutil.SetNodeLabels(ctx, k.nc, node.Name, enableUpdateAgentLabel); err != nil {
			klog.Errorf("Failed setting label 'agent=true' on %q", node.Name)
		}
	}
}
