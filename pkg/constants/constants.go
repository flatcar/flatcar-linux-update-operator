// Package constants has Kubernetes label and annotation constants shared by
// the update-agent and update-operator.
package constants

const (
	// True is annotation value used by update-agent and update-operator.
	True = "true"

	// False is annotation value used by update-agent and update-operator.
	False = "false"

	// Prefix used by all label and annotation keys.
	Prefix = "flatcar-linux-update.v1.flatcar-linux.net/"

	// AnnotationRebootNeeded is a key set to "true" by the update-agent when a reboot is requested.
	AnnotationRebootNeeded = Prefix + "reboot-needed"

	// LabelRebootNeeded is an label name set to "true" by the update-agent when a reboot is requested.
	LabelRebootNeeded = Prefix + "reboot-needed"

	// AnnotationRebootInProgress is a key set to "true" by the update-agent when node-drain and reboot is
	// initiated.
	AnnotationRebootInProgress = Prefix + "reboot-in-progress"

	// AnnotationOkToReboot is a key set to "true" by the update-operator when an agent may proceed
	// with a node-drain and reboot.
	AnnotationOkToReboot = Prefix + "reboot-ok"

	// AnnotationRebootPaused is a key that may be set by the administrator to "true" to prevent
	// update-operator from considering a node for rebooting.  Never set by
	// the update-agent or update-operator.
	AnnotationRebootPaused = Prefix + "reboot-paused"

	// AnnotationStatus is a key set by the update-agent to the current operator status of update_agent.
	//
	// Possible values are:
	//  - "UPDATE_STATUS_IDLE"
	//  - "UPDATE_STATUS_CHECKING_FOR_UPDATE"
	//  - "UPDATE_STATUS_UPDATE_AVAILABLE"
	//  - "UPDATE_STATUS_DOWNLOADING"
	//  - "UPDATE_STATUS_VERIFYING"
	//  - "UPDATE_STATUS_FINALIZING"
	//  - "UPDATE_STATUS_UPDATED_NEED_REBOOT"
	//  - "UPDATE_STATUS_REPORTING_ERROR_EVENT"
	//
	// It is possible, but extremely unlike for it to be "unknown status".
	AnnotationStatus = Prefix + "status"

	// AnnotationLastCheckedTime is a keyset by the update-agent to LAST_CHECKED_TIME reported
	// by update_engine.
	//
	// It is zero if an update has never been checked for, or a UNIX timestamp.
	AnnotationLastCheckedTime = Prefix + "last-checked-time"

	// AnnotationNewVersion is a key set by the update-agent to NEW_VERSION reported by update_engine.
	//
	// It is an opaque string, but might be semver.
	AnnotationNewVersion = Prefix + "new-version"

	// AnnotationAgentMadeUnschedulable is a key set by update-agent to indicate
	// it was responsible for making node unschedulable.
	AnnotationAgentMadeUnschedulable = Prefix + "agent-made-unschedulable"

	// LabelBeforeReboot is a key set to true when the operator is waiting for configured annotation
	// before and after the reboot respectively.
	LabelBeforeReboot = Prefix + "before-reboot"

	// LabelAfterReboot is a key set to true when the operator is waiting for configured annotation
	// before and after the reboot respectively.
	LabelAfterReboot = Prefix + "after-reboot"

	// LabelID is a key set by the update-agent to the value of "ID" in /etc/os-release.
	LabelID = Prefix + "id"

	// LabelGroup is a key set by the update-agent to the value of "GROUP" in
	// /usr/share/flatcar/update.conf, overridden by the value of "GROUP" in
	// /etc/flatcar/update.conf.
	LabelGroup = Prefix + "group"

	// LabelVersion is a key set by the update-agent to the value of "VERSION" in /etc/os-release.
	LabelVersion = Prefix + "version"

	// LabelUpdateAgentEnabled is a key set to "true" on nodes where update-agent pods should be scheduled.
	// This applies only when update-operator is run with the flag
	// auto-label-flatcar-linux=true
	LabelUpdateAgentEnabled = Prefix + "agent"

	// AgentVersion is the key used to indicate the
	// flatcar-linux-update-operator's agent's version.
	// The value is a semver-parseable string. It should be present on each agent
	// pod, as well as on the daemonset that manages them.
	AgentVersion = Prefix + "agent-version"
)
