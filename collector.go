package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/mtlang/cloudwatch_exporter/config"
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
	Target            string
	ScrapeTime        prometheus.Gauge
	ErroneousRequests prometheus.Counter
	Templates         []*cwCollectorTemplate
}

var templates []*cwCollectorTemplate

func buildTemplate(task config.Task) *cwCollectorTemplate {
	var template = new(cwCollectorTemplate)

	// Save the task it belongs to (Perform a deep copy)
	template.Task = new(config.Task)
	template.Task.Region = task.Region
	template.Task.Metrics = *new([]config.Metric)
	for _, metric := range task.Metrics {
		template.Task.Metrics = append(template.Task.Metrics, metric)
	}
	template.Task.Name = task.Name
	template.Task.RoleName = task.RoleName
	template.Task.Account = task.Account

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

		for _, statistic := range metric.Statistics {
			template.Metrics = append(template.Metrics, cwMetric{
				Desc: prometheus.NewDesc(
					safeName(toSnakeCase(fmt.Sprintf("%s_%s_%s", metric.Namespace, metric.Name, statistic))),
					fmt.Sprintf("%s %s", metric.Namespace, metric.Name),
					labels,
					nil),
				ValType:    prometheus.GaugeValue,
				ConfMetric: &metric,
				LabelNames: labels,
			})
		}
	}

	return template
}

func getAllRegions() []string {
	regionList := []string{}
	session := session.Must(session.NewSession())
	svc := ec2.New(session, &aws.Config{Region: aws.String("us-east-1")})
	result, err := svc.DescribeRegions(&ec2.DescribeRegionsInput{})
	if err != nil {
		println(err.Error())
		return regionList
	}

	for _, region := range result.Regions {
		regionList = append(regionList, *region.RegionName)
	}

	return regionList
}

// generateTemplates creates pre-generated metrics descriptions so that only the metrics are created from them during a scrape.
func generateTemplates(cfg *config.Settings) {
	templates = []*cwCollectorTemplate{}
	allRegions := getAllRegions()

	for _, task := range cfg.Tasks {
		if strings.EqualFold(task.Account, "all") {
			region := task.Region
			for _, account := range cfg.Accounts {
				task.Account = account
				if strings.EqualFold(region, "all") {
					for _, regionToAdd := range allRegions {
						task.Region = regionToAdd
	
						template := buildTemplate(task)
						templates = append(templates, template)
					}
				} else {
					template := buildTemplate(task)
					templates = append(templates, template)
				}
			}
		} else {
			if strings.EqualFold(task.Region, "all") {
				for _, region := range allRegions {
					task.Region = region

					template := buildTemplate(task)
					templates = append(templates, template)
				}
			} else {
				template := buildTemplate(task)
				templates = append(templates, template)
			}
		}
	}
}

// NewCwCollector creates a new instance of a CwCollector for a specific task
// The newly created instance will reference its parent template so that metric descriptions are not recreated on every call.
// It returns either a pointer to a new instance of cwCollector or an error.
func NewCwCollector(target string, taskName string, region string) (*cwCollector, error) {
	// Check if task exists
	_, err := settings.GetTasks(taskName)
	if err != nil {
		return nil, err
	}

	templatesToUse := templates
	if region != "" {
		templatesToUse = []*cwCollectorTemplate{}
		for _, template := range templates {
			if template.Task.Region == region && template.Task.Name == taskName {
				templatesToUse = append(templatesToUse, template)
			}
		}
	} else {
		templatesToUse = []*cwCollectorTemplate{}
		for _, template := range templates {
			if template.Task.Name == taskName {
				templatesToUse = append(templatesToUse, template)
			}
		}
	}
	

	return &cwCollector{
		Target: target,
		ScrapeTime: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "cloudwatch_exporter_scrape_duration_seconds",
			Help: "Time this CloudWatch scrape took, in seconds.",
		}),
		ErroneousRequests: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "cloudwatch_exporter_erroneous_requests",
			Help: "The number of erroneous request made by this scrape.",
		}),
		Templates: templatesToUse,
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

	for region := range collector.Templates {
		for _, metric := range collector.Templates[region].Metrics {
			ch <- metric.Desc
		}
	}
}
