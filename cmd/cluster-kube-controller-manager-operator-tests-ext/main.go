package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	otecmd "github.com/openshift-eng/openshift-tests-extension/pkg/cmd"
	oteextension "github.com/openshift-eng/openshift-tests-extension/pkg/extension"
	oteginkgo "github.com/openshift-eng/openshift-tests-extension/pkg/ginkgo"

	"k8s.io/klog/v2"
)

func main() {
	cmd, err := newOperatorTestCommand()
	if err != nil {
		klog.Fatal(err)
	}
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func newOperatorTestCommand() (*cobra.Command, error) {
	registry, err := prepareOperatorTestsRegistry()
	if err != nil {
		return nil, err
	}

	cmd := &cobra.Command{
		Use:   "cluster-kube-controller-manager-operator-tests-ext",
		Short: "A binary used to run cluster-kube-controller-manager-operator tests as part of OTE.",
		Long:  "Cluster Kube Controller Manager Operator Tests Extension",
		Run: func(cmd *cobra.Command, args []string) {
			if err := cmd.Help(); err != nil {
				klog.Fatal(err)
			}
		},
	}

	cmd.AddCommand(otecmd.DefaultExtensionCommands(registry)...)

	return cmd, nil
}

func prepareOperatorTestsRegistry() (*oteextension.Registry, error) {
	registry := oteextension.NewRegistry()
	extension := oteextension.NewExtension("openshift", "payload", "cluster-kube-controller-manager-operator")

	// parallel suite runs non-serial, non-disruptive tests concurrently with parallelism of 4.
	extension.AddSuite(oteextension.Suite{
		Name:        "openshift/cluster-kube-controller-manager-operator/operator/parallel",
		Parallelism: 4,
		Qualifiers: []string{
			`!name.contains("[Serial]") && !name.contains("[Disruptive]")`,
		},
	})

	// disruptive suite runs serial or disruptive tests one at a time, may impact cluster stability.
	extension.AddSuite(oteextension.Suite{
		Name:             "openshift/cluster-kube-controller-manager-operator/operator/disruptive",
		Parallelism:      1,
		ClusterStability: oteextension.ClusterStabilityDisruptive,
		Qualifiers: []string{
			`(name.contains("[Serial]") || name.contains("[Disruptive]")) && !name.contains("[preferred-host]")`,
		},
	})

	// preferred-host suite runs tests that validate KCM communication over the preferred host to KAS.
	extension.AddSuite(oteextension.Suite{
		Name:             "openshift/cluster-kube-controller-manager-operator/operator/preferred-host",
		Parallelism:      1,
		ClusterStability: oteextension.ClusterStabilityDisruptive,
		Qualifiers: []string{
			`name.contains("[Serial]") && name.contains("[Disruptive]") && name.contains("[preferred-host]")`,
		},
	})

	specs, err := oteginkgo.BuildExtensionTestSpecsFromOpenShiftGinkgoSuite()
	if err != nil {
		return nil, fmt.Errorf("couldn't build extension test specs from ginkgo: %w", err)
	}

	extension.AddSpecs(specs)
	registry.Register(extension)
	return registry, nil
}
