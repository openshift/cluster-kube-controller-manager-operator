# Kubernetes Controller Manager operator

The Kubernetes Controller Manager operator manages and updates the [Kubernetes Controller Manager](https://github.com/kubernetes/kubernetes) deployed on top of
[OpenShift](https://openshift.io). The operator is based on OpenShift [library-go](https://github.com/openshift/library-go) framework and it
is installed via [Cluster Version Operator](https://github.com/openshift/cluster-version-operator) (CVO).

It contains the following components:

* Operator
* Bootstrap manifest renderer
* Installer based on static pods
* Configuration observer

By default, the operator exposes [Prometheus](https://prometheus.io) metrics via `metrics` service.
The metrics are collected from following components:

* Kubernetes Controller Manager operator


## Configuration

The configuration for the Kubernetes Controller Manager is coming from:

* a [default config](https://github.com/openshift/cluster-kube-controller-manager-operator/blob/master/bindata/assets/config/defaultconfig.yaml)


## Debugging

Operator also expose events that can help debugging issues. To get operator events, run following command:

```
$ oc get events -n  openshift-kube-controller-manager-operator
```

This operator is configured via [`KubeControllerManager`](https://github.com/openshift/api/blob/master/operator/v1/types_kubecontrollermanager.go) custom resource:

```
$ oc describe kubecontrollermanager
```
```yaml
apiVersion: operator.openshift.io/v1
kind: KubeControllerManager
metadata:
  name: cluster
spec:
  managementState: Managed
  ...
```
The log level of individual kube-controller-manager instances can be increased by setting `.spec.logLevel` field:
```
$ oc explain KubeControllerManager.spec.logLevel
KIND:     KubeControllerManager
VERSION:  operator.openshift.io/v1
FIELD:    logLevel <string>
DESCRIPTION:
     logLevel is an intent based logging for an overall component. It does not
     give fine grained control, but it is a simple way to manage coarse grained
     logging choices that operators have to interpret for their operands. Valid
     values are: "Normal", "Debug", "Trace", "TraceAll". Defaults to "Normal".
```
For example:
```yaml
apiVersion: operator.openshift.io/v1
kind: KubeControllerManager
metadata:
  name: cluster
spec:
  logLevel: Debug
  ...
```

Currently the log levels correspond to:

| logLevel | log level |
| -------- | --------- |
| Normal   | 2         |
| Debug    | 4         |
| Trace    | 6         |
| TraceAll | 10        |

```
$ oc explain kubecontrollermanager
```
to learn more about the resource itself.

The current operator status is reported using the `ClusterOperator` resource. To get the current status you can run follow command:

```
$ oc get clusteroperator/kube-controller-manager
```


## Developing and debugging the operator

In the running cluster [cluster-version-operator](https://github.com/openshift/cluster-version-operator/) is responsible
for maintaining functioning and non-altered elements.  In that case to be able to use custom operator image one has to
perform one of these operations:

1. Set your operator in umanaged state, see [here](https://github.com/openshift/enhancements/blob/master/dev-guide/cluster-version-operator/dev/clusterversion.md) for details, in short:

```
oc patch clusterversion/version --type='merge' -p "$(cat <<- EOF
spec:
  overrides:
  - group: apps
    kind: Deployment
    name: kube-controller-manager-operator
    namespace: openshift-kube-controller-manager-operator
    unmanaged: true
EOF
)"
```

2. Scale down cluster-version-operator:

```
oc scale --replicas=0 deploy/cluster-version-operator -n openshift-cluster-version
```

IMPORTANT: This apprach disables cluster-version-operator completly, whereas previous only tells it to not manage a kube-controller-manager-operator!

After doing this you can now change the image of the operator to the desired one:

```
oc patch deployment/kube-controller-manager-operator -n openshift-kube-controller-manager-operator -p '{"spec":{"template":{"spec":{"containers":[{"name":"kube-controller-manager-operator","image":"<user>/cluster-kube-controller-manager-operator","env":[{"name":"OPERATOR_IMAGE","value":"<user>/cluster-kube-controller-manager-operator"}]}]}}}}'
```


## Developing and debugging the bootkube bootstrap phase

The operator image version used by the [installer](https://github.com/openshift/installer/blob/master/pkg/asset/ignition/bootstrap/) bootstrap phase can be overridden by creating a custom origin-release image pointing to the developer's operator `:latest` image:

```
$ IMAGE_ORG=<user> make images
$ docker push <user>/origin-cluster-kube-controller-manager-operator

$ cd ../cluster-kube-apiserver-operator
$ IMAGES=cluster-kube-controller-manager-operator IMAGE_ORG=<user> make origin-release
$ docker push <user>/origin-release:latest

$ cd ../installer
$ OPENSHIFT_INSTALL_RELEASE_IMAGE_OVERRIDE=docker.io/<user>/origin-release:latest bin/openshift-install cluster ...
```
