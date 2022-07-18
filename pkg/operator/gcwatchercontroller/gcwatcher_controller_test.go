package gcwatchercontroller

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	prometheusv1 "github.com/prometheus/client_golang/api/prometheus/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/cache"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	configlisters "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/operatorclient"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
)

func TestExtractAlertingRules(t *testing.T) {
	type Test struct {
		name                string
		requiredAlertsSet   sets.String
		rules               []prometheusv1.AlertingRule
		expectedRules       []prometheusv1.AlertingRule
		expectMissingAlerts string
	}
	tests := []Test{
		{
			name:              "does not extract empty required alerts",
			requiredAlertsSet: sets.NewString(),
			rules: []prometheusv1.AlertingRule{
				{Name: "AlertOne"},
				{Name: "AlertTwo"},
			},
			expectedRules: []prometheusv1.AlertingRule{},
		},
		{
			name:              "does not extracts missing required alerts",
			requiredAlertsSet: sets.NewString("AlertTwo"),
			rules: []prometheusv1.AlertingRule{
				{Name: "AlertOne"},
			},
			expectedRules: []prometheusv1.AlertingRule{},
		},
		{
			name:              "extracts required alerts",
			requiredAlertsSet: sets.NewString("AlertOne", "AlertTwo"),
			rules: []prometheusv1.AlertingRule{
				{Name: "AlertOne"},
				{Name: "AlertTwo"},
				{Name: "AlertThree"},
			},
			expectedRules: []prometheusv1.AlertingRule{
				{Name: "AlertOne"},
				{Name: "AlertTwo"},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// init required struct
			rules := prometheusv1.RulesResult{Groups: []prometheusv1.RuleGroup{{Rules: prometheusv1.Rules{}}}}
			for _, rule := range test.rules {
				rules.Groups[0].Rules = append(rules.Groups[0].Rules, rule)
			}

			result := extractAlertingRules(test.requiredAlertsSet, rules)
			if !reflect.DeepEqual(test.expectedRules, result) {
				t.Errorf(" expected extracted alerting rules: %v, got: %v", test.expectedRules, result)
			}
		})
	}
}

func TestCheckMissingAlerts(t *testing.T) {
	type Test struct {
		name                string
		requiredAlertsSet   sets.String
		alertingRules       []prometheusv1.AlertingRule
		expectMissingAlerts string
	}
	tests := []Test{
		{
			name:              "has all required alerts",
			requiredAlertsSet: sets.NewString("AlertOne", "AlertTwo"),
			alertingRules: []prometheusv1.AlertingRule{
				{Name: "AlertOne"},
				{Name: "AlertTwo"},
				{Name: "AlertThree"},
			},
			expectMissingAlerts: "",
		},
		{
			name:              "has all required alerts single",
			requiredAlertsSet: sets.NewString("AlertOne"),
			alertingRules: []prometheusv1.AlertingRule{
				{Name: "AlertOne"},
			},
			expectMissingAlerts: "",
		},
		{
			name:              "has missing required alerts",
			requiredAlertsSet: sets.NewString("AlertOne", "AlertTwo"),
			alertingRules: []prometheusv1.AlertingRule{
				{Name: "AlertThree"},
				{Name: "AlertFour"},
				{Name: "AlertFive"},
			},
			expectMissingAlerts: "AlertOne, AlertTwo",
		},
		{
			name:              "has missing required alerts intersection",
			requiredAlertsSet: sets.NewString("AlertOne", "AlertTwo"),
			alertingRules: []prometheusv1.AlertingRule{
				{Name: "AlertOne"},
				{Name: "AlertThree"},
			},
			expectMissingAlerts: "AlertTwo",
		},
		{
			name:                "has missing required alerts single",
			requiredAlertsSet:   sets.NewString("AlertOne"),
			alertingRules:       nil,
			expectMissingAlerts: "AlertOne",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := checkMissingAlerts(test.requiredAlertsSet, test.alertingRules)
			if err != nil {
				if len(test.expectMissingAlerts) == 0 {
					t.Fatalf("did not expect error, but got: %v", err)
				}
				if !strings.Contains(err.Error(), test.expectMissingAlerts) {
					t.Fatalf("expected missing alerts: %v, but got: %v", test.expectMissingAlerts, err)

				}
			} else {
				if len(test.expectMissingAlerts) > 0 {
					t.Fatalf("expected error, but got none")
				}
			}
		})
	}
}

