module github.com/openshift/cluster-kube-controller-manager-operator

go 1.13

require (
	github.com/ghodss/yaml v1.0.0
	github.com/go-bindata/go-bindata v3.1.2+incompatible
	github.com/gonum/graph v0.0.0-20170401004347-50b27dea7ebb
	github.com/google/go-cmp v0.4.0
	github.com/openshift/api v0.0.0-20201019163320-c6a5ec25f267
	github.com/openshift/build-machinery-go v0.0.0-20200917070002-f171684f77ab
	github.com/openshift/client-go v0.0.0-20201020074620-f8fd44879f7c
	github.com/openshift/library-go v0.0.0-20201026125231-a28d3d1bad23
	github.com/prometheus/common v0.10.0
	github.com/spf13/cobra v1.0.0
	github.com/spf13/pflag v1.0.5
	github.com/stretchr/testify v1.4.0
	k8s.io/api v0.19.2
	k8s.io/apimachinery v0.19.2
	k8s.io/apiserver v0.19.2
	k8s.io/client-go v0.19.2
	k8s.io/component-base v0.19.2
	k8s.io/klog/v2 v2.3.0
)

replace (
	github.com/stretchr/testify => github.com/stretchr/testify v1.2.2-0.20180319223459-c679ae2cc0cb
	k8s.io/gengo => k8s.io/gengo v0.0.0-20200205140755-e0e292d8aa12
)

replace vbom.ml/util => github.com/fvbommel/util v0.0.0-20180919145318-efcd4e0f9787
