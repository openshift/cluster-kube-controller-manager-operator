module github.com/openshift/cluster-kube-controller-manager-operator

go 1.12

require (
	github.com/certifi/gocertifi v0.0.0-20180905225744-ee1a9a0726d2 // indirect
	github.com/getsentry/raven-go v0.0.0-20190513200303-c977f96e1095 // indirect
	github.com/ghodss/yaml v0.0.0-20150909031657-73d445a93680
	github.com/gonum/blas v0.0.0-20170728112917-37e82626499e // indirect
	github.com/gonum/floats v0.0.0-20170731225635-f74b330d45c5 // indirect
	github.com/gonum/graph v0.0.0-20170401004347-50b27dea7ebb
	github.com/gonum/internal v0.0.0-20170731230106-e57e4534cf9b // indirect
	github.com/gonum/lapack v0.0.0-20170731225844-5ed4b826becd // indirect
	github.com/gonum/matrix v0.0.0-20170731230223-dd6034299e42 // indirect
	github.com/jteeuwen/go-bindata v0.0.0-00010101000000-000000000000
	github.com/openshift/api v0.0.0-20191014195513-c9253efc14f4
	github.com/openshift/client-go v0.0.0-20191001081553-3b0e988f8cb0
	github.com/openshift/library-go v0.0.0-20190927184318-c355e2019bb3
	github.com/pkg/errors v0.8.0
	github.com/pkg/profile v1.3.0 // indirect
	github.com/spf13/cobra v0.0.5
	github.com/spf13/pflag v1.0.3
	github.com/stretchr/testify v1.3.0
	github.com/xlab/handysort v0.0.0-20150421192137-fb3537ed64a1 // indirect
	k8s.io/api v0.0.0-20190918155943-95b840bb6a1f
	k8s.io/apiextensions-apiserver v0.0.0-20190918161926-8f644eb6e783 // indirect
	k8s.io/apimachinery v0.0.0-20190913080033-27d36303b655
	k8s.io/client-go v0.0.0-20190918160344-1fbdaa4c8d90
	k8s.io/component-base v0.0.0-20190918160511-547f6c5d7090
	k8s.io/klog v0.4.0
	k8s.io/kube-aggregator v0.0.0-20190918161219-8c8f079fddc3 // indirect
	vbom.ml/util v0.0.0-20180919145318-efcd4e0f9787 // indirect
)

replace (
	github.com/jteeuwen/go-bindata => github.com/jteeuwen/go-bindata v3.0.8-0.20151023091102-a0ff2567cfb7+incompatible
	github.com/openshift/api => github.com/openshift/api v3.9.1-0.20191209132752-992bc3a41fe6+incompatible
	github.com/openshift/client-go => github.com/openshift/client-go v0.0.0-20191205152420-9faca5198b4f
	github.com/openshift/library-go => github.com/openshift/library-go v0.0.0-20191211124107-e0f1590a316e
	github.com/stretchr/testify => github.com/stretchr/testify v1.2.2-0.20180319223459-c679ae2cc0cb
	k8s.io/api => k8s.io/api v0.0.0-20190918155943-95b840bb6a1f
	k8s.io/apimachinery => k8s.io/apimachinery v0.0.0-20190913080033-27d36303b655
	k8s.io/apiserver => k8s.io/apiserver v0.0.0-20190918160949-bfa5e2e684ad
	k8s.io/client-go => k8s.io/client-go v0.0.0-20190918160344-1fbdaa4c8d90
	k8s.io/code-generator => k8s.io/code-generator v0.0.0-20190912054826-cd179ad6a269
	k8s.io/component-base => k8s.io/component-base v0.0.0-20190918160511-547f6c5d7090
	k8s.io/gengo => k8s.io/gengo v0.0.0-20190822140433-26a664648505
	k8s.io/kube-aggregator => k8s.io/kube-aggregator v0.0.0-20190918161219-8c8f079fddc3
)
