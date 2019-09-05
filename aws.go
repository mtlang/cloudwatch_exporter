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

func scrapeTemplate(collector *cwCollector, ch chan<- prometheus.Metric, template *cwCollectorTemplate, wg *sync.WaitGroup) {
	defer wg.Done()

	var innerWg sync.WaitGroup

	session := session.Must(session.NewSession())
	var svc *cloudwatch.CloudWatch
	region := template.Task.Region
	if len(template.Task.Account) > 0 && len(template.Task.RoleName) > 0 {
		roleArn := fmt.Sprintf("arn:aws:iam::%s:role/%s", template.Task.Account, template.Task.RoleName)
		roleCreds := stscreds.NewCredentials(session, roleArn)
		svc = cloudwatch.New(session, aws.NewConfig().WithCredentials(roleCreds).WithRegion(region))
	} else {
		svc = cloudwatch.New(session, aws.NewConfig().WithRegion(region))
	}

	for m := range template.Metrics {
		configMetric := &template.Metrics[m]

		now := time.Now()
		end := now.Add(time.Duration(-configMetric.ConfMetric.DelaySeconds) * time.Second)

		params := &cloudwatch.GetMetricStatisticsInput{
			EndTime:   aws.Time(end),
			StartTime: aws.Time(end.Add(time.Duration(-configMetric.ConfMetric.RangeSeconds) * time.Second)),

			Period:     aws.Int64(int64(configMetric.ConfMetric.PeriodSeconds)),
			MetricName: aws.String(configMetric.ConfMetric.Name),
			Namespace:  aws.String(configMetric.ConfMetric.Namespace),
			Dimensions: []*cloudwatch.Dimension{},
			Statistics: []*string{},
			Unit:       nil,
		}

		dimensions := []*cloudwatch.Dimension{}

		//This map will hold dimensions name which has been already collected
		valueCollected := map[string]bool{}

		if len(configMetric.ConfMetric.DimensionsSelectRegex) == 0 {
			configMetric.ConfMetric.DimensionsSelectRegex = map[string]string{}
		}

		//Check for dimensions who does not have either select or dimensions select_regex and make them select everything using regex
		for _, dimension := range configMetric.ConfMetric.Dimensions {
			_, found := configMetric.ConfMetric.DimensionsSelect[dimension]
			_, found2 := configMetric.ConfMetric.DimensionsSelectRegex[dimension]
			if !found && !found2 {
				configMetric.ConfMetric.DimensionsSelectRegex[dimension] = ".*"
			}
		}

		for _, stat := range configMetric.ConfMetric.Statistics {
			params.Statistics = append(params.Statistics, aws.String(stat))
		}

		labels := make([]string, 0, len(configMetric.LabelNames))

		// Loop through the dimensions selects to build the filters and the labels array
		for dim := range configMetric.ConfMetric.DimensionsSelect {
			for val := range configMetric.ConfMetric.DimensionsSelect[dim] {
				dimValue := configMetric.ConfMetric.DimensionsSelect[dim][val]

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

		if len(dimensions) > 0 || len(configMetric.ConfMetric.Dimensions) == 0 {
			labels = append(labels, template.Task.Name)
			labels = append(labels, region)
			account := template.Task.Account
			if len(account) > 0 {
				labels = append(labels, account)
			} else {
				labels = append(labels, "Not Specified")
			}
			params.Dimensions = dimensions
			innerWg.Add(1)
			scrapeSingleDataPoint(collector, ch, *params, configMetric, labels, svc, &innerWg)
		}

		//If no regex is specified, continue
		if len(configMetric.ConfMetric.DimensionsSelectRegex) == 0 {
			continue
		}

		// Get all the metric to select the ones who'll match the regex
		result, err := svc.ListMetrics(&cloudwatch.ListMetricsInput{
			MetricName: aws.String(configMetric.ConfMetric.Name),
			Namespace:  aws.String(configMetric.ConfMetric.Namespace),
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
				MetricName: aws.String(configMetric.ConfMetric.Name),
				Namespace:  aws.String(configMetric.ConfMetric.Namespace),
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
			labels := make([]string, 0, len(configMetric.LabelNames))
			dimensions = []*cloudwatch.Dimension{}

			//Try to match each dimensions to the regex
			for _, dim := range met.Dimensions {
				dimRegex := configMetric.ConfMetric.DimensionsSelectRegex[*dim.Name]
				if dimRegex == "" {
					dimRegex = "\\b" + strings.Join(configMetric.ConfMetric.DimensionsSelect[*dim.Name], "\\b|\\b") + "\\b"
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
			if len(labels) == len(configMetric.ConfMetric.Dimensions) {

				//Checking if this couple of dimensions has already been scraped
				if _, ok := valueCollected[strings.Join(labels, ";")]; ok {
					continue
				}

				//If no, then scrape them
				valueCollected[strings.Join(labels, ";")] = true

				params.Dimensions = dimensions

				labels = append(labels, template.Task.Name)
				labels = append(labels, region)
				account := template.Task.Account
				if len(account) > 0 {
					labels = append(labels, account)
				} else {
					labels = append(labels, "Not Specified")
				}
				innerWg.Add(1)
				go scrapeSingleDataPoint(collector, ch, *params, configMetric, labels, svc, &innerWg)
			}

		}
	}
	innerWg.Wait()
}

// scrape makes the required calls to AWS CloudWatch by using the parameters in the cwCollector
// Once converted into Prometheus format, the metrics are pushed on the ch channel.
func scrape(collector *cwCollector, ch chan<- prometheus.Metric) {
	var wg sync.WaitGroup
	for _, template := range collector.Templates {
		wg.Add(1)
		go scrapeTemplate(collector, ch, template, &wg)
	}
	wg.Wait()
}

//Send a single dataPoint to the Prometheus lib
func scrapeSingleDataPoint(collector *cwCollector, ch chan<- prometheus.Metric, params cloudwatch.GetMetricStatisticsInput, metric *cwMetric, labels []string, svc *cloudwatch.CloudWatch, wg *sync.WaitGroup) error {
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
		ch <- prometheus.MustNewConstMetric(metric.Desc, metric.ValType, float64(*dp.Sum), labels...)
	}

	if dp.Average != nil {
		ch <- prometheus.MustNewConstMetric(metric.Desc, metric.ValType, float64(*dp.Average), labels...)
	}

	if dp.Maximum != nil {
		ch <- prometheus.MustNewConstMetric(metric.Desc, metric.ValType, float64(*dp.Maximum), labels...)
	}

	if dp.Minimum != nil {
		ch <- prometheus.MustNewConstMetric(metric.Desc, metric.ValType, float64(*dp.Minimum), labels...)
	}

	if dp.SampleCount != nil {
		ch <- prometheus.MustNewConstMetric(metric.Desc, metric.ValType, float64(*dp.SampleCount), labels...)
	}
	return nil
}
