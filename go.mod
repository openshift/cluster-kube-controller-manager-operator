module github.com/openshift/cluster-kube-controller-manager-operator

go 1.16

require (
	github.com/ghodss/yaml v1.0.0
	github.com/gonum/graph v0.0.0-20170401004347-50b27dea7ebb
	github.com/google/go-cmp v0.5.6
	github.com/google/gofuzz v1.2.0 // indirect
	github.com/openshift/api v0.0.0-20211108165917-be1be0e89115
	github.com/openshift/build-machinery-go v0.0.0-20210806203541-4ea9b6da3a37
	github.com/openshift/client-go v0.0.0-20210916133943-9acee1a0fb83
	github.com/openshift/library-go v0.0.0-20210930103404-8911cacccb05
	github.com/prometheus/common v0.26.0
	github.com/spf13/cobra v1.1.3
	github.com/spf13/pflag v1.0.5
	github.com/stretchr/testify v1.7.0
	golang.org/x/net v0.0.0-20210716203947-853a461950ff // indirect
	k8s.io/api v0.22.1
	k8s.io/apimachinery v0.22.1
	k8s.io/apiserver v0.22.1
	k8s.io/client-go v0.22.1
	k8s.io/component-base v0.22.1
	k8s.io/klog/v2 v2.10.0
)

// Workaround to deal with https://github.com/kubernetes/klog/issues/253
// Should be deleted when https://github.com/kubernetes/klog/pull/242 is merged
exclude github.com/go-logr/logr v1.0.0
