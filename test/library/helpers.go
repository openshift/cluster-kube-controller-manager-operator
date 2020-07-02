package library

import (
	"context"

	configclient "github.com/openshift/client-go/config/clientset/versioned"
	routeclient "github.com/openshift/client-go/route/clientset/versioned"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"
	watchtools "k8s.io/client-go/tools/watch"
	"k8s.io/klog"
)

// ClientConfig returns a config configured to connect to the api server
func GetClientConfig() *rest.Config {
	loader := clientcmd.NewDefaultClientConfigLoadingRules()
	clientConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		loader,
		&clientcmd.ConfigOverrides{
			ClusterInfo: api.Cluster{
				InsecureSkipTLSVerify: true,
			},
		},
	)

	config, err := clientConfig.ClientConfig()
	if err != nil {
		panic(err)
	}

	return config
}

func KubeClient() *kubernetes.Clientset {
	kubeClient, err := kubernetes.NewForConfig(GetClientConfig())
	if err != nil {
		panic(err)
	}

	return kubeClient
}

func RouteClient() *routeclient.Clientset {
	routeClient, err := routeclient.NewForConfig(GetClientConfig())
	if err != nil {
		panic(err)
	}

	return routeClient
}

func ConfigClient() *configclient.Clientset {
	configClient, err := configclient.NewForConfig(GetClientConfig())
	if err != nil {
		panic(err)
	}

	return configClient
}

func WaitForServiceAccount(ctx context.Context, namespace, name string) error {
	klog.V(2).Infof("Waiting for ServiceAccount %s/%s to be created", namespace, name)

	fieldSelector := fields.OneTermEqualSelector("metadata.name", name).String()
	lw := &cache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
			options.FieldSelector = fieldSelector
			return KubeClient().CoreV1().ServiceAccounts(namespace).List(ctx, options)
		},
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			options.FieldSelector = fieldSelector
			return KubeClient().CoreV1().ServiceAccounts(namespace).Watch(ctx, options)
		},
	}
	_, err := watchtools.UntilWithSync(ctx, lw, &corev1.ServiceAccount{}, nil, func(event watch.Event) (bool, error) {
		switch event.Type {
		case watch.Added, watch.Modified:
			klog.V(2).Infof("ServiceAccount %s/%s is present.", namespace, name)
			return true, nil
		case watch.Error:
			return true, apierrors.FromObject(event.Object)
		default:
			return false, nil
		}
	})
	return err
}

func CreateTestNamespace(ctx context.Context) (*corev1.Namespace, error) {
	namespace, err := KubeClient().CoreV1().Namespaces().Create(
		ctx,
		&corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "operator-e2e-",
				Labels: map[string]string{
					"operator-e2e-temporary": "true", // For easy cleanup
				},
			},
		},
		metav1.CreateOptions{},
	)
	if err != nil {
		return nil, err
	}

	klog.Infof("Created test namespace %q", namespace.Name)

	// Wait for essential ServiceAccounts to be provisioned
	err = WaitForServiceAccount(ctx, namespace.Name, "default")
	if err != nil {
		return nil, err
	}

	return namespace, nil
}
