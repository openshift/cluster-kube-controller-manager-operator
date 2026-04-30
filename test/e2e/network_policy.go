package e2e

import (
	"context"
	"fmt"
	"time"

	g "github.com/onsi/ginkgo/v2"
	o "github.com/onsi/gomega"

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"

	testlib "github.com/openshift/cluster-kube-controller-manager-operator/test/library"
)

const (
	kcmOperatorNamespace  = "openshift-kube-controller-manager-operator"
	kcmTargetNamespace    = "openshift-kube-controller-manager"
	defaultDenyPolicyName = "default-deny-all"
	allowOperatorPolicy   = "allow-operator"
	allowKCMPolicy        = "allow"
	allowInstallerPolicy  = "allow-installer"
)

type policyTestCase struct {
	desc      string
	namespace string
	name      string
	denyAll   bool
}

var _ = g.Describe("kube-controller-manager-operator NetworkPolicy", func() {
	g.It("should ensure KCM NetworkPolicies are defined", func() {
		testKCMNetworkPolicies()
	})
	g.It("should restore KCM NetworkPolicies after delete or mutation [Serial][Disruptive]", func() {
		testKCMNetworkPolicyReconcile()
	})
})

func testKCMNetworkPolicies() {
	policies := []policyTestCase{
		{desc: "operator default-deny", namespace: kcmOperatorNamespace, name: defaultDenyPolicyName, denyAll: true},
		{desc: "operator allow", namespace: kcmOperatorNamespace, name: allowOperatorPolicy},
		{desc: "operand default-deny", namespace: kcmTargetNamespace, name: defaultDenyPolicyName, denyAll: true},
		{desc: "operand allow", namespace: kcmTargetNamespace, name: allowKCMPolicy},
		{desc: "operand allow-installer", namespace: kcmTargetNamespace, name: allowInstallerPolicy},
	}

	ctx := context.Background()
	kubeConfig, err := testlib.NewClientConfigForTest()
	o.Expect(err).NotTo(o.HaveOccurred())
	kubeClient, err := kubernetes.NewForConfig(kubeConfig)
	o.Expect(err).NotTo(o.HaveOccurred())

	for _, tc := range policies {
		g.By(fmt.Sprintf("%s: validating %s/%s", tc.desc, tc.namespace, tc.name))

		policy, err := kubeClient.NetworkingV1().NetworkPolicies(tc.namespace).Get(ctx, tc.name, metav1.GetOptions{})
		o.Expect(err).NotTo(o.HaveOccurred(), "%s: policy %s/%s should exist", tc.desc, tc.namespace, tc.name)

		if tc.denyAll {
			o.Expect(policy.Spec.PodSelector.MatchLabels).To(o.BeEmpty(),
				"%s: should select all pods", tc.desc)
			o.Expect(policy.Spec.PodSelector.MatchExpressions).To(o.BeEmpty(),
				"%s: should have no match expressions", tc.desc)
			o.Expect(policy.Spec.Ingress).To(o.BeEmpty(),
				"%s: should have no ingress rules", tc.desc)
			o.Expect(policy.Spec.Egress).To(o.BeEmpty(),
				"%s: should have no egress rules", tc.desc)
			continue
		}

		hasAllowAll := false
		for _, rule := range policy.Spec.Egress {
			if len(rule.To) == 0 && len(rule.Ports) == 0 {
				hasAllowAll = true
				break
			}
		}
		o.Expect(hasAllowAll).To(o.BeTrue(),
			"%s: should have an egress allow-all rule", tc.desc)

		for _, pt := range policy.Spec.PolicyTypes {
			if pt == networkingv1.PolicyTypeIngress {
				o.Expect(policy.Spec.Ingress).NotTo(o.BeEmpty(),
					"%s: should have ingress rules when Ingress policyType is present", tc.desc)
				for _, rule := range policy.Spec.Ingress {
					o.Expect(rule.Ports).NotTo(o.BeEmpty(),
						"%s: ingress rules should specify ports", tc.desc)
				}
			}
		}
	}
}

func testKCMNetworkPolicyReconcile() {
	policies := []policyTestCase{
		{desc: "operand default-deny", namespace: kcmTargetNamespace, name: defaultDenyPolicyName, denyAll: true},
		{desc: "operand allow", namespace: kcmTargetNamespace, name: allowKCMPolicy},
		{desc: "operand allow-installer", namespace: kcmTargetNamespace, name: allowInstallerPolicy},
	}

	ctx := context.Background()
	kubeConfig, err := testlib.NewClientConfigForTest()
	o.Expect(err).NotTo(o.HaveOccurred())
	kubeClient, err := kubernetes.NewForConfig(kubeConfig)
	o.Expect(err).NotTo(o.HaveOccurred())

	for _, tc := range policies {
		g.By(fmt.Sprintf("%s: reconciling %s/%s", tc.desc, tc.namespace, tc.name))

		original, err := kubeClient.NetworkingV1().NetworkPolicies(tc.namespace).Get(ctx, tc.name, metav1.GetOptions{})
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By(fmt.Sprintf("%s: deleting %s/%s and waiting for restoration", tc.desc, tc.namespace, tc.name))
		err = kubeClient.NetworkingV1().NetworkPolicies(tc.namespace).Delete(ctx, tc.name, metav1.DeleteOptions{})
		o.Expect(err).NotTo(o.HaveOccurred())

		o.Eventually(func() (*networkingv1.NetworkPolicySpec, error) {
			restored, err := kubeClient.NetworkingV1().NetworkPolicies(tc.namespace).Get(ctx, tc.name, metav1.GetOptions{})
			if err != nil {
				return nil, err
			}
			return &restored.Spec, nil
		}).WithTimeout(5*time.Minute).WithPolling(5*time.Second).Should(o.Equal(&original.Spec),
			"%s: %s/%s should be restored after deletion", tc.desc, tc.namespace, tc.name)

		g.By(fmt.Sprintf("%s: mutating %s/%s and waiting for reconciliation", tc.desc, tc.namespace, tc.name))
		patch := []byte(`{"spec":{"podSelector":{"matchLabels":{"np-reconcile":"mutated"}}}}`)
		_, err = kubeClient.NetworkingV1().NetworkPolicies(tc.namespace).Patch(ctx, tc.name, types.MergePatchType, patch, metav1.PatchOptions{})
		o.Expect(err).NotTo(o.HaveOccurred())

		o.Eventually(func() (*networkingv1.NetworkPolicySpec, error) {
			current, err := kubeClient.NetworkingV1().NetworkPolicies(tc.namespace).Get(ctx, tc.name, metav1.GetOptions{})
			if err != nil {
				return nil, err
			}
			return &current.Spec, nil
		}).WithTimeout(5*time.Minute).WithPolling(5*time.Second).Should(o.Equal(&original.Spec),
			"%s: %s/%s should be reconciled after mutation", tc.desc, tc.namespace, tc.name)
	}
}
