package library

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	g "github.com/onsi/ginkgo/v2"
)

const ocCommandTimeout = 5 * time.Minute

var bearerTokenRe = regexp.MustCompile(`Bearer\s+\S+`)

// OC wraps oc CLI for e2e tests that still rely on openshift/cli behavior.
// Each instance gets its own kubeconfig copy to avoid corruption from
// concurrent oc commands modifying the same file.
type OC struct {
	namespace  string
	noNS       bool
	kubeconfig string
}

func NewOC() *OC {
	oc := &OC{}
	oc.kubeconfig = copyKubeconfig()
	return oc
}

func (c *OC) Namespace() string {
	return c.namespace
}

func (c *OC) SetupProject() {
	name := randomString()
	_, err := c.WithoutNamespace().Run("new-project").Args(name).Output()
	if err != nil {
		g.Fail(fmt.Sprintf("create project: %v", err))
	}
	c.namespace = name
}

func (c *OC) WithoutNamespace() *OC {
	clone := *c
	clone.noNS = true
	return &clone
}

type ocCommand struct {
	oc   *OC
	args []string
}

func (c *OC) Run(subcommand string) *ocCommand {
	return &ocCommand{oc: c, args: []string{subcommand}}
}

func (cmd *ocCommand) Args(args ...string) *ocCommand {
	cmd.args = append(cmd.args, args...)
	return cmd
}

func (cmd *ocCommand) Output() (string, error) {
	out, err := cmd.run()
	return string(out), err
}

func (cmd *ocCommand) Execute() error {
	_, err := cmd.run()
	return err
}

func (cmd *ocCommand) Outputs() (string, string, error) {
	out, err := cmd.run()
	return string(out), string(out), err
}

func (cmd *ocCommand) run() ([]byte, error) {
	args := append([]string{}, cmd.args...)
	if !cmd.oc.noNS && cmd.oc.namespace != "" {
		args = append(args, "-n", cmd.oc.namespace)
	}
	full := append([]string{"oc"}, args...)
	fmt.Fprintf(g.GinkgoWriter, "running: %s\n", redactSensitive(strings.Join(full, " ")))
	ctx, cancel := context.WithTimeout(context.Background(), ocCommandTimeout)
	defer cancel()
	c := exec.CommandContext(ctx, "oc", args...)
	c.Env = append(os.Environ(), "KUBECONFIG="+cmd.oc.kubeconfig)
	out, err := c.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

// copyKubeconfig creates a per-instance copy of the kubeconfig so that
// parallel tests don't corrupt a shared file when oc modifies contexts.
func copyKubeconfig() string {
	src := os.Getenv("KUBECONFIG")
	if src == "" {
		src = os.ExpandEnv("$HOME/.kube/config")
	}
	data, err := os.ReadFile(src)
	if err != nil {
		g.Fail(fmt.Sprintf("read kubeconfig %s: %v", src, err))
	}
	tmp, err := os.CreateTemp("", "kcm-e2e-kubeconfig-*")
	if err != nil {
		g.Fail(fmt.Sprintf("create temp kubeconfig: %v", err))
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		g.Fail(fmt.Sprintf("write temp kubeconfig: %v", err))
	}
	tmp.Close()
	return tmp.Name()
}

func randomString() string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	seed := rand.New(rand.NewSource(time.Now().UnixNano()))
	b := make([]byte, 8)
	for i := range b {
		b[i] = chars[seed.Intn(len(chars))]
	}
	return "kcm-e2e-" + string(b)
}

func redactSensitive(s string) string {
	return bearerTokenRe.ReplaceAllString(s, "Bearer [REDACTED]")
}
