package library

import (
	"context"
	"time"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	watchtools "k8s.io/client-go/tools/watch"
)

var (
	// serviceAccountProvisionTimeout is how long to wait for a service account to be provisioned.
	// service accounts are provisioned after namespace creation
	// a service account is required to support pod creation in a namespace as part of admission control
	serviceAccountProvisionTimeout = 2 * time.Minute
)

// WaitForServiceAccountInNamespace waits for a creation of a service account in a new namespace
// note this method has been copied from k/k repo
func WaitForServiceAccountInNamespace(c clientset.Interface, ns, serviceAccountName string) error {
	fieldSelector := fields.OneTermEqualSelector("metadata.name", serviceAccountName).String()
	lw := &cache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (object runtime.Object, e error) {
			options.FieldSelector = fieldSelector
			return c.CoreV1().ServiceAccounts(ns).List(context.TODO(), options)
		},
		WatchFunc: func(options metav1.ListOptions) (i watch.Interface, e error) {
			options.FieldSelector = fieldSelector
			return c.CoreV1().ServiceAccounts(ns).Watch(context.TODO(), options)
		},
	}
	ctx, cancel := watchtools.ContextWithOptionalTimeout(context.Background(), serviceAccountProvisionTimeout)
	defer cancel()
	_, err := watchtools.UntilWithSync(ctx, lw, &v1.ServiceAccount{}, nil, serviceAccountHasSecrets)
	return err
}

// serviceAccountHasSecrets returns true if the service account has at least one secret,
// false if it does not, or an error.
// note this method has been copied from k/k repo
func serviceAccountHasSecrets(event watch.Event) (bool, error) {
	switch event.Type {
	case watch.Deleted:
		return false, apierrors.NewNotFound(schema.GroupResource{Resource: "serviceaccounts"}, "")
	}
	switch t := event.Object.(type) {
	case *v1.ServiceAccount:
		return len(t.Secrets) > 0, nil
	}
	return false, nil
}
