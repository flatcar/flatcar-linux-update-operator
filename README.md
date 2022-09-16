# Flatcar Linux Update Operator

Flatcar Linux Update Operator is a node reboot controller for Kubernetes running
Flatcar Container Linux images. When a reboot is needed after updating the system via
[update_engine](https://github.com/coreos/update_engine), the operator will
drain the node before rebooting it.

Flatcar Linux Update Operator fulfills the same purpose as
[locksmith](https://github.com/coreos/locksmith), but has better integration
with Kubernetes by explicitly marking a node as unschedulable and deleting pods
on the node before rebooting.

## Design

Flatcar Linux Update Operator is divided into two parts: `update-operator` and `update-agent`.

`update-agent` runs as a DaemonSet on each node, waiting for a `UPDATE_STATUS_UPDATED_NEED_REBOOT` signal via D-Bus from `update_engine`.
It will indicate via [node annotations](./pkg/constants/constants.go) that it needs a reboot.

`update-operator` runs as a Deployment, watching changes to node annotations and reboots the nodes as needed.
It coordinates the reboots of multiple nodes in the cluster, ensuring that not too many are rebooting at once.

Currently, `update-operator` only reboots one node at a time.

## Requirements

- A Kubernetes cluster (>= 1.6) running on Flatcar Container Linux
- The `update-engine.service` systemd unit on each machine should be unmasked, enabled and started in systemd
- The `locksmithd.service` systemd unit on each machine should be masked and stopped in systemd

To unmask a service, run `systemctl unmask <name>`.
To enable a service, run `systemctl enable <name>`.
To start/stop a service, run `systemctl start <name>` or `systemctl stop <name>` respectively.

or using a [Container Linux Config](https://www.flatcar.org/docs/latest/provisioning/cl-config/) file:
```
systemd:
  units:
    - name: locksmithd.service
      mask: true
    - name: update-engine.service
      enabled: true
```

## Usage

Create the `update-operator` deployment and `update-agent` daemonset.

```
kubectl apply -f examples/deploy -R
```

## Test

To test that it is working, you can SSH to a node and trigger an update check by running `update_engine_client -check_for_update` or simulate a reboot is needed by running `locksmithctl send-need-reboot`.

You can also annotate one of your nodes using the command below. Shortly after, you should see the Node being drained as a preparation for the reboot.

```sh
export NODE="<node name>"
kubectl annotate node $NODE --overwrite \
    flatcar-linux-update.v1.flatcar-linux.net/reboot-needed="true"
```
