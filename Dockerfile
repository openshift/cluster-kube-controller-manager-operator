# The standard name for this image is openshift/origin-cluster-kube-controller-manager-operator
#
FROM openshift/origin-release:golang-1.10
COPY . /go/src/github.com/openshift/cluster-kube-controller-manager-operator
RUN cd /go/src/github.com/openshift/cluster-kube-controller-manager-operator && go build ./cmd/cluster-kube-controller-manager-operator

FROM centos:7
RUN mkdir -p /usr/share/bootkube/manifests
COPY --from=0 /go/src/github.com/openshift/cluster-kube-controller-manager-operator/manifests/bootkube/* /usr/share/bootkube/manifests/
COPY --from=0 /go/src/github.com/openshift/cluster-kube-controller-manager-operator/cluster-kube-controller-manager-operator /usr/bin/cluster-kube-controller-manager-operator
