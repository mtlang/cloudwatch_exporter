package main

import (
	"errors"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/mtlang/cloudwatch_exporter/config"
)

var (
	templates = map[string]map[string]*cwCollectorTemplate{}
)

type cwMetric struct {
	Desc    *prometheus.Desc
	ValType prometheus.ValueType

	ConfMetric  *config.Metric
	LabelNames  []string
	LabelValues []string
}

type cwCollectorTemplate struct {
	Metrics []cwMetric
	Task    *config.Task
}

type cwCollector struct {
	Regions           []string
	Target            string
	ScrapeTime        prometheus.Gauge
	ErroneousRequests prometheus.Counter
	Template          map[string]*cwCollectorTemplate
}

// generateTemplates creates pre-generated metrics descriptions so that only the metrics are created from them during a scrape.
func generateTemplates(cfg *config.Settings) {
	for _, task := range cfg.Tasks {
		var template = new(cwCollectorTemplate)

		// Save the task it belongs to (Perform a deep copy)
		template.Task = new(config.Task)
		template.Task.DefaultRegion = task.DefaultRegion
		template.Task.Metrics = *new([]config.Metric)
		for _, metric := range task.Metrics {
			template.Task.Metrics = append(template.Task.Metrics, metric)
		}
		template.Task.Name = task.Name
		template.Task.RoleArn = task.RoleArn

		//Pre-allocate at least a few metrics
		template.Metrics = make([]cwMetric, 0, len(task.Metrics))

		for _, metric := range task.Metrics {
			labels := make([]string, len(metric.Dimensions))

			for i, dimension := range metric.Dimensions {
				labels[i] = toSnakeCase(dimension)
			}
			labels = append(labels, "task")
			labels = append(labels, "region")
			labels = append(labels, "account")

			for s := range metric.Statistics {
				template.Metrics = append(template.Metrics, cwMetric{
					Desc: prometheus.NewDesc(
						safeName(toSnakeCase(metric.Namespace)+"_"+toSnakeCase(metric.Name)+"_"+toSnakeCase(metric.Statistics[s])),
						metric.Name,
						labels,
						nil),
					ValType:    prometheus.GaugeValue,
					ConfMetric: &metric,
					LabelNames: labels,
				})
			}
		}

		if templates[task.Name] == nil {
			templates[task.Name] = make(map[string]*cwCollectorTemplate)
		}
		templates[task.Name][task.DefaultRegion] = template
	}
}

// NewCwCollector creates a new instance of a CwCollector for a specific task
// The newly created instance will reference its parent template so that metric descriptions are not recreated on every call.
// It returns either a pointer to a new instance of cwCollector or an error.
func NewCwCollector(target string, taskName string, region string) (*cwCollector, error) {
	// Check if task exists
	tasks, err := settings.GetTasks(taskName)

	if err != nil {
		return nil, err
	}

	var regionsToCollect []string

	if region == "" {
		for _, task := range tasks {
			if task.DefaultRegion == "" {
				return nil, errors.New("No region or default region set requested task")
			}
			regionsToCollect = append(regionsToCollect, task.DefaultRegion)
		}
	} else {
		regionsToCollect = []string{region}
	}

	return &cwCollector{
		Regions: regionsToCollect,
		Target:  target,
		ScrapeTime: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "cloudwatch_exporter_scrape_duration_seconds",
			Help: "Time this CloudWatch scrape took, in seconds.",
		}),
		ErroneousRequests: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "cloudwatch_exporter_erroneous_requests",
			Help: "The number of erroneous request made by this scrape.",
		}),
		Template: templates[taskName],
	}, nil
}

func (collector *cwCollector) Collect(ch chan<- prometheus.Metric) {
	now := time.Now()
	scrape(collector, ch)
	collector.ScrapeTime.Set(time.Since(now).Seconds())

	ch <- collector.ScrapeTime
	ch <- collector.ErroneousRequests
}

func (collector *cwCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- collector.ScrapeTime.Desc()
	ch <- collector.ErroneousRequests.Desc()

	for region := range collector.Template {
		for _, metric := range collector.Template[region].Metrics {
			ch <- metric.Desc
		}
	}
}
