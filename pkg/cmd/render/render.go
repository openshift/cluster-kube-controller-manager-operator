package render

import (
	"errors"
	"fmt"
	"io/ioutil"
	"path/filepath"

	"github.com/ghodss/yaml"
	configv1 "github.com/openshift/api/config/v1"
	kubecontrolplanev1 "github.com/openshift/api/kubecontrolplane/v1"
	"github.com/openshift/cluster-kube-controller-manager-operator/bindata"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/targetconfigcontroller"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	genericrender "github.com/openshift/library-go/pkg/operator/render"
	genericrenderoptions "github.com/openshift/library-go/pkg/operator/render/options"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
)

// renderOpts holds values to drive the render command.
type renderOpts struct {
	manifest genericrenderoptions.ManifestOptions
	generic  genericrenderoptions.GenericOptions

	clusterConfigFile                       string
	clusterPolicyControllerConfigOutputFile string
	clusterPolicyControllerImage            string
	disablePhase2                           bool

	// errHandler is used to handle errors in the command run.
	// It is used by unit tests to change the behavior of the command on error.
	// By default it will exit with a klog.Fatal.
	// It may return an error to indicate that processing should stop.
	errHandler func(error) error
}

// NewRenderCommand creates a render command.
func NewRenderCommand(errHandler func(error) error) *cobra.Command {
	if errHandler == nil {
		errHandler = func(err error) error {
			if err != nil {
				klog.Fatal(err)
			}
			return nil
		}
	}

	renderOpts := &renderOpts{
		manifest:   *genericrenderoptions.NewManifestOptions("kube-controller-manager", "openshift/origin-hyperkube:latest"),
		generic:    *genericrenderoptions.NewGenericOptions(),
		errHandler: errHandler,
	}
	cmd := &cobra.Command{
		Use:   "render",
		Short: "Render kubernetes controller manager bootstrap manifests, secrets and configMaps",
		Run: func(cmd *cobra.Command, args []string) {
			if err := renderOpts.errHandler(renderOpts.Validate()); err != nil {
				return
			}
			if err := renderOpts.errHandler(renderOpts.Complete()); err != nil {
				return
			}

			if err := renderOpts.errHandler(renderOpts.Run()); err != nil {
				return
			}
		},
	}

	renderOpts.AddFlags(cmd.Flags())

	return cmd
}

func (r *renderOpts) AddFlags(fs *pflag.FlagSet) {
	r.manifest.AddFlags(fs, "controller manager")
	r.generic.AddFlags(fs, kubecontrolplanev1.GroupVersion.WithKind("KubeControllerManagerConfig"))

	fs.StringVar(&r.clusterConfigFile, "cluster-config-file", r.clusterConfigFile, "Openshift Cluster API Config file.")
	fs.StringVar(&r.clusterPolicyControllerImage, "cluster-policy-controller-image", r.clusterPolicyControllerImage, "Image to use for the cluster-policy-controller.")
	fs.StringVar(&r.clusterPolicyControllerConfigOutputFile, "cpc-config-output-file", r.clusterPolicyControllerConfigOutputFile, "Output path for the Openshift Cluster API Config yaml file.")

	// TODO: remove when the installer has stopped using it
	fs.BoolVar(&r.disablePhase2, "disable-phase-2", r.disablePhase2, "Disable rendering of the phase 2 daemonset and dependencies.")
	fs.MarkHidden("disable-phase-2")
	fs.MarkDeprecated("disable-phase-2", "Only used temporarily to synchronize roll out of the phase 2 removal. Does nothing anymore.")
}

// Validate verifies the inputs.
func (r *renderOpts) Validate() error {
	if err := r.manifest.Validate(); err != nil {
		return err
	}
	if err := r.generic.Validate(); err != nil {
		return err
	}

	if len(r.clusterPolicyControllerConfigOutputFile) == 0 {
		return errors.New("missing required flag: --cpc-config-output-file")
	}

	return nil
}

// Complete fills in missing values before command execution.
func (r *renderOpts) Complete() error {
	if err := r.manifest.Complete(); err != nil {
		return err
	}
	if err := r.generic.Complete(); err != nil {
		return err
	}
	return nil
}

type TemplateData struct {
	genericrenderoptions.TemplateData

	// FeatureGates is list of featuregates to apply
	FeatureGates                          []string
	ExtendedArguments                     string
	ClusterPolicyControllerImage          string
	ClusterPolicyControllerConfigFileName string
	ClusterPolicyControllerFileConfig     genericrenderoptions.FileConfig
	ClusterCIDR                           []string
	ServiceClusterIPRange                 []string
}

