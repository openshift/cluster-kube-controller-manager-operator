package extended

import (
	g "github.com/onsi/ginkgo/v2"
	o "github.com/onsi/gomega"
)

var _ = g.Describe("[Jira:kube-controller-manager][sig-api-machinery] sanity test", func() {
	g.It("should always pass [Suite:openshift/cluster-kube-controller-manager-operator/conformance/parallel]", func() {
		o.Expect(true).To(o.BeTrue())
	})
})

var _ = g.Describe("[Jira:kube-controller-manager][sig-api-machinery] serial test", func() {
	g.It("should run serially [Serial][Suite:openshift/cluster-kube-controller-manager-operator/conformance/serial]", func() {
		o.Expect(true).To(o.BeTrue())
	})
})

var _ = g.Describe("[Jira:kube-controller-manager][sig-api-machinery] slow test", func() {
	g.It("should be marked as slow [Slow][Suite:openshift/cluster-kube-controller-manager-operator/optional/slow]", func() {
		o.Expect(true).To(o.BeTrue())
	})
})

var _ = g.Describe("[Jira:kube-controller-manager][sig-api-machinery] disruptive test", func() {
	g.It("should be marked as disruptive [Serial][Disruptive][Suite:openshift/cluster-kube-controller-manager-operator/conformance/serial]", func() {
		o.Expect(true).To(o.BeTrue())
	})
})
