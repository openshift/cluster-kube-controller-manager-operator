module github.com/openshift/cluster-kube-controller-manager-operator

go 1.12

require (
	github.com/ghodss/yaml v1.0.0
	github.com/gonum/graph v0.0.0-20170401004347-50b27dea7ebb
	github.com/jteeuwen/go-bindata v3.0.8-0.20151023091102-a0ff2567cfb7+incompatible
	github.com/openshift/api v0.0.0-20191217141120-791af96035a5
	github.com/openshift/client-go v0.0.0-20191216194936-57f413491e9e
	github.com/openshift/library-go v0.0.0-20191218095328-1c12909e5923
	github.com/pkg/errors v0.8.1
	github.com/spf13/cobra v0.0.5
	github.com/spf13/pflag v1.0.5
	github.com/stretchr/testify v1.4.0
	k8s.io/api v0.17.0
	k8s.io/apimachinery v0.17.0
	k8s.io/client-go v0.17.0
	k8s.io/component-base v0.17.0
	k8s.io/klog v1.0.0
)

replace (
	github.com/jteeuwen/go-bindata => github.com/jteeuwen/go-bindata v3.0.8-0.20151023091102-a0ff2567cfb7+incompatible
	github.com/openshift/api => github.com/openshift/api v3.9.1-0.20191209132752-992bc3a41fe6+incompatible
	github.com/openshift/client-go => github.com/openshift/client-go v0.0.0-20191205152420-9faca5198b4f
	github.com/openshift/library-go => github.com/openshift/library-go v0.0.0-20191218095328-1c12909e5923
	github.com/stretchr/testify => github.com/stretchr/testify v1.2.2-0.20180319223459-c679ae2cc0cb
	k8s.io/api => k8s.io/api v0.0.0-20191121175643-4ed536977f46
	k8s.io/apimachinery => k8s.io/apimachinery v0.17.1-beta.0
	k8s.io/apiserver => k8s.io/apiserver v0.0.0-20191204085032-7fb3a25c3bc4
	k8s.io/client-go => k8s.io/client-go v0.17.0
	k8s.io/code-generator => k8s.io/code-generator v0.17.1-beta.0
	k8s.io/component-base => k8s.io/component-base v0.0.0-20191128032904-4bcd454928ff
	k8s.io/gengo => k8s.io/gengo v0.0.0-20190822140433-26a664648505
	k8s.io/kube-aggregator => k8s.io/kube-aggregator v0.0.0-20191121180901-7ce2d4f093e4
)
