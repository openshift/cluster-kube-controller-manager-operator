package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	operatorsv1alpha1api "github.com/openshift/api/operator/v1alpha1"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// KubeControllerManagerConfig provides information to configure kube-controller-manager
type KubeControllerManagerConfig struct {
	metav1.TypeMeta `json:",inline"`
}

// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// KubeControllerManagerOperatorConfig provides information to configure an operator to manage kube-controller-manager.
type KubeControllerManagerOperatorConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata" protobuf:"bytes,1,opt,name=metadata"`

	Spec   KubeControllerManagerOperatorConfigSpec   `json:"spec" protobuf:"bytes,2,opt,name=spec"`
	Status KubeControllerManagerOperatorConfigStatus `json:"status" protobuf:"bytes,3,opt,name=status"`
}

type KubeControllerManagerOperatorConfigSpec struct {
	operatorsv1alpha1api.OperatorSpec `json:",inline" protobuf:"bytes,1,opt,name=operatorSpec"`

	// kubeControllerManagerConfig holds a sparse config that the user wants for this component.  It only needs to be the overrides from the defaults
	// it will end up overlaying in the following order:
	// 1. hardcoded default
	// 2. this config
	KubeControllerManagerConfig runtime.RawExtension `json:"kubeControllerManagerConfig" protobuf:"bytes,2,opt,name=kubeControllerManagerConfig"`
}

type KubeControllerManagerOperatorConfigStatus struct {
	operatorsv1alpha1api.OperatorStatus `json:",inline" protobuf:"bytes,1,opt,name=operatorStatus"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// KubeControllerManagerOperatorConfigList is a collection of items
type KubeControllerManagerOperatorConfigList struct {
	metav1.TypeMeta `json:",inline"`
	// Standard object's metadata.
	metav1.ListMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`
	// Items contains the items
	Items []KubeControllerManagerOperatorConfig `json:"items" protobuf:"bytes,2,rep,name=items"`
}
