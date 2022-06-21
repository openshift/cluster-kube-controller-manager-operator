package gcwatchercontroller

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	prometheusv1 "github.com/prometheus/client_golang/api/prometheus/v1"

	"k8s.io/apimachinery/pkg/util/sets"
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
