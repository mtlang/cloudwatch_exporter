package config

import (
	"fmt"
	"io/ioutil"
	"log"

	"github.com/prometheus/client_golang/prometheus"
	"gopkg.in/yaml.v2"
)

// Metric is the smallest unit of scraping. It represents metrics from a single namespace in Cloudwatch Metrics.
type Metric struct {
	Namespace string `yaml:"aws_namespace"`
	Name      string `yaml:"aws_metric_name"`

	Statistics            []string            `yaml:"aws_statistics"`
	ExtendedStatistics    []string            `yaml:"aws_extended_statistics"`
	Dimensions            []string            `yaml:"aws_dimensions,omitempty"`
	DimensionsSelect      map[string][]string `yaml:"aws_dimensions_select,omitempty"`
	DimensionsSelectRegex map[string]string   `yaml:"aws_dimensions_select_regex,omitempty"`

	RangeSeconds  int `yaml:"range_seconds,omitempty"`
	PeriodSeconds int `yaml:"period_seconds,omitempty"`
	DelaySeconds  int `yaml:"delay_seconds,omitempty"`
}

// Task represents a single task. A task is confined to a single region and a single account.
type Task struct {
	// These fields come from the config file
	Name     string   `yaml:"name"`
	Region   string   `yaml:"region,omitempty"`
	Metrics  []Metric `yaml:"metrics"`
	RoleName string   `yaml:"role_name,omitempty"`
	Account  string   `yaml:"account,omitempty"`

	// These fields are determined at runtime
	Desc        *prometheus.Desc
	ValType     prometheus.ValueType
	LabelNames  []string
	LabelValues []string
}

// Settings is a top level struct representing the settings file.
// It divides what is scraped into several "tasks".
type Settings struct {
	Accounts        []string `yaml:"accounts,omitempty"`
	ExcludeAccounts []string `yaml:"exclude_accounts,omitempty"`
	Tasks           []Task   `yaml:"tasks"`
}

// GetTasks returns all tasks with a given name
func (settings *Settings) GetTasks(name string) ([]*Task, error) {
	var taskList []*Task
	for _, task := range settings.Tasks {
		if task.Name == name {
			// Add the task to the list (with a deep copy)
			newTask := new(Task)
			newTask.Region = task.Region
			newTask.Metrics = *new([]Metric)
			for _, metric := range task.Metrics {
				newTask.Metrics = append(newTask.Metrics, metric)
			}
			newTask.Name = task.Name
			newTask.Account = task.Account
			newTask.RoleName = task.RoleName
			taskList = append(taskList, newTask)
		}
	}
	if len(taskList) > 0 {
		return taskList, nil
	}

	return nil, fmt.Errorf("can't find task '%s' in configuration", name)
}

// Load returns a settings struct loaded from a given file
func Load(filename string) (*Settings, error) {
	log.SetFlags(log.Lshortfile)
	content, err := ioutil.ReadFile(filename)
	if err != nil {
		return nil, err
	}

	cfg := &Settings{}
	err = yaml.Unmarshal(content, cfg)
	if err != nil {
		return nil, err
	}

	return cfg, nil
}