func TestCheckFiringAlerts(t *testing.T) {
	type Test struct {
		name              string
		requiredAlertsSet sets.String
		firingAlerts      []string
		queryErr          error
		expectError       string
	}
	tests := []Test{
		// client err
		{
			name:              "client not responding",
			requiredAlertsSet: sets.NewString("AlertOne", "AlertTwo"),
			queryErr:          errors.New("500 Server Error"),
			expectError:       "500 Server Error",
		},
		// not firing
		{
			name:              "required alerts not firing: empty response",
			requiredAlertsSet: sets.NewString("AlertOne", "AlertTwo"),
			firingAlerts:      nil,
			expectError:       "",
		},
		{
			name:              "empty required alerts not firing",
			requiredAlertsSet: sets.NewString(),
			firingAlerts: []string{
				"AlertThree",
				"AlertFour",
			},
			expectError: "",
		},
		{
			name:              "required alerts not firing",
			requiredAlertsSet: sets.NewString("AlertOne", "AlertTwo"),
			firingAlerts: []string{
				"AlertThree",
				"AlertFour",
			},
			expectError: "",
		},
		{
			name:              "required alerts not firing single",
			requiredAlertsSet: sets.NewString("AlertOne"),
			firingAlerts: []string{
				"AlertOnes",
				"AlertFour",
			},
			expectError: "",
		},
		// firing
		{
			name:              "required alerts firing",
			requiredAlertsSet: sets.NewString("AlertOne", "AlertTwo"),
			firingAlerts: []string{
				"AlertOne",
				"AlertThree",
				"AlertFour",
			},
			expectError: "alerts firing: AlertOne",
		},
		{
			name:              "required alerts firing multiple",
			requiredAlertsSet: sets.NewString("AlertOne", "AlertTwo"),
			firingAlerts: []string{
				"AlertOne",
				"AlertThree",
				"AlertTwo",
				"AlertFour",
			},
			expectError: "alerts firing: AlertOne, AlertTwo",
		},
		{
			name:              "required alerts firing single",
			requiredAlertsSet: sets.NewString("AlertOne"),
			firingAlerts: []string{
				"AlertOne",
				"AlertTwo",
			},
			expectError: "alerts firing: AlertOne",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			prometheusClient := newFakePrometheusClient(test.firingAlerts, test.queryErr)

			err := checkFiringAlerts(context.TODO(), test.requiredAlertsSet, prometheusClient)
			if err != nil {
				if len(test.expectError) == 0 {
					t.Fatalf("did not expect error, but got: %v", err)
				}
				if !strings.Contains(err.Error(), test.expectError) {
					t.Fatalf("expected firing alerts: %v, but got: %v", test.expectError, err)

				}
			} else {
				if len(test.expectError) > 0 {
					t.Fatalf("expected error, but got none")
				}
			}
		})
	}
}

