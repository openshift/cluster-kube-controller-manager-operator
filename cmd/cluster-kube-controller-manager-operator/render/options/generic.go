package options

import (
	"bytes"
	"errors"
	"fmt"
	"io/ioutil"
	"path/filepath"
	"text/template"

	"github.com/ghodss/yaml"
	"github.com/openshift/library-go/pkg/assets"
	"github.com/openshift/library-go/pkg/operator/resource/resourcemerge"
	"github.com/spf13/pflag"
)

// GenericOptions contains the generic render command options.
type GenericOptions struct {
	ConfigOverrideFiles []string
	ConfigOutputFile    string

	TemplatesDir   string
	AssetInputDir  string
	AssetOutputDir string
}

// LoadAsset loads asset files.
type LoadAsset func(name string) []byte

// AddFlags adds the generic flags to the flagset.
func (o *GenericOptions) AddFlags(fs *pflag.FlagSet) {
	fs.StringVar(&o.AssetOutputDir, "asset-output-dir", "", "Output path for rendered manifests.")
	fs.StringVar(&o.AssetInputDir, "asset-input-dir", "", "A path to directory with certificates and secrets.")
	fs.StringVar(&o.TemplatesDir, "templates-input-dir", "/usr/share/bootkube/manifests", "A path to a directory with manifest templates.")
	fs.StringSliceVar(&o.ConfigOverrideFiles, "config-override-files", nil, "Additional sparse KubeControllerManagerConfig.kubecontrolplane.config.openshift.io/v1 files for customiziation through the installer, merged into the default config in the given order.")
	fs.StringVar(&o.ConfigOutputFile, "config-output-file", "", "Output path for the KubeControllerManagerConfig yaml file.")
}

// Complete fills in missing values before execution.
func (o *GenericOptions) Complete() error {
	return nil
}

// Validate verifies the inputs.
func (o *GenericOptions) Validate() error {
	if len(o.AssetInputDir) == 0 {
		return errors.New("missing required flag: --asset-output-dir")
	}
	if len(o.AssetOutputDir) == 0 {
		return errors.New("missing required flag: --asset-input-dir")
	}
	if len(o.TemplatesDir) == 0 {
		return errors.New("missing required flag: --templates-dir")
	}
	if len(o.ConfigOutputFile) == 0 {
		return errors.New("missing required flag: --config-output-file")
	}

	return nil
}

// ApplyTo applies the options ot the given config struct using the provides text/template data.
func (o *GenericOptions) ApplyTo(cfg *FileConfig, templateData interface{}, bootstrapVersion string, loadAsset LoadAsset) error {
	var err error

	cfg.BootstrapConfig, err = o.configFromDefaultsPlusOverride(templateData, filepath.Join(o.TemplatesDir, "config", "bootstrap-config-overrides.yaml"), bootstrapVersion, loadAsset)
	if err != nil {
		return fmt.Errorf("failed to generate bootstrap config (phase 1): %v", err)
	}

	if cfg.PostBootstrapConfig, err = o.configFromDefaultsPlusOverride(templateData, filepath.Join(o.TemplatesDir, "config", "config-overrides.yaml"), bootstrapVersion, loadAsset); err != nil {
		return fmt.Errorf("failed to generate post-bootstrap config (phase 2): %v", err)
	}

	// load and render templates
	if cfg.Assets, err = assets.LoadFilesRecursively(o.AssetInputDir); err != nil {
		return fmt.Errorf("failed loading assets from %q: %v", o.AssetInputDir, err)
	}

	return nil
}

func (o *GenericOptions) configFromDefaultsPlusOverride(templateData interface{}, tlsOverride, bootstrapVersion string, loadAsset LoadAsset) ([]byte, error) {
	defaultConfig := loadAsset(filepath.Join(bootstrapVersion, "kube-controller-manager", "defaultconfig.yaml"))
	bootstrapOverrides, err := readFileTemplate(tlsOverride, templateData)
	if err != nil {
		return nil, fmt.Errorf("failed to load config override file %q: %v", tlsOverride, err)
	}
	configs := [][]byte{defaultConfig, bootstrapOverrides}

	for _, fname := range o.ConfigOverrideFiles {
		overrides, err := readFileTemplate(fname, templateData)
		if err != nil {
			return nil, fmt.Errorf("failed to load config overrides at %q: %v", fname, err)
		}

		configs = append(configs, overrides)
	}
	mergedConfig, err := resourcemerge.MergeProcessConfig(nil, configs...)
	if err != nil {
		return nil, fmt.Errorf("failed to merge configs: %v", err)
	}
	yml, err := yaml.JSONToYAML(mergedConfig)
	if err != nil {
		return nil, err
	}

	return yml, nil
}

func readFileTemplate(fname string, data interface{}) ([]byte, error) {
	tpl, err := ioutil.ReadFile(fname)
	if err != nil {
		return nil, fmt.Errorf("failed to load %q: %v", fname, err)
	}

	tmpl, err := template.New(fname).Parse(string(tpl))
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}
