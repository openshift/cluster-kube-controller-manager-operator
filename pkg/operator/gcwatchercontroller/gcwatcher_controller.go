package gcwatchercontroller

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	prometheusv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	prometheusmodel "github.com/prometheus/common/model"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/kubernetes"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/klog/v2"

	operatorv1 "github.com/openshift/api/operator/v1"
	configinformers "github.com/openshift/client-go/config/informers/externalversions"
	configlisters "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/v1helpers"

	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/operatorclient"
)

type GarbageCollectorWatcherController struct {
	operatorClient         v1helpers.StaticPodOperatorClient
	configMapClient        corev1client.ConfigMapsGetter
	alertNames             []string
	alertingRulesCache     []prometheusv1.AlertingRule
	alertingRulesCacheLock sync.RWMutex
	clusterLister          configlisters.ClusterOperatorLister
	promConnectivity       prometheusConnectivity
}

// prometheusConnectivity sets up the prometheus connectivity.
type prometheusConnectivity struct {
	// usedCachedClient asks reconciler not to establish new connection but re-use existing connections.
	// This is only used for unit testing. In future, we can break the newPrometheusClient function to be a method
	// in this  struct for easier testing and debugging
	useCachedClient bool
	// client is the actual prometheus client
	client prometheusv1.API
}

const (
	controllerName                  = "garbage-collector-watcher-controller"
	invalidateAlertingRulesCacheKey = "__internal/invalidateAlertingRulesCacheKey"
	invalidateAlertingRulesPeriod   = 12 * time.Hour
)

func NewGarbageCollectorWatcherController(
	operatorClient v1helpers.StaticPodOperatorClient,
	kubeInformersForNamespaces v1helpers.KubeInformersForNamespaces,
	configInformers configinformers.SharedInformerFactory,
	kubeClient kubernetes.Interface,
	eventRecorder events.Recorder,
	alertNames []string,
) factory.Controller {
	c := &GarbageCollectorWatcherController{
		operatorClient:   operatorClient,
		configMapClient:  v1helpers.CachedConfigMapGetter(kubeClient.CoreV1(), kubeInformersForNamespaces),
		alertNames:       alertNames,
		clusterLister:    configInformers.Config().V1().ClusterOperators().Lister(),
		promConnectivity: prometheusConnectivity{useCachedClient: false, client: nil},
	}

	eventRecorderWithSuffix := eventRecorder.WithComponentSuffix(controllerName)
	syncContext := factory.NewSyncContext(controllerName, eventRecorder)
	syncContext.Queue().Add(invalidateAlertingRulesCacheKey)

	return factory.New().WithInformers(
		operatorClient.Informer(),
		configInformers.Config().V1().ClusterOperators().Informer(), // To check if monitoring is installed or not
		kubeInformersForNamespaces.InformersFor(operatorclient.GlobalMachineSpecifiedConfigNamespace).Core().V1().ConfigMaps().Informer(), // for prometheus client
	).ResyncEvery(5*time.Minute).WithSyncContext(syncContext).WithSync(c.sync).ToController("GarbageCollectorWatcherController", eventRecorderWithSuffix)
}

func (c *GarbageCollectorWatcherController) sync(ctx context.Context, syncCtx factory.SyncContext) error {
	key := syncCtx.QueueKey()
	if key == invalidateAlertingRulesCacheKey {
		// fetching all rules is expensive, so cache them and invalidate it every 12 hours
		defer syncCtx.Queue().AddAfter(invalidateAlertingRulesCacheKey, invalidateAlertingRulesPeriod)
		c.invalidateRulesCache()
		return nil
	}
	condition := operatorv1.OperatorCondition{
		Type:   "GarbageCollectorDegraded",
		Status: operatorv1.ConditionFalse,
		Reason: "AsExpected",
	}
	_, err := c.clusterLister.Get("monitoring")
	if err != nil && errors.IsNotFound(err) {
		klog.V(5).Info("Monitoring is disabled in the cluster and a diagnostic of the garbage collector is not working. Please look at the kcm logs for more information to debug the garbage collector further")
		// Disabled monitoring works as expected and is not degraded
		condition.Reason = "MonitoringDisabled"
		_, _, updateErr := v1helpers.UpdateStatus(ctx, c.operatorClient, v1helpers.UpdateConditionFn(condition))
		return updateErr
	}
	if err != nil { // Could be intermittent issues with connectivity, try after sometime, don't set the status yet.
		return err
	}
	syncErr := c.syncWorker(ctx, syncCtx)

	if syncErr != nil {
		condition.Status = operatorv1.ConditionTrue
		condition.Reason = "Error"
		condition.Message = syncErr.Error()
	}

	_, _, updateErr := v1helpers.UpdateStatus(ctx, c.operatorClient, v1helpers.UpdateConditionFn(condition))
	if updateErr != nil {
		return updateErr
	}

	return syncErr
}

