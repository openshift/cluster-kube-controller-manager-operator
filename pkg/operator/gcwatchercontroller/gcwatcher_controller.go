package gcwatchercontroller

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	prometheusv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	prometheusmodel "github.com/prometheus/common/model"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/kubernetes"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/klog/v2"

	operatorv1 "github.com/openshift/api/operator/v1"
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
}

const (
	controllerName                  = "garbage-collector-watcher-controller"
	invalidateAlertingRulesCacheKey = "__internal/invalidateAlertingRulesCacheKey"
	invalidateAlertingRulesPeriod   = 12 * time.Hour
)

func NewGarbageCollectorWatcherController(
	operatorClient v1helpers.StaticPodOperatorClient,
	kubeInformersForNamespaces v1helpers.KubeInformersForNamespaces,
	kubeClient kubernetes.Interface,
	eventRecorder events.Recorder,
	alertNames []string,
) factory.Controller {
	c := &GarbageCollectorWatcherController{
		operatorClient:  operatorClient,
		configMapClient: v1helpers.CachedConfigMapGetter(kubeClient.CoreV1(), kubeInformersForNamespaces),
		alertNames:      alertNames,
	}

	eventRecorderWithSuffix := eventRecorder.WithComponentSuffix(controllerName)
	syncContext := factory.NewSyncContext(controllerName, eventRecorder)
	syncContext.Queue().Add(invalidateAlertingRulesCacheKey)

	return factory.New().WithInformers(
		operatorClient.Informer(),
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

	syncErr := c.syncWorker(ctx, syncCtx)
	condition := operatorv1.OperatorCondition{
		Type:   "GarbageCollectorDegraded",
		Status: operatorv1.ConditionFalse,
		Reason: "AsExpected",
	}
	if syncErr != nil {
		condition.Status = operatorv1.ConditionTrue
		condition.Reason = "Error"
		condition.Message = syncErr.Error()
	}

	if _, _, updateErr := v1helpers.UpdateStatus(ctx, c.operatorClient, v1helpers.UpdateConditionFn(condition)); updateErr != nil {
		return updateErr
	}

	return syncErr
}

func (c *GarbageCollectorWatcherController) syncWorker(ctx context.Context, syncCtx factory.SyncContext) error {
	if len(c.alertNames) == 0 {
		return nil
	}
	requiredAlertsSet := sets.NewString(c.alertNames...)

	prometheusClient, err := newPrometheusClient(ctx, c.configMapClient)
	if err != nil {
		return err
	}

	alertingRules, err := c.getAlertingRulesCached(ctx, requiredAlertsSet, prometheusClient)
	if err != nil {
		return err
	}

	missingAlertsErr := checkMissingAlerts(requiredAlertsSet, alertingRules)
	if missingAlertsErr != nil {
		klog.Warning(missingAlertsErr)
	}

	return checkFiringAlerts(ctx, requiredAlertsSet, prometheusClient)
}

func (c *GarbageCollectorWatcherController) invalidateRulesCache() {
	c.alertingRulesCacheLock.Lock()
	defer c.alertingRulesCacheLock.Unlock()
	c.alertingRulesCache = nil
}

func (c *GarbageCollectorWatcherController) getAlertingRulesCached(ctx context.Context, requiredAlertsSet sets.String, prometheusClient prometheusv1.API) ([]prometheusv1.AlertingRule, error) {
	c.alertingRulesCacheLock.Lock()
	defer c.alertingRulesCacheLock.Unlock()

	if c.alertingRulesCache != nil {
		return c.alertingRulesCache, nil
	}

	rules, err := prometheusClient.Rules(ctx)
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
