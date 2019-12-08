package wavefront

import (
	"fmt"
	"strconv"
	"time"

	log "github.com/sirupsen/logrus"
	wavefront_api "github.com/spaceapegames/go-wavefront"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/argoproj/argo-rollouts/pkg/apis/rollouts/v1alpha1"
	"github.com/argoproj/argo-rollouts/utils/evaluate"
	metricutil "github.com/argoproj/argo-rollouts/utils/metric"
	templateutil "github.com/argoproj/argo-rollouts/utils/template"
)

const (
	//ProviderType indicates the provider is wavefront
	ProviderType = "Wavefront"
)

// Provider contains all the required components to run a prometheus query
type Provider struct {
	api    wavefront_api.Client
	logCtx log.Entry
}

// Type incidates provider is a prometheus provider
func (p *Provider) Type() string {
	return ProviderType
}

// Run queries with wavefront provider for the metric
func (p *Provider) Run(run *v1alpha1.AnalysisRun, metric v1alpha1.Metric) v1alpha1.Measurement {
	startTime := metav1.Now()
	newMeasurement := v1alpha1.Measurement{
		StartedAt: &startTime,
	}

	//TODO(dthomson) make timeout configuriable
	//ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	//defer cancel()

	query, err := templateutil.ResolveArgs(metric.Provider.Wavefront.Query, run.Spec.Arguments)
	if err != nil {
		return metricutil.MarkMeasurementError(newMeasurement, err)
	}

	queryParams := wavefront_api.QueryParams {
		QueryString: query,
		StartTime:  strconv.FormatInt(time.Now().Unix() * 1000, 10),
		Granularity: "s",
		MaxPoints: "1",
		SeriesOutsideTimeWindow: false,
		SummarizationStrategy: "MIN",
		ListMode: true,
	}
	response, err := p.api.NewQuery(&queryParams).Execute()
	if err != nil {
		return metricutil.MarkMeasurementError(newMeasurement, err)
	}
	newValue, newStatus, err := p.processResponse(metric, response)
	if err != nil {
		return metricutil.MarkMeasurementError(newMeasurement, err)

	}
	newMeasurement.Value = newValue

	newMeasurement.Phase = newStatus
	finishedTime := metav1.Now()
	newMeasurement.FinishedAt = &finishedTime
	return newMeasurement
}

// Resume should not be used the Wavefront provider since all the work should occur in the Run method
func (p *Provider) Resume(run *v1alpha1.AnalysisRun, metric v1alpha1.Metric, measurement v1alpha1.Measurement) v1alpha1.Measurement {
	p.logCtx.Warn("Wavefront provider should not execute the Resume method")
	return measurement
}

// Terminate should not be used the Wavefront provider since all the work should occur in the Run method
func (p *Provider) Terminate(run *v1alpha1.AnalysisRun, metric v1alpha1.Metric, measurement v1alpha1.Measurement) v1alpha1.Measurement {
	p.logCtx.Warn("Wavefront provider should not execute the Terminate method")
	return measurement
}

// GarbageCollect is a no-op for the Wavefront provider
func (p *Provider) GarbageCollect(run *v1alpha1.AnalysisRun, metric v1alpha1.Metric, limit int) error {
	return nil
}

func (p *Provider) evaluateResult(result interface{}, metric v1alpha1.Metric) v1alpha1.AnalysisPhase {
	successCondition := false
	failCondition := false
	var err error

	if metric.SuccessCondition != "" {
		successCondition, err = evaluate.EvalCondition(result, metric.SuccessCondition)
		if err != nil {
			p.logCtx.Warning(err.Error())
			return v1alpha1.AnalysisPhaseError
		}
	}
	if metric.FailureCondition != "" {
		failCondition, err = evaluate.EvalCondition(result, metric.FailureCondition)
		if err != nil {
			return v1alpha1.AnalysisPhaseError
		}
	}

	switch {
	case metric.SuccessCondition == "" && metric.FailureCondition == "":
		//Always return success unless there is an error
		return v1alpha1.AnalysisPhaseSuccessful
	case metric.SuccessCondition != "" && metric.FailureCondition == "":
		// Without a failure condition, a measurement is considered a failure if the measurement's success condition is not true
		failCondition = !successCondition
	case metric.SuccessCondition == "" && metric.FailureCondition != "":
		// Without a success condition, a measurement is considered a successful if the measurement's failure condition is not true
		successCondition = !failCondition
	}

	if failCondition {
		return v1alpha1.AnalysisPhaseFailed
	}

	if !failCondition && !successCondition {
		return v1alpha1.AnalysisPhaseInconclusive
	}

	// If we reach this code path, failCondition is false and successCondition is true
	return v1alpha1.AnalysisPhaseSuccessful
}

func (p *Provider) processResponse(metric v1alpha1.Metric, response *wavefront_api.QueryResponse) (string, v1alpha1.AnalysisPhase, error) {
	if len(response.TimeSeries) == 1 {
		series := response.TimeSeries[0]
		result := series.DataPoints[0][1]
		newStatus := p.evaluateResult(result, metric)
		return fmt.Sprintf("%f", result) , newStatus, nil
	}
	return "",v1alpha1.AnalysisPhaseFailed, nil
}

// NewWavefrontProvider Creates a new Wavefront client
func NewWavefrontProvider(api wavefront_api.Client, logCtx log.Entry) *Provider {
	return &Provider{
		logCtx: logCtx,
		api:    api,
	}
}

// NewPrometheusAPI generates a Wavefront API client from the metric configuration
func NewWavefrontAPI(metric v1alpha1.Metric) (*wavefront_api.Client, error) {
	client, err := wavefront_api.NewClient(&wavefront_api.Config{
		Address: "mon.wavefront.com",
		Token:   "",
	},)
	if err != nil {
		return nil, err
	}
	return client, nil
}
