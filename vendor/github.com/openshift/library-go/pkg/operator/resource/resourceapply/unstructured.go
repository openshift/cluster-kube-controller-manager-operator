package resourceapply

import (
	"context"
	"fmt"

	"github.com/openshift/library-go/pkg/operator/events"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

// ApplyKnownUnstructured applies few selected Unstructured types, where it semantic knowledge
// to merge existing & required objects intelligently. Feel free to add more.
func ApplyKnownUnstructured(ctx context.Context, client dynamic.Interface, recorder events.Recorder, obj *unstructured.Unstructured, deleteConditionals ...ConditionalFunction) (*unstructured.Unstructured, bool, error) {
	switch obj.GetObjectKind().GroupVersionKind().GroupKind() {
	case schema.GroupKind{Group: "monitoring.coreos.com", Kind: "ServiceMonitor"}:
		return ApplyServiceMonitor(ctx, client, recorder, obj, deleteConditionals...)
	case schema.GroupKind{Group: "monitoring.coreos.com", Kind: "PrometheusRule"}:
		return ApplyPrometheusRule(ctx, client, recorder, obj, deleteConditionals...)
	case schema.GroupKind{Group: "snapshot.storage.k8s.io", Kind: "VolumeSnapshotClass"}:
		return ApplyVolumeSnapshotClass(ctx, client, recorder, obj, deleteConditionals...)

	}

	return nil, false, fmt.Errorf("unsupported object type: %s", obj.GetKind())
}
