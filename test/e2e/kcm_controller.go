package e2e

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	g "github.com/onsi/ginkgo/v2"
	o "github.com/onsi/gomega"

	testlib "github.com/openshift/cluster-kube-controller-manager-operator/test/library"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
)

var _ = g.Describe("kube-controller-manager controller workloads", func() {
	g.It("[OTP][OCP-43039] openshift-object-counts quota dynamically updating as the resource is deleted", func() {
		oc := testlib.NewOC()
		oc.SetupProject()
		testlib.SetNamespacePrivileged(oc, oc.Namespace())
		g.DeferCleanup(func() {
			_ = oc.WithoutNamespace().Run("delete").Args("project", oc.Namespace()).Execute()
		})
		ns := oc.Namespace()

		err := oc.Run("create").Args("quota", "quota43039", "--hard=openshift.io/imagestreams=10").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		output, err := oc.WithoutNamespace().Run("describe").Args("quota", "quota43039", "-n", ns).Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(output).To(o.ContainSubstring("openshift.io/imagestreams  0     10"))

		err = oc.WithoutNamespace().Run("new-app").Args("quay.io/openshifttest/hello-openshift@sha256:4200f438cf2e9446f6bcff9d67ceea1f69ed07a2f83363b7fb52529f7ddd8a83", "-n", ns, "--import-mode=PreserveOriginal").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		output, err = oc.WithoutNamespace().Run("describe").Args("quota", "quota43039", "-n", ns).Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(output).To(o.ContainSubstring("openshift.io/imagestreams  1     10"))

		err = oc.WithoutNamespace().Run("delete").Args("is", "--all", "-n", ns).Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err = wait.PollUntilContextCancel(ctx, 5*time.Second, true, func(ctx context.Context) (bool, error) {
			output, err = oc.WithoutNamespace().Run("get").Args("is", "-n", ns).Output()
			if err != nil {
				return false, err
			}
			return strings.Contains(output, "No resources found"), nil
		})
		testlib.AssertWaitPollNoErr(err, "ImageStream not deleted")

		output, err = oc.WithoutNamespace().Run("describe").Args("quota", "quota43039", "-n", ns).Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(output).To(o.ContainSubstring("openshift.io/imagestreams  0     10"))
	})

	g.It("[OTP][OCP-43092] Namespaced dependents try to use cross-namespace owner references will be deleted", func() {
		oc := testlib.NewOC()
		deployT := filepath.Join(testlib.FixturePath("workloads"), "deploy_duplicatepodsrs.yaml")

		err := oc.WithoutNamespace().Run("new-project").Args("p43092-1").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())
		g.DeferCleanup(func() {
			_ = oc.WithoutNamespace().Run("delete").Args("project", "p43092-1").Execute()
		})

		err = oc.WithoutNamespace().Run("new-app").Args("quay.io/openshifttest/hello-openshift@sha256:4200f438cf2e9446f6bcff9d67ceea1f69ed07a2f83363b7fb52529f7ddd8a83", "-n", "p43092-1", "--import-mode=PreserveOriginal").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())
		testlib.CheckPodStatus(oc, "deployment=hello-openshift", "p43092-1", "Running")

		var refer string
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		err = wait.PollUntilContextCancel(ctx, 2*time.Second, true, func(ctx context.Context) (bool, error) {
			refer, err = oc.WithoutNamespace().Run("get").Args("rs", "-o=jsonpath={.items[0].metadata.ownerReferences}", "-n", "p43092-1").Output()
			return err == nil && refer != "", nil
		})
		testlib.AssertWaitPollNoErr(err, "RS ownerReferences not found")

		err = oc.WithoutNamespace().Run("new-project").Args("p43092-2").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())
		g.DeferCleanup(func() {
			_ = oc.WithoutNamespace().Run("delete").Args("project", "p43092-2").Execute()
		})

		testlib.CreateDuplicatePodsRS(oc, "hello-openshift", "p43092-2", deployT, 1)
		err = oc.WithoutNamespace().Run("patch").Args("rs/hello-openshift", "-n", "p43092-2", "--type=json", "-p", "[{\"op\": \"add\" , \"path\" : \"/metadata/ownerReferences\", \"value\":"+refer+"}]").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		ctx2, cancel2 := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel2()
		err = wait.PollUntilContextCancel(ctx2, 5*time.Second, true, func(ctx context.Context) (bool, error) {
			output, err := oc.WithoutNamespace().Run("get").Args("rs", "-n", "p43092-2").Output()
			if err != nil {
				return false, err
			}
			return strings.Contains(output, "No resources found"), nil
		})
		testlib.AssertWaitPollNoErr(err, "RS not deleted")

		eve, err := oc.WithoutNamespace().Run("get").Args("events", "-n", "p43092-2").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(eve).To(o.ContainSubstring("OwnerRefInvalidNamespace"))
	})

	g.It("[OTP][OCP-43099] Cluster-scoped dependents with namespaced kind owner references will trigger warning Event", func() {
		oc := testlib.NewOC()
		err := oc.WithoutNamespace().Run("new-project").Args("p43099").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())
		g.DeferCleanup(func() {
			_ = oc.WithoutNamespace().Run("delete").Args("project", "p43099").Execute()
		})

		err = oc.WithoutNamespace().Run("new-app").Args("quay.io/openshifttest/hello-openshift@sha256:4200f438cf2e9446f6bcff9d67ceea1f69ed07a2f83363b7fb52529f7ddd8a83", "-n", "p43099", "--import-mode=PreserveOriginal").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		var refer string
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err = wait.PollUntilContextCancel(ctx, 2*time.Second, true, func(ctx context.Context) (bool, error) {
			refer, err = oc.WithoutNamespace().Run("get").Args("rs", "-o=jsonpath={.items[0].metadata.ownerReferences}", "-n", "p43099").Output()
			return err == nil && refer != "", nil
		})
		testlib.AssertWaitPollNoErr(err, "RS ownerReferences not found in p43099")

		err = oc.WithoutNamespace().Run("create").Args("clusterrole", "foo43099", "--verb=get,list,watch", "--resource=pods,pods/status").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())
		g.DeferCleanup(func() {
			_ = oc.WithoutNamespace().Run("delete").Args("clusterrole/foo43099").Execute()
		})
		err = oc.WithoutNamespace().Run("patch").Args("clusterrole/foo43099", "--type=json", "-p", "[{\"op\": \"add\" , \"path\" : \"/metadata/ownerReferences\", \"value\":"+refer+"}]").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		ctx2, cancel2 := context.WithTimeout(context.Background(), 200*time.Second)
		defer cancel2()
		err = wait.PollUntilContextCancel(ctx2, 20*time.Second, true, func(ctx context.Context) (bool, error) {
			output, err := oc.WithoutNamespace().Run("get").Args("events", "-n", "default").Output()
			if err != nil {
				return false, err
			}
			matched, _ := regexp.MatchString("Warning.*OwnerRefInvalidNamespace.*clusterrole/foo43099", output)
			return matched, nil
		})
		testlib.AssertWaitPollNoErr(err, "OwnerRefInvalidNamespace event not found")

		output, err := oc.WithoutNamespace().Run("get").Args("clusterrole", "foo43099").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(output).To(o.ContainSubstring("foo43099"))
	})

	g.It("[OTP][OCP-63694] Verify MaxUnavailableStatefulSet feature works fine", func() {
		if !testlib.IsTechPreviewNoUpgrade() {
			g.Skip("requires TechPreviewNoUpgrade feature set")
		}
		oc := testlib.NewOC()
		oc.SetupProject()
		testlib.SetNamespacePrivileged(oc, oc.Namespace())
		g.DeferCleanup(func() {
			_ = oc.WithoutNamespace().Run("delete").Args("project", oc.Namespace()).Execute()
		})
		ns := oc.Namespace()
		ssFile := filepath.Join(testlib.FixturePath("workloads"), "statefulset_63694.yaml")

		err := oc.WithoutNamespace().Run("create").Args("-f", ssFile, "-n", ns).Execute()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(testlib.WaitForAvailableRsRunning(oc, "statefulset", "web", ns, "5")).To(o.BeTrue())

		patch := `[{"op":"replace", "path":"/spec/template/spec/containers/0/image", "value":"quay.io/openshifttest/nginx-alpine@sha256:f78c5a93df8690a5a937a6803ef4554f5b6b1ef7af4f19a441383b8976304b4c"}]`
		err = oc.WithoutNamespace().Run("patch").Args("statefulset", "web", "-n", ns, "--type=json", "-p", patch).Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
		defer cancel()
		err = wait.PollUntilContextCancel(ctx, 1*time.Second, true, func(ctx context.Context) (bool, error) {
			output, err := oc.Run("get").Args("pods", "-n", ns, "-o=name").Output()
			if err != nil {
				return false, err
			}
			return strings.Count(output, "web-") >= 5, nil
		})
		testlib.AssertWaitPollNoErr(err, "StatefulSet pods not rolled out")
	})

	g.It("[OTP][OCP-67765] Make sure rolling update logic to exclude unsetting nodes [Serial]", func() {
		if testlib.IsSNOCluster(testlib.NewOC()) {
			g.Skip("requires multi-node cluster")
		}
		oc := testlib.NewOC()
		oc.SetupProject()
		testlib.SetNamespacePrivileged(oc, oc.Namespace())
		g.DeferCleanup(func() {
			_ = oc.WithoutNamespace().Run("delete").Args("project", oc.Namespace()).Execute()
		})
		ns := oc.Namespace()

		nodeList := testlib.GetClusterNodesBy(oc, "worker")
		if len(nodeList) == 0 {
			g.Skip("no worker nodes available in cluster")
		}
		g.DeferCleanup(func() {
			_ = oc.WithoutNamespace().Run("adm").Args("taint", "node", nodeList[0], "dedicated:NoSchedule-").Execute()
		})
		err := oc.WithoutNamespace().Run("adm").Args("taint", "node", nodeList[0], "dedicated=special-user:NoSchedule").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		dsOrigin := filepath.Join(testlib.FixturePath("workloads"), "daemonset-origin.yaml")
		dsUpdate := filepath.Join(testlib.FixturePath("workloads"), "daemonset-update.yaml")
		err = oc.Run("create").Args("-f", dsOrigin).Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		kubeConfig, err := testlib.NewClientConfigForTest()
		o.Expect(err).NotTo(o.HaveOccurred())
		kubeClient, err := kubernetes.NewForConfig(kubeConfig)
		o.Expect(err).NotTo(o.HaveOccurred())

		testlib.WaitForDaemonsetPodsToBeReady(kubeClient, ns, "hello-openshift")
		o.Expect(testlib.GetDaemonsetDesiredNum(oc, ns, "hello-openshift")).To(o.Equal(len(nodeList)))

		err = oc.Run("replace").Args("-f", dsUpdate).Execute()
		o.Expect(err).NotTo(o.HaveOccurred())
		testlib.WaitForDaemonsetPodsToBeReady(kubeClient, ns, "hello-openshift")
		o.Expect(testlib.GetDaemonsetDesiredNum(oc, ns, "hello-openshift")).To(o.Equal(len(nodeList) - 1))
	})

	g.It("[OTP][OCP-69072] Infinite PODs loop creation with NodeAffinity status [Serial]", func() {
		oc := testlib.NewOC()
		projectFile := filepath.Join(testlib.FixturePath("workloads"), "project-69072.yaml")
		deployFile := filepath.Join(testlib.FixturePath("workloads"), "deployment-69072.yaml")

		err := oc.WithoutNamespace().Run("create").Args("-f", projectFile).Execute()
		o.Expect(err).NotTo(o.HaveOccurred())
		g.DeferCleanup(func() {
			_ = oc.WithoutNamespace().Run("delete").Args("-f", projectFile).Execute()
		})
		err = oc.WithoutNamespace().Run("create").Args("-f", deployFile, "-n", "infinite-pod-creation-69072").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())
		g.DeferCleanup(func() {
			_ = oc.WithoutNamespace().Run("delete").Args("deployment", "infinite-pod-creation-69072", "-n", "infinite-pod-creation-69072").Execute()
		})

		testlib.CheckPodStatus(oc, "app=infinite-pod-creation-69072", "infinite-pod-creation-69072", "Running")
		masters := testlib.GetClusterNodesBy(oc, "master")
		if len(masters) == 0 {
			g.Skip("no master nodes available in cluster")
		}
		patch := fmt.Sprintf(`{"spec":{"template":{"spec":{"nodeName": "%s"}}}}`, masters[0])
		_, err = oc.WithoutNamespace().Run("patch").Args("deployment", "-n", "infinite-pod-creation-69072", "infinite-pod-creation-69072", "--type=merge", "-p", patch).Output()
		o.Expect(err).NotTo(o.HaveOccurred())

		podStatus, err := oc.WithoutNamespace().Run("get").Args("pods", "-n", "infinite-pod-creation-69072").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(podStatus).NotTo(o.ContainSubstring("NodeAffinity"))
	})

	g.It("[OTP][OCP-19922] Terminating pod should be removed from endpoints list for service", func() {
		oc := testlib.NewOC()
		oc.SetupProject()
		testlib.SetNamespacePrivileged(oc, oc.Namespace())
		g.DeferCleanup(func() {
			_ = oc.WithoutNamespace().Run("delete").Args("project", oc.Namespace()).Execute()
		})
		ns := oc.Namespace()
		deployFile := filepath.Join(testlib.FixturePath("workloads"), "deployment-with-shutdown-gracefully.yaml")

		err := oc.Run("create").Args("-f", deployFile).Execute()
		o.Expect(err).NotTo(o.HaveOccurred())
		err = oc.Run("expose").Args("deployment", "hello-19922").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(testlib.WaitForAvailableRsRunning(oc, "deployment", "hello-19922", ns, "1")).To(o.BeTrue())

		podIP, err := oc.Run("get").Args("pod", "-l", "deployment=hello-19922", "-o=jsonpath={.items[*].status.podIP}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())

		err = oc.Run("set").Args("env", "deployment", "hello-19922", "foo=bar").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		err = wait.PollUntilContextCancel(ctx, 10*time.Second, true, func(ctx context.Context) (bool, error) {
			describeSVC, err := oc.Run("describe").Args("svc", "hello-19922").Output()
			if err != nil {
				return false, err
			}
			return !strings.Contains(describeSVC, podIP), nil
		})
		testlib.AssertWaitPollNoErr(err, "terminating pod still in endpoints")
	})
})