func (c *GarbageCollectorWatcherController) syncWorker(ctx context.Context, syncCtx factory.SyncContext) error {
	if len(c.alertNames) == 0 {
		return nil
	}
	requiredAlertsSet := sets.NewString(c.alertNames...)

	// useCachedClient for unit testing. We can try re-using the connections in future
	if !c.promConnectivity.useCachedClient {
		prometheusClient, err := newPrometheusClient(ctx, c.configMapClient)
		if err != nil {
			// Prometheus client when failed to instantiate should not result in error being generated. We can reach
			// this stage if CMO is disabled day-2  and thanos services are removed after cluster installation
			// has happened.
			// TODO: In future, cluster operators can have status which states if they are managed by CVO or not
			//		and we can use to represent failure.
			klog.Errorf("failed to instantiate prometheus client. Thanos is not queriable at the moment with %v",
				err)
			return nil
		}
		c.promConnectivity.client = prometheusClient
	}

	alertingRules, err := c.getAlertingRulesCached(ctx, requiredAlertsSet)
	if err != nil {
		return err
	}

	missingAlertsErr := checkMissingAlerts(requiredAlertsSet, alertingRules)
	if missingAlertsErr != nil {
		klog.Warning(missingAlertsErr)
	}
	return checkFiringAlerts(ctx, requiredAlertsSet, c.promConnectivity.client)
}

func (c *GarbageCollectorWatcherController) invalidateRulesCache() {
	c.alertingRulesCacheLock.Lock()
	defer c.alertingRulesCacheLock.Unlock()
	c.alertingRulesCache = nil
}

func (c *GarbageCollectorWatcherController) getAlertingRulesCached(ctx context.Context, requiredAlertsSet sets.String) ([]prometheusv1.AlertingRule, error) {
	c.alertingRulesCacheLock.Lock()
	defer c.alertingRulesCacheLock.Unlock()

	if c.alertingRulesCache != nil {
		return c.alertingRulesCache, nil
	}

	rules, err := c.promConnectivity.client.Rules(ctx)

	if err != nil {
		return nil, fmt.Errorf("error fetching rules: %v", err)
	}

	c.alertingRulesCache = extractAlertingRules(requiredAlertsSet, rules)

	klog.Infof("Synced alerting rules cache")
	return c.alertingRulesCache, nil
}

func extractAlertingRules(requiredAlertsSet sets.String, rules prometheusv1.RulesResult) []prometheusv1.AlertingRule {
	// empty object to initialize cache even if there are no rules
	alertingRules := []prometheusv1.AlertingRule{}
	for _, group := range rules.Groups {
		for _, rule := range group.Rules {
			// filter so we do not store all rules since there are a lot of them
			if alertingRule, ok := rule.(prometheusv1.AlertingRule); ok && requiredAlertsSet.Has(alertingRule.Name) {
				alertingRules = append(alertingRules, alertingRule)
			}
		}
	}
	return alertingRules
}

func checkMissingAlerts(requiredAlertsSet sets.String, alertingRules []prometheusv1.AlertingRule) error {
	alertingRulesSet := sets.String{}
	for _, alertingRule := range alertingRules {
		alertingRulesSet.Insert(alertingRule.Name)
	}
	missingAlertsSet := requiredAlertsSet.Difference(alertingRulesSet)

	if len(missingAlertsSet) > 0 {
		return fmt.Errorf("missing required alerts: %v", strings.Join(missingAlertsSet.List(), ", "))
	}
	return nil
}

func checkFiringAlerts(ctx context.Context, requiredAlertsSet sets.String, prometheusClient prometheusv1.API) error {
	query := fmt.Sprintf("ALERTS{alertstate=\"firing\", namespace=\"%s\"}", operatorclient.TargetNamespace)
	queryResultVal, warnings, err := prometheusClient.Query(ctx, query, time.Now())
	if len(warnings) > 0 {
		klog.Warningf("received warnings when querying alerts: %v\n", strings.Join(warnings, ", "))
	}
	if err != nil {
		return fmt.Errorf("error querying alerts: %v", err)
	}
	queryResultVector, ok := queryResultVal.(prometheusmodel.Vector)
	if !ok {
		return fmt.Errorf("could not assert Vector type on prometheus query response")
	}

	allFiringAlertSet := sets.String{}
	for _, alert := range queryResultVector {
		alertName := alert.Metric[prometheusmodel.AlertNameLabel]
		allFiringAlertSet.Insert(string(alertName))
	}
	firingAlertsSet := allFiringAlertSet.Intersection(requiredAlertsSet)

	if len(firingAlertsSet) > 0 {
		return fmt.Errorf("alerts firing: %v", strings.Join(firingAlertsSet.List(), ", "))
	}
	return nil
}