func setFeatureGates(renderConfig *TemplateData, opts *renderOpts) error {
	featureSet, ok := configv1.FeatureSets[configv1.FeatureSet(opts.generic.FeatureSet)]
	if !ok {
		return fmt.Errorf("featureSet %q not found", featureSet)
	}
	allGates := []string{}
	for _, enabled := range featureSet.Enabled {
		allGates = append(allGates, fmt.Sprintf("%v=true", enabled.FeatureGateAttributes.Name))
	}
	for _, disabled := range featureSet.Disabled {
		allGates = append(allGates, fmt.Sprintf("%v=false", disabled.FeatureGateAttributes.Name))
	}
	renderConfig.FeatureGates = allGates
	return nil
}

func setFeatureGatesFromAccessor(renderConfig *TemplateData, featureGates featuregates.FeatureGateAccess) error {
	currFeatureGates, err := featureGates.CurrentFeatureGates()
	if err != nil {
		return fmt.Errorf("unable to get FeatureGates: %w", err)
	}
	allGates := []string{}
	for _, featureGateName := range currFeatureGates.KnownFeatures() {
		if currFeatureGates.Enabled(featureGateName) {
			allGates = append(allGates, fmt.Sprintf("%v=true", featureGateName))
		} else {
			allGates = append(allGates, fmt.Sprintf("%v=false", featureGateName))
		}
	}
	renderConfig.FeatureGates = allGates
	return nil
}

func discoverRestrictedCIDRs(clusterConfigFileData []byte, renderConfig *TemplateData) error {
	if err := discoverRestrictedCIDRsFromNetwork(clusterConfigFileData, renderConfig); err != nil {
		if err = discoverRestrictedCIDRsFromClusterAPI(clusterConfigFileData, renderConfig); err != nil {
			return err
		}
	}
	return nil
}

func discoverRestrictedCIDRsFromClusterAPI(clusterConfigFileData []byte, renderConfig *TemplateData) error {
	configJson, err := yaml.YAMLToJSON(clusterConfigFileData)
	if err != nil {
		return err
	}

	clusterConfigObj, err := runtime.Decode(unstructured.UnstructuredJSONScheme, configJson)
	if err != nil {
		return err
	}
	clusterConfig, ok := clusterConfigObj.(*unstructured.Unstructured)
	if !ok {
		return fmt.Errorf("unexpected object in %t", clusterConfigObj)
	}

	if clusterCIDR, found, err := unstructured.NestedStringSlice(
		clusterConfig.Object, "spec", "clusterNetwork", "pods", "cidrBlocks"); found && err == nil {
		renderConfig.ClusterCIDR = clusterCIDR
	}
	if err != nil {
		return err
	}
	if serviceClusterIPRange, found, err := unstructured.NestedStringSlice(
		clusterConfig.Object, "spec", "clusterNetwork", "services", "cidrBlocks"); found && err == nil {
		renderConfig.ServiceClusterIPRange = serviceClusterIPRange
	}
	if err != nil {
		return err
	}
	return nil
}

func discoverRestrictedCIDRsFromNetwork(clusterConfigFileData []byte, renderConfig *TemplateData) error {
	configJson, err := yaml.YAMLToJSON(clusterConfigFileData)
	if err != nil {
		return err
	}
	clusterConfigObj, err := runtime.Decode(unstructured.UnstructuredJSONScheme, configJson)
	if err != nil {
		return err
	}
	clusterConfig, ok := clusterConfigObj.(*unstructured.Unstructured)
	if !ok {
		return fmt.Errorf("unexpected object in %t", clusterConfigObj)
	}
	clusterCIDR, found, err := unstructured.NestedSlice(
		clusterConfig.Object, "spec", "clusterNetwork")
	if found && err == nil {
		for key := range clusterCIDR {
			slice, ok := clusterCIDR[key].(map[string]interface{})
			if !ok {
				return fmt.Errorf("unexpected object in %t", clusterCIDR[key])
			}
			if CIDR, found, err := unstructured.NestedString(slice, "cidr"); found && err == nil {
				renderConfig.ClusterCIDR = append(renderConfig.ClusterCIDR, CIDR)
			}
		}
	}
	if err != nil {
		return err
	}
	serviceCIDR, found, err := unstructured.NestedStringSlice(
		clusterConfig.Object, "spec", "serviceNetwork")
	if found && err == nil {
		renderConfig.ServiceClusterIPRange = serviceCIDR
	}
	if err != nil {
		return err
	}
	return nil
}

