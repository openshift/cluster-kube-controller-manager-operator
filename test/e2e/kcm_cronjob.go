package e2e

import (
	"context"
	"path/filepath"
	"regexp"
	"time"

	g "github.com/onsi/ginkgo/v2"
	o "github.com/onsi/gomega"

	testlib "github.com/openshift/cluster-kube-controller-manager-operator/test/library"
	"k8s.io/apimachinery/pkg/util/wait"
)

var _ = g.Describe("kube-controller-manager cronjob workloads", func() {
	g.It("[OTP][OCP-56176] oc debug cronjob should fail with a meaningful error", func() {
		oc := testlib.NewOC()
		oc.SetupProject()
		testlib.SetNamespacePrivileged(oc, oc.Namespace())
		g.DeferCleanup(func() {
			_ = oc.WithoutNamespace().Run("delete").Args("project", oc.Namespace()).Execute()
		})
		cronjobF := filepath.Join(testlib.FixturePath("workloads"), "cronjob56176.yaml")

		err := oc.Run("create").Args("-f", cronjobF).Execute()
		o.Expect(err).NotTo(o.HaveOccurred())
		output, err := oc.Run("get").Args("cronjob").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(output).To(o.ContainSubstring("cronjob56176"))

		debugErr, err := oc.Run("debug").Args("cronjob/cronjob56176").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(debugErr).NotTo(o.ContainSubstring("v1.CronJob is not supported by debug"))
	})

	g.It("[OTP][OCP-54195] Enable CronJobTimeZone feature and verify that it works fine", func() {
		oc := testlib.NewOC()
		oc.SetupProject()
		testlib.SetNamespacePrivileged(oc, oc.Namespace())
		g.DeferCleanup(func() {
			_ = oc.WithoutNamespace().Run("delete").Args("project", oc.Namespace()).Execute()
		})
		base := testlib.FixturePath("workloads")

		err := oc.Run("create").Args("-f", filepath.Join(base, "cronjob54195.yaml")).Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
		defer cancel()
		err = wait.PollUntilContextCancel(ctx, 10*time.Second, true, func(ctx context.Context) (bool, error) {
			name, err := oc.WithoutNamespace().Run("get").Args("cronjob", "cronjob54195", "-n", oc.Namespace(), "-o=jsonpath={.metadata.name}").Output()
			return err == nil && name == "cronjob54195", nil
		})
		testlib.AssertWaitPollNoErr(err, "cronjob54195 not created")

		tz, err := oc.WithoutNamespace().Run("get").Args("cronjob", "cronjob54195", "-n", oc.Namespace(), "-o=jsonpath={.spec.timeZone}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(tz).To(o.ContainSubstring("Asia/Calcutta"))

		ctx2, cancel2 := context.WithTimeout(context.Background(), 100*time.Second)
		defer cancel2()
		err = wait.PollUntilContextCancel(ctx2, 10*time.Second, true, func(ctx context.Context) (bool, error) {
			jobs, err := oc.WithoutNamespace().Run("get").Args("job", "-n", oc.Namespace()).Output()
			return err == nil && stringsContains(jobs, "cronjob54195"), nil
		})
		testlib.AssertWaitPollNoErr(err, "job not created from cronjob54195")

		incorrect, err := oc.Run("create").Args("-f", filepath.Join(base, "cronjob54195ic.yaml")).Output()
		o.Expect(err).To(o.HaveOccurred())
		o.Expect(incorrect).To(o.ContainSubstring("unknown time zone Asia/china"))

		err = oc.Run("create").Args("-f", filepath.Join(base, "cronjob54195notz.yaml")).Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		ctx3, cancel3 := context.WithTimeout(context.Background(), 100*time.Second)
		defer cancel3()
		err = wait.PollUntilContextCancel(ctx3, 10*time.Second, true, func(ctx context.Context) (bool, error) {
			name, err := oc.WithoutNamespace().Run("get").Args("cronjob", "cronjob54195notz", "-n", oc.Namespace(), "-o=jsonpath={.metadata.name}").Output()
			return err == nil && name == "cronjob54195notz", nil
		})
		testlib.AssertWaitPollNoErr(err, "cronjob54195notz not created")
	})

	g.It("[OTP][OCP-54196] Create cronjob by retreiving current time by its timezone", func() {
		oc := testlib.NewOC()
		oc.SetupProject()
		testlib.SetNamespacePrivileged(oc, oc.Namespace())
		g.DeferCleanup(func() {
			_ = oc.WithoutNamespace().Run("delete").Args("project", oc.Namespace()).Execute()
		})
		schedule, timeZoneName := testlib.GetTimeFromTimezone()
		o.Expect(schedule).NotTo(o.BeEmpty())
		o.Expect(timeZoneName).NotTo(o.BeEmpty())

		template := filepath.Join(testlib.FixturePath("workloads"), "cronjob_54196.yaml")
		err := testlib.ApplyResourceFromTemplate(oc, "--ignore-unknown-parameters=true", "-f", template,
			"-p", "CNAME=cronjob54196", "NAMESPACE="+oc.Namespace(), "SCHEDULE="+schedule, "TIMEZONE="+timeZoneName)
		o.Expect(err).NotTo(o.HaveOccurred())

		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
		defer cancel()
		err = wait.PollUntilContextCancel(ctx, 10*time.Second, true, func(ctx context.Context) (bool, error) {
			name, err := oc.WithoutNamespace().Run("get").Args("cronjob", "cronjob54196", "-n", oc.Namespace(), "-o=jsonpath={.metadata.name}").Output()
			return err == nil && name == "cronjob54196", nil
		})
		testlib.AssertWaitPollNoErr(err, "cronjob54196 not created")

		tz, err := oc.WithoutNamespace().Run("get").Args("cronjob", "cronjob54196", "-n", oc.Namespace(), "-o=jsonpath={.spec.timeZone}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(tz).To(o.ContainSubstring(timeZoneName))

		ctx2, cancel2 := context.WithTimeout(context.Background(), 180*time.Second)
		defer cancel2()
		err = wait.PollUntilContextCancel(ctx2, 10*time.Second, true, func(ctx context.Context) (bool, error) {
			jobs, err := oc.WithoutNamespace().Run("get").Args("job", "-n", oc.Namespace()).Output()
			return err == nil && stringsContains(jobs, "cronjob54196"), nil
		})
		testlib.AssertWaitPollNoErr(err, "job not created from cronjob54196")
	})

	// OCP-75375 verifies that creating a Job from a CronJob via "oc create job --from=cronjob/..."
	// succeeds without "cannot set blockOwnerDeletion" errors.
	g.It("[OTP][OCP-75375] oc create job fails with cannot set blockOwnerDeletion", func() {
		oc := testlib.NewOC()
		oc.SetupProject()
		testlib.SetNamespacePrivileged(oc, oc.Namespace())
		g.DeferCleanup(func() {
			_ = oc.WithoutNamespace().Run("delete").Args("project", oc.Namespace()).Execute()
		})
		cronjobF := filepath.Join(testlib.FixturePath("workloads"), "cronjob75375.yaml")

		err := oc.Run("create").Args("-f", cronjobF).Execute()
		o.Expect(err).NotTo(o.HaveOccurred())
		output, err := oc.Run("get").Args("cronjob").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(output).To(o.ContainSubstring("pi75375"))

		_, err = oc.Run("create").Args("job", "example-75375", "--from=cronjob/pi75375").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
	})
})

func stringsContains(s, sub string) bool {
	matched, _ := regexp.MatchString(sub, s)
	return matched
}