func TestGarbageCollectorSync(t *testing.T) {
	status := &operatorv1.StaticPodOperatorStatus{}
	successCondition := operatorv1.OperatorCondition{
		Type:   "GarbageCollectorDegraded",
		Status: operatorv1.ConditionFalse,
		Reason: "AsExpected",
	}
	syncError := fmt.Errorf("error querying alerts: prometheus querying failed")
	failureCondition := operatorv1.OperatorCondition{
		Type:    "GarbageCollectorDegraded",
		Status:  operatorv1.ConditionTrue,
		Reason:  "Error",
		Message: syncError.Error(),
	}
	gcw := &GarbageCollectorWatcherController{
		operatorClient:         v1helpers.NewFakeStaticPodOperatorClient(&operatorv1.StaticPodOperatorSpec{OperatorSpec: operatorv1.OperatorSpec{ManagementState: operatorv1.Managed}}, status, nil, nil),
		configMapClient:        nil,
		alertNames:             []string{"dummy"},
		alertingRulesCache:     []prometheusv1.AlertingRule{},
		alertingRulesCacheLock: sync.RWMutex{},
	}
	prometheusResponseError := fmt.Errorf("prometheus querying failed")
	type test struct {
		name                    string
		clusterMonitoringExists bool
		isPrometheusEnabled     bool
		gc                      *GarbageCollectorWatcherController
		expectErr               bool
		expectedErrorMsg        error
		expectedStatusCondition operatorv1.OperatorCondition
	}
	tests := []test{
		{
			name:                    "Garbage Collector Watcher with monitoring disabled",
			clusterMonitoringExists: false,
			gc:                      gcw,
			expectErr:               false,
			expectedStatusCondition: operatorv1.OperatorCondition{},
		},
		{
			name:                    "Garbage Collector Watcher with monitoring enabled but with failing prometheus client",
			clusterMonitoringExists: true,
			isPrometheusEnabled:     false,
			gc:                      gcw,
			expectErr:               false,
			expectedStatusCondition: successCondition,
		},
		{
			name:                    "Garbage Collector Watcher with monitoring enabled but with correct prometheus client",
			clusterMonitoringExists: true,
			isPrometheusEnabled:     true,
			gc:                      gcw,
			expectErr:               true,
			expectedErrorMsg:        syncError,
			expectedStatusCondition: failureCondition,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
			var monitoringName string
			if tc.clusterMonitoringExists {
				monitoringName = "monitoring"
			}
			if tc.isPrometheusEnabled {
				promConnectivity := prometheusConnectivity{useCachedClient: true,
					client: newFakePrometheusClient([]string{}, prometheusResponseError)}
				tc.gc.promConnectivity = promConnectivity
			}
			if err := indexer.Add(&configv1.ClusterOperator{
				ObjectMeta: metav1.ObjectMeta{Name: monitoringName},
			}); err != nil {
				t.Fatal(err.Error())
			}
			kubeClient := fake.NewSimpleClientset()
			configMapInformers := informers.NewSharedInformerFactoryWithOptions(kubeClient, 1*time.Minute, informers.WithNamespace(operatorclient.GlobalMachineSpecifiedConfigNamespace))
			configMapGetter := v1helpers.CachedConfigMapGetter(kubeClient.CoreV1(), v1helpers.NewFakeKubeInformersForNamespaces(map[string]informers.SharedInformerFactory{
				operatorclient.GlobalMachineSpecifiedConfigNamespace: configMapInformers,
			}))
			ctx, ctxCancel := context.WithCancel(context.TODO())
			defer ctxCancel()
			go configMapInformers.Start(ctx.Done())
			tc.gc.configMapClient = configMapGetter
			clusterListers := configlisters.NewClusterOperatorLister(indexer)
			tc.gc.clusterLister = clusterListers
			eventRecorder := events.NewInMemoryRecorder("dummy")
			syncContext := factory.NewSyncContext(controllerName, eventRecorder)
			syncContext.Queue().Add(invalidateAlertingRulesCacheKey)
			err := tc.gc.sync(context.TODO(), syncContext)
			if tc.expectErr {
				if err == nil {
					t.Fatal("expected error but did not get one")
				} else if tc.expectedErrorMsg.Error() != err.Error() {
					t.Fatalf("expected err %v but got %v", tc.expectedErrorMsg, err)
				}
			} else if err != nil {
				t.Fatalf("did not expect error but got one %v", err)
			}
			if status.Conditions != nil {
				// Update the last transition time
				conditionsGot := status.Conditions[0].DeepCopy()
				tc.expectedStatusCondition.LastTransitionTime = conditionsGot.LastTransitionTime
			}
			if status.Conditions != nil && !reflect.DeepEqual(tc.expectedStatusCondition, status.Conditions[0]) {
				t.Fatalf("expected status condition %v got %v", tc.expectedStatusCondition, status.Conditions[0])
			}
		})
	}
}
