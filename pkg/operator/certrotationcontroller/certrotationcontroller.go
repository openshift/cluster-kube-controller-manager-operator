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
			Name: "csr-signer-signer",
			AdditionalAnnotations: certrotation.AdditionalAnnotations{
				JiraComponent: "kube-controller-manager",
			},
			Validity:            60 * rotationDay,
			Refresh:             30 * rotationDay,
			Informer:            kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets(),
			Lister:              kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets().Lister(),
			Client:              secretsGetter,
			EventRecorder:       eventRecorder,
			UseSecretUpdateOnly: true,
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
			Validity: 30 * rotationDay,
			Refresh:  15 * rotationDay,
			CertCreator: &certrotation.SignerRotation{
				SignerName: "kube-csr-signer",
			},
			Informer:            kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets(),
			Lister:              kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets().Lister(),
			Client:              secretsGetter,
			EventRecorder:       eventRecorder,
			UseSecretUpdateOnly: true,
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
