// Code generated for package v411_00_assets by go-bindata DO NOT EDIT. (@generated)
// sources:
// bindata/v4.1.0/config/default-cluster-policy-controller-config.yaml
// bindata/v4.1.0/config/defaultconfig.yaml
// bindata/v4.1.0/kube-controller-manager/cluster-policy-controller-cm.yaml
// bindata/v4.1.0/kube-controller-manager/cm.yaml
// bindata/v4.1.0/kube-controller-manager/gce/cloud-provider-binding.yaml
// bindata/v4.1.0/kube-controller-manager/gce/cloud-provider-role.yaml
// bindata/v4.1.0/kube-controller-manager/kubeconfig-cert-syncer.yaml
// bindata/v4.1.0/kube-controller-manager/kubeconfig-cm.yaml
// bindata/v4.1.0/kube-controller-manager/leader-election-cluster-policy-controller-role.yaml
// bindata/v4.1.0/kube-controller-manager/leader-election-cluster-policy-controller-rolebinding.yaml
// bindata/v4.1.0/kube-controller-manager/leader-election-kube-controller-manager-role-kube-system.yaml
// bindata/v4.1.0/kube-controller-manager/leader-election-kube-controller-manager-rolebinding-kube-system.yaml
// bindata/v4.1.0/kube-controller-manager/leader-election-rolebinding.yaml
// bindata/v4.1.0/kube-controller-manager/localhost-recovery-client-crb.yaml
// bindata/v4.1.0/kube-controller-manager/localhost-recovery-sa.yaml
// bindata/v4.1.0/kube-controller-manager/localhost-recovery-token.yaml
// bindata/v4.1.0/kube-controller-manager/namespace-security-allocation-controller-clusterrole.yaml
// bindata/v4.1.0/kube-controller-manager/namespace-security-allocation-controller-clusterrolebinding.yaml
// bindata/v4.1.0/kube-controller-manager/ns.yaml
// bindata/v4.1.0/kube-controller-manager/pod-cm.yaml
// bindata/v4.1.0/kube-controller-manager/pod.yaml
// bindata/v4.1.0/kube-controller-manager/sa.yaml
// bindata/v4.1.0/kube-controller-manager/svc.yaml
// bindata/v4.1.0/kube-controller-manager/trusted-ca-cm.yaml
package v411_00_assets

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type asset struct {
	bytes []byte
	info  os.FileInfo
}

type bindataFileInfo struct {
	name    string
	size    int64
	mode    os.FileMode
	modTime time.Time
}

// Name return file name
func (fi bindataFileInfo) Name() string {
	return fi.name
}

// Size return file size
func (fi bindataFileInfo) Size() int64 {
	return fi.size
}

// Mode return file mode
func (fi bindataFileInfo) Mode() os.FileMode {
	return fi.mode
}

// Mode return file modify time
func (fi bindataFileInfo) ModTime() time.Time {
	return fi.modTime
}

// IsDir return file whether a directory
func (fi bindataFileInfo) IsDir() bool {
	return fi.mode&os.ModeDir != 0
}

// Sys return file is sys mode
func (fi bindataFileInfo) Sys() interface{} {
	return nil
}

var _v410ConfigDefaultClusterPolicyControllerConfigYaml = []byte(`apiVersion: openshiftcontrolplane.config.openshift.io/v1
kind: OpenShiftControllerManagerConfig
kubeClientConfig:
  kubeConfig: /etc/kubernetes/static-pod-resources/configmaps/controller-manager-kubeconfig/kubeconfig
servingInfo:
  bindAddress: 0.0.0.0:10357
  bindNetwork: tcp
  clientCA: /etc/kubernetes/static-pod-certs/configmaps/client-ca/ca-bundle.crt
  certFile: /etc/kubernetes/static-pod-resources/secrets/serving-cert/tls.crt
  keyFile: /etc/kubernetes/static-pod-resources/secrets/serving-cert/tls.key

`)

func v410ConfigDefaultClusterPolicyControllerConfigYamlBytes() ([]byte, error) {
	return _v410ConfigDefaultClusterPolicyControllerConfigYaml, nil
}

