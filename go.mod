module github.com/openshift/cluster-kube-controller-manager-operator

go 1.16

require (
	github.com/ghodss/yaml v1.0.0
	github.com/go-bindata/go-bindata v3.1.2+incompatible
	github.com/gonum/graph v0.0.0-20170401004347-50b27dea7ebb
	github.com/google/go-cmp v0.5.2
	github.com/google/gofuzz v1.2.0 // indirect
	github.com/openshift/api v0.0.0-20210409143810-a99ffa1cac67
	github.com/openshift/build-machinery-go v0.0.0-20210423112049-9415d7ebd33e
	github.com/openshift/client-go v0.0.0-20210331195552-cf6c2669e01f
	github.com/openshift/library-go v0.0.0-20210513103829-aa02c23cb230
	github.com/prometheus/common v0.10.0
	github.com/spf13/cobra v1.1.1
	github.com/spf13/pflag v1.0.5
	github.com/stretchr/testify v1.6.1
	k8s.io/api v0.21.0
	k8s.io/apimachinery v0.21.0
	k8s.io/apiserver v0.21.0
	k8s.io/client-go v0.21.0
	k8s.io/component-base v0.21.0
	k8s.io/klog/v2 v2.8.0
)

// points to temporary-watch-reduction-patch-1.21 to pick up k/k/pull/101102 - please remove it once the pr merges and a new Z release is cut
replace k8s.io/apiserver => github.com/openshift/kubernetes-apiserver v0.0.0-20210419140141-620426e63a99
