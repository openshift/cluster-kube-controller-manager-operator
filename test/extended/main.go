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
