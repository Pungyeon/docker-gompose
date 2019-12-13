package compose

import "github.com/docker/docker/api/types/container"

type Definition struct {
	Services map[string]Service
}

type RestartPolicy struct {
	Condition string
	MaximumRetries int `yaml:"max_attempts"`
}

func (policy RestartPolicy) ToDockerPolicy() container.RestartPolicy {
	if policy.Condition == "always" {
		return container.RestartPolicy{
			Name:             policy.Condition,
			MaximumRetryCount: 0,
		}
	}
	return container.RestartPolicy{
		Name:              policy.Condition,
		MaximumRetryCount: policy.MaximumRetries,
	}
}
