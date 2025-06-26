package certrotationcontroller

import (
	"context"
	"fmt"
	"time"

	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/klog/v2"

	"github.com/openshift/library-go/pkg/operator/certrotation"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/v1helpers"

	features "github.com/openshift/api/features"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/operatorclient"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
)

type CertRotationController struct {
	certRotators []factory.Controller
}

func NewCertRotationController(
	secretsGetter corev1client.SecretsGetter,
	configMapsGetter corev1client.ConfigMapsGetter,
	operatorClient v1helpers.StaticPodOperatorClient,
	kubeInformersForNamespaces v1helpers.KubeInformersForNamespaces,
	eventRecorder events.Recorder,
	featureGateAccessor featuregates.FeatureGateAccess,
) (*CertRotationController, error) {
	return newCertRotationController(
		secretsGetter,
		configMapsGetter,
		operatorClient,
		kubeInformersForNamespaces,
		eventRecorder,
		featureGateAccessor,
		false,
	)
}

func NewCertRotationControllerOnlyWhenExpired(
	secretsGetter corev1client.SecretsGetter,
	configMapsGetter corev1client.ConfigMapsGetter,
	operatorClient v1helpers.StaticPodOperatorClient,
	kubeInformersForNamespaces v1helpers.KubeInformersForNamespaces,
	eventRecorder events.Recorder,
	featureGateAccessor featuregates.FeatureGateAccess,
) (*CertRotationController, error) {
	return newCertRotationController(
		secretsGetter,
		configMapsGetter,
		operatorClient,
		kubeInformersForNamespaces,
		eventRecorder,
		featureGateAccessor,
		true,
	)
}

func newCertRotationController(
	secretsGetter corev1client.SecretsGetter,
	configMapsGetter corev1client.ConfigMapsGetter,
	operatorClient v1helpers.StaticPodOperatorClient,
	kubeInformersForNamespaces v1helpers.KubeInformersForNamespaces,
	eventRecorder events.Recorder,
	featureGateAccessor featuregates.FeatureGateAccess,
	refreshOnlyWhenExpired bool,
) (*CertRotationController, error) {
	ret := &CertRotationController{}

	refreshPeriod := time.Hour * 24 * 30

	featureGates, err := featureGateAccessor.CurrentFeatureGates()
	if err != nil {
		return nil, fmt.Errorf("unable to get FeatureGates: %w", err)
	}

	// This featuregate should be enabled on install time, we don't support enabling or disabling it after install.
	if featureGates.Enabled(features.FeatureShortCertRotation) {
		refreshPeriod = time.Hour * 2
		klog.Infof("Setting refreshPeriod to %v", refreshPeriod)
	}

	certRotator := certrotation.NewCertRotationController(
		"CSRSigningCert",
		certrotation.RotatedSigningCASecret{
			Namespace: operatorclient.OperatorNamespace,
			// this is not a typo, this is the signer of the signer
			Name: "csr-signer-signer",
			AdditionalAnnotations: certrotation.AdditionalAnnotations{
				JiraComponent: "kube-controller-manager",
			},
			Validity:               refreshPeriod * 2,
			Refresh:                refreshPeriod,
			RefreshOnlyWhenExpired: refreshOnlyWhenExpired,
			Informer:               kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets(),
			Lister:                 kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets().Lister(),
			Client:                 secretsGetter,
			EventRecorder:          eventRecorder,
		},
		certrotation.CABundleConfigMap{
			Namespace: operatorclient.OperatorNamespace,
			Name:      "csr-controller-signer-ca",
			AdditionalAnnotations: certrotation.AdditionalAnnotations{
				JiraComponent: "kube-controller-manager",
			},
			Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps(),
			Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps().Lister(),
			Client:        configMapsGetter,
			EventRecorder: eventRecorder,
		},
		certrotation.RotatedSelfSignedCertKeySecret{
			Namespace: operatorclient.OperatorNamespace,
			Name:      "csr-signer",
			AdditionalAnnotations: certrotation.AdditionalAnnotations{
				JiraComponent: "kube-controller-manager",
			},
			Validity:               refreshPeriod,
			Refresh:                refreshPeriod / 2,
			RefreshOnlyWhenExpired: refreshOnlyWhenExpired,
			CertCreator: &certrotation.SignerRotation{
				SignerName: "kube-csr-signer",
			},
			Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets(),
			Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets().Lister(),
			Client:        secretsGetter,
			EventRecorder: eventRecorder,
		},
		eventRecorder,
		&certrotation.StaticPodConditionStatusReporter{OperatorClient: operatorClient},
	)

	ret.certRotators = append(ret.certRotators, certRotator)

	return ret, nil
}

func (c *CertRotationController) Run(ctx context.Context, workers int) {
	syncCtx := context.WithValue(ctx, certrotation.RunOnceContextKey, false)
	for _, certRotator := range c.certRotators {
		go certRotator.Run(syncCtx, workers)
	}
}
