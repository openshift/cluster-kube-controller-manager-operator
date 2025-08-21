package main

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	"github.com/openshift-eng/openshift-tests-extension/pkg/cmd"
	"github.com/openshift-eng/openshift-tests-extension/pkg/dbtime"
	"github.com/openshift-eng/openshift-tests-extension/pkg/extension"
	"github.com/openshift-eng/openshift-tests-extension/pkg/extension/extensiontests"

	"github.com/spf13/cobra"

	_ "github.com/openshift/cluster-kube-controller-manager-operator/test/extended"
)

var (
	CommitFromGit string
	BuildDate     string
	GitTreeState  string
)

// GinkgoTestingT implements the minimal TestingT interface needed by Ginkgo
type GinkgoTestingT struct{}

func (GinkgoTestingT) Errorf(format string, args ...interface{}) {}
func (GinkgoTestingT) Fail()                                     {}
func (GinkgoTestingT) FailNow()                                  { os.Exit(1) }

// NewGinkgoTestingT creates a new testing.T compatible instance for Ginkgo
func NewGinkgoTestingT() *GinkgoTestingT {
	return &GinkgoTestingT{}
}

// escapeRegexChars escapes special regex characters in test names for Ginkgo focus
func escapeRegexChars(s string) string {
	// Only escape the problematic characters that cause regex parsing issues
	// We need to escape [ and ] which are treated as character classes
	s = strings.ReplaceAll(s, "[", "\\[")
	s = strings.ReplaceAll(s, "]", "\\]")
	return s
}

// createTestSpec creates a test spec with proper execution functions
func createTestSpec(name, source string, codeLocations []string) *extensiontests.ExtensionTestSpec {
	return &extensiontests.ExtensionTestSpec{
		Name:          name,
		Source:        source,
		CodeLocations: codeLocations,
		Lifecycle:     extensiontests.LifecycleBlocking,
		Resources: extensiontests.Resources{
			Isolation: extensiontests.Isolation{},
		},
		EnvironmentSelector: extensiontests.EnvironmentSelector{},
		Run: func(ctx context.Context) *extensiontests.ExtensionTestResult {
			return runGinkgoTest(ctx, name)
		},
		RunParallel: func(ctx context.Context) *extensiontests.ExtensionTestResult {
			return runGinkgoTest(ctx, name)
		},
	}
}

// runGinkgoTest runs a Ginkgo test in-process
func runGinkgoTest(ctx context.Context, testName string) *extensiontests.ExtensionTestResult {
	startTime := time.Now()

	// Configure Ginkgo to run specific test
	gomega.RegisterFailHandler(ginkgo.Fail)

	// Run the test suite with focus on specific test
	suiteConfig, reporterConfig := ginkgo.GinkgoConfiguration()
	suiteConfig.FocusStrings = []string{escapeRegexChars(testName)}
	
	// Configure JUnit reporter for CI integration
	reporterConfig.JUnitReport = "junit.xml"
	reporterConfig.JSONReport = "report.json"

	passed := ginkgo.RunSpecs(NewGinkgoTestingT(), "OpenShift Kube Controller Manager Operator Test Suite", suiteConfig, reporterConfig)

	endTime := time.Now()
	duration := endTime.Sub(startTime)

	result := extensiontests.ResultPassed
	if !passed {
		result = extensiontests.ResultFailed
	}

	return &extensiontests.ExtensionTestResult{
		Name:      testName,
		Result:    result,
		StartTime: dbtime.Ptr(startTime),
		EndTime:   dbtime.Ptr(endTime),
		Duration:  int64(duration.Seconds()),
		Output:    "",
	}
}

func main() {
	// Create a new registry
	registry := extension.NewRegistry()

	// Create extension for this component
	ext := extension.NewExtension("openshift", "payload", "cluster-kube-controller-manager-operator")

	// Set source information
	ext.Source = extension.Source{
		Commit:       CommitFromGit,
		BuildDate:    BuildDate,
		GitTreeState: GitTreeState,
	}

	// Add test suites
	ext.AddGlobalSuite(extension.Suite{
		Name:        "openshift/cluster-kube-controller-manager-operator/conformance/parallel",
		Description: "",
		Parents:     []string{"openshift/conformance/parallel"},
		Qualifiers:  []string{"(source == \"openshift:payload:cluster-kube-controller-manager-operator\") && (!(name.contains(\"[Serial]\") || name.contains(\"[Slow]\")))"},
	})

	ext.AddGlobalSuite(extension.Suite{
		Name:        "openshift/cluster-kube-controller-manager-operator/conformance/serial",
		Description: "",
		Parents:     []string{"openshift/conformance/serial"},
		Qualifiers:  []string{"(source == \"openshift:payload:cluster-kube-controller-manager-operator\") && (name.contains(\"[Serial]\"))"},
	})

	ext.AddGlobalSuite(extension.Suite{
		Name:        "openshift/cluster-kube-controller-manager-operator/optional/slow",
		Description: "",
		Parents:     []string{"openshift/optional/slow"},
		Qualifiers:  []string{"(source == \"openshift:payload:cluster-kube-controller-manager-operator\") && (name.contains(\"[Slow]\"))"},
	})

	ext.AddGlobalSuite(extension.Suite{
		Name:        "openshift/cluster-kube-controller-manager-operator/all",
		Description: "",
		Qualifiers:  []string{"source == \"openshift:payload:cluster-kube-controller-manager-operator\""},
	})

	// Add test specs with proper execution functions
	testSpecs := extensiontests.ExtensionTestSpecs{
		createTestSpec(
			"[Jira:kube-controller-manager][sig-api-machinery] sanity test should always pass [Suite:openshift/cluster-kube-controller-manager-operator/conformance/parallel]",
			"openshift:payload:cluster-kube-controller-manager-operator",
			[]string{
				"/test/extended/main.go:8",
				"/test/extended/main.go:9",
			},
		),
	}
	ext.AddSpecs(testSpecs)

	// Register the extension
	registry.Register(ext)

	// Create root command with default extension commands
	rootCmd := &cobra.Command{
		Use:   "cluster-kube-controller-manager-operator-tests-ext",
		Short: "OpenShift kube-controller-manager operator tests extension",
	}

	// Add all the default extension commands (info, list, run-test, run-suite, update)
	rootCmd.AddCommand(cmd.DefaultExtensionCommands(registry)...)

	// Execute the command
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
