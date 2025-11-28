package mom

import (
	"context"

	"github.com/openshift/multi-operator-manager/pkg/library/libraryinputresources"
	"github.com/openshift/multi-operator-manager/pkg/library/libraryoutputresources"
	"github.com/spf13/cobra"
	"k8s.io/cli-runtime/pkg/genericiooptions"
)

func NewInputResourcesCommand(streams genericiooptions.IOStreams) *cobra.Command {
	return libraryinputresources.NewInputResourcesCommand(runInputResources, runOutputResources, streams)
}

func runInputResources(ctx context.Context) (*libraryinputresources.InputResources, error) {
	return &libraryinputresources.InputResources{
		ApplyConfigurationResources: libraryinputresources.ResourceList{
			ExactResources: []libraryinputresources.ExactResourceID{
				// Operator CR
				libraryinputresources.ExactLowLevelOperator("kubecontrollermanagers"),

				// Config resources
				libraryinputresources.ExactConfigResource("infrastructures"),
				libraryinputresources.ExactConfigResource("networks"),
				libraryinputresources.ExactConfigResource("featuregates"),
				libraryinputresources.ExactConfigResource("nodes"),
				libraryinputresources.ExactConfigResource("proxies"),
				libraryinputresources.ExactConfigResource("apiservers"),
				libraryinputresources.ExactConfigResource("clusterversions"),

				// Namespaces
				libraryinputresources.ExactNamespace("openshift-config"),
				libraryinputresources.ExactNamespace("openshift-config-managed"),
				libraryinputresources.ExactNamespace("openshift-kube-controller-manager"),
				libraryinputresources.ExactNamespace("openshift-kube-controller-manager-operator"),
				libraryinputresources.ExactNamespace("kube-system"),
				libraryinputresources.ExactNamespace("openshift-infra"),

				// ConfigMaps that may be synced or referenced
				libraryinputresources.ExactConfigMap("openshift-config", "cloud-provider-config"),
				libraryinputresources.ExactConfigMap("openshift-config-managed", "kube-controller-cert-syncer-kubeconfig"),
				libraryinputresources.ExactConfigMap("kube-system", "cluster-config-v1"),

				// Secrets that may be synced or referenced
				libraryinputresources.ExactSecret("openshift-config", "cloud-credentials"),
			},
		},
	}, nil
}