// Run contains the logic of the render command.
func (r *renderOpts) Run() error {
	renderConfig := TemplateData{}
	if len(r.clusterConfigFile) > 0 {
		clusterConfigFileData, err := ioutil.ReadFile(r.clusterConfigFile)
		if err != nil {
			return err
		}
		err = discoverRestrictedCIDRs(clusterConfigFileData, &renderConfig)
		if err != nil {
			return fmt.Errorf("unable to parse restricted CIDRs from config: %v", err)
		}
	}

	featureGates, err := r.generic.FeatureGates()
	if err != nil {
		klog.Warningf(fmt.Sprintf("error getting FeatureGates: %v", err))
		if err := setFeatureGates(&renderConfig, r); err != nil {
			return err
		}

	} else {
		if err := setFeatureGatesFromAccessor(&renderConfig, featureGates); err != nil {
			return err
		}
	}

	if err := r.manifest.ApplyTo(&renderConfig.ManifestConfig); err != nil {
		return err
	}
	renderConfig.ClusterPolicyControllerImage = r.clusterPolicyControllerImage
	renderConfig.ClusterPolicyControllerConfigFileName = "cluster-policy-controller-config.yaml"

	if err := r.generic.ApplyTo(
		&renderConfig.FileConfig,
		genericrenderoptions.Template{FileName: "defaultconfig.yaml", Content: bindata.MustAsset(filepath.Join("assets", "config", "defaultconfig.yaml"))},
		mustReadTemplateFile(filepath.Join(r.generic.TemplatesDir, "config", "bootstrap-config-overrides.yaml")),
		&renderConfig,
		nil,
	); err != nil {
		return err
	}

	if err := r.generic.ApplyTo(
		&renderConfig.ClusterPolicyControllerFileConfig,
		genericrenderoptions.Template{
			FileName: "default-cluster-policy-controller-config.yaml",
			Content:  bindata.MustAsset(filepath.Join("assets", "config", "default-cluster-policy-controller-config.yaml")),
		},
		mustReadTemplateFile(filepath.Join(r.generic.TemplatesDir, "config", "bootstrap-cluster-policy-controller-config-overrides.yaml")),
		&renderConfig,
		nil,
	); err != nil {
		return err
	}

	// extendedArguments are no longer being parsed by kube-controller-mananger,
	// we need to parse and pass them explicitly
	var kubeControllerManagerConfig map[string]interface{}
	if err := yaml.Unmarshal(renderConfig.FileConfig.BootstrapConfig, &kubeControllerManagerConfig); err != nil {
		return fmt.Errorf("failed to unmarshal the kube-controller-manager config: %v", err)
	}
	extendedArguments := targetconfigcontroller.GetKubeControllerManagerArgs(kubeControllerManagerConfig)
	for _, arg := range extendedArguments {
		renderConfig.ExtendedArguments += fmt.Sprintf("\n    - %s", arg)
	}

	// add additional kubeconfig asset
	if kubeConfig, err := r.readBootstrapSecretsKubeconfig(); err != nil {
		return fmt.Errorf("failed to read %s/kubeconfig: %v", r.manifest.SecretsHostPath, err)
	} else {
		renderConfig.Assets["kubeconfig"] = kubeConfig
	}

	if err := genericrender.WriteFiles(&r.generic, &renderConfig.FileConfig, renderConfig); err != nil {
		return err
	}

	if err := ioutil.WriteFile(
		r.clusterPolicyControllerConfigOutputFile,
		renderConfig.ClusterPolicyControllerFileConfig.BootstrapConfig,
		0644,
	); err != nil {
		return fmt.Errorf("failed to write merged config to %q: %v", r.clusterPolicyControllerConfigOutputFile, err)
	}

	return nil
}

func (r *renderOpts) readBootstrapSecretsKubeconfig() ([]byte, error) {
	return ioutil.ReadFile(filepath.Join(r.generic.AssetInputDir, "..", "auth", "kubeconfig"))
}

func mustReadTemplateFile(fname string) genericrenderoptions.Template {
	bs, err := ioutil.ReadFile(fname)
	if err != nil {
		panic(fmt.Sprintf("Failed to load %q: %v", fname, err))
	}
	return genericrenderoptions.Template{FileName: fname, Content: bs}
}
