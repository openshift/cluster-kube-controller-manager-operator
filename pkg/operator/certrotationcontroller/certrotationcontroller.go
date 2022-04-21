package certrotationcontroller

import (
	"context"
	"time"

	"k8s.io/klog/v2"

	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"

	"github.com/openshift/library-go/pkg/operator/certrotation"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/v1helpers"

	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/operatorclient"
	"github.com/openshift/library-go/pkg/controller/factory"
)

// defaultRotationDay is the default rotation base for all cert rotation operations.
const defaultRotationDay = 24 * time.Hour

type CertRotationController struct {
	certRotators []factory.Controller
}

func NewCertRotationController(
	secretsGetter corev1client.SecretsGetter,
	configMapsGetter corev1client.ConfigMapsGetter,
	operatorClient v1helpers.StaticPodOperatorClient,
	kubeInformersForNamespaces v1helpers.KubeInformersForNamespaces,
	eventRecorder events.Recorder,
	day time.Duration,
) (*CertRotationController, error) {
	return newCertRotationController(
		secretsGetter,
		configMapsGetter,
		operatorClient,
		kubeInformersForNamespaces,
		eventRecorder,
		day,
		false,
	)
}

func NewCertRotationControllerOnlyWhenExpired(
	secretsGetter corev1client.SecretsGetter,
	configMapsGetter corev1client.ConfigMapsGetter,
	operatorClient v1helpers.StaticPodOperatorClient,
	kubeInformersForNamespaces v1helpers.KubeInformersForNamespaces,
	eventRecorder events.Recorder,
	day time.Duration,
) (*CertRotationController, error) {
	return newCertRotationController(
		secretsGetter,
		configMapsGetter,
		operatorClient,
		kubeInformersForNamespaces,
		eventRecorder,
		day,
		true,
	)
}

func newCertRotationController(
	secretsGetter corev1client.SecretsGetter,
	configMapsGetter corev1client.ConfigMapsGetter,
	operatorClient v1helpers.StaticPodOperatorClient,
	kubeInformersForNamespaces v1helpers.KubeInformersForNamespaces,
	eventRecorder events.Recorder,
	day time.Duration,
	refreshOnlyWhenExpired bool,
) (*CertRotationController, error) {
	ret := &CertRotationController{}

	rotationDay := defaultRotationDay
	if day != time.Duration(0) {
		rotationDay = day
		klog.Warningf("!!! UNSUPPORTED VALUE SET !!!")
		klog.Warningf("Certificate rotation base set to %q", rotationDay)
	}

	certRotator := certrotation.NewCertRotationController(
		"CSRSigningCert",
		certrotation.RotatedSigningCASecret{
			Namespace: operatorclient.OperatorNamespace,
			// this is not a typo, this is the signer of the signer
			Name:                   "csr-signer-signer",
			Validity:               60 * rotationDay,
			Refresh:                30 * rotationDay,
			RefreshOnlyWhenExpired: refreshOnlyWhenExpired,
			Informer:               kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets(),
			Lister:                 kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets().Lister(),
			Client:                 secretsGetter,
			EventRecorder:          eventRecorder,
		},
		certrotation.CABundleConfigMap{
			Namespace:     operatorclient.OperatorNamespace,
			Name:          "csr-controller-signer-ca",
			Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps(),
			Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps().Lister(),
			Client:        configMapsGetter,
			EventRecorder: eventRecorder,
		},
		certrotation.RotatedSelfSignedCertKeySecret{
			Namespace:              operatorclient.OperatorNamespace,
			Name:                   "csr-signer",
			Validity:               30 * rotationDay,
			Refresh:                15 * rotationDay,
			RefreshOnlyWhenExpired: refreshOnlyWhenExpired,
			CertCreator: &certrotation.SignerRotation{
				SignerName: "kube-csr-signer",
			},
			Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets(),
			Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets().Lister(),
			Client:        secretsGetter,
			EventRecorder: eventRecorder,
		},
		operatorClient,
		eventRecorder,
	)
	ret.certRotators = append(ret.certRotators, certRotator)

	certRotator = certrotation.NewCertRotationController(
		"KubeControllerManagerLocalhostServing",
		certrotation.RotatedSigningCASecret{
			Namespace:              operatorclient.OperatorNamespace,
			Name:                   "kcm-localhost-serving-signer",
			Validity:               10 * 365 * rotationDay,
			Refresh:                8 * 365 * rotationDay, // this means we effectively do not rotate
			RefreshOnlyWhenExpired: refreshOnlyWhenExpired,
			Informer:               kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets(),
			Lister:                 kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets().Lister(),
			Client:                 secretsGetter,
			EventRecorder:          eventRecorder,
		},
		certrotation.CABundleConfigMap{
			Namespace:     operatorclient.TargetNamespace,
			Name:          "kcm-localhost-serving-ca",
			Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().ConfigMaps(),
			Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().ConfigMaps().Lister(),
			Client:        configMapsGetter,
			EventRecorder: eventRecorder,
		},
		certrotation.RotatedSelfSignedCertKeySecret{
			Namespace:              operatorclient.TargetNamespace,
			Name:                   "kcm-localhost-serving-cert",
			Validity:               850 * rotationDay, // 28 months - kcm-cpc-reverse-proxy-serving-cert has 26 months
			Refresh:                425 * rotationDay, // 14 months - kcm-cpc-reverse-proxy-serving-cert has 13 months
			RefreshOnlyWhenExpired: refreshOnlyWhenExpired,
			CertCreator: &certrotation.ServingRotation{
				Hostnames: func() []string { return []string{"localhost", "127.0.0.1"} },
			},
			Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets(),
			Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets().Lister(),
			Client:        secretsGetter,
			EventRecorder: eventRecorder,
		},
		operatorClient,
		eventRecorder,
	)
	ret.certRotators = append(ret.certRotators, certRotator)

	certRotator = certrotation.NewCertRotationController(
		"ClusterPolicyControllerLocalhostServing",
		certrotation.RotatedSigningCASecret{
			Namespace:              operatorclient.OperatorNamespace,
			Name:                   "cpc-localhost-serving-signer",
			Validity:               10 * 365 * rotationDay,
			Refresh:                8 * 365 * rotationDay, // this means we effectively do not rotate
			RefreshOnlyWhenExpired: refreshOnlyWhenExpired,
			Informer:               kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets(),
			Lister:                 kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets().Lister(),
			Client:                 secretsGetter,
			EventRecorder:          eventRecorder,
		},
		certrotation.CABundleConfigMap{
			Namespace:     operatorclient.TargetNamespace,
			Name:          "cpc-localhost-serving-ca",
			Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().ConfigMaps(),
			Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().ConfigMaps().Lister(),
			Client:        configMapsGetter,
			EventRecorder: eventRecorder,
		},
		certrotation.RotatedSelfSignedCertKeySecret{
			Namespace:              operatorclient.TargetNamespace,
			Name:                   "cpc-localhost-serving-cert",
			Validity:               850 * rotationDay, // 28 months - kcm-cpc-reverse-proxy-serving-cert has 26 months
			Refresh:                425 * rotationDay, // 14 months - kcm-cpc-reverse-proxy-serving-cert has 13 months
			RefreshOnlyWhenExpired: refreshOnlyWhenExpired,
			CertCreator: &certrotation.ServingRotation{
				Hostnames: func() []string { return []string{"localhost", "127.0.0.1"} },
			},
			Informer:      kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets(),
			Lister:        kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets().Lister(),
			Client:        secretsGetter,
			EventRecorder: eventRecorder,
		},
		operatorClient,
		eventRecorder,
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
