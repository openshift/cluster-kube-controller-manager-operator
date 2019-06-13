FROM registry.svc.ci.openshift.org/ocp/builder:golang-1.12 AS builder
WORKDIR /go/src/github.com/openshift/cluster-kube-controller-manager-operator
COPY . .
RUN go build ./cmd/cluster-kube-controller-manager-operator

FROM registry.svc.ci.openshift.org/ocp/4.0:base
RUN mkdir -p /usr/share/bootkube/manifests
COPY --from=builder /go/src/github.com/openshift/cluster-kube-controller-manager-operator/bindata/bootkube/* /usr/share/bootkube/manifests/
COPY --from=builder /go/src/github.com/openshift/cluster-kube-controller-manager-operator/cluster-kube-controller-manager-operator /usr/bin/
COPY manifests /manifests
LABEL io.openshift.release.operator true
