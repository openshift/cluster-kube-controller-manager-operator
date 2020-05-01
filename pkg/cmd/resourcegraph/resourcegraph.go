package resourcegraph

import (
	"fmt"

	"github.com/gonum/graph/encoding/dot"
	"github.com/spf13/cobra"
	"k8s.io/klog"

	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/operatorclient"
	"github.com/openshift/library-go/pkg/operator/resource/resourcegraph"
)

func NewResourceChainCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "resource-graph",
		Short: "Where do resources come from? Ask your mother.",
		Run: func(cmd *cobra.Command, args []string) {
			resources := Resources()
			g := resources.NewGraph()

			data, err := dot.Marshal(g, resourcegraph.Quote("kube-apiserver-operator"), "", "  ", false)
			if err != nil {
				klog.Fatal(err)
			}
			fmt.Println(string(data))
		},
	}

	return cmd
}

func Resources() resourcegraph.Resources {
	ret := resourcegraph.NewResources()

	payload := resourcegraph.NewResource(resourcegraph.NewCoordinates("", "Payload", "", "cluster")).
		Add(ret)
	installer := resourcegraph.NewResource(resourcegraph.NewCoordinates("", "Installer", "", "cluster")).
		Add(ret)
	user := resourcegraph.NewResource(resourcegraph.NewCoordinates("", "User", "", "cluster")).
		Add(ret)

	cvo := resourcegraph.NewOperator("cluster-version").
		From(payload).
		Add(ret)
	kasOperator := resourcegraph.NewOperator("kube-apiserver").
		From(cvo).
		Add(ret)
	kcmOperator := resourcegraph.NewOperator("kube-controller-manager").
		From(cvo).
		Add(ret)
	networkOperator := resourcegraph.NewOperator("network").
		From(cvo).
		Add(ret)
	ingressOperator := resourcegraph.NewOperator("ingress").
		From(cvo).
		Add(ret)
	serviceCAOperator := resourcegraph.NewOperator("service-ca").
		From(cvo).
		Add(ret)

	// config.openshift.io
	networkConfig := resourcegraph.NewConfig("networks").
		From(user).
		From(networkOperator).
		Add(ret)
	infrastructureConfig := resourcegraph.NewConfig("infrastructures").
		From(user).
		From(installer).
		Add(ret)

	// client cert/key
	kasCertKey := resourcegraph.NewSecret(operatorclient.GlobalMachineSpecifiedConfigNamespace, "kube-controller-manager-client-cert-key").
		Note("Rotated").
		From(kasOperator).
		Add(ret)
	clientCertKey := resourcegraph.NewSecret(operatorclient.TargetNamespace, "kube-controller-manager-client-cert-key").
		Note("Synchronized").
		From(kasCertKey).
		Add(ret)

	// CSR
	managedCSRSignerSigner := resourcegraph.NewSecret(operatorclient.OperatorNamespace, "csr-signer-signer").
		Note("Rotated").
		From(kcmOperator).
		Add(ret)
	managedCSRSignerSignerCA := resourcegraph.NewConfigMap(operatorclient.OperatorNamespace, "csr-controller-signer-ca").
		Note("Rotated").
		From(managedCSRSignerSigner).
		Add(ret)
	managedCSRSigner := resourcegraph.NewSecret(operatorclient.OperatorNamespace, "csr-signer").
		Note("Rotated").
		From(managedCSRSignerSigner).
		Add(ret)
	strippedSigner := resourcegraph.NewSecret(operatorclient.TargetNamespace, "csr-signer").
		Note("Reduced").
		From(managedCSRSigner).
		Add(ret)
	managedCSRSignerCA := resourcegraph.NewConfigMap(operatorclient.OperatorNamespace, "csr-signer-ca").
		Note("Rotated").
		From(managedCSRSigner).
		Add(ret)
	operatorCSRCA := resourcegraph.NewConfigMap(operatorclient.OperatorNamespace, "csr-controller-ca").
		Note("Unioned").
		From(managedCSRSignerCA).
		From(managedCSRSignerSignerCA).
		Add(ret)
	// this is a destination for KAS
	_ = resourcegraph.NewConfigMap(operatorclient.GlobalMachineSpecifiedConfigNamespace, "csr-controller-ca").
		Note("Synchronized").
		From(operatorCSRCA).
		Add(ret)

	// serviceaccount token ca.crt
	initialSACA := resourcegraph.NewConfigMap(operatorclient.GlobalUserSpecifiedConfigNamespace, "initial-serviceaccount-ca").
		Note("Static").
		From(installer).
		Add(ret)
	routerWildcardCA := resourcegraph.NewConfigMap(operatorclient.GlobalMachineSpecifiedConfigNamespace, "default-ingress-cert").
		Note("Static").
		From(ingressOperator).
		Add(ret)
	saCA := resourcegraph.NewConfigMap(operatorclient.TargetNamespace, "serviceaccount-ca").
		Note("Unioned").
		From(routerWildcardCA).
		From(initialSACA).
		Add(ret)

	// serving cert
	serviceCAController := resourcegraph.NewResource(resourcegraph.NewCoordinates("apps", "deployments", "openshift-service-ca", "service-serving-cert-signer")).
		From(serviceCAOperator).
		Add(ret)
	servingCert := resourcegraph.NewConfigMap(operatorclient.TargetNamespace, "serving-cert").
		Note("Rotated").
		From(serviceCAController).
		Add(ret)

	// observedConfig
	config := resourcegraph.NewConfigMap(operatorclient.OperatorNamespace, "config").
		Note("Managed").
		From(infrastructureConfig). // cloud provider
		From(networkConfig).        // service cidr for controllers
		Add(ret)

	// and finally our target pod
	_ = resourcegraph.NewResource(resourcegraph.NewCoordinates("", "pods", operatorclient.TargetNamespace, "kube-controller-manager")).
		From(clientCertKey).
		From(saCA).
		From(servingCert).
		From(strippedSigner).
		From(config).
		Add(ret)

	return ret
}
