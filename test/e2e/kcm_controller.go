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

const (
	project43092Source = "p43092-1"
	project43092Target = "p43092-2"
	project43099       = "p43099"
	clusterRole43099   = "foo43099"
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

		_ = oc.WithoutNamespace().Run("delete").Args("project", project43092Source, "--ignore-not-found").Execute()
		_ = oc.WithoutNamespace().Run("delete").Args("project", project43092Target, "--ignore-not-found").Execute()

		err := oc.WithoutNamespace().Run("new-project").Args(project43092Source).Execute()
		o.Expect(err).NotTo(o.HaveOccurred())
		g.DeferCleanup(func() {
			_ = oc.WithoutNamespace().Run("delete").Args("project", project43092Source).Execute()
		})

		err = oc.WithoutNamespace().Run("new-app").Args("quay.io/openshifttest/hello-openshift@sha256:4200f438cf2e9446f6bcff9d67ceea1f69ed07a2f83363b7fb52529f7ddd8a83", "-n", project43092Source, "--import-mode=PreserveOriginal").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())
		testlib.CheckPodStatus(oc, "deployment=hello-openshift", project43092Source, "Running")

		var reference string
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		err = wait.PollUntilContextCancel(ctx, 2*time.Second, true, func(ctx context.Context) (bool, error) {
			reference, err = oc.WithoutNamespace().Run("get").Args("rs", "-o=jsonpath={.items[0].metadata.ownerReferences}", "-n", project43092Source).Output()
			return err == nil && reference != "", nil
		})
		testlib.AssertWaitPollNoErr(err, "RS ownerReferences not found")

		err = oc.WithoutNamespace().Run("new-project").Args(project43092Target).Execute()
		o.Expect(err).NotTo(o.HaveOccurred())
		g.DeferCleanup(func() {
			_ = oc.WithoutNamespace().Run("delete").Args("project", project43092Target).Execute()
		})

		ctx3, cancel3 := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel3()
		err = wait.PollUntilContextCancel(ctx3, 5*time.Second, true, func(ctx context.Context) (bool, error) {
			err := testlib.ApplyResourceFromTemplate(oc, "--ignore-unknown-parameters=true", "-f", deployT,
				"-p", "DNAME=hello-openshift", "NAMESPACE="+project43092Target, "REPLICASNUM=1")
			return err == nil, nil
		})
		testlib.AssertWaitPollNoErr(err, "create duplicate pods RS")
		err = oc.WithoutNamespace().Run("patch").Args("rs/hello-openshift", "-n", project43092Target, "--type=json", "-p", "[{\"op\": \"add\" , \"path\" : \"/metadata/ownerReferences\", \"value\":"+reference+"}]").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		ctx2, cancel2 := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel2()
		err = wait.PollUntilContextCancel(ctx2, 5*time.Second, true, func(ctx context.Context) (bool, error) {
			output, err := oc.WithoutNamespace().Run("get").Args("rs", "-n", project43092Target).Output()
			if err != nil {
				return false, err
			}
			return strings.Contains(output, "No resources found"), nil
		})
		testlib.AssertWaitPollNoErr(err, "RS not deleted")

		eve, err := oc.WithoutNamespace().Run("get").Args("events", "-n", project43092Target).Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(eve).To(o.ContainSubstring("OwnerRefInvalidNamespace"))
	})

	g.It("[OTP][OCP-43099] Cluster-scoped dependents with namespaced kind owner references will trigger warning Event", func() {
		oc := testlib.NewOC()
		_ = oc.WithoutNamespace().Run("delete").Args("project", project43099, "--ignore-not-found").Execute()
		_ = oc.WithoutNamespace().Run("delete").Args("clusterrole", clusterRole43099, "--ignore-not-found").Execute()

		err := oc.WithoutNamespace().Run("new-project").Args(project43099).Execute()
		o.Expect(err).NotTo(o.HaveOccurred())
		g.DeferCleanup(func() {
			_ = oc.WithoutNamespace().Run("delete").Args("project", project43099).Execute()
		})

		err = oc.WithoutNamespace().Run("new-app").Args("quay.io/openshifttest/hello-openshift@sha256:4200f438cf2e9446f6bcff9d67ceea1f69ed07a2f83363b7fb52529f7ddd8a83", "-n", project43099, "--import-mode=PreserveOriginal").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		var reference string
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err = wait.PollUntilContextCancel(ctx, 2*time.Second, true, func(ctx context.Context) (bool, error) {
			reference, err = oc.WithoutNamespace().Run("get").Args("rs", "-o=jsonpath={.items[0].metadata.ownerReferences}", "-n", project43099).Output()
			return err == nil && reference != "", nil
		})
		testlib.AssertWaitPollNoErr(err, "RS ownerReferences not found in "+project43099)

		err = oc.WithoutNamespace().Run("create").Args("clusterrole", clusterRole43099, "--verb=get,list,watch", "--resource=pods,pods/status").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())
		g.DeferCleanup(func() {
			_ = oc.WithoutNamespace().Run("delete").Args("clusterrole/" + clusterRole43099).Execute()
		})
		err = oc.WithoutNamespace().Run("patch").Args("clusterrole/"+clusterRole43099, "--type=json", "-p", "[{\"op\": \"add\" , \"path\" : \"/metadata/ownerReferences\", \"value\":"+reference+"}]").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		ctx2, cancel2 := context.WithTimeout(context.Background(), 200*time.Second)
		defer cancel2()
		err = wait.PollUntilContextCancel(ctx2, 20*time.Second, true, func(ctx context.Context) (bool, error) {
			output, err := oc.WithoutNamespace().Run("get").Args("events", "-n", "default").Output()
			if err != nil {
				return false, err
			}
			matched, _ := regexp.MatchString("Warning.*OwnerRefInvalidNamespace.*clusterrole/"+clusterRole43099, output)
			return matched, nil
		})
		testlib.AssertWaitPollNoErr(err, "OwnerRefInvalidNamespace event not found")

		output, err := oc.WithoutNamespace().Run("get").Args("clusterrole", clusterRole43099).Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(output).To(o.ContainSubstring(clusterRole43099))
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
		fmt.Fprintf(g.GinkgoWriter, "pod listing:\n%s\n", podStatus)
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
