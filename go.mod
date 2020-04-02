module github.com/openshift/cluster-kube-controller-manager-operator

go 1.13

require (
	github.com/ghodss/yaml v1.0.0
	github.com/gonum/graph v0.0.0-20170401004347-50b27dea7ebb
	github.com/jteeuwen/go-bindata v3.0.8-0.20151023091102-a0ff2567cfb7+incompatible
	github.com/openshift/api v0.0.0-20200327145400-6efe1ee417e1
	github.com/openshift/build-machinery-go v0.0.0-20200211121458-5e3d6e570160
	github.com/openshift/client-go v0.0.0-20200326155132-2a6cd50aedd0
	github.com/openshift/library-go v0.0.0-20200402123743-4015ba624cae
	github.com/pkg/errors v0.8.1
	github.com/prometheus/common v0.6.0
	github.com/spf13/cobra v0.0.5
	github.com/spf13/pflag v1.0.5
	github.com/stretchr/testify v1.4.0
	k8s.io/api v0.18.0
	k8s.io/apimachinery v0.18.0
	k8s.io/apiserver v0.18.0
	k8s.io/client-go v0.18.0
	k8s.io/component-base v0.18.0
	k8s.io/klog v1.0.0
)

replace (
	github.com/jteeuwen/go-bindata => github.com/jteeuwen/go-bindata v3.0.8-0.20151023091102-a0ff2567cfb7+incompatible
	github.com/stretchr/testify => github.com/stretchr/testify v1.2.2-0.20180319223459-c679ae2cc0cb

	k8s.io/gengo => k8s.io/gengo v0.0.0-20200205140755-e0e292d8aa12
)
