package e2e

import (
	"context"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	g "github.com/onsi/ginkgo/v2"
	o "github.com/onsi/gomega"

	configv1 "github.com/openshift/api/config/v1"
	configclient "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/operatorclient"
	testlib "github.com/openshift/cluster-kube-controller-manager-operator/test/library"
	"k8s.io/apimachinery/pkg/util/wait"
)

var _ = g.Describe("kube-controller-manager-operator workloads", func() {
	g.It("[OTP][OCP-28001] KCM should recover when its temporary secrets are deleted [Serial][Disruptive]", func() {
		oc := testlib.NewOC()
		namespace := operatorclient.TargetNamespace

		kubeConfig, err := testlib.NewClientConfigForTest()
		o.Expect(err).NotTo(o.HaveOccurred())
		configClient, err := configclient.NewForConfig(kubeConfig)
		o.Expect(err).NotTo(o.HaveOccurred())

		// Ensure operator recovers to healthy state even if test fails mid-way
		g.DeferCleanup(func() {
			recoverCtx, recoverCancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer recoverCancel()
			testlib.WaitForKubeControllerManagerClusterOperator(g.GinkgoTB(), recoverCtx, configClient, configv1.ConditionTrue, configv1.ConditionFalse, configv1.ConditionFalse)
		})

		var temporarySecrets []string
		output, err := oc.Run("get").Args("secrets", "-n", namespace, "-o=jsonpath={.items[*].metadata.name}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		for _, name := range strings.Fields(output) {
			annotations, err := oc.Run("get").Args("secrets", "-n", namespace, name, "-o=jsonpath={.metadata.annotations}").Output()
			o.Expect(err).NotTo(o.HaveOccurred())
			if matched, _ := regexp.MatchString("kubernetes.io/service-account.name", annotations); matched {
				continue
			}
			ownerKind, err := oc.Run("get").Args("secrets", "-n", namespace, name, "-o=jsonpath={.metadata.ownerReferences[0].kind}").Output()
			o.Expect(err).NotTo(o.HaveOccurred())
			if ownerKind == "ConfigMap" {
				continue
			}
			temporarySecrets = append(temporarySecrets, name)
		}

		for _, secret := range temporarySecrets {
			_, err = oc.Run("delete").Args("secrets", "-n", namespace, secret).Output()
			o.Expect(err).NotTo(o.HaveOccurred())
		}

		err = testlib.WaitCoBecomes(oc, "kube-controller-manager", 100, map[string]string{"Progressing": "True"})
		testlib.AssertWaitPollNoErr(err, "kube-controller-manager operator did not start progressing")

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		testlib.WaitForKubeControllerManagerClusterOperator(g.GinkgoTB(), ctx, configClient, configv1.ConditionTrue, configv1.ConditionFalse, configv1.ConditionFalse)
	})

	g.It("[OTP][OCP-43035] KCM use internal LB to avoid outages during kube-apiserver rollout [Serial][Disruptive]", func() {
		oc := testlib.NewOC()
		infra, err := oc.WithoutNamespace().Run("get").Args("infrastructures", "cluster", "-o=jsonpath={.status.infrastructureTopology}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		if infra == "SingleReplica" {
			g.Skip("SNO cluster")
		}

		output, err := oc.WithoutNamespace().Run("whoami").Args("--show-server").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		apiURL, err := url.Parse(strings.TrimSpace(output))
		o.Expect(err).NotTo(o.HaveOccurred())
		host := apiURL.Hostname()
		o.Expect(host).To(o.HavePrefix("api."))
		internalLB := "server: https://api-int." + strings.TrimPrefix(host, "api.")

		output, err = oc.WithoutNamespace().Run("get").Args("configmap", "controller-manager-kubeconfig", "-n", operatorclient.TargetNamespace, "-oyaml").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(output).To(o.ContainSubstring(internalLB))

		leaderKcm := testlib.GetLeaderKCM(oc)
		g.DeferCleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			var lastErr error
			err := wait.PollUntilContextCancel(ctx, 5*time.Second, true, func(ctx context.Context) (bool, error) {
				_, lastErr = testlib.DebugNodeWithChroot(oc, leaderKcm, "mv", "/home/kube-apiserver-pod.yaml", "/etc/kubernetes/manifests/")
				return lastErr == nil, nil
			})
			if err != nil {
				o.Expect(lastErr).NotTo(o.HaveOccurred())
			}
		})
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		err = wait.PollUntilContextCancel(ctx, 5*time.Second, true, func(ctx context.Context) (bool, error) {
			_, err := testlib.DebugNodeWithChroot(oc, leaderKcm, "mv", "/etc/kubernetes/manifests/kube-apiserver-pod.yaml", "/home/")
			return err == nil, nil
		})
		o.Expect(err).NotTo(o.HaveOccurred())
		err = oc.WithoutNamespace().Run("delete").Args("-n", "openshift-kube-apiserver", "pod/"+"kube-apiserver-"+leaderKcm).Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		ctx2, cancel2 := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel2()
		err = wait.PollUntilContextCancel(ctx2, 5*time.Second, true, func(ctx context.Context) (bool, error) {
			output, err := oc.Run("get").Args("co", "kube-controller-manager").Output()
			if err != nil {
				return false, nil
			}
			matched, _ := regexp.MatchString("True.*False.*False", output)
			return matched, nil
		})
		testlib.AssertWaitPollNoErr(err, "kube-controller-manager operator unhealthy during KAS disruption")
	})

	g.It("[OTP][OCP-60194] Make sure KCM KS operator is rebased onto the latest version of Kubernetes", func() {
		oc := testlib.NewOC()
		ocVersion, err := oc.WithoutNamespace().Run("get").Args("node", "-o=jsonpath={.items[0].status.nodeInfo.kubeletVersion}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		kubeVersion := strings.Split(strings.Split(ocVersion, "+")[0], "v")[1]
		kuberVersion := strings.Split(kubeVersion, ".")[0] + "." + strings.Split(kubeVersion, ".")[1]

		kcmPod, err := oc.WithoutNamespace().Run("describe").Args("pod", "-n", operatorclient.OperatorNamespace, "-l", "app=kube-controller-manager-operator").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(kcmPod).To(o.ContainSubstring(kuberVersion))

		ksPod, err := oc.WithoutNamespace().Run("describe").Args("pod", "-n", "openshift-kube-scheduler-operator", "-l", "app=openshift-kube-scheduler-operator").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(ksPod).To(o.ContainSubstring(kuberVersion))
	})

	g.It("63962 Verify MaxUnavailableStatefulSet feature is available via TechPreviewNoUpgrade", func() {
		if !testlib.IsTechPreviewNoUpgrade() {
			g.Skip("requires TechPreviewNoUpgrade feature set")
		}
		oc := testlib.NewOC()
		kcmPodOut, err := oc.WithoutNamespace().Run("get").Args("pod", "-n", operatorclient.TargetNamespace, "-l", "app=kube-controller-manager", "-o=jsonpath={.items[0].spec.containers[0].args}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(kcmPodOut).To(o.ContainSubstring("--feature-gates=MaxUnavailableStatefulSet=true"))
	})

	g.It("[OTP][OCP-70877] Add annotation in the kube-controller-manager-guard static pod for workload partitioning", func() {
		oc := testlib.NewOC()
		testlib.SkipForSNOCluster(oc)
		guardPods, err := oc.WithoutNamespace().Run("get").Args("po", "-n", operatorclient.TargetNamespace, "-l=app=guard", `-ojsonpath={.items[?(@.status.phase=="Running")].metadata.name}`).Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		names := strings.Fields(guardPods)
		o.Expect(names).NotTo(o.BeEmpty())
		annotation, err := oc.WithoutNamespace().Run("get").Args("po", "-n", operatorclient.TargetNamespace, names[0], `-ojsonpath={.metadata.annotations}`).Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(annotation).To(o.ContainSubstring(`"target.workload.openshift.io/management":"{\"effect\": \"PreferredDuringScheduling\"}"`))
	})

	g.It("[OTP][OCP-76130] make sure kcm should not restart when processing StatefulSet with spec.podManagementPolicy is Parallel [Serial]", func() {
		oc := testlib.NewOC()
		oc.SetupProject()
		testlib.SetNamespacePrivileged(oc, oc.Namespace())
		g.DeferCleanup(func() {
			_ = oc.WithoutNamespace().Run("delete").Args("project", oc.Namespace()).Execute()
		})
		kcmRestartOrigin, err := oc.WithoutNamespace().Run("get").Args("pod", "-l", "app=kube-controller-manager", "-o=jsonpath={.items[*].status.containerStatuses[?(@.name==\"kube-controller-manager\")].restartCount}", "-n", operatorclient.TargetNamespace).Output()
		o.Expect(err).NotTo(o.HaveOccurred())

		appFile := filepath.Join(testlib.FixturePath("workloads"), "statefulset-76130.yaml")
		err = oc.Run("create").Args("-f", appFile).Execute()
		o.Expect(err).NotTo(o.HaveOccurred())
		time.Sleep(600 * time.Second)

		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		err = wait.PollUntilContextCancel(ctx, 30*time.Second, true, func(ctx context.Context) (bool, error) {
			kcmRestartSecond, err := oc.WithoutNamespace().Run("get").Args("pod", "-l", "app=kube-controller-manager", "-o=jsonpath={.items[*].status.containerStatuses[?(@.name==\"kube-controller-manager\")].restartCount}", "-n", operatorclient.TargetNamespace).Output()
			if err != nil {
				return false, err
			}
			return kcmRestartSecond == kcmRestartOrigin, nil
		})
		testlib.AssertWaitPollNoErr(err, "unexpected KCM restart after parallel StatefulSet")
	})

	g.It("[OTP][OCP-79105] Verify KCM does not crash with panic when RevisionHistoryLimit is set to Negative value", func() {
		oc := testlib.NewOC()
		oc.SetupProject()
		testlib.SetNamespacePrivileged(oc, oc.Namespace())
		g.DeferCleanup(func() {
			_ = oc.WithoutNamespace().Run("delete").Args("project", oc.Namespace()).Execute()
		})
		ns := oc.Namespace()
		kcmRevision, err := oc.WithoutNamespace().Run("get").Args("pod", "-n", operatorclient.TargetNamespace, "-l", "app=kube-controller-manager", "-o=jsonpath={.items[0].metadata.labels.revision}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())

		ssFile := filepath.Join(testlib.FixturePath("workloads"), "statefulset-79105.yaml")
		err = oc.WithoutNamespace().Run("create").Args("-f", ssFile, "-n", ns).Execute()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(testlib.WaitForAvailableRsRunning(oc, "statefulset", "hello-statefulset", ns, "2")).To(o.BeTrue())

		kcmRevisionNew, err := oc.WithoutNamespace().Run("get").Args("pod", "-n", operatorclient.TargetNamespace, "-l", "app=kube-controller-manager", "-o=jsonpath={.items[0].metadata.labels.revision}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(kcmRevisionNew).To(o.Equal(kcmRevision))

		err = testlib.WaitCoBecomes(oc, "kube-controller-manager", 60, map[string]string{"Available": "True", "Progressing": "False", "Degraded": "False"})
		o.Expect(err).NotTo(o.HaveOccurred())
	})

})
