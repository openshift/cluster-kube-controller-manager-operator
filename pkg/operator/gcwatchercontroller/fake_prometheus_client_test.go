package gcwatchercontroller

import (
	"context"
	"time"

	prometheusv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	prometheusmodel "github.com/prometheus/common/model"
)

func newFakePrometheusClient(firingAlerts []string, responseError error) prometheusv1.API {
	queryResult := prometheusmodel.Vector{}

	for _, alert := range firingAlerts {
		alertLabels := prometheusmodel.LabelSet{
			prometheusmodel.AlertNameLabel: prometheusmodel.LabelValue(alert),
		}
		smpl := &prometheusmodel.Sample{
			Metric: prometheusmodel.Metric(alertLabels),
		}
		queryResult = append(queryResult, smpl)
	}

	client := fakePrometheusClient{}
	client.queryResultVal = queryResult
	client.queryErr = responseError
	return client
}

type fakePrometheusClient struct {
	queryResultVal prometheusmodel.Vector
	queryErr       error
}

func (f fakePrometheusClient) Query(ctx context.Context, query string, ts time.Time) (prometheusmodel.Value, prometheusv1.Warnings, error) {
	return f.queryResultVal, nil, f.queryErr
}

func (f fakePrometheusClient) Alerts(ctx context.Context) (prometheusv1.AlertsResult, error) {
	panic("implement me")
}

func (f fakePrometheusClient) AlertManagers(ctx context.Context) (prometheusv1.AlertManagersResult, error) {
	panic("implement me")
}

func (f fakePrometheusClient) CleanTombstones(ctx context.Context) error {
	panic("implement me")
}

func (f fakePrometheusClient) Config(ctx context.Context) (prometheusv1.ConfigResult, error) {
	panic("implement me")
}

func (f fakePrometheusClient) DeleteSeries(ctx context.Context, matches []string, startTime time.Time, endTime time.Time) error {
	panic("implement me")
}

func (f fakePrometheusClient) Flags(ctx context.Context) (prometheusv1.FlagsResult, error) {
	panic("implement me")
}

func (f fakePrometheusClient) LabelNames(ctx context.Context, matches []string, startTime time.Time, endTime time.Time) ([]string, prometheusv1.Warnings, error) {
	panic("implement me")
}

func (f fakePrometheusClient) LabelValues(ctx context.Context, label string, matches []string, startTime time.Time, endTime time.Time) (prometheusmodel.LabelValues, prometheusv1.Warnings, error) {
	panic("implement me")
}
func (f fakePrometheusClient) QueryRange(ctx context.Context, query string, r prometheusv1.Range) (prometheusmodel.Value, prometheusv1.Warnings, error) {
	panic("implement me")
}

func (f fakePrometheusClient) QueryExemplars(ctx context.Context, query string, startTime time.Time, endTime time.Time) ([]prometheusv1.ExemplarQueryResult, error) {
	panic("implement me")
}

func (f fakePrometheusClient) Buildinfo(ctx context.Context) (prometheusv1.BuildinfoResult, error) {
	panic("implement me")
}

func (f fakePrometheusClient) Runtimeinfo(ctx context.Context) (prometheusv1.RuntimeinfoResult, error) {
	panic("implement me")
}

func (f fakePrometheusClient) Series(ctx context.Context, matches []string, startTime time.Time, endTime time.Time) ([]prometheusmodel.LabelSet, prometheusv1.Warnings, error) {
	panic("implement me")
}

func (f fakePrometheusClient) Snapshot(ctx context.Context, skipHead bool) (prometheusv1.SnapshotResult, error) {
	panic("implement me")
}

func (f fakePrometheusClient) Rules(ctx context.Context) (prometheusv1.RulesResult, error) {
	panic("implement me")
}

func (f fakePrometheusClient) Targets(ctx context.Context) (prometheusv1.TargetsResult, error) {
	panic("implement me")
}

func (f fakePrometheusClient) TargetsMetadata(ctx context.Context, matchTarget string, metric string, limit string) ([]prometheusv1.MetricMetadata, error) {
	panic("implement me")
}

func (f fakePrometheusClient) Metadata(ctx context.Context, metric string, limit string) (map[string][]prometheusv1.Metadata, error) {
	panic("implement me")
}

func (f fakePrometheusClient) TSDB(ctx context.Context) (prometheusv1.TSDBResult, error) {
	panic("implement me")
}

func (f fakePrometheusClient) WalReplay(ctx context.Context) (prometheusv1.WalReplayStatus, error) {
	panic("implement me")
}
