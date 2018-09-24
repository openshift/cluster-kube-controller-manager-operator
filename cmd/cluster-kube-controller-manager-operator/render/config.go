package render

type Config struct {
	// ConfigHostPath is a host path mounted into the controller manager pods to hold the config file.
	ConfigHostPath string

	// ConfigFileName is the filename of config file inside ConfigHostPath.
	ConfigFileName string

	// CloudProviderHostPath is a host path mounted into the apiserver pods to hold cloud provider configuration.
	CloudProviderHostPath string

	// SecretsHostPath holds certs and keys
	SecretsHostPath string

	// Namespace is the target namespace for the bootstrap controller manager to be created.
	Namespace string

	// Image is the pull spec of the image to use for the controller manager.
	Image string

	// ImagePullPolicy specifies the image pull policy to use for the images.
	ImagePullPolicy string

	// PostBootstrapKubeControllerManagerConfig holds the rendered kube-controller-manager config file after bootstrapping.
	PostBootstrapKubeControllerManagerConfig []byte

	// Assets holds the loaded assets like certs and keys.
	Assets map[string][]byte
}