// runOutputResources is defined here to support the input-resources command
// This is shared with output_resources_command.go
func runOutputResources(ctx context.Context) (*libraryoutputresources.OutputResources, error) {
	return &libraryoutputresources.OutputResources{
		ConfigurationResources: libraryoutputresources.ResourceList{
			ExactResources: []libraryoutputresources.ExactResourceID{},
		},
		ManagementResources: libraryoutputresources.ResourceList{
			ExactResources: []libraryoutputresources.ExactResourceID{
				// ClusterOperator status
				libraryoutputresources.ExactClusterOperator("kube-controller-manager"),

				// Namespaces managed by the operator
				libraryoutputresources.ExactNamespace("openshift-kube-controller-manager"),
				libraryoutputresources.ExactNamespace("openshift-kube-controller-manager-operator"),
				libraryoutputresources.ExactNamespace("openshift-infra"),

				// Operator deployment and service
				libraryoutputresources.ExactDeployment("openshift-kube-controller-manager-operator", "kube-controller-manager-operator"),
				libraryoutputresources.ExactService("openshift-kube-controller-manager-operator", "kube-controller-manager-operator"),
				libraryoutputresources.ExactServiceAccount("openshift-kube-controller-manager-operator", "kube-controller-manager-operator"),

				// Static pod resources in target namespace
				libraryoutputresources.ExactService("openshift-kube-controller-manager", "kube-controller-manager"),
				libraryoutputresources.ExactServiceAccount("openshift-kube-controller-manager", "kube-controller-manager"),
				libraryoutputresources.ExactServiceAccount("openshift-kube-controller-manager", "localhost-recovery-client"),
				libraryoutputresources.ExactServiceAccount("openshift-kube-controller-manager", "kube-controller-manager-sa"),

				// ConfigMaps
				libraryoutputresources.ExactConfigMap("openshift-kube-controller-manager", "config"),
				libraryoutputresources.ExactConfigMap("openshift-kube-controller-manager", "kube-controller-manager-pod"),
				libraryoutputresources.ExactConfigMap("openshift-kube-controller-manager", "cluster-policy-controller-config"),
				libraryoutputresources.ExactConfigMap("openshift-kube-controller-manager", "controller-manager-kubeconfig"),
				libraryoutputresources.ExactConfigMap("openshift-kube-controller-manager", "kube-controller-cert-syncer-kubeconfig"),
				libraryoutputresources.ExactConfigMap("openshift-kube-controller-manager", "serviceaccount-ca"),
				libraryoutputresources.ExactConfigMap("openshift-kube-controller-manager", "service-ca"),
				libraryoutputresources.ExactConfigMap("openshift-kube-controller-manager", "recycler-config"),
				libraryoutputresources.ExactConfigMap("openshift-kube-controller-manager", "trusted-ca-bundle"),
				libraryoutputresources.ExactConfigMap("openshift-kube-controller-manager", "aggregator-client-ca"),
				libraryoutputresources.ExactConfigMap("openshift-kube-controller-manager", "client-ca"),

				// Secrets
				libraryoutputresources.ExactSecret("openshift-kube-controller-manager", "service-account-private-key"),
				libraryoutputresources.ExactSecret("openshift-kube-controller-manager", "serving-cert"),
				libraryoutputresources.ExactSecret("openshift-kube-controller-manager", "localhost-recovery-client-token"),
				libraryoutputresources.ExactSecret("openshift-kube-controller-manager", "kube-controller-manager-client-cert-key"),
				libraryoutputresources.ExactSecret("openshift-kube-controller-manager", "csr-signer"),

				// Roles and RoleBindings in target namespace
				libraryoutputresources.ExactRole("kube-system", "system:openshift:controller:cluster-policy-controller"),
				libraryoutputresources.ExactRoleBinding("kube-system", "system:openshift:controller:cluster-policy-controller"),

				// PodDisruptionBudget
				libraryoutputresources.ExactPDB("openshift-kube-controller-manager-operator", "kube-controller-manager-operator"),
			},
			EventingNamespaces: []string{
				"openshift-kube-controller-manager",
				"openshift-kube-controller-manager-operator",
			},
		},
		UserWorkloadResources: libraryoutputresources.ResourceList{
			ExactResources: []libraryoutputresources.ExactResourceID{
				// CSR-related resources
				libraryoutputresources.ExactClusterRole("system:openshift:controller:cluster-csr-approver"),
				libraryoutputresources.ExactClusterRoleBinding("system:openshift:controller:cluster-csr-approver"),

				// Namespace security allocation controller
				libraryoutputresources.ExactClusterRole("system:openshift:controller:namespace-security-allocation-controller"),
				libraryoutputresources.ExactClusterRoleBinding("system:openshift:controller:namespace-security-allocation-controller"),

				// PodSecurity admission label syncer controller
				libraryoutputresources.ExactClusterRole("system:openshift:controller:podsecurity-admission-label-syncer-controller"),
				libraryoutputresources.ExactClusterRoleBinding("system:openshift:controller:podsecurity-admission-label-syncer-controller"),

				// PodSecurity admission label privileged namespaces syncer controller
				libraryoutputresources.ExactClusterRole("system:openshift:controller:podsecurity-admission-label-privileged-namespaces-syncer-controller"),
				libraryoutputresources.ExactClusterRoleBinding("system:openshift:controller:podsecurity-admission-label-privileged-namespaces-syncer-controller"),

				// Localhost recovery
				libraryoutputresources.ExactClusterRoleBinding("system:openshift:operator:kube-controller-manager-recovery"),

				// Operator RBAC
				libraryoutputresources.ExactClusterRoleBinding("system:openshift:operator:kube-controller-manager-operator"),
			},
		},
	}, nil
}
