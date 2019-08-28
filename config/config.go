package config

import (
	"fmt"
	"io/ioutil"

	"gopkg.in/yaml.v2"
)

// Metric is the smallest unit of scraping. It represents metrics from a single namespace in Cloudwatch Metrics.
type Metric struct {
	Namespace string `yaml:"aws_namespace"`
	Name      string `yaml:"aws_metric_name"`

	Statistics            []string            `yaml:"aws_statistics"`
	Dimensions            []string            `yaml:"aws_dimensions,omitempty"`
	DimensionsSelect      map[string][]string `yaml:"aws_dimensions_select,omitempty"`
	DimensionsSelectRegex map[string]string   `yaml:"aws_dimensions_select_regex,omitempty"`
	DimensionsSelectParam map[string][]string `yaml:"aws_dimensions_select_param,omitempty"`

	RangeSeconds  int `yaml:"range_seconds,omitempty"`
	PeriodSeconds int `yaml:"period_seconds,omitempty"`
	DelaySeconds  int `yaml:"delay_seconds,omitempty"`
}

// Task represents a single task. A task is confined to a single region and a single account.
type Task struct {
	Name     string   `yaml:"name"`
	Region   string   `yaml:"region,omitempty"`
	Metrics  []Metric `yaml:"metrics"`
	RoleName string   `yaml:"role_name,omitempty"`
	Account  string   `yaml:"account,omitempty"`
}

// Settings is a top level struct representing the settings file.
// It divides what is scraped into several "tasks"
type Settings struct {
	AutoReload  bool     `yaml:"auto_reload,omitempty"`
	ReloadDelay int      `yaml:"auto_reload_delay,omitempty"`
	Accounts    []string `yaml:"accounts,omitempty"`
	Tasks       []Task   `yaml:"tasks"`
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

// UnmarshalYAML unmarshalls
func (m *Metric) UnmarshalYAML(unmarshal func(interface{}) error) error {
	type plain Metric

	// These are the default values for a basic metric config
	rawMetric := plain{
		PeriodSeconds: 60,
		RangeSeconds:  600,
		DelaySeconds:  600,
	}
	if err := unmarshal(&rawMetric); err != nil {
		return err
	}

	*m = Metric(rawMetric)
	return nil
}
