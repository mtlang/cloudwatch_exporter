# Mass CloudWatch Exporter

An [AWS CloudWatch](http://aws.amazon.com/cloudwatch/) exporter for [Prometheus](https://github.com/prometheus/prometheus) coded in Go, with multi-region, multi-account, and dynamic target support.

This fork was made to extend the work done by [Technofy's CloudWatch Exporter](https://github.com/Technofy/cloudwatch_exporter). The goal of this project is to create a cloudwatch exporter that can easily scale to monitor resources across many AWS accounts and many AWS regions. Where other exporters would require an instance to be run in every account or every region, this project aims to monitor an entire AWS landscape and make all gathered information available from a single endpoint. Where other exporters aim to monitor many metrics within a single account and region, this exporter aims to monitor a small number of metrics across many accounts and regions. 

## Running the Exporter

To run the exporter, simply download the latest release for your platform. Alternatively, if you have Go installed, you can run the exporter from source. When invoking the exporter, you can specify the following flags:

| Flag Name | Default Value | Description |
|------------|------|-------------|
| --web.listen-address | :9042 | Address on which to expose metrics. |
| --web.telemetry-path | /metrics | Path under which to expose exporter's metrics. |
| --web.telemetry-scrape-path | /scrape | Path under which to expose CloudWatch metrics. |
| --config.file | config.yml | Path to configuration file. |

## Configuration

The exporter is configured with a single YAML file. The following demonstrates the structure of the configuration file:

```yaml
accounts:
 - 'aws_account_number_1'
 - 'aws_account_number_2'
tasks:
  - name: 'unique_task_name'
   region: 'aws_region' or 'all' (Optional)
   account: 'aws_account_number' or 'all' (Optional)
   role_name: 'name_of_role_to_assume' (Optional)
   metrics:
    - aws_namespace: 'cloudwatch_metric_namespace'
      aws_dimensions: ['cloudwatch_metric_dimension_1', 'cloudwatch_metric_dimension_2'] (Optional)
      aws_dimensions_select: (Optional)
        <name_of_dimension>: ['value_of_dimension']
      aws_dimensions_select_regex: (Optional)
        <name_of_dimension>: 'regex_for_value_to_match'
      aws_metric_name: 'cloudwatch_metric_name'
      aws_statistics: ['metric_statistic_1', 'metric_statistic_2']
      range_seconds: length_of_search_window_in_seconds (Defaults to 600)
      period_seconds: aws_metric_period_in_seconds (Defaults to 60)
      delay_seconds: delay_in_seconds (Defaults to 0)
```
### Configuration Fields
At the top level of the configuration file are three fields: accounts, exclude_accounts, and tasks. Accounts is just a list of AWS account numbers. This list is used by tasks that are set to scrape all accounts. If exclude_accounts are specified, any accounts in that list will not be scraped, even if they're in the accounts list.

A task is a group of metrics which you would like to be presented together. Metrics are scraped by task, so only put metrics under the same task if you want them to always be presented together. 

In addition to a list of metrics and a unique identifier, each task can also have three optional fields. A 'region' can be specified or set to 'all'. An 'account' number can be specified, or set to 'all' to use the list defined at the top level. If 'account' is specified, you must also specify a 'role_name'. The exporter will attempt to assume the specified role in the specified account to gather metrics. If any of the optional fields are not specified, the default credential chain will be used instead.

Each metric is defined by several fields:

| Field Name | Type | Required? | Description |
|------------|------|-----------|-------------|
| aws_metric_name | string | Yes | Name of the metric. 
| aws_namespace | string | Yes | The namespace of the metric. Supports custom namespaces. 
| aws_dimensions | list of strings | No | Dimentions to aggregate metric across. Required for metrics with dimensions. 
| aws_dimensions_select | map | No | Optional filter. Maps from name of dimension to acceptable values for dimension. 
| aws_dimensions_select_regex | map | No | Optional filter. Maps from name of dimension to regex for values to match. 
| aws_statistics | list of strings | Yes | Statistics to display. Doesn't support extended statistics. |
| range_seconds | number | No | Length of metric window in seconds. 
| delay_seconds | number | No | Delays the end of the metric window by x seconds. If 0, ends window at current time. 
| period_seconds | number | No | Metric period. 

The **$_target** token in the dimensions select is used to pass a parameter given by Prometheus (for example a \__meta tag with service discovery).

### Example Configuration

```yaml
accounts:
 - '111111111111'
 - '222222222222'
exclude_accounts:
 - '222222222222'
tasks:
  - name: lambda_errors
   region: 'all'
   account: 'all'
   role_name: 'cloudwatch-reader'
   metrics:
    - aws_namespace: "AWS/Lambda"
      aws_dimensions: ['FunctionName']
      aws_dimensions_select_regex:
        FunctionName: 'custodian-.*'
      aws_metric_name: 'Errors'
      aws_statistics: ['Maximum']
      range_seconds: 900
      period_seconds: 300
      delay_seconds: 0
 - name: billing
   region: us-east-1
   metrics:
    - aws_namespace: "AWS/Billing"
      aws_dimensions: [Currency]
      aws_dimensions_select:
        Currency: [USD]
      aws_metric_name: EstimatedCharges
      aws_statistics: [Maximum]
      range_seconds: 86400

 - name: ec2_cloudwatch
   region: 'all'
   metrics:
    - aws_namespace: "AWS/EC2"
      aws_dimensions: [InstanceId]
      aws_dimensions_select:
        InstanceId: [$_target]
      aws_metric_name: CPUUtilization
      aws_statistics: [Average]

    - aws_namespace: "AWS/EC2"
      aws_dimensions: [InstanceId]
      aws_dimensions_select:
        InstanceId: [$_target]
      aws_metric_name: NetworkOut
      aws_statistics: [Average]

  - name: vpn_mon
    region: 'all'
    metrics:
     - aws_namespace: "AWS/VPN"
       aws_dimensions: [VpnId]
       aws_dimensions_select:
         VpnId: [$_target]
       aws_metric_name: TunnelState
       aws_statistics: [Average]
       range_seconds: 3600
```

### Hot reload of the configuration

Let's say you can't afford to kill the process and restart it for any reason and you need to modify the configuration on the fly. It's possible! Just call the `/reload` endpoint.

## Endpoints

| Endpoint      | Description                                  |
| ------------- | -------------------------------------------- |
| `/metrics`    | Gathers metrics from the CloudWatch exporter itself such as the total number of requests made to the AWS CloudWatch API.
| `/scrape`     | Gathers metrics from the CloudWatch API depending on the task and (optionally) the target passed as parameters.
| `/reload`     | Does a live reload of the configuration without restarting the process

For example a scrape URL could look like this:

`http://localhost:9042/scrape?task=ec2_cloudwatch&target=i-0123456789&region=eu-west-1`

The "target" and "region" parameters are optional, but the "task" parameter is required.

## How to configure Prometheus

```yaml
  - job_name: 'aws_billing'
    metrics_path: '/scrape'
    params:
      task: [billing]
    static_configs:
      - targets: ['localhost:9042']

  - job_name: 'ec2_cloudwatch'
    metrics_path: '/scrape'
    ec2_sd_configs:
      - region: eu-west-1
    params:
      region: [eu-west-1]
    relabel_configs:
      - source_labels: [__meta_ec2_tag_role]
        regex: webapp
        action: keep
      - source_labels: [job]
        target_label: __param_task
      - source_labels: [__meta_ec2_instance_id]
        target_label: __param_target
      - target_label: __address__
        replacement: 'localhost:9042'

  - job_name: 'vpn_mon'
    metrics_path: '/scrape'
    params:
      task: [vpn_mon]
    static_configs:
      - targets: ['vpn-aabbccdd']
    relabel_configs:
      - source_labels: [__address__]
        target_label: __param_target
      - target_label: __address__
        replacement: 'localhost:9042'
```

Thanks to Prometheus relabelling feature, in the second job configuration, we tell it to use the `job_name` as the `task` parameter and to use the `__meta_ec2_instance_id` as the `target` parameter. The region is specified in the `params` section.

The Billing example is there to demonstrate the multi-region capability of this exporter, the `default_region` parameter is specified in the exporter's configuration.

**Note:** It would also work if no default_region was specified but a `params` block with the `region` parameter was set in the Prometheus configuration.


## End Note

This exporter is largely inspired by the [official CloudWatch Exporter](https://github.com/prometheus/cloudwatch_exporter) and we'd like to thank all the contributors who participated to the original project.

This project is licensed under the [Apache 2.0 license](https://github.com/mtlang/cloudwatch_exporter/blob/master/LICENSE).
