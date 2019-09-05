package main

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials/stscreds"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/cloudwatch"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/mtlang/cloudwatch_exporter/config"
)

func getLatestDatapoint(datapoints []*cloudwatch.Datapoint) *cloudwatch.Datapoint {
	var latest *cloudwatch.Datapoint

	for _, datapoint := range datapoints {
		if latest == nil || latest.Timestamp.Before(*datapoint.Timestamp) {
			latest = datapoint
		}
	}

	return latest
}

func scrapeTask(collector *Collector, ch chan<- prometheus.Metric, task *config.Task, wg *sync.WaitGroup) {
	defer wg.Done()

	var innerWg sync.WaitGroup

	session := session.Must(session.NewSession())
	var svc *cloudwatch.CloudWatch
	region := task.Region
	if len(task.Account) > 0 && len(task.RoleName) > 0 {
		roleArn := fmt.Sprintf("arn:aws:iam::%s:role/%s", task.Account, task.RoleName)
		roleCreds := stscreds.NewCredentials(session, roleArn)
		svc = cloudwatch.New(session, aws.NewConfig().WithCredentials(roleCreds).WithRegion(region))
	} else {
		svc = cloudwatch.New(session, aws.NewConfig().WithRegion(region))
	}

	for m := range task.Metrics {
		configMetric := &task.Metrics[m]

		now := time.Now()
		end := now.Add(time.Duration(-configMetric.DelaySeconds) * time.Second)

		params := &cloudwatch.GetMetricStatisticsInput{
			EndTime:   aws.Time(end),
			StartTime: aws.Time(end.Add(time.Duration(-configMetric.RangeSeconds) * time.Second)),

			Period:     aws.Int64(int64(configMetric.PeriodSeconds)),
			MetricName: aws.String(configMetric.Name),
			Namespace:  aws.String(configMetric.Namespace),
			Dimensions: []*cloudwatch.Dimension{},
			Statistics: []*string{},
			Unit:       nil,
		}

		dimensions := []*cloudwatch.Dimension{}

		//This map will hold dimensions name which has been already collected
		valueCollected := map[string]bool{}

		if len(configMetric.DimensionsSelectRegex) == 0 {
			configMetric.DimensionsSelectRegex = map[string]string{}
		}

		//Check for dimensions who does not have either select or dimensions select_regex and make them select everything using regex
		for _, dimension := range configMetric.Dimensions {
			_, found := configMetric.DimensionsSelect[dimension]
			_, found2 := configMetric.DimensionsSelectRegex[dimension]
			if !found && !found2 {
				configMetric.DimensionsSelectRegex[dimension] = ".*"
			}
		}

		for _, stat := range configMetric.Statistics {
			params.Statistics = append(params.Statistics, aws.String(stat))
		}

		labels := make([]string, 0, len(task.LabelNames))

		// Loop through the dimensions selects to build the filters and the labels array
		for dim := range configMetric.DimensionsSelect {
			for val := range configMetric.DimensionsSelect[dim] {
				dimValue := configMetric.DimensionsSelect[dim][val]

				// Replace $_target token by the actual URL target
				if dimValue == "$_target" {
					dimValue = collector.Target
				}

				dimensions = append(dimensions, &cloudwatch.Dimension{
					Name:  aws.String(dim),
					Value: aws.String(dimValue),
				})

				labels = append(labels, dimValue)
			}
		}

		if len(dimensions) > 0 || len(configMetric.Dimensions) == 0 {
			labels = append(labels, task.Name)
			labels = append(labels, region)
			account := task.Account
			if len(account) > 0 {
				labels = append(labels, account)
			} else {
				labels = append(labels, "Not Specified")
			}
			params.Dimensions = dimensions
			innerWg.Add(1)
			scrapeSingleDataPoint(collector, ch, *params, task, labels, svc, &innerWg)
		}

		//If no regex is specified, continue
		if len(configMetric.DimensionsSelectRegex) == 0 {
			continue
		}

		// Get all the metric to select the ones who'll match the regex
		result, err := svc.ListMetrics(&cloudwatch.ListMetricsInput{
			MetricName: aws.String(configMetric.Name),
			Namespace:  aws.String(configMetric.Namespace),
		})
		if err != nil {
			fmt.Println(err)
			continue
		}
		nextToken := result.NextToken
		metrics := result.Metrics
		totalRequests.Inc()

		for nextToken != nil {
			result, err := svc.ListMetrics(&cloudwatch.ListMetricsInput{
				MetricName: aws.String(configMetric.Name),
				Namespace:  aws.String(configMetric.Namespace),
				NextToken:  nextToken,
			})
			if err != nil {
				fmt.Println(err)
				continue
			}
			nextToken = result.NextToken
			metrics = append(metrics, result.Metrics...)
		}

		//For each metric returned by aws
		for _, met := range result.Metrics {
			labels := make([]string, 0, len(task.LabelNames))
			dimensions = []*cloudwatch.Dimension{}

			//Try to match each dimensions to the regex
			for _, dim := range met.Dimensions {
				dimRegex := configMetric.DimensionsSelectRegex[*dim.Name]
				if dimRegex == "" {
					dimRegex = "\\b" + strings.Join(configMetric.DimensionsSelect[*dim.Name], "\\b|\\b") + "\\b"
				}

				match, _ := regexp.MatchString(dimRegex, *dim.Value)
				if match {
					dimensions = append(dimensions, &cloudwatch.Dimension{
						Name:  aws.String(*dim.Name),
						Value: aws.String(*dim.Value),
					})
					labels = append(labels, *dim.Value)

				}
			}

			//Cheking if all dimensions matched
			if len(labels) == len(configMetric.Dimensions) {

				//Checking if this couple of dimensions has already been scraped
				if _, ok := valueCollected[strings.Join(labels, ";")]; ok {
					continue
				}

				//If no, then scrape them
				valueCollected[strings.Join(labels, ";")] = true

				params.Dimensions = dimensions

				labels = append(labels, task.Name)
				labels = append(labels, region)
				account := task.Account
				if len(account) > 0 {
					labels = append(labels, account)
				} else {
					labels = append(labels, "Not Specified")
				}
				innerWg.Add(1)
				go scrapeSingleDataPoint(collector, ch, *params, task, labels, svc, &innerWg)
			}

		}
	}
	innerWg.Wait()
}

