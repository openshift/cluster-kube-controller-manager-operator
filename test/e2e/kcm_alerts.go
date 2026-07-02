package e2e

import (
	"path/filepath"

	g "github.com/onsi/ginkgo/v2"
	o "github.com/onsi/gomega"

	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/operatorclient"
	testlib "github.com/openshift/cluster-kube-controller-manager-operator/test/library"
)

var _ = g.Describe("kube-controller-manager alert workloads", func() {
	g.It("[OTP][OCP-26247] Alert when pod has a PodDisruptionBudget with minAvailable 1 disruptionsAllowed 0", func() {
		oc := testlib.NewOC()
		oc.SetupProject()
		testlib.SetNamespacePrivileged(oc, oc.Namespace())
		g.DeferCleanup(func() {
			_ = oc.WithoutNamespace().Run("delete").Args("project", oc.Namespace()).Execute()
		})
		ns := oc.Namespace()

		err := oc.WithoutNamespace().Run("create").Args("deployment", "deploy26247", "-n", ns, "--image", "quay.io/openshifttest/hello-openshift@sha256:4200f438cf2e9446f6bcff9d67ceea1f69ed07a2f83363b7fb52529f7ddd8a83").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		err = oc.WithoutNamespace().Run("create").Args("poddisruptionbudget", "pdb26247", "--selector=app=deploy26247", "--min-available=1", "-n", ns).Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		token := testlib.GetSAToken(oc)
		testlib.CheckMetric(oc, testlib.PrometheusURL+` --data-urlencode 'query=ALERTS{alertname="PodDisruptionBudgetAtLimit"}'`, token, "pdb26247", 600)

		err = oc.Run("scale").Args("deploy", "deploy26247", "--replicas=0").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())
		testlib.CheckMetric(oc, testlib.PrometheusURL+` --data-urlencode 'query=ALERTS{alertname="PodDisruptionBudgetLimit"}'`, token, "pdb26247", 600)
	})

	g.It("[OTP][OCP-73887] validate for alert KubeDeploymentReplicasMismatch [Serial][Disruptive]", func() {
		oc := testlib.NewOC()
		workers := testlib.GetClusterNodesBy(oc, "worker")

		g.DeferCleanup(func() {
			for _, v := range workers {
				_ = oc.WithoutNamespace().Run("adm").Args("uncordon", v).Execute()
			}
			for _, v := range workers {
				_ = testlib.CheckNodeUncordoned(oc, v)
			}
		})

		if len(workers) > 1 {
			for i := 0; i < len(workers)-1; i++ {
				err := oc.Run("adm").Args("cordon", workers[i]).Execute()
				o.Expect(err).NotTo(o.HaveOccurred())
			}
		}

		deployFile := filepath.Join(testlib.FixturePath("workloads"), "deployment-73887.yaml")
		g.DeferCleanup(func() {
			_ = oc.Run("delete").Args("-f", deployFile, "-n", "default").Execute()
		})
		err := oc.Run("create").Args("-f", deployFile, "-n", "default").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		if !testlib.WaitForAvailableRsRunning(oc, "deployment", "replicas-mismatch", "default", "5") {
			token := testlib.GetSAToken(oc)
			testlib.CheckMetric(oc, testlib.PrometheusURL+` --data-urlencode 'query=ALERTS{alertname="KubeDeploymentReplicasMismatch"}'`, token, "replicas-mismatch", 600)
		}
	})

	g.It("[OTP][OCP-73886] Validate for alert KubeJobFailed", func() {
		oc := testlib.NewOC()
		jobFile := filepath.Join(testlib.FixturePath("workloads"), "kubejobfailed-73886.yaml")
		g.DeferCleanup(func() {
			_ = oc.Run("delete").Args("-f", jobFile, "-n", "default").Execute()
		})
		err := oc.Run("create").Args("-f", jobFile, "-n", "default").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		token := testlib.GetSAToken(oc)
		testlib.CheckMetric(oc, testlib.PrometheusURL+` --data-urlencode 'query=ALERTS{alertname="KubeJobFailed"}'`, token, "fail-job", 600)
	})

	g.It("[OTP][OCP-73874] validate for alert KubeControllerManagerDown [Serial][Disruptive]", func() {
		oc := testlib.NewOC()
		masterNodes := testlib.GetClusterNodesBy(oc, "master")
		if len(masterNodes) == 0 {
			g.Skip("no master nodes available in cluster")
		}

		g.DeferCleanup(func() {
			for _, node := range masterNodes {
				_ = oc.WithoutNamespace().Run("debug").Args("node/"+node, "-n", operatorclient.TargetNamespace, "--", "chroot", "/host", "mv", "/etc/kubernetes/kube-controller-manager-pod.yaml", "/etc/kubernetes/manifests/").Execute()
			}
		})

		for _, node := range masterNodes {
			err := oc.WithoutNamespace().Run("debug").Args("node/"+node, "-n", operatorclient.TargetNamespace, "--", "chroot", "/host", "mv", "/etc/kubernetes/manifests/kube-controller-manager-pod.yaml", "/etc/kubernetes/").Execute()
			o.Expect(err).NotTo(o.HaveOccurred())
		}

		token := testlib.GetSAToken(oc)
		testlib.CheckMetric(oc, testlib.PrometheusURL+` --data-urlencode 'query=ALERTS{alertname="KubeControllerManagerDown"}'`, token, "openshift-kube-controller-manager", 600)
	})
})
