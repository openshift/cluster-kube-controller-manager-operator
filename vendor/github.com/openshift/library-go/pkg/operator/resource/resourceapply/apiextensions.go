package resourceapply

import (
	"context"

	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resource/resourcemerge"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextclientv1 "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/typed/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
)

// ApplyCustomResourceDefinitionV1 applies the required CustomResourceDefinition to the cluster.
func ApplyCustomResourceDefinitionV1(ctx context.Context, client apiextclientv1.CustomResourceDefinitionsGetter, recorder events.Recorder, required *apiextensionsv1.CustomResourceDefinition, deleteConditionals ...ConditionalFunction) (*apiextensionsv1.CustomResourceDefinition, bool, error) {
	shouldDelete := false
	// If any of the delete conditionals is true, we should delete the resource
	for _, deleteConditional := range deleteConditionals {
		if deleteConditional() {
			shouldDelete = true
			break
		}
	}
	existing, err := client.CustomResourceDefinitions().Get(context.TODO(), required.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		actual, err := client.CustomResourceDefinitions().Create(ctx, required, metav1.CreateOptions{})
		reportCreateEvent(recorder, required, err)
		return actual, true, err
	} else if apierrors.IsNotFound(err) && shouldDelete {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}

	if shouldDelete {
		err := client.CustomResourceDefinitions().Delete(context.TODO(), existing.Name, metav1.DeleteOptions{})
		if err != nil {
			return nil, false, err
		}
		reportDeleteEvent(recorder, required, err)
		return nil, true, nil
	}

	modified := resourcemerge.BoolPtr(false)
	existingCopy := existing.DeepCopy()
	resourcemerge.EnsureCustomResourceDefinitionV1(modified, existingCopy, *required)
	if !*modified {
		return existing, false, nil
	}

	if klog.V(4).Enabled() {
		klog.Infof("CustomResourceDefinition %q changes: %s", existing.Name, JSONPatchNoError(existing, existingCopy))
	}

	actual, err := client.CustomResourceDefinitions().Update(ctx, existingCopy, metav1.UpdateOptions{})
	reportUpdateEvent(recorder, required, err)

	return actual, true, err
}
