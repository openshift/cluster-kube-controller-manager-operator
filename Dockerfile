#
# This is the integrated OpenShift Service Serving Cert Signer.  It signs serving certificates for use inside the platform.
#
# The standard name for this image is openshift/origin-cluster-kube-controller-manager-operator
#
FROM openshift/origin-release:golang-1.10
COPY . /go/src/github.com/openshift/cluster-kube-controller-manager-operator
RUN cd /go/src/github.com/openshift/cluster-kube-controller-manager-operator && go build ./cmd/cluster-kube-controller-manager-operator

FROM centos:7
COPY --from=0 /go/src/github.com/openshift/cluster-kube-controller-manager-operator/cluster-kube-controller-manager-operator /usr/bin/cluster-kube-controller-manager-operator
