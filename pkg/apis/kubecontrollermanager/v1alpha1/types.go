package v1alpha1

import (
	operatorsv1 "github.com/openshift/api/operator/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	metav1.ObjectMeta `json:"metadata"`

	Spec   KubeControllerManagerOperatorConfigSpec   `json:"spec"`
	Status KubeControllerManagerOperatorConfigStatus `json:"status"`
}

type KubeControllerManagerOperatorConfigSpec struct {
	operatorsv1.StaticPodOperatorSpec `json:",inline"`

	// forceRedeploymentReason can be used to force the redeployment of the kube-controller-manager by providing a unique string.
	// This provides a mechanism to kick a previously failed deployment and provide a reason why you think it will work
	// this time instead of failing again on the same config.
	ForceRedeploymentReason string `json:"forceRedeploymentReason"`
}

type KubeControllerManagerOperatorConfigStatus struct {
	operatorsv1.StaticPodOperatorStatus `json:",inline"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// KubeControllerManagerOperatorConfigList is a collection of items
type KubeControllerManagerOperatorConfigList struct {
	metav1.TypeMeta `json:",inline"`
	// Standard object's metadata.
	metav1.ListMeta `json:"metadata,omitempty"`
	// Items contains the items
	Items []KubeControllerManagerOperatorConfig `json:"items"`
}