func v410ConfigDefaultClusterPolicyControllerConfigYaml() (*asset, error) {
	bytes, err := v410ConfigDefaultClusterPolicyControllerConfigYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "v4.1.0/config/default-cluster-policy-controller-config.yaml", size: 0, mode: os.FileMode(0), modTime: time.Unix(0, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

var _v410ConfigDefaultconfigYaml = []byte(`apiVersion: kubecontrolplane.config.openshift.io/v1
kind: KubeControllerManagerConfig
extendedArguments:
  enable-dynamic-provisioning:
  - "true"
  allocate-node-cidrs:
  - "true"
  configure-cloud-routes:
  - "false"
  cluster-cidr:
  - "10.2.0.0/16"
  service-cluster-ip-range:
  - "10.3.0.0/16"
  use-service-account-credentials:
  - "true"
  flex-volume-plugin-dir:
  - "/etc/kubernetes/kubelet-plugins/volume/exec" # created by machine-config-operator, owned by storage team/hekumar@redhat.com
  pv-recycler-pod-template-filepath-nfs:
  - "/etc/kubernetes/recycler-pod.yaml" # created by machine-config-operator, owned by storage team/fbertina@redhat.com
  pv-recycler-pod-template-filepath-hostpath:
  - "/etc/kubernetes/recycler-pod.yaml" # created by machine-config-operator, owned by storage team/fbertina@redhat.com
  leader-elect:
  - "true"
  leader-elect-retry-period:
  - "3s"
  leader-elect-resource-lock:
  - "configmaps"
  controllers:
  - "*"
  - "-ttl" # TODO: this is excluded in kube-core, but not in #21092
  - "-bootstrapsigner"
  - "-tokencleaner"
  experimental-cluster-signing-duration:
  - "720h"
  secure-port:
  - "10257"
  port:
  - "0"
  cert-dir:
  - "/var/run/kubernetes"
  root-ca-file:
  - "/etc/kubernetes/static-pod-resources/configmaps/serviceaccount-ca/ca-bundle.crt"
  service-account-private-key-file:
  - "/etc/kubernetes/static-pod-resources/secrets/service-account-private-key/service-account.key"
  cluster-signing-cert-file:
  - "/etc/kubernetes/static-pod-certs/secrets/csr-signer/tls.crt"
  cluster-signing-key-file:
  - "/etc/kubernetes/static-pod-certs/secrets/csr-signer/tls.key"
  kube-api-qps:
  - "150" # this is a historical values
  kube-api-burst:
  - "300" # this is a historical values
`)

func v410ConfigDefaultconfigYamlBytes() ([]byte, error) {
	return _v410ConfigDefaultconfigYaml, nil
}

func v410ConfigDefaultconfigYaml() (*asset, error) {
	bytes, err := v410ConfigDefaultconfigYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "v4.1.0/config/defaultconfig.yaml", size: 0, mode: os.FileMode(0), modTime: time.Unix(0, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

var _v410KubeControllerManagerClusterPolicyControllerCmYaml = []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  namespace: openshift-kube-controller-manager
  name: cluster-policy-controller-config
data:
  config.yaml:
`)

func v410KubeControllerManagerClusterPolicyControllerCmYamlBytes() ([]byte, error) {
	return _v410KubeControllerManagerClusterPolicyControllerCmYaml, nil
}

func v410KubeControllerManagerClusterPolicyControllerCmYaml() (*asset, error) {
	bytes, err := v410KubeControllerManagerClusterPolicyControllerCmYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "v4.1.0/kube-controller-manager/cluster-policy-controller-cm.yaml", size: 0, mode: os.FileMode(0), modTime: time.Unix(0, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

var _v410KubeControllerManagerCmYaml = []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  namespace: openshift-kube-controller-manager
  name: config
data:
  config.yaml:
`)

func v410KubeControllerManagerCmYamlBytes() ([]byte, error) {
	return _v410KubeControllerManagerCmYaml, nil
}

func v410KubeControllerManagerCmYaml() (*asset, error) {
	bytes, err := v410KubeControllerManagerCmYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "v4.1.0/kube-controller-manager/cm.yaml", size: 0, mode: os.FileMode(0), modTime: time.Unix(0, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

var _v410KubeControllerManagerGceCloudProviderBindingYaml = []byte(`apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: system:openshift:kube-controller-manager:gce-cloud-provider
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: system:openshift:kube-controller-manager:gce-cloud-provider
subjects:
- kind: ServiceAccount
  name: cloud-provider
  namespace: kube-system
`)

func v410KubeControllerManagerGceCloudProviderBindingYamlBytes() ([]byte, error) {
	return _v410KubeControllerManagerGceCloudProviderBindingYaml, nil
}

func v410KubeControllerManagerGceCloudProviderBindingYaml() (*asset, error) {
	bytes, err := v410KubeControllerManagerGceCloudProviderBindingYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "v4.1.0/kube-controller-manager/gce/cloud-provider-binding.yaml", size: 0, mode: os.FileMode(0), modTime: time.Unix(0, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

var _v410KubeControllerManagerGceCloudProviderRoleYaml = []byte(`apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: system:openshift:kube-controller-manager:gce-cloud-provider
rules:
- apiGroups:
  - ""
  resources:
  - events
  verbs:
  - create
  - patch
  - update
- apiGroups:
  - ""
  resources:
  - services/status
  verbs:
  - patch
  - update
`)

func v410KubeControllerManagerGceCloudProviderRoleYamlBytes() ([]byte, error) {
	return _v410KubeControllerManagerGceCloudProviderRoleYaml, nil
}

func v410KubeControllerManagerGceCloudProviderRoleYaml() (*asset, error) {
	bytes, err := v410KubeControllerManagerGceCloudProviderRoleYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "v4.1.0/kube-controller-manager/gce/cloud-provider-role.yaml", size: 0, mode: os.FileMode(0), modTime: time.Unix(0, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

var _v410KubeControllerManagerKubeconfigCertSyncerYaml = []byte(`# TODO provide distinct trust between this and the KCM itself
apiVersion: v1
kind: ConfigMap
metadata:
  name: kube-controller-cert-syncer-kubeconfig
  namespace: openshift-kube-controller-manager
data:
  kubeconfig: |
    apiVersion: v1
    clusters:
      - cluster:
          certificate-authority: /etc/kubernetes/static-pod-resources/secrets/localhost-recovery-client-token/ca.crt
          server: https://localhost:6443
          tls-server-name: localhost-recovery
        name: loopback
    contexts:
      - context:
          cluster: loopback
          user: kube-controller-manager
        name: kube-controller-manager
    current-context: kube-controller-manager
    kind: Config
    preferences: {}
    users:
      - name: kube-controller-manager
        user:
          tokenFile: /etc/kubernetes/static-pod-resources/secrets/localhost-recovery-client-token/token
`)

func v410KubeControllerManagerKubeconfigCertSyncerYamlBytes() ([]byte, error) {
	return _v410KubeControllerManagerKubeconfigCertSyncerYaml, nil
}

func v410KubeControllerManagerKubeconfigCertSyncerYaml() (*asset, error) {
	bytes, err := v410KubeControllerManagerKubeconfigCertSyncerYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "v4.1.0/kube-controller-manager/kubeconfig-cert-syncer.yaml", size: 0, mode: os.FileMode(0), modTime: time.Unix(0, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

var _v410KubeControllerManagerKubeconfigCmYaml = []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: controller-manager-kubeconfig
  namespace: openshift-kube-controller-manager
data:
  kubeconfig: |
    apiVersion: v1
    clusters:
      - cluster:
          certificate-authority: /etc/kubernetes/static-pod-resources/configmaps/serviceaccount-ca/ca-bundle.crt
          server: $LB_INT_URL
        name: lb-int
    contexts:
      - context:
          cluster: lb-int
          user: kube-controller-manager
        name: kube-controller-manager
    current-context: kube-controller-manager
    kind: Config
    preferences: {}
    users:
      - name: kube-controller-manager
        user:
          client-certificate: /etc/kubernetes/static-pod-certs/secrets/kube-controller-manager-client-cert-key/tls.crt
          client-key: /etc/kubernetes/static-pod-certs/secrets/kube-controller-manager-client-cert-key/tls.key
`)

func v410KubeControllerManagerKubeconfigCmYamlBytes() ([]byte, error) {
	return _v410KubeControllerManagerKubeconfigCmYaml, nil
}

func v410KubeControllerManagerKubeconfigCmYaml() (*asset, error) {
	bytes, err := v410KubeControllerManagerKubeconfigCmYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "v4.1.0/kube-controller-manager/kubeconfig-cm.yaml", size: 0, mode: os.FileMode(0), modTime: time.Unix(0, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

var _v410KubeControllerManagerLeaderElectionClusterPolicyControllerRoleYaml = []byte(`# This role is necessary to create leader lock configmap for upgrades 4.2-> 4.3
# cluster-policy-controller is split from openshift-controller-manager in 4.3
# leader lock in openshift-controller-manager NamespaceSecurityAllocationController and in ClusterPolicyController
# cluster-policy-controller container runs in ns openshift-kube-controller-manager static pod
# The lock, role, and rolebinding can be removed in 4.4
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  namespace: openshift-kube-controller-manager
  name: system:openshift:leader-election-lock-cluster-policy-controller
rules:
- apiGroups:
  - ""
  resources:
  - configmaps
  verbs:
  - get
  - list
  - watch
  - create
  - update
  - delete
`)

func v410KubeControllerManagerLeaderElectionClusterPolicyControllerRoleYamlBytes() ([]byte, error) {
	return _v410KubeControllerManagerLeaderElectionClusterPolicyControllerRoleYaml, nil
}

func v410KubeControllerManagerLeaderElectionClusterPolicyControllerRoleYaml() (*asset, error) {
	bytes, err := v410KubeControllerManagerLeaderElectionClusterPolicyControllerRoleYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "v4.1.0/kube-controller-manager/leader-election-cluster-policy-controller-role.yaml", size: 0, mode: os.FileMode(0), modTime: time.Unix(0, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

var _v410KubeControllerManagerLeaderElectionClusterPolicyControllerRolebindingYaml = []byte(`# This rolebinding binds role for creation of leader lock configmap for upgrades 4.2-> 4.3
# cluster-policy-controller is split from openshift-controller-manager in 4.3
# leader lock in openshift-controller-manager NamespaceSecurityAllocationController and in ClusterPolicyController
# cluster-policy-controller container runs in ns openshift-kube-controller-manager static pod
# The locks, role, and rolebinding can be removed in 4.4
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  namespace: openshift-kube-controller-manager
  name: system:openshift:leader-election-lock-cluster-policy-controller
roleRef:
  kind: Role
  name: system:openshift:leader-election-lock-cluster-policy-controller
subjects:
- kind: User
  name: system:kube-controller-manager
- kind: ServiceAccount
  name: namespace-security-allocation-controller
  namespace: openshift-infra
`)

func v410KubeControllerManagerLeaderElectionClusterPolicyControllerRolebindingYamlBytes() ([]byte, error) {
	return _v410KubeControllerManagerLeaderElectionClusterPolicyControllerRolebindingYaml, nil
}

func v410KubeControllerManagerLeaderElectionClusterPolicyControllerRolebindingYaml() (*asset, error) {
	bytes, err := v410KubeControllerManagerLeaderElectionClusterPolicyControllerRolebindingYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "v4.1.0/kube-controller-manager/leader-election-cluster-policy-controller-rolebinding.yaml", size: 0, mode: os.FileMode(0), modTime: time.Unix(0, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

var _v410KubeControllerManagerLeaderElectionKubeControllerManagerRoleKubeSystemYaml = []byte(`# This role is necessary to create leader lock configmap for upgrades 4.2-> 4.3
# cluster-policy-controller is split from openshift-controller-manager in 4.3
# leader lock in openshift-controller-manager NamespaceSecurityAllocationController and in ClusterPolicyController
# cluster-policy-controller container runs in ns openshift-kube-controller-manager static pod
# The lock, role, and rolebinding can be removed in 4.4
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  namespace: kube-system
  name: system:openshift:leader-election-lock-kube-controller-manager
rules:
- apiGroups:
  - ""
  resources:
  - configmaps
  verbs:
  - create
`)

func v410KubeControllerManagerLeaderElectionKubeControllerManagerRoleKubeSystemYamlBytes() ([]byte, error) {
	return _v410KubeControllerManagerLeaderElectionKubeControllerManagerRoleKubeSystemYaml, nil
}

func v410KubeControllerManagerLeaderElectionKubeControllerManagerRoleKubeSystemYaml() (*asset, error) {
	bytes, err := v410KubeControllerManagerLeaderElectionKubeControllerManagerRoleKubeSystemYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "v4.1.0/kube-controller-manager/leader-election-kube-controller-manager-role-kube-system.yaml", size: 0, mode: os.FileMode(0), modTime: time.Unix(0, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

var _v410KubeControllerManagerLeaderElectionKubeControllerManagerRolebindingKubeSystemYaml = []byte(`# This rolebinding binds role for creation of leader lock configmap for upgrades 4.2-> 4.3
# cluster-policy-controller is split from openshift-controller-manager in 4.3
# leader lock in openshift-controller-manager NamespaceSecurityAllocationController and in ClusterPolicyController
# cluster-policy-controller container runs in ns openshift-kube-controller-manager static pod
# The locks, role, and rolebinding can be removed in 4.4
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  namespace: kube-system
  name: system:openshift:leader-election-lock-kube-controller-manager
roleRef:
  kind: Role
  name: system:openshift:leader-election-lock-kube-controller-manager
subjects:
- kind: User
  name: system:kube-controller-manager
- kind: ServiceAccount
  name: namespace-security-allocation-controller
  namespace: openshift-infra
`)

func v410KubeControllerManagerLeaderElectionKubeControllerManagerRolebindingKubeSystemYamlBytes() ([]byte, error) {
	return _v410KubeControllerManagerLeaderElectionKubeControllerManagerRolebindingKubeSystemYaml, nil
}

func v410KubeControllerManagerLeaderElectionKubeControllerManagerRolebindingKubeSystemYaml() (*asset, error) {
	bytes, err := v410KubeControllerManagerLeaderElectionKubeControllerManagerRolebindingKubeSystemYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "v4.1.0/kube-controller-manager/leader-election-kube-controller-manager-rolebinding-kube-system.yaml", size: 0, mode: os.FileMode(0), modTime: time.Unix(0, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

var _v410KubeControllerManagerLeaderElectionRolebindingYaml = []byte(`apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  namespace: kube-system
  name: system:openshift:leader-locking-kube-controller-manager
roleRef:
  kind: Role
  name: system::leader-locking-kube-controller-manager
subjects:
- kind: User
  name: system:kube-controller-manager
`)

func v410KubeControllerManagerLeaderElectionRolebindingYamlBytes() ([]byte, error) {
	return _v410KubeControllerManagerLeaderElectionRolebindingYaml, nil
}

func v410KubeControllerManagerLeaderElectionRolebindingYaml() (*asset, error) {
	bytes, err := v410KubeControllerManagerLeaderElectionRolebindingYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "v4.1.0/kube-controller-manager/leader-election-rolebinding.yaml", size: 0, mode: os.FileMode(0), modTime: time.Unix(0, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

var _v410KubeControllerManagerLocalhostRecoveryClientCrbYaml = []byte(`apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: system:openshift:operator:kube-controller-manager-recovery
roleRef:
  kind: ClusterRole
  name: cluster-admin
subjects:
- kind: ServiceAccount
  name: localhost-recovery-client
  namespace: openshift-kube-controller-manager
`)

func v410KubeControllerManagerLocalhostRecoveryClientCrbYamlBytes() ([]byte, error) {
	return _v410KubeControllerManagerLocalhostRecoveryClientCrbYaml, nil
}

func v410KubeControllerManagerLocalhostRecoveryClientCrbYaml() (*asset, error) {
	bytes, err := v410KubeControllerManagerLocalhostRecoveryClientCrbYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "v4.1.0/kube-controller-manager/localhost-recovery-client-crb.yaml", size: 0, mode: os.FileMode(0), modTime: time.Unix(0, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

var _v410KubeControllerManagerLocalhostRecoverySaYaml = []byte(`apiVersion: v1
kind: ServiceAccount
metadata:
  name: localhost-recovery-client
  namespace: openshift-kube-controller-manager
`)

func v410KubeControllerManagerLocalhostRecoverySaYamlBytes() ([]byte, error) {
	return _v410KubeControllerManagerLocalhostRecoverySaYaml, nil
}

func v410KubeControllerManagerLocalhostRecoverySaYaml() (*asset, error) {
	bytes, err := v410KubeControllerManagerLocalhostRecoverySaYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "v4.1.0/kube-controller-manager/localhost-recovery-sa.yaml", size: 0, mode: os.FileMode(0), modTime: time.Unix(0, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

var _v410KubeControllerManagerLocalhostRecoveryTokenYaml = []byte(`apiVersion: v1
kind: Secret
metadata:
  name: localhost-recovery-client-token
  namespace: openshift-kube-controller-manager
  annotations:
    kubernetes.io/service-account.name: localhost-recovery-client
type: kubernetes.io/service-account-token
`)

func v410KubeControllerManagerLocalhostRecoveryTokenYamlBytes() ([]byte, error) {
	return _v410KubeControllerManagerLocalhostRecoveryTokenYaml, nil
}

func v410KubeControllerManagerLocalhostRecoveryTokenYaml() (*asset, error) {
	bytes, err := v410KubeControllerManagerLocalhostRecoveryTokenYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "v4.1.0/kube-controller-manager/localhost-recovery-token.yaml", size: 0, mode: os.FileMode(0), modTime: time.Unix(0, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

var _v410KubeControllerManagerNamespaceSecurityAllocationControllerClusterroleYaml = []byte(`apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  annotations:
    rbac.authorization.kubernetes.io/autoupdate: "true"
  creationTimestamp: null
  name: system:openshift:controller:namespace-security-allocation-controller
rules:
- apiGroups:
  - security.openshift.io
  - security.internal.openshift.io
  resources:
  - rangeallocations
  verbs:
  - create
  - get
  - update
- apiGroups:
  - ""
  resources:
  - namespaces
  verbs:
  - get
  - list
  - update
  - watch
  - patch
- apiGroups:
  - ""
  resources:
  - events
  verbs:
  - create
  - patch
  - update
`)

func v410KubeControllerManagerNamespaceSecurityAllocationControllerClusterroleYamlBytes() ([]byte, error) {
	return _v410KubeControllerManagerNamespaceSecurityAllocationControllerClusterroleYaml, nil
}

func v410KubeControllerManagerNamespaceSecurityAllocationControllerClusterroleYaml() (*asset, error) {
	bytes, err := v410KubeControllerManagerNamespaceSecurityAllocationControllerClusterroleYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "v4.1.0/kube-controller-manager/namespace-security-allocation-controller-clusterrole.yaml", size: 0, mode: os.FileMode(0), modTime: time.Unix(0, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

var _v410KubeControllerManagerNamespaceSecurityAllocationControllerClusterrolebindingYaml = []byte(`apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  annotations:
    rbac.authorization.kubernetes.io/autoupdate: "true"
  creationTimestamp: null
  name: system:openshift:controller:namespace-security-allocation-controller
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: system:openshift:controller:namespace-security-allocation-controller
subjects:
- kind: ServiceAccount
  name: namespace-security-allocation-controller
  namespace: openshift-infra`)

func v410KubeControllerManagerNamespaceSecurityAllocationControllerClusterrolebindingYamlBytes() ([]byte, error) {
	return _v410KubeControllerManagerNamespaceSecurityAllocationControllerClusterrolebindingYaml, nil
}

func v410KubeControllerManagerNamespaceSecurityAllocationControllerClusterrolebindingYaml() (*asset, error) {
	bytes, err := v410KubeControllerManagerNamespaceSecurityAllocationControllerClusterrolebindingYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "v4.1.0/kube-controller-manager/namespace-security-allocation-controller-clusterrolebinding.yaml", size: 0, mode: os.FileMode(0), modTime: time.Unix(0, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

var _v410KubeControllerManagerNsYaml = []byte(`apiVersion: v1
kind: Namespace
metadata:
  annotations:
    openshift.io/node-selector: ""
  name: openshift-kube-controller-manager
  labels:
    openshift.io/run-level: "0"
    openshift.io/cluster-monitoring: "true"
`)

func v410KubeControllerManagerNsYamlBytes() ([]byte, error) {
	return _v410KubeControllerManagerNsYaml, nil
}

func v410KubeControllerManagerNsYaml() (*asset, error) {
	bytes, err := v410KubeControllerManagerNsYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "v4.1.0/kube-controller-manager/ns.yaml", size: 0, mode: os.FileMode(0), modTime: time.Unix(0, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

var _v410KubeControllerManagerPodCmYaml = []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  namespace: openshift-kube-controller-manager
  name: kube-controller-manager-pod
data:
  pod.yaml:
  forceRedeploymentReason:
  version:
`)

func v410KubeControllerManagerPodCmYamlBytes() ([]byte, error) {
	return _v410KubeControllerManagerPodCmYaml, nil
}

func v410KubeControllerManagerPodCmYaml() (*asset, error) {
	bytes, err := v410KubeControllerManagerPodCmYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "v4.1.0/kube-controller-manager/pod-cm.yaml", size: 0, mode: os.FileMode(0), modTime: time.Unix(0, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

var _v410KubeControllerManagerPodYaml = []byte(`apiVersion: v1
kind: Pod
metadata:
  name: kube-controller-manager
  namespace: openshift-kube-controller-manager
  annotations:
    kubectl.kubernetes.io/default-logs-container: kube-controller-manager
  labels:
    app: kube-controller-manager
    kube-controller-manager: "true"
    revision: "REVISION"
spec:
  containers:
  - name: kube-controller-manager
    image: ${IMAGE}
    imagePullPolicy: IfNotPresent
    terminationMessagePolicy: FallbackToLogsOnError
    command: ["/bin/bash", "-euxo", "pipefail", "-c"]
    args:
        - |
          timeout 3m /bin/bash -exuo pipefail -c 'while [ -n "$(ss -Htanop \( sport = 10257 \))" ]; do sleep 1; done'

          if [ -f /etc/kubernetes/static-pod-certs/configmaps/trusted-ca-bundle/ca-bundle.crt ]; then
            echo "Copying system trust bundle"
            cp -f /etc/kubernetes/static-pod-certs/configmaps/trusted-ca-bundle/ca-bundle.crt /etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem
          fi

          exec hyperkube kube-controller-manager --openshift-config=/etc/kubernetes/static-pod-resources/configmaps/config/config.yaml \
            --kubeconfig=/etc/kubernetes/static-pod-resources/configmaps/controller-manager-kubeconfig/kubeconfig \
            --authentication-kubeconfig=/etc/kubernetes/static-pod-resources/configmaps/controller-manager-kubeconfig/kubeconfig \
            --authorization-kubeconfig=/etc/kubernetes/static-pod-resources/configmaps/controller-manager-kubeconfig/kubeconfig \
            --client-ca-file=/etc/kubernetes/static-pod-certs/configmaps/client-ca/ca-bundle.crt \
            --requestheader-client-ca-file=/etc/kubernetes/static-pod-certs/configmaps/aggregator-client-ca/ca-bundle.crt
    resources:
      requests:
        memory: 200Mi
        cpu: 80m
    ports:
      - containerPort: 10257
    volumeMounts:
    - mountPath: /etc/kubernetes/manifests
      name: manifests-dir # Used in the KubeControllerManagerConfig to pass in recycler pod templates
    - mountPath: /etc/kubernetes/static-pod-resources
      name: resource-dir
    - mountPath: /etc/kubernetes/static-pod-certs
      name: cert-dir
    startupProbe:
      httpGet:
        scheme: HTTPS
        port: 10257
        path: healthz
      initialDelaySeconds: 0
      timeoutSeconds: 3
    livenessProbe:
      httpGet:
        scheme: HTTPS
        port: 10257
        path: healthz
      initialDelaySeconds: 45
      timeoutSeconds: 10
    readinessProbe:
      httpGet:
        scheme: HTTPS
        port: 10257
        path: healthz
      initialDelaySeconds: 10
      timeoutSeconds: 10
  - name: cluster-policy-controller
    image: ${CLUSTER_POLICY_CONTROLLER_IMAGE}
    imagePullPolicy: IfNotPresent
    terminationMessagePolicy: FallbackToLogsOnError
    command: ["/bin/bash", "-euxo", "pipefail", "-c"]
    args:
      - |
        timeout 3m /bin/bash -exuo pipefail -c 'while [ -n "$(ss -Htanop \( sport = 10357 \))" ]; do sleep 1; done'

        exec cluster-policy-controller start --config=/etc/kubernetes/static-pod-resources/configmaps/cluster-policy-controller-config/config.yaml
    resources:
      requests:
        memory: 200Mi
        cpu: 10m
    ports:
      - containerPort: 10357
    volumeMounts:
      - mountPath: /etc/kubernetes/static-pod-resources
        name: resource-dir
      - mountPath: /etc/kubernetes/static-pod-certs
        name: cert-dir
    startupProbe:
      httpGet:
        scheme: HTTPS
        port: 10357
        path: healthz
      initialDelaySeconds: 0
      timeoutSeconds: 3
    livenessProbe:
      httpGet:
        scheme: HTTPS
        port: 10357
        path: healthz
      initialDelaySeconds: 45
      timeoutSeconds: 10
    readinessProbe:
      httpGet:
        scheme: HTTPS
        port: 10357
        path: healthz
      initialDelaySeconds: 10
      timeoutSeconds: 10
  - name: kube-controller-manager-cert-syncer
    env:
      - name: POD_NAME
        valueFrom:
          fieldRef:
            fieldPath: metadata.name
      - name: POD_NAMESPACE
        valueFrom:
          fieldRef:
            fieldPath: metadata.namespace
    image: ${OPERATOR_IMAGE}
    imagePullPolicy: IfNotPresent
    terminationMessagePolicy: FallbackToLogsOnError
    command: ["cluster-kube-controller-manager-operator", "cert-syncer"]
    args:
      - --kubeconfig=/etc/kubernetes/static-pod-resources/configmaps/kube-controller-cert-syncer-kubeconfig/kubeconfig
      - --namespace=$(POD_NAMESPACE)
      - --destination-dir=/etc/kubernetes/static-pod-certs
    resources:
      requests:
        memory: 50Mi
        cpu: 5m
    volumeMounts:
      - mountPath: /etc/kubernetes/static-pod-resources
        name: resource-dir
      - mountPath: /etc/kubernetes/static-pod-certs
        name: cert-dir
  - name: kube-controller-manager-recovery-controller
    env:
    - name: POD_NAMESPACE
      valueFrom:
        fieldRef:
          fieldPath: metadata.namespace
    image: ${OPERATOR_IMAGE}
    imagePullPolicy: IfNotPresent
    terminationMessagePolicy: FallbackToLogsOnError
    command: ["/bin/bash", "-euxo", "pipefail", "-c"]
    args:
      - |
        timeout 3m /bin/bash -exuo pipefail -c 'while [ -n "$(ss -Htanop \( sport = 9443 \))" ]; do sleep 1; done'

        exec cluster-kube-controller-manager-operator cert-recovery-controller --kubeconfig=/etc/kubernetes/static-pod-resources/configmaps/kube-controller-cert-syncer-kubeconfig/kubeconfig --namespace=${POD_NAMESPACE} --listen=0.0.0.0:9443 -v=2
    resources:
      requests:
        memory: 50Mi
        cpu: 5m
    volumeMounts:
      - mountPath: /etc/kubernetes/static-pod-resources
        name: resource-dir
      - mountPath: /etc/kubernetes/static-pod-certs
        name: cert-dir
  hostNetwork: true
  priorityClassName: system-node-critical
  tolerations:
  - operator: "Exists"
  volumes:
  - hostPath:
      path: /etc/kubernetes/manifests
    name: manifests-dir
  - hostPath:
      path: /etc/kubernetes/static-pod-resources/kube-controller-manager-pod-REVISION
    name: resource-dir
  - hostPath:
      path: /etc/kubernetes/static-pod-resources/kube-controller-manager-certs
    name: cert-dir
`)

func v410KubeControllerManagerPodYamlBytes() ([]byte, error) {
	return _v410KubeControllerManagerPodYaml, nil
}

func v410KubeControllerManagerPodYaml() (*asset, error) {
	bytes, err := v410KubeControllerManagerPodYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "v4.1.0/kube-controller-manager/pod.yaml", size: 0, mode: os.FileMode(0), modTime: time.Unix(0, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

var _v410KubeControllerManagerSaYaml = []byte(`apiVersion: v1
kind: ServiceAccount
metadata:
  namespace: openshift-kube-controller-manager
  name: kube-controller-manager-sa
`)

func v410KubeControllerManagerSaYamlBytes() ([]byte, error) {
	return _v410KubeControllerManagerSaYaml, nil
}

func v410KubeControllerManagerSaYaml() (*asset, error) {
	bytes, err := v410KubeControllerManagerSaYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "v4.1.0/kube-controller-manager/sa.yaml", size: 0, mode: os.FileMode(0), modTime: time.Unix(0, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

var _v410KubeControllerManagerSvcYaml = []byte(`apiVersion: v1
kind: Service
metadata:
  namespace: openshift-kube-controller-manager
  name: kube-controller-manager
  annotations:
    service.alpha.openshift.io/serving-cert-secret-name: serving-cert
    prometheus.io/scrape: "true"
    prometheus.io/scheme: https
spec:
  selector:
    kube-controller-manager: "true"
  ports:
  - name: https
    port: 443
    targetPort: 10257
`)

func v410KubeControllerManagerSvcYamlBytes() ([]byte, error) {
	return _v410KubeControllerManagerSvcYaml, nil
}

func v410KubeControllerManagerSvcYaml() (*asset, error) {
	bytes, err := v410KubeControllerManagerSvcYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "v4.1.0/kube-controller-manager/svc.yaml", size: 0, mode: os.FileMode(0), modTime: time.Unix(0, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

var _v410KubeControllerManagerTrustedCaCmYaml = []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  namespace: openshift-kube-controller-manager
  name: trusted-ca-bundle
  labels:
    config.openshift.io/inject-trusted-cabundle: "true"
`)

func v410KubeControllerManagerTrustedCaCmYamlBytes() ([]byte, error) {
	return _v410KubeControllerManagerTrustedCaCmYaml, nil
}

func v410KubeControllerManagerTrustedCaCmYaml() (*asset, error) {
	bytes, err := v410KubeControllerManagerTrustedCaCmYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "v4.1.0/kube-controller-manager/trusted-ca-cm.yaml", size: 0, mode: os.FileMode(0), modTime: time.Unix(0, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

// Asset loads and returns the asset for the given name.
// It returns an error if the asset could not be found or
// could not be loaded.
func Asset(name string) ([]byte, error) {
	cannonicalName := strings.Replace(name, "\\", "/", -1)
	if f, ok := _bindata[cannonicalName]; ok {
		a, err := f()
		if err != nil {
			return nil, fmt.Errorf("Asset %s can't read by error: %v", name, err)
		}
		return a.bytes, nil
	}
	return nil, fmt.Errorf("Asset %s not found", name)
}

// MustAsset is like Asset but panics when Asset would return an error.
// It simplifies safe initialization of global variables.
func MustAsset(name string) []byte {
	a, err := Asset(name)
	if err != nil {
		panic("asset: Asset(" + name + "): " + err.Error())
	}

	return a
}

// AssetInfo loads and returns the asset info for the given name.
// It returns an error if the asset could not be found or
// could not be loaded.
func AssetInfo(name string) (os.FileInfo, error) {
	cannonicalName := strings.Replace(name, "\\", "/", -1)
	if f, ok := _bindata[cannonicalName]; ok {
		a, err := f()
		if err != nil {
			return nil, fmt.Errorf("AssetInfo %s can't read by error: %v", name, err)
		}
		return a.info, nil
	}
	return nil, fmt.Errorf("AssetInfo %s not found", name)
}

// AssetNames returns the names of the assets.
func AssetNames() []string {
	names := make([]string, 0, len(_bindata))
	for name := range _bindata {
		names = append(names, name)
	}
	return names
}

// _bindata is a table, holding each asset generator, mapped to its name.
var _bindata = map[string]func() (*asset, error){
	"v4.1.0/config/default-cluster-policy-controller-config.yaml":                                         v410ConfigDefaultClusterPolicyControllerConfigYaml,
	"v4.1.0/config/defaultconfig.yaml":                                                                    v410ConfigDefaultconfigYaml,
	"v4.1.0/kube-controller-manager/cluster-policy-controller-cm.yaml":                                    v410KubeControllerManagerClusterPolicyControllerCmYaml,
	"v4.1.0/kube-controller-manager/cm.yaml":                                                              v410KubeControllerManagerCmYaml,
	"v4.1.0/kube-controller-manager/gce/cloud-provider-binding.yaml":                                      v410KubeControllerManagerGceCloudProviderBindingYaml,
	"v4.1.0/kube-controller-manager/gce/cloud-provider-role.yaml":                                         v410KubeControllerManagerGceCloudProviderRoleYaml,
	"v4.1.0/kube-controller-manager/kubeconfig-cert-syncer.yaml":                                          v410KubeControllerManagerKubeconfigCertSyncerYaml,
	"v4.1.0/kube-controller-manager/kubeconfig-cm.yaml":                                                   v410KubeControllerManagerKubeconfigCmYaml,
	"v4.1.0/kube-controller-manager/leader-election-cluster-policy-controller-role.yaml":                  v410KubeControllerManagerLeaderElectionClusterPolicyControllerRoleYaml,
	"v4.1.0/kube-controller-manager/leader-election-cluster-policy-controller-rolebinding.yaml":           v410KubeControllerManagerLeaderElectionClusterPolicyControllerRolebindingYaml,
	"v4.1.0/kube-controller-manager/leader-election-kube-controller-manager-role-kube-system.yaml":        v410KubeControllerManagerLeaderElectionKubeControllerManagerRoleKubeSystemYaml,
	"v4.1.0/kube-controller-manager/leader-election-kube-controller-manager-rolebinding-kube-system.yaml": v410KubeControllerManagerLeaderElectionKubeControllerManagerRolebindingKubeSystemYaml,
	"v4.1.0/kube-controller-manager/leader-election-rolebinding.yaml":                                     v410KubeControllerManagerLeaderElectionRolebindingYaml,
	"v4.1.0/kube-controller-manager/localhost-recovery-client-crb.yaml":                                   v410KubeControllerManagerLocalhostRecoveryClientCrbYaml,
	"v4.1.0/kube-controller-manager/localhost-recovery-sa.yaml":                                           v410KubeControllerManagerLocalhostRecoverySaYaml,
	"v4.1.0/kube-controller-manager/localhost-recovery-token.yaml":                                        v410KubeControllerManagerLocalhostRecoveryTokenYaml,
	"v4.1.0/kube-controller-manager/namespace-security-allocation-controller-clusterrole.yaml":            v410KubeControllerManagerNamespaceSecurityAllocationControllerClusterroleYaml,
	"v4.1.0/kube-controller-manager/namespace-security-allocation-controller-clusterrolebinding.yaml":     v410KubeControllerManagerNamespaceSecurityAllocationControllerClusterrolebindingYaml,
	"v4.1.0/kube-controller-manager/ns.yaml":                                                              v410KubeControllerManagerNsYaml,
	"v4.1.0/kube-controller-manager/pod-cm.yaml":                                                          v410KubeControllerManagerPodCmYaml,
	"v4.1.0/kube-controller-manager/pod.yaml":                                                             v410KubeControllerManagerPodYaml,
	"v4.1.0/kube-controller-manager/sa.yaml":                                                              v410KubeControllerManagerSaYaml,
	"v4.1.0/kube-controller-manager/svc.yaml":                                                             v410KubeControllerManagerSvcYaml,
	"v4.1.0/kube-controller-manager/trusted-ca-cm.yaml":                                                   v410KubeControllerManagerTrustedCaCmYaml,
}

// AssetDir returns the file names below a certain
// directory embedded in the file by go-bindata.
// For example if you run go-bindata on data/... and data contains the
// following hierarchy:
//     data/
//       foo.txt
//       img/
//         a.png
//         b.png
// then AssetDir("data") would return []string{"foo.txt", "img"}
// AssetDir("data/img") would return []string{"a.png", "b.png"}
// AssetDir("foo.txt") and AssetDir("notexist") would return an error
// AssetDir("") will return []string{"data"}.
func AssetDir(name string) ([]string, error) {
	node := _bintree
	if len(name) != 0 {
		cannonicalName := strings.Replace(name, "\\", "/", -1)
		pathList := strings.Split(cannonicalName, "/")
		for _, p := range pathList {
			node = node.Children[p]
			if node == nil {
				return nil, fmt.Errorf("Asset %s not found", name)
			}
		}
	}
	if node.Func != nil {
		return nil, fmt.Errorf("Asset %s not found", name)
	}
	rv := make([]string, 0, len(node.Children))
	for childName := range node.Children {
		rv = append(rv, childName)
	}
	return rv, nil
}

type bintree struct {
	Func     func() (*asset, error)
	Children map[string]*bintree
}

var _bintree = &bintree{nil, map[string]*bintree{
	"v4.1.0": {nil, map[string]*bintree{
		"config": {nil, map[string]*bintree{
			"default-cluster-policy-controller-config.yaml": {v410ConfigDefaultClusterPolicyControllerConfigYaml, map[string]*bintree{}},
			"defaultconfig.yaml":                            {v410ConfigDefaultconfigYaml, map[string]*bintree{}},
		}},
		"kube-controller-manager": {nil, map[string]*bintree{
			"cluster-policy-controller-cm.yaml": {v410KubeControllerManagerClusterPolicyControllerCmYaml, map[string]*bintree{}},
			"cm.yaml":                           {v410KubeControllerManagerCmYaml, map[string]*bintree{}},
			"gce": {nil, map[string]*bintree{
				"cloud-provider-binding.yaml": {v410KubeControllerManagerGceCloudProviderBindingYaml, map[string]*bintree{}},
				"cloud-provider-role.yaml":    {v410KubeControllerManagerGceCloudProviderRoleYaml, map[string]*bintree{}},
			}},
			"kubeconfig-cert-syncer.yaml":                                          {v410KubeControllerManagerKubeconfigCertSyncerYaml, map[string]*bintree{}},
			"kubeconfig-cm.yaml":                                                   {v410KubeControllerManagerKubeconfigCmYaml, map[string]*bintree{}},
			"leader-election-cluster-policy-controller-role.yaml":                  {v410KubeControllerManagerLeaderElectionClusterPolicyControllerRoleYaml, map[string]*bintree{}},
			"leader-election-cluster-policy-controller-rolebinding.yaml":           {v410KubeControllerManagerLeaderElectionClusterPolicyControllerRolebindingYaml, map[string]*bintree{}},
			"leader-election-kube-controller-manager-role-kube-system.yaml":        {v410KubeControllerManagerLeaderElectionKubeControllerManagerRoleKubeSystemYaml, map[string]*bintree{}},
			"leader-election-kube-controller-manager-rolebinding-kube-system.yaml": {v410KubeControllerManagerLeaderElectionKubeControllerManagerRolebindingKubeSystemYaml, map[string]*bintree{}},
			"leader-election-rolebinding.yaml":                                     {v410KubeControllerManagerLeaderElectionRolebindingYaml, map[string]*bintree{}},
			"localhost-recovery-client-crb.yaml":                                   {v410KubeControllerManagerLocalhostRecoveryClientCrbYaml, map[string]*bintree{}},
			"localhost-recovery-sa.yaml":                                           {v410KubeControllerManagerLocalhostRecoverySaYaml, map[string]*bintree{}},
			"localhost-recovery-token.yaml":                                        {v410KubeControllerManagerLocalhostRecoveryTokenYaml, map[string]*bintree{}},
			"namespace-security-allocation-controller-clusterrole.yaml":            {v410KubeControllerManagerNamespaceSecurityAllocationControllerClusterroleYaml, map[string]*bintree{}},
			"namespace-security-allocation-controller-clusterrolebinding.yaml":     {v410KubeControllerManagerNamespaceSecurityAllocationControllerClusterrolebindingYaml, map[string]*bintree{}},
			"ns.yaml":            {v410KubeControllerManagerNsYaml, map[string]*bintree{}},
			"pod-cm.yaml":        {v410KubeControllerManagerPodCmYaml, map[string]*bintree{}},
			"pod.yaml":           {v410KubeControllerManagerPodYaml, map[string]*bintree{}},
			"sa.yaml":            {v410KubeControllerManagerSaYaml, map[string]*bintree{}},
			"svc.yaml":           {v410KubeControllerManagerSvcYaml, map[string]*bintree{}},
			"trusted-ca-cm.yaml": {v410KubeControllerManagerTrustedCaCmYaml, map[string]*bintree{}},
		}},
	}},
}}

// RestoreAsset restores an asset under the given directory
func RestoreAsset(dir, name string) error {
	data, err := Asset(name)
	if err != nil {
		return err
	}
	info, err := AssetInfo(name)
	if err != nil {
		return err
	}
	err = os.MkdirAll(_filePath(dir, filepath.Dir(name)), os.FileMode(0755))
	if err != nil {
		return err
	}
	err = ioutil.WriteFile(_filePath(dir, name), data, info.Mode())
	if err != nil {
		return err
	}
	err = os.Chtimes(_filePath(dir, name), info.ModTime(), info.ModTime())
	if err != nil {
		return err
	}
	return nil
}

// RestoreAssets restores an asset under the given directory recursively
func RestoreAssets(dir, name string) error {
	children, err := AssetDir(name)
	// File
	if err != nil {
		return RestoreAsset(dir, name)
	}
	// Dir
	for _, child := range children {
		err = RestoreAssets(dir, filepath.Join(name, child))
		if err != nil {
			return err
		}
	}
	return nil
}

func _filePath(dir, name string) string {
	cannonicalName := strings.Replace(name, "\\", "/", -1)
	return filepath.Join(append([]string{dir}, strings.Split(cannonicalName, "/")...)...)
}
