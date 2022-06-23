package node

import (
	configv1 "github.com/openshift/api/config/v1"
	nodeobserver "github.com/openshift/library-go/pkg/operator/configobserver/node"
)

var LatencyConfigs = []nodeobserver.LatencyConfigProfileTuple{
	// node-monitor-grace-period: Default=40s;Medium=2m;Low=5m
	{
		ConfigPath: []string{"extendedArguments", "node-monitor-grace-period"},
		ProfileConfigValues: map[configv1.WorkerLatencyProfileType]string{
			configv1.DefaultUpdateDefaultReaction: configv1.DefaultNodeMonitorGracePeriod.String(),
			configv1.MediumUpdateAverageReaction:  configv1.MediumNodeMonitorGracePeriod.String(),
			configv1.LowUpdateSlowReaction:        configv1.LowNodeMonitorGracePeriod.String(),
		},
	},
}

var LatencyProfileRejectionScenarios = []nodeobserver.LatencyProfileRejectionScenario{
	{FromProfile: "", ToProfile: configv1.LowUpdateSlowReaction},
	{FromProfile: configv1.LowUpdateSlowReaction, ToProfile: ""},

	{FromProfile: configv1.DefaultUpdateDefaultReaction, ToProfile: configv1.LowUpdateSlowReaction},
	{FromProfile: configv1.LowUpdateSlowReaction, ToProfile: configv1.DefaultUpdateDefaultReaction},
}