// scrape makes the required calls to AWS CloudWatch by using the parameters in the cwCollector
// Once converted into Prometheus format, the metrics are pushed on the ch channel.
func scrape(collector *Collector, ch chan<- prometheus.Metric) {
	var wg sync.WaitGroup
	for _, task := range collector.Tasks {
		wg.Add(1)
		go scrapeTask(collector, ch, task, &wg)
	}
	wg.Wait()
}

//Send a single dataPoint to the Prometheus lib
func scrapeSingleDataPoint(collector *Collector, ch chan<- prometheus.Metric, params cloudwatch.GetMetricStatisticsInput, task *config.Task, labels []string, svc *cloudwatch.CloudWatch, wg *sync.WaitGroup) error {
	defer wg.Done()
	resp, err := svc.GetMetricStatistics(&params)
	totalRequests.Inc()

	if err != nil {
		collector.ErroneousRequests.Inc()
		fmt.Println(err)
		return err
	}

	// There's nothing in there, don't publish the metric
	if len(resp.Datapoints) == 0 {
		return nil
	}
	// Pick the latest datapoint
	dp := getLatestDatapoint(resp.Datapoints)

	if dp.Sum != nil {
		ch <- prometheus.MustNewConstMetric(task.Desc, task.ValType, float64(*dp.Sum), labels...)
	}

	if dp.Average != nil {
		ch <- prometheus.MustNewConstMetric(task.Desc, task.ValType, float64(*dp.Average), labels...)
	}

	if dp.Maximum != nil {
		ch <- prometheus.MustNewConstMetric(task.Desc, task.ValType, float64(*dp.Maximum), labels...)
	}

	if dp.Minimum != nil {
		ch <- prometheus.MustNewConstMetric(task.Desc, task.ValType, float64(*dp.Minimum), labels...)
	}

	if dp.SampleCount != nil {
		ch <- prometheus.MustNewConstMetric(task.Desc, task.ValType, float64(*dp.SampleCount), labels...)
	}
	return nil
}
