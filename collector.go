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

// Collector represents a prometheus collector. A single collector is used for each scrape.
type Collector struct {
	Target            string
	ScrapeTime        prometheus.Gauge
	ErroneousRequests prometheus.Counter
	Tasks             []*config.Task
}

var tasks []*config.Task

func buildTask(task config.Task) *config.Task {
	var newTask = new(config.Task)

	// Save the task it belongs to (Perform a deep copy)
	newTask = new(config.Task)
	newTask.Region = task.Region
	newTask.Metrics = *new([]config.Metric)
	for _, metric := range task.Metrics {
		newTask.Metrics = append(newTask.Metrics, metric)
	}
	newTask.Name = task.Name
	newTask.RoleName = task.RoleName
	newTask.Account = task.Account

	for _, metric := range task.Metrics {
		labels := make([]string, len(metric.Dimensions))

		for i, dimension := range metric.Dimensions {
			labels[i] = toSnakeCase(dimension)
		}
		labels = append(labels, "task")
		labels = append(labels, "region")
		labels = append(labels, "account")
		labels = append(labels, "statistic")

		newTask.Desc = prometheus.NewDesc(
			safeName(toSnakeCase(fmt.Sprintf("%s_%s", metric.Namespace, metric.Name))),
			fmt.Sprintf("%s %s", metric.Namespace, metric.Name),
			labels,
			nil)
		newTask.ValType = prometheus.GaugeValue
		newTask.LabelNames = labels
	}

	return newTask
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

// generateTasks creates pre-generated metrics descriptions so that only the metrics are created from them during a scrape.
func generateTasks(cfg *config.Settings) {
	tasks = []*config.Task{}

	for _, task := range cfg.Tasks {
		if strings.EqualFold(task.Account, "all") {
			region := task.Region
			for _, account := range cfg.Accounts {
				task.Account = account
				if strings.EqualFold(region, "all") {

					allRegions := getAllRegions()
					
					for _, regionToAdd := range allRegions {
						task.Region = regionToAdd

						newTask := buildTask(task)
						tasks = append(tasks, newTask)
					}
				} else {
					newTask := buildTask(task)
					tasks = append(tasks, newTask)
				}
			}
		} else {
			if strings.EqualFold(task.Region, "all") {

				allRegions := getAllRegions()
				
				for _, region := range allRegions {
					task.Region = region

					newTask := buildTask(task)
					tasks = append(tasks, newTask)
				}
			} else {
				newTask := buildTask(task)
				tasks = append(tasks, newTask)
			}
		}
	}
}

// NewCwCollector creates a new instance of a CwCollector for a specific task
// The newly created instance will reference its parent task so that metric descriptions are not recreated on every call.
// It returns either a pointer to a new instance of cwCollector or an error.
func NewCwCollector(target string, taskName string, region string) (*Collector, error) {
	// Check if task exists
	_, err := settings.GetTasks(taskName)
	if err != nil {
		return nil, err
	}

	tasksToUse := tasks
	if region != "" {
		tasksToUse = []*config.Task{}
		for _, task := range tasks {
			if task.Region == region && task.Name == taskName {
				tasksToUse = append(tasksToUse, task)
			}
		}
	} else {
		tasksToUse = []*config.Task{}
		for _, task := range tasks {
			if task.Name == taskName {
				tasksToUse = append(tasksToUse, task)
			}
		}
	}

	return &Collector{
		Target: target,
		ScrapeTime: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "cloudwatch_exporter_scrape_duration_seconds",
			Help: "Time this CloudWatch scrape took, in seconds.",
		}),
		ErroneousRequests: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "cloudwatch_exporter_erroneous_requests",
			Help: "The number of erroneous request made by this scrape.",
		}),
		Tasks: tasksToUse,
	}, nil
}

func (collector *Collector) Collect(ch chan<- prometheus.Metric) {
	now := time.Now()
	scrape(collector, ch)
	collector.ScrapeTime.Set(time.Since(now).Seconds())

	ch <- collector.ScrapeTime
	ch <- collector.ErroneousRequests
}

func (collector *Collector) Describe(ch chan<- *prometheus.Desc) {
	ch <- collector.ScrapeTime.Desc()
	ch <- collector.ErroneousRequests.Desc()

	for _, task := range collector.Tasks {
		ch <- task.Desc
	}
}
